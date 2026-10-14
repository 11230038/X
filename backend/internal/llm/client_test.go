package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const testAPIKey = "unit-test-api-key"

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
	client, err := NewClient(server.URL, oneKey(testAPIKey), 5*time.Second)
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

func TestStreamSendsTheDocumentedRequest(t *testing.T) {
	var captured map[string]any
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+testAPIKey {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		if got := r.URL.Path; got != chatCompletionsPath {
			t.Errorf("path = %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := io.WriteString(w, sseData(`{"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`, `[DONE]`)); err != nil {
			t.Errorf("write stream: %v", err)
		}
	})

	_, err := client.Stream(context.Background(), Request{
		Model:       "deepseek-chat",
		Messages:    []Message{{Role: RoleSystem, Content: "sys"}, {Role: RoleUser, Content: "hi"}},
		Tools:       []ToolDefinition{{Name: "ask_user", Description: "ask", Parameters: json.RawMessage(`{"type":"object"}`)}},
		Temperature: 0.7,
		MaxTokens:   4096,
	}, func(Delta) error { return nil })
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	if captured["model"] != "deepseek-chat" {
		t.Errorf("model = %v", captured["model"])
	}
	if captured["stream"] != true {
		t.Errorf("stream = %v, want true", captured["stream"])
	}
	// Usage is only reported when the request asks for it.
	options, ok := captured["stream_options"].(map[string]any)
	if !ok || options["include_usage"] != true {
		t.Errorf("stream_options = %v, want include_usage true", captured["stream_options"])
	}
	if captured["temperature"] != 0.7 {
		t.Errorf("temperature = %v", captured["temperature"])
	}
	if captured["max_tokens"] != float64(4096) {
		t.Errorf("max_tokens = %v", captured["max_tokens"])
	}

	messages, ok := captured["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("messages = %v", captured["messages"])
	}
	if first := messages[0].(map[string]any); first["role"] != "system" || first["content"] != "sys" {
		t.Errorf("first message = %v", first)
	}

	tools, ok := captured["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v", captured["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Errorf("tool type = %v", tool["type"])
	}
	function := tool["function"].(map[string]any)
	if function["name"] != "ask_user" || function["description"] != "ask" {
		t.Errorf("tool function = %v", function)
	}
	if parameters, ok := function["parameters"].(map[string]any); !ok || parameters["type"] != "object" {
		t.Errorf("tool parameters = %v", function["parameters"])
	}
}

func TestStreamOmitsToolsWhenThereAreNone(t *testing.T) {
	var captured map[string]any
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := io.WriteString(w, sseData(`{"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`, `[DONE]`)); err != nil {
			t.Errorf("write stream: %v", err)
		}
	})

	if _, err := client.Stream(context.Background(), Request{Model: "m"}, func(Delta) error { return nil }); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if _, present := captured["tools"]; present {
		t.Errorf("tools = %v, want the field to be omitted", captured["tools"])
	}
}

func TestStreamSendsAssistantToolCallsBack(t *testing.T) {
	var captured map[string]any
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := io.WriteString(w, sseData(`{"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`, `[DONE]`)); err != nil {
			t.Errorf("write stream: %v", err)
		}
	})

	_, err := client.Stream(context.Background(), Request{
		Model: "deepseek-chat",
		Messages: []Message{
			{Role: RoleUser, Content: "hi"},
			{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call_1", Name: "ask_user", Arguments: `{"question":"q"}`}}},
			{Role: RoleTool, ToolCallID: "call_1", Content: "yes"},
		},
	}, func(Delta) error { return nil })
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	messages := captured["messages"].([]any)
	assistant := messages[1].(map[string]any)
	calls, ok := assistant["tool_calls"].([]any)
	if !ok || len(calls) != 1 {
		t.Fatalf("assistant tool_calls = %v", assistant["tool_calls"])
	}
	call := calls[0].(map[string]any)
	if call["id"] != "call_1" || call["type"] != "function" {
		t.Errorf("tool call = %v", call)
	}
	if function := call["function"].(map[string]any); function["name"] != "ask_user" || function["arguments"] != `{"question":"q"}` {
		t.Errorf("tool call function = %v", call["function"])
	}

	tool := messages[2].(map[string]any)
	if tool["role"] != "tool" || tool["tool_call_id"] != "call_1" || tool["content"] != "yes" {
		t.Errorf("tool message = %v", tool)
	}
}

