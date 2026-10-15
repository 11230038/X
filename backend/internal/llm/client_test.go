package llm

// The stream itself: what a provider's event stream is assembled into, and the
// cases where it is not an answer at all.
//
// The fixtures these run on — the key sources, the server builder, the helpers
// that collect a stream — are below; the request those cases send is
// client_request_test.go's, the refusals are client_error_test.go's, and what a
// call says back to the key pool is client_keys_test.go's.

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"backend/internal/outbound"
)

const testAPIKey = "unit-test-api-key"

// testDial is the boundary these cases run behind.
//
// Every client now needs a dialer (§2.5), and the provider these cases talk to
// is an httptest server on this machine — so the policy that admits loopback is
// the one that lets them reach it. Which policy a client gets in a deployment
// is the assembly point's decision (§2.6) and is asserted in internal/outbound;
// what is under test here is the protocol, behind the same hook production uses.
func testDial(t *testing.T) outbound.DialFunc {
	t.Helper()
	dial, err := outbound.DialContext(outbound.LANServicePolicy{})
	if err != nil {
		t.Fatalf("build the test dialer: %v", err)
	}
	return dial
}

// oneKey is the smallest key source there is: the same credential every time.
// What a pool does with several is internal/keypool's business; what these cases
// are about is what the client asks for and what it says back.
type oneKey string

func (k oneKey) Next() string       { return string(k) }
func (k oneKey) Succeeded(string)   {}
func (k oneKey) RateLimited(string) {}

// watchingKeys records what the client asked for and what it said afterwards, so
// that a case can assert the client reports the outcome of the call it just made
// rather than holding a key of its own.
type watchingKeys struct {
	mu        sync.Mutex
	handed    []string
	succeeded []string
	limited   []string
}

func (w *watchingKeys) Next() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	key := testAPIKey
	w.handed = append(w.handed, key)
	return key
}

func (w *watchingKeys) Succeeded(key string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.succeeded = append(w.succeeded, key)
}

func (w *watchingKeys) RateLimited(key string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.limited = append(w.limited, key)
}

// sseData renders payloads as the event stream a provider would send.
func sseData(payloads ...string) string {
	var builder strings.Builder
	for _, payload := range payloads {
		builder.WriteString("data: ")
		builder.WriteString(payload)
		builder.WriteString("\n\n")
	}
	return builder.String()
}

// newTestClient points a client at a handler and returns it.
func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, oneKey(testAPIKey), 5*time.Second, testDial(t))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}

func streamHandler(t *testing.T, body string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("write stream: %v", err)
		}
	}
}

// The dialer is not an ornament. A client that took one and installed nothing
// would pass every other case in this package — the call would still be made,
// just without the boundary §2.5 put there — so this is the case that fails when
// the transport hook is dropped.
func TestARequestGoesThroughTheDialerTheClientWasBuiltWith(t *testing.T) {
	var mu sync.Mutex
	var reached []string
	body := sseData(
		`{"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
		`[DONE]`,
	)
	server := httptest.NewServer(streamHandler(t, body))
	t.Cleanup(server.Close)

	through := testDial(t)
	client, err := NewClient(server.URL, oneKey(testAPIKey), 5*time.Second,
		func(ctx context.Context, network, address string) (net.Conn, error) {
			mu.Lock()
			reached = append(reached, address)
			mu.Unlock()
			return through(ctx, network, address)
		})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := client.Stream(context.Background(), Request{Model: "deepseek-chat"}, func(Delta) error { return nil }); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(reached) == 0 {
		t.Fatal("the call never passed through the dialer the client was built with")
	}
}

func collectDeltas(t *testing.T, client *Client, req Request) (Response, []Delta) {
	t.Helper()
	var deltas []Delta
	response, err := client.Stream(context.Background(), req, func(delta Delta) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	return response, deltas
}

func TestStreamAssemblesContentAndUsage(t *testing.T) {
	body := sseData(
		`{"id":"1","object":"chat.completion.chunk","model":"deepseek-chat","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"content":"你好"},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"content":"，世界"},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}`,
		`[DONE]`,
	)
	client := newTestClient(t, streamHandler(t, body))

	response, deltas := collectDeltas(t, client, Request{Model: "deepseek-chat"})

	if response.Content != "你好，世界" {
		t.Errorf("Content = %q, want %q", response.Content, "你好，世界")
	}
	if response.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want stop", response.FinishReason)
	}
	if response.Usage != (Usage{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14}) {
		t.Errorf("Usage = %+v", response.Usage)
	}
	// The opening role-only chunk carries no text, so it must not be reported.
	if len(deltas) != 2 || deltas[0].Content != "你好" || deltas[1].Content != "，世界" {
		t.Errorf("deltas = %+v", deltas)
	}
}

