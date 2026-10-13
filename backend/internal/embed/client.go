package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	embeddingsPath = "/embeddings"
	// encodingFormatFloat asks for plain numbers. The alternative some
	// providers default to is base64, which would decode into a vector of
	// plausible-looking nonsense.
	encodingFormatFloat = "float"
	// maxErrorBodyBytes bounds how much of a failed response is read back.
	maxErrorBodyBytes = 8 << 10
	// maxResponseBodyBytes bounds a successful one. Sixteen vectors of 1024
	// floats is a few hundred kilobytes, so this ceiling is far above any batch
	// this service configures and far below anything that could exhaust memory.
	maxResponseBodyBytes = 64 << 20
)

// HTTPError is a non-2xx response from the provider. It never carries the API
// key: the key travels in a header, not in the URL or the body, and the
// provider's own message is redacted before it is kept.
type HTTPError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *HTTPError) Error() string {
	switch {
	case e.Message == "":
		return fmt.Sprintf("embed: provider returned status %d", e.StatusCode)
	case e.Code == "":
		return fmt.Sprintf("embed: provider returned status %d: %s", e.StatusCode, e.Message)
	default:
		return fmt.Sprintf("embed: provider returned status %d (%s): %s", e.StatusCode, e.Code, e.Message)
	}
}

// Retryable reports whether repeating the same call could succeed. Client
// errors other than rate limiting will fail the same way every time.
func (e *HTTPError) Retryable() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500
}

// Client is an OpenAI-compatible embeddings client.
type Client struct {
	baseURL   string
	apiKey    string
	model     string
	dimension int
	batchSize int
	http      *http.Client
}

var _ Embedder = (*Client)(nil)

// NewClient builds a client from validated options.
func NewClient(opts Options) (*Client, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = opts.Timeout
	return &Client{
		baseURL:   strings.TrimSuffix(opts.BaseURL, "/"),
		apiKey:    opts.APIKey,
		model:     opts.Model,
		dimension: opts.Dimension,
		batchSize: opts.BatchSize,
		http:      &http.Client{Transport: transport},
	}, nil
}

// Embed runs the input through the provider, one request per batch.
//
// The batches are sequential rather than concurrent: the point of BatchSize is
// to bound how much work the provider is asked to do at once, and a client that
// fired them all off in parallel would bound nothing. Concurrency across
// documents is the index worker's job, where it is visible in one place.
func (c *Client) Embed(ctx context.Context, texts []string) (Result, error) {
	var result Result
	for start := 0; start < len(texts); start += c.batchSize {
		end := min(start+c.batchSize, len(texts))

		vectors, usage, err := c.embedBatch(ctx, texts[start:end])
		if err != nil {
			// Nothing partial is returned: a caller that stored half the
			// vectors of a document would have a document that is half
			// retrievable with nothing to say which half.
			return Result{}, err
		}
		result.Vectors = append(result.Vectors, vectors...)
		result.Usage.PromptTokens += usage.PromptTokens
		result.Usage.TotalTokens += usage.TotalTokens
	}
	return result, nil
}

func (c *Client) embedBatch(ctx context.Context, texts []string) ([]Vector, Usage, error) {
	body, err := json.Marshal(embeddingRequest{
		Model:          c.model,
		Input:          texts,
		EncodingFormat: encodingFormatFloat,
	})
	if err != nil {
		return nil, Usage{}, fmt.Errorf("embed: encode request")
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+embeddingsPath, bytes.NewReader(body))
	if err != nil {
		return nil, Usage{}, fmt.Errorf("embed: build request")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	// A provider that needs no key — a local model, a test server — is not sent
	// an empty authorization header, because "Bearer " is not the same request
	// as no header at all.
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, Usage{}, wrapContext(ctx, fmt.Errorf("embed: call provider: %w", err))
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, Usage{}, c.readHTTPError(resp)
	}
	return c.decode(ctx, resp.Body, len(texts))
}

func (c *Client) decode(ctx context.Context, body io.Reader, want int) ([]Vector, Usage, error) {
	raw, err := io.ReadAll(io.LimitReader(body, maxResponseBodyBytes+1))
	if err != nil {
		return nil, Usage{}, wrapContext(ctx, fmt.Errorf("embed: read response: %w", err))
	}
	if len(raw) > maxResponseBodyBytes {
		return nil, Usage{}, fmt.Errorf("embed: response is larger than %d bytes", maxResponseBodyBytes)
	}

	var payload embeddingResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, Usage{}, fmt.Errorf("embed: decode response")
	}
	if len(payload.Data) != want {
		return nil, Usage{}, fmt.Errorf("%w: %d texts, %d vectors", ErrVectorCount, want, len(payload.Data))
	}

	vectors := make([]Vector, want)
	filled := make([]bool, want)
	for position, datum := range payload.Data {
		index := position
		// index is what says which input a vector belongs to. It is optional in
		// the wire shape, so a provider that omits it is taken at its word that
		// the order is the order — and one that sends it is believed instead.
		if datum.Index != nil {
			index = *datum.Index
		}
		if index < 0 || index >= want {
			return nil, Usage{}, fmt.Errorf("embed: provider returned a vector for position %d of %d", index, want)
		}
		if filled[index] {
			return nil, Usage{}, fmt.Errorf("embed: provider returned two vectors for position %d", index)
		}
		if len(datum.Embedding) != c.dimension {
			return nil, Usage{}, fmt.Errorf("%w: expected %d, got %d",
				ErrDimensionMismatch, c.dimension, len(datum.Embedding))
		}
		filled[index] = true
		vectors[index] = Vector(datum.Embedding)
	}
	return vectors, payload.Usage.usage(), nil
}

// readHTTPError turns a failed response into the error the rest of the service
// reads, with this client's own key taken out of it.
//
// The text is the provider's, and providers differ in how much of the request
// they repeat back: one that echoes the key would otherwise put it in this
// service's logs, which is the one place configuration says it must never be.
func (c *Client) readHTTPError(resp *http.Response) error {
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
		Message:    redact(payload.Error.Message, c.apiKey),
	}
}

// redact takes a secret out of text this service did not write.
func redact(text, secret string) string {
	if secret == "" || !strings.Contains(text, secret) {
		return text
	}
	return strings.ReplaceAll(text, secret, "[redacted]")
}

// wrapContext prefers the context's own error so that callers can reliably tell
// cancellation apart from a transport failure.
func wrapContext(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

// wrapContextCause is wrapContext for a stage that fails after the request was
// accepted: the context is checked the same way, and the underlying failure is
// kept because a body that stopped mid-read says something the status did not.
func wrapContextCause(ctx context.Context, err error, prefix string) error {
	return wrapContext(ctx, fmt.Errorf("%s: %w", prefix, err))
}

// embeddingRequest is the documented shape of a request. dimensions is
// deliberately not sent: the width is checked on what comes back instead, so
// the client does not depend on a field every compatible provider may not
// accept.
type embeddingRequest struct {
	Model          string   `json:"model"`
	Input          []string `json:"input"`
	EncodingFormat string   `json:"encoding_format"`
}

type embeddingResponse struct {
	Data []struct {
		// Index is a pointer because absent and zero are different answers: the
		// first means the order is the order, the second means this is the
		// first input.
		Index     *int      `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Usage embeddingUsage `json:"usage"`
}

type embeddingUsage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

func (u embeddingUsage) usage() Usage {
	return Usage{PromptTokens: u.PromptTokens, TotalTokens: u.TotalTokens}
}
