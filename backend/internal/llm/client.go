package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"backend/internal/outbound"
)

const chatCompletionsPath = "/chat/completions"

// streamTerminator is the payload that closes an OpenAI-compatible stream.
const streamTerminator = "[DONE]"

// ErrIncompleteStream reports a stream that ended before the provider said the
// model had finished. The answer so far is discarded rather than passed off as
// complete.
var ErrIncompleteStream = errors.New("llm: stream ended before the answer finished")

// maxErrorBodyBytes bounds how much of a failed response is read back.
const maxErrorBodyBytes = 8 << 10

// HTTPError is a non-2xx response from the provider. It never carries the API
// key: the key travels in a header, not in the URL.
type HTTPError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *HTTPError) Error() string {
	switch {
	case e.Message == "":
		return fmt.Sprintf("llm: provider returned status %d", e.StatusCode)
	case e.Code == "":
		return fmt.Sprintf("llm: provider returned status %d: %s", e.StatusCode, e.Message)
	default:
		return fmt.Sprintf("llm: provider returned status %d (%s): %s", e.StatusCode, e.Code, e.Message)
	}
}

// Retryable reports whether repeating the same call could succeed. Client errors
// other than rate limiting will fail the same way every time.
func (e *HTTPError) Retryable() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500
}

// ErrNoKeySource reports a client built without anything to authenticate with.
//
// It is refused at construction rather than at the first call: a client with no
// keys sends unauthenticated requests, and the mistake would arrive as a
// provider error in the middle of somebody's turn.
var ErrNoKeySource = errors.New("llm: a client needs a key source")

// ErrNoDialer is what a client built without a boundary answers. It is refused
// for the same reason as the line above and not a weaker one: a client whose
// transport dials wherever a URL says is a client that will send a conversation
// to the deployment's own network if a base URL is ever mistyped, and the
// mistake would arrive as a provider error rather than as a refusal.
var ErrNoDialer = errors.New("llm: a client needs a dialer")

// Client is an OpenAI-compatible chat completions client.
type Client struct {
	baseURL string
	keys    KeySource
	http    *http.Client
}

var _ Provider = (*Client)(nil)

// NewClient builds a client for baseURL. timeout bounds how long a call may
// take to start responding, not how long the answer may take to stream: a long
// answer is bounded by the caller's context instead.
//
// The credentials come in as a source rather than as a key, so that one process
// can hold several and rotate them (§17.3) without this package knowing that
// anybody keeps a pool: what it knows is that somebody can give it the next key
// and wants to hear how the provider treated it.
//
// dial is where the request is allowed to go, and it is required — see
// ErrNoDialer. The provider is somewhere on the internet and this client sends
// whole conversations to it, so "which addresses may this reach" is not a
// question the client should be able to be built without an answer to.
func NewClient(baseURL string, keys KeySource, timeout time.Duration, dial outbound.DialFunc) (*Client, error) {
	if keys == nil {
		return nil, ErrNoKeySource
	}
	if dial == nil {
		return nil, ErrNoDialer
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = timeout
	// The one hook every connection this client makes has to pass through,
	// including the ones a redirect or a retry causes. Nothing in this package
	// checks an address anywhere else, and that is the design (§2.5).
	transport.DialContext = dial
	return &Client{
		baseURL: baseURL,
		keys:    keys,
		http:    &http.Client{Transport: transport},
	}, nil
}

// Stream runs one chat completion and assembles the streamed fragments.
func (c *Client) Stream(ctx context.Context, req Request, onDelta func(Delta) error) (Response, error) {
	// One key per call, taken here rather than remembered on the client: a client
	// that held a key would keep using a credential the pool has since rested,
	// which is precisely the state the pool exists to leave behind.
	key := c.keys.Next()
	body, err := json.Marshal(newWireRequest(req))
	if err != nil {
		return Response{}, fmt.Errorf("llm: encode request")
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+chatCompletionsPath, bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("llm: build request")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+key)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		// The call never arrived, so nothing is known about the key: it is neither
		// credited nor blamed. Resting a credential because the network failed
		// would take a working key out of service for a fault that had nothing to
		// do with it, and the retry would then have one fewer to choose from.
		return Response{}, wrapContext(ctx, fmt.Errorf("llm: call provider: %w", err))
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		httpErr := readHTTPError(resp, key)
		// Only a rate limit is about the key. A provider that is unwell (a 500) is
		// a fault this pool cannot help with, and a key the provider rejects (a 401)
		// is a configuration error that resting would only disguise as a temporary
		// one — and would then hide for as long as the rest lasts.
		if httpErr.StatusCode == http.StatusTooManyRequests {
			c.keys.RateLimited(key)
		}
		return Response{}, httpErr
	}
	// A 2xx is the provider's verdict on the credential, and it is taken before
	// the answer is read: how long the stream takes and whether it ends properly
	// are facts about the answer rather than about the key that asked for it.
	c.keys.Succeeded(key)
	return c.consume(ctx, resp.Body, onDelta)
}