func TestStreamReturnsAProviderErrorWithoutTheAPIKey(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		// The body repeats the key back, which is what a gateway that reports
		// what it received does. The error must not carry it further.
		body := `{"error":{"message":"Authentication Fails for key ` + testAPIKey + `","type":"authentication_error","code":"invalid_api_key"}}`
		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("write body: %v", err)
		}
	})

	_, err := client.Stream(context.Background(), Request{Model: "deepseek-chat"}, func(Delta) error { return nil })
	if err == nil {
		t.Fatal("Stream() error = nil, want a provider error")
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("Stream() error = %v, want *HTTPError", err)
	}
	if httpErr.StatusCode != http.StatusUnauthorized || httpErr.Code != "invalid_api_key" {
		t.Errorf("HTTPError = %+v", httpErr)
	}
	if httpErr.Retryable() {
		t.Error("Retryable() = true, want false for a rejected key")
	}
	if strings.Contains(err.Error(), testAPIKey) {
		t.Fatal("provider error exposed the API key")
	}
}

func TestStreamHandlesAnUnrecognisedErrorBody(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		if _, err := io.WriteString(w, "upstream down"); err != nil {
			t.Errorf("write body: %v", err)
		}
	})

	_, err := client.Stream(context.Background(), Request{Model: "deepseek-chat"}, func(Delta) error { return nil })
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("Stream() error = %v, want *HTTPError", err)
	}
	if httpErr.StatusCode != http.StatusBadGateway || !httpErr.Retryable() {
		t.Errorf("HTTPError = %+v, retryable = %v", httpErr, httpErr.Retryable())
	}
	if strings.Contains(err.Error(), testAPIKey) {
		t.Fatal("provider error exposed the API key")
	}
}

func TestHTTPErrorRetryable(t *testing.T) {
	for _, tt := range []struct {
		status int
		want   bool
	}{
		{status: http.StatusBadRequest, want: false},
		{status: http.StatusUnauthorized, want: false},
		{status: http.StatusPaymentRequired, want: false},
		{status: http.StatusTooManyRequests, want: true},
		{status: http.StatusInternalServerError, want: true},
		{status: http.StatusBadGateway, want: true},
	} {
		if got := (&HTTPError{StatusCode: tt.status}).Retryable(); got != tt.want {
			t.Errorf("status %d Retryable() = %v, want %v", tt.status, got, tt.want)
		}
	}
}

func TestStreamStopsWhenTheConsumerFails(t *testing.T) {
	stop := errors.New("consumer stopped")
	body := sseData(
		`{"choices":[{"index":0,"delta":{"content":"a"},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"content":"b"},"finish_reason":null}]}`,
		`[DONE]`,
	)
	client := newTestClient(t, streamHandler(t, body))

	calls := 0
	_, err := client.Stream(context.Background(), Request{Model: "deepseek-chat"}, func(Delta) error {
		calls++
		return stop
	})

	if !errors.Is(err, stop) {
		t.Fatalf("Stream() error = %v, want %v", err, stop)
	}
	if calls != 1 {
		t.Errorf("onDelta called %d times, want 1", calls)
	}
}

func TestStreamReturnsCancellationAsAContextError(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		// Hold the stream open so the answer never finishes.
		<-release
	})
	// Registered after the server's own cleanup so it runs before it: the server
	// will not shut down while a handler is still blocked.
	t.Cleanup(func() { once.Do(func() { close(release) }) })

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := client.Stream(ctx, Request{Model: "deepseek-chat"}, func(Delta) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Stream() error = %v, want context.Canceled", err)
	}
}

// newClientWithKeys points a client at a handler over a chosen key source, so
// that a case about what the client asks for and reports back can watch the
// source rather than the wire.
func newClientWithKeys(t *testing.T, handler http.HandlerFunc, keys KeySource) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, keys, 5*time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}

// A client built without anything to authenticate with is refused where it is
// built. Left until the first call it would send an unauthenticated request and
// surface as a provider error inside somebody's turn.
func TestAClientWithoutAKeySourceIsRefused(t *testing.T) {
	client, err := NewClient("http://127.0.0.1:1", nil, time.Second)
	if !errors.Is(err, ErrNoKeySource) {
		t.Fatalf("NewClient() error = %v, want ErrNoKeySource", err)
	}
	if client != nil {
		t.Error("a client was returned alongside the failure")
	}
}

