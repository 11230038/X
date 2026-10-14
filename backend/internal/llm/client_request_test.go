package llm

// What the client sends: the documented request body, and the two optional
// fields it must omit rather than spell empty.
//
// What comes back is client_test.go's business; these cases only look at the
// bytes on the way out.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

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