func (c *Client) consume(ctx context.Context, body io.Reader, onDelta func(Delta) error) (Response, error) {
	reader := newSSEReader(body)
	var (
		result   Response
		builder  toolCallBuilder
		finished bool
	)
	for {
		data, ok, err := reader.Next()
		if err != nil {
			return Response{}, wrapContext(ctx, fmt.Errorf("llm: read stream: %w", err))
		}
		if !ok {
			break
		}
		if data == streamTerminator {
			finished = true
			break
		}

		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return Response{}, fmt.Errorf("llm: decode stream chunk")
		}
		// Usage arrives on the chunk before the terminator. DeepSeek attaches it
		// to the last content chunk while other compatible servers send a
		// usage-only chunk with an empty choices array, so read it from both.
		if chunk.Usage != nil {
			result.Usage = chunk.Usage.usage()
		}
		if len(chunk.Choices) == 0 {
			continue
		}

		choice := chunk.Choices[0]
		if choice.FinishReason != nil {
			result.FinishReason = *choice.FinishReason
		}
		delta := Delta{Content: choice.Delta.Content, Reasoning: choice.Delta.Reasoning}
		if delta != (Delta{}) {
			if err := onDelta(delta); err != nil {
				return Response{}, err
			}
		}
		result.Content += delta.Content
		builder.add(choice.Delta.ToolCalls)
	}
	// Returning a half-finished answer as if it were complete would silently
	// truncate the reply, so a stream that stops without the provider saying the
	// model finished is an error. Either the terminator or a finish reason
	// counts as that evidence.
	if !finished && result.FinishReason == "" {
		return Response{}, wrapContext(ctx, ErrIncompleteStream)
	}
	result.ToolCalls = builder.calls()
	return result, nil
}

// wrapContext prefers the context's own error so that callers can reliably tell
// cancellation apart from a transport failure.
func wrapContext(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

// readHTTPError turns a failed response into the error the rest of the service
// reads, with the key this call went out with taken out of it.
//
// The text is the provider's, and providers differ in how much of the request
// they repeat back: one that echoes the key would otherwise put it in this
// service's logs, which is the one place configuration says it must never be.
// The key that travels in is the one that was sent rather than every key the
// pool holds — a key this call did not use cannot be in the provider's answer,
// and reading the pool's list here would mean this package knew about the pool.
func readHTTPError(resp *http.Response, sent string) *HTTPError {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	if err != nil {
		return &HTTPError{StatusCode: resp.StatusCode}
	}
	var payload struct {
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	// A body that is not the documented error shape is not an error in itself;
	// the status code alone still describes what happened.
	if err := json.Unmarshal(body, &payload); err != nil || payload.Error.Message == "" {
		return &HTTPError{StatusCode: resp.StatusCode}
	}
	return &HTTPError{
		StatusCode: resp.StatusCode,
		Code:       payload.Error.Code,
		Message:    redact(payload.Error.Message, sent),
	}
}

// redact takes a secret out of text this service did not write.
func redact(text, secret string) string {
	if secret == "" || !strings.Contains(text, secret) {
		return text
	}
	return strings.ReplaceAll(text, secret, "[redacted]")
}

// toolCallBuilder reassembles tool calls that arrive as fragments. Fragments
// carry an index rather than an id, and only the first carries the id and name.
type toolCallBuilder struct {
	byIndex map[int]*ToolCall
}

func (b *toolCallBuilder) add(fragments []toolCallFragment) {
	for _, fragment := range fragments {
		call, ok := b.byIndex[fragment.Index]
		if !ok {
			call = &ToolCall{}
			if b.byIndex == nil {
				b.byIndex = make(map[int]*ToolCall)
			}
			b.byIndex[fragment.Index] = call
		}
		if fragment.ID != "" {
			call.ID = fragment.ID
		}
		// Both DeepSeek and OpenAI send the name once, in the opening fragment,
		// so assigning beats appending: appending would duplicate it.
		if fragment.Function.Name != "" {
			call.Name = fragment.Function.Name
		}
		call.Arguments += fragment.Function.Arguments
	}
}

// calls returns the assembled calls in index order. The index, not the arrival
// order, is what says which call is which, so sorting by it keeps the result
// stable even if a provider interleaves fragments.
func (b *toolCallBuilder) calls() []ToolCall {
	if len(b.byIndex) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(b.byIndex))
	for index := range b.byIndex {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	calls := make([]ToolCall, 0, len(indexes))
	for _, index := range indexes {
		calls = append(calls, *b.byIndex[index])
	}
	return calls
}