// A key is taken once per call rather than once per client: a client that kept
// one would keep using a credential the pool has since rested.
func TestEachCallTakesItsOwnKey(t *testing.T) {
	keys := &watchingKeys{}
	client := newClientWithKeys(t, streamHandler(t, sseData(`{"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`)), keys)

	for range 2 {
		if _, err := client.Stream(context.Background(), Request{Model: "deepseek-chat"}, func(Delta) error { return nil }); err != nil {
			t.Fatalf("Stream() error = %v", err)
		}
	}
	if len(keys.handed) != 2 {
		t.Errorf("keys taken = %d, want one per call", len(keys.handed))
	}
	if !slices.Equal(keys.succeeded, []string{testAPIKey, testAPIKey}) {
		t.Errorf("succeeded = %v, want both calls reported as answered", keys.succeeded)
	}
	if len(keys.limited) != 0 {
		t.Errorf("rate limited = %v, want nothing: no call was refused", keys.limited)
	}
}

// A refusal that is about the key reaches the pool, and it is the only refusal
// that does: a provider that is unwell is not this credential's problem.
func TestOnlyARateLimitIsLaidAtTheKeysDoor(t *testing.T) {
	for _, tt := range []struct {
		name        string
		status      int
		wantLimited bool
	}{
		{name: "rate limited", status: http.StatusTooManyRequests, wantLimited: true},
		{name: "the provider is unwell", status: http.StatusInternalServerError},
		{name: "the key is rejected", status: http.StatusUnauthorized},
		{name: "the request is bad", status: http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			keys := &watchingKeys{}
			client := newClientWithKeys(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
			}, keys)

			if _, err := client.Stream(context.Background(), Request{Model: "deepseek-chat"}, func(Delta) error { return nil }); err == nil {
				t.Fatal("Stream() error = nil, want the provider's refusal")
			}
			want := []string{}
			if tt.wantLimited {
				want = []string{testAPIKey}
			}
			if !slices.Equal(keys.limited, want) {
				t.Errorf("rate limited = %v, want %v", keys.limited, want)
			}
			if len(keys.succeeded) != 0 {
				t.Errorf("succeeded = %v, want nothing: the call did not come back", keys.succeeded)
			}
		})
	}
}

// A call that never reached the provider says nothing about the key, so it says
// nothing: resting a credential for a transport fault would take a working key
// out of service for a reason that had nothing to do with it.
func TestACallThatNeverArrivedSaysNothingAboutTheKey(t *testing.T) {
	keys := &watchingKeys{}
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := server.URL
	server.Close()

	client, err := NewClient(address, keys, time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := client.Stream(context.Background(), Request{Model: "deepseek-chat"}, func(Delta) error { return nil }); err == nil {
		t.Fatal("Stream() error = nil, want a transport failure")
	}
	if len(keys.succeeded) != 0 || len(keys.limited) != 0 {
		t.Errorf("the key was reported on: %v / %v", keys.succeeded, keys.limited)
	}
}

// The provider's verdict on a credential is its status line. How the answer it
// then sends behaves is about the answer, so a stream that dies halfway still
// counts as the key having worked.
func TestAStatusLineIsTheVerdictOnTheKey(t *testing.T) {
	keys := &watchingKeys{}
	client := newClientWithKeys(t, streamHandler(t, sseData(`{"choices":[{"index":0,"delta":{"content":"half"},"finish_reason":null}]}`)), keys)

	_, err := client.Stream(context.Background(), Request{Model: "deepseek-chat"}, func(Delta) error { return nil })
	if !errors.Is(err, ErrIncompleteStream) {
		t.Fatalf("Stream() error = %v, want ErrIncompleteStream", err)
	}
	if !slices.Equal(keys.succeeded, []string{testAPIKey}) {
		t.Errorf("succeeded = %v, want the key credited: the provider accepted it", keys.succeeded)
	}
	if len(keys.limited) != 0 {
		t.Errorf("rate limited = %v, want nothing", keys.limited)
	}
}
