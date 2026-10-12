// Package llm talks to an OpenAI-compatible chat completions API. This version
// targets DeepSeek, but nothing here is provider-specific beyond the wire shape
// that DeepSeek and OpenAI share.
package llm

import (
	"context"
	"encoding/json"
)

// Role identifies who wrote a message.
type Role string

// The roles every OpenAI-compatible provider understands.
const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one entry in a chat completion request or response.
type Message struct {
	Role    Role
	Content string
	// ToolCalls is set on assistant messages that asked for tools.
	ToolCalls []ToolCall
	// ToolCallID answers a specific tool call on a tool message.
	ToolCallID string
}

// ToolCall is a tool invocation the model asked for.
type ToolCall struct {
	ID   string
	Name string
	// Arguments is the raw JSON the model produced. It is deliberately kept as
	// text: the model does not always emit valid JSON, so the caller must
	// validate it rather than trusting a decode.
	Arguments string
}

// ToolDefinition describes one callable tool.
type ToolDefinition struct {
	Name        string
	Description string
	// Parameters is the JSON Schema object for the arguments. A nil value omits
	// the field.
	Parameters json.RawMessage
}

// Request is a single chat completion.
type Request struct {
	Model       string
	Messages    []Message
	Tools       []ToolDefinition
	Temperature float64
	MaxTokens   int
}

// Delta is one incremental piece of a streaming response.
type Delta struct {
	Content string
	// Reasoning carries reasoning_content, which only thinking models emit.
	Reasoning string
}

// Usage reports token accounting for one call.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// Response is a fully assembled completion. Tool calls arrive as fragments on
// the stream, so the provider assembles them rather than making every caller
// repeat that work.
type Response struct {
	Content      string
	ToolCalls    []ToolCall
	Usage        Usage
	FinishReason string
}

// Provider is the seam every model call goes through. Streaming is the only
// mode: the runtime needs deltas as they arrive, and a non-streaming path would
// be a second, differently-behaved way to call a model.
type Provider interface {
	// Stream runs one completion, calling onDelta for each text fragment as it
	// arrives. If onDelta returns an error, the call stops and returns that
	// error. A cancelled ctx surfaces as ctx.Err() so callers can tell
	// cancellation apart from a provider failure.
	Stream(ctx context.Context, req Request, onDelta func(Delta) error) (Response, error)
}
