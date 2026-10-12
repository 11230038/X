package llm

import "encoding/json"

// The request and response shapes of the OpenAI-compatible chat completions
// API. They are kept separate from the domain types in types.go so that a wire
// change stays in one file.

type wireRequest struct {
	Model       string      `json:"model"`
	Messages    []wireMsg   `json:"messages"`
	Tools       []wireTool  `json:"tools,omitempty"`
	Temperature float64     `json:"temperature"`
	MaxTokens   int         `json:"max_tokens"`
	Stream      bool        `json:"stream"`
	StreamOpts  *wireStream `json:"stream_options,omitempty"`
}

// wireStream asks for the token accounting that streaming otherwise omits.
// DeepSeek rejects it unless stream is true, which is always the case here.
type wireStream struct {
	IncludeUsage bool `json:"include_usage"`
}

type wireMsg struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

// wireToolCall is an assistant message's own tool call being sent back, so its
// function carries arguments rather than a description.
type wireToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function wireCallArgs `json:"function"`
}

type wireCallArgs struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// wireTool declares a tool the model may call.
type wireTool struct {
	Type     string      `json:"type"`
	Function wireToolDef `json:"function"`
}

type wireToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type streamChunk struct {
	Choices []streamChoice `json:"choices"`
	Usage   *usageWire     `json:"usage"`
}

type streamChoice struct {
	Delta        streamDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

type streamDelta struct {
	Content   string             `json:"content"`
	Reasoning string             `json:"reasoning_content"`
	ToolCalls []toolCallFragment `json:"tool_calls"`
}

type toolCallFragment struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type usageWire struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func (u usageWire) usage() Usage {
	return Usage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
	}
}

func newWireRequest(req Request) wireRequest {
	messages := make([]wireMsg, 0, len(req.Messages))
	for _, message := range req.Messages {
		wire := wireMsg{
			Role:       string(message.Role),
			Content:    message.Content,
			ToolCallID: message.ToolCallID,
		}
		for _, call := range message.ToolCalls {
			wire.ToolCalls = append(wire.ToolCalls, wireToolCall{
				ID:       call.ID,
				Type:     "function",
				Function: wireCallArgs{Name: call.Name, Arguments: call.Arguments},
			})
		}
		messages = append(messages, wire)
	}

	tools := make([]wireTool, 0, len(req.Tools))
	for _, definition := range req.Tools {
		tools = append(tools, wireTool{
			Type: "function",
			Function: wireToolDef{
				Name:        definition.Name,
				Description: definition.Description,
				Parameters:  definition.Parameters,
			},
		})
	}

	return wireRequest{
		Model:       req.Model,
		Messages:    messages,
		Tools:       tools,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      true,
		StreamOpts:  &wireStream{IncludeUsage: true},
	}
}