func TestStreamAssemblesToolCallsFromFragments(t *testing.T) {
	body := sseData(
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":"","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"ask_user","arguments":""}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"quest"}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ion\":\"继续吗？\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	)
	client := newTestClient(t, streamHandler(t, body))

	response, _ := collectDeltas(t, client, Request{Model: "deepseek-chat"})

	if len(response.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %+v, want one call", response.ToolCalls)
	}
	call := response.ToolCalls[0]
	if call.ID != "call_1" || call.Name != "ask_user" {
		t.Errorf("call = %+v", call)
	}
	// The name arrives once, in the opening fragment; appending it would double it.
	if call.Arguments != `{"question":"继续吗？"}` {
		t.Errorf("Arguments = %q", call.Arguments)
	}
	if response.FinishReason != "tool_calls" {
		t.Errorf("FinishReason = %q", response.FinishReason)
	}
}

func TestStreamOrdersParallelToolCallsByIndex(t *testing.T) {
	body := sseData(
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"second","arguments":"{}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"first","arguments":"{}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	)
	client := newTestClient(t, streamHandler(t, body))

	response, _ := collectDeltas(t, client, Request{Model: "deepseek-chat"})

	if len(response.ToolCalls) != 2 {
		t.Fatalf("ToolCalls = %+v, want two calls", response.ToolCalls)
	}
	if response.ToolCalls[0].Name != "first" || response.ToolCalls[1].Name != "second" {
		t.Errorf("tool calls are not in index order: %+v", response.ToolCalls)
	}
}

func TestStreamReadsAUsageOnlyChunk(t *testing.T) {
	// Some OpenAI-compatible servers end with a usage-only chunk whose choices
	// array is empty, rather than attaching usage to the last content chunk.
	body := sseData(
		`{"choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}}`,
		`[DONE]`,
	)
	client := newTestClient(t, streamHandler(t, body))

	response, _ := collectDeltas(t, client, Request{Model: "deepseek-chat"})

	if response.Content != "hi" {
		t.Errorf("Content = %q, want hi", response.Content)
	}
	if response.Usage != (Usage{PromptTokens: 7, CompletionTokens: 2, TotalTokens: 9}) {
		t.Errorf("Usage = %+v", response.Usage)
	}
}

func TestStreamSkipsCommentsAndBlankLines(t *testing.T) {
	body := ": keep-alive\n\n\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	client := newTestClient(t, streamHandler(t, body))

	response, _ := collectDeltas(t, client, Request{Model: "deepseek-chat"})

	if response.Content != "ok" {
		t.Errorf("Content = %q, want ok", response.Content)
	}
}

func TestStreamReportsReasoningContent(t *testing.T) {
	body := sseData(
		`{"choices":[{"index":0,"delta":{"reasoning_content":"先想一想"},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"content":"答案"},"finish_reason":"stop"}]}`,
		`[DONE]`,
	)
	client := newTestClient(t, streamHandler(t, body))

	response, deltas := collectDeltas(t, client, Request{Model: "deepseek-reasoner"})

	if len(deltas) != 2 || deltas[0].Reasoning != "先想一想" || deltas[1].Content != "答案" {
		t.Errorf("deltas = %+v", deltas)
	}
	// Reasoning is streamed for display but is not part of the answer text.
	if response.Content != "答案" {
		t.Errorf("Content = %q, want 答案", response.Content)
	}
}

func TestStreamRejectsATruncatedAnswer(t *testing.T) {
	// The connection drops mid-answer: no terminator and no finish reason.
	body := sseData(`{"choices":[{"index":0,"delta":{"content":"half"},"finish_reason":null}]}`)
	client := newTestClient(t, streamHandler(t, body))

	_, err := client.Stream(context.Background(), Request{Model: "deepseek-chat"}, func(Delta) error { return nil })
	if !errors.Is(err, ErrIncompleteStream) {
		t.Fatalf("Stream() error = %v, want ErrIncompleteStream", err)
	}
}

func TestStreamAcceptsEndOfStreamWithAFinishReason(t *testing.T) {
	// A provider that omits the terminator but does report that it finished is
	// not truncated, so this must not be treated as a failure.
	body := sseData(`{"choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}]}`)
	client := newTestClient(t, streamHandler(t, body))

	response, _ := collectDeltas(t, client, Request{Model: "deepseek-chat"})

	if response.Content != "done" {
		t.Errorf("Content = %q, want done", response.Content)
	}
}
