package agentloop

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"

	"backend/internal/event"
	"backend/internal/llm"
	"backend/internal/tool"
)

// toolFailureText is what a model is told when a tool could not run. It is
// deliberately about the call rather than about the failure: whatever went
// wrong inside the tool is the operator's to read, not the model's, and not the
// client's.
const toolFailureText = "The tool could not be run. Try a different approach or answer without it."

// dispatch runs one tool call and returns the message that answers it.
//
// Every path returns a message. A refused or failed call still becomes a tool
// result the model can read and react to (TOOL-04); dropping it would leave the
// model waiting on a call that never answers, and the next request would be
// rejected for the missing tool result.
func (l *Loop) dispatch(ctx context.Context, req Request, call llm.ToolCall) (llm.Message, error) {
	// Without the id there is nothing to answer, and the provider would reject
	// the next request rather than say so clearly.
	if call.ID == "" {
		return llm.Message{}, fmt.Errorf("%w (%s)", ErrToolCallWithoutID, call.Name)
	}

	if err := l.publishCall(ctx, req, call); err != nil {
		return llm.Message{}, err
	}
	result := l.invoke(ctx, req, call)
	if err := l.publishResult(ctx, req, call, result); err != nil {
		return llm.Message{}, err
	}

	return llm.Message{
		Role:       llm.RoleTool,
		ToolCallID: call.ID,
		Content:    result.Content,
	}, nil
}

// invoke resolves and runs a call, turning every failure into a result.
//
// Authorization is re-checked here, at the moment of dispatch, rather than
// trusting that the call was authorized when the plane was composed: the plane
// the model was shown and the plane it may call are not the same thing
// (TOOL-07).
func (l *Loop) invoke(ctx context.Context, req Request, call llm.ToolCall) tool.Result {
	if call.Name == "" {
		return failure("The call named no tool.")
	}
	t, ok := l.plane.Tool(call.Name)
	if !ok {
		return failure(fmt.Sprintf("There is no tool named %q available here.", call.Name))
	}
	// The model is not obliged to produce valid JSON. Saying so is more useful
	// to it than failing the turn.
	if !json.Valid([]byte(call.Arguments)) {
		return failure(fmt.Sprintf("The arguments for %q were not valid JSON.", call.Name))
	}

	return l.run(ctx, t, call)
}

// run calls the tool, converting a panic or an error into a failure result.
func (l *Loop) run(ctx context.Context, t tool.Tool, call llm.ToolCall) (result tool.Result) {
	defer func() {
		if recovered := recover(); recovered != nil {
			// A tool that panics is a defect, and a defect must not end the
			// turn. The stack goes to the log, where an operator can read it;
			// the model is told only that the tool failed.
			l.logger.Error("tool panicked",
				"tool", call.Name,
				"call_id", call.ID,
				"panic", recovered,
				"stack", string(debug.Stack()))
			result = failure(toolFailureText)
		}
	}()

	invoked, err := t.Invoke(ctx, tool.Call{
		ID:        call.ID,
		Name:      call.Name,
		Arguments: json.RawMessage(call.Arguments),
	})
	if err != nil {
		l.logger.Error("tool failed", "tool", call.Name, "call_id", call.ID, "error", err)
		return failure(toolFailureText)
	}
	return invoked
}

// publishCall reports a call before it runs, so a client sees the tool start
// rather than wait for it in silence.
func (l *Loop) publishCall(ctx context.Context, req Request, call llm.ToolCall) error {
	return l.publish(ctx, event.Event{
		Stage:   req.Stage,
		Type:    event.TypeToolCall,
		Content: call.Name,
		Metadata: map[string]any{
			event.MetaArgs: metadataArgs(call.Arguments),
			"call_id":      call.ID,
		},
	})
}

// publishResult reports what came back, with the failure flag so a client does
// not have to read the text to know the call did not work.
func (l *Loop) publishResult(ctx context.Context, req Request, call llm.ToolCall, result tool.Result) error {
	metadata := map[string]any{
		"call_id": call.ID,
		"success": result.Success,
	}
	for key, value := range result.Metadata {
		metadata[key] = value
	}
	return l.publish(ctx, event.Event{
		Stage:    req.Stage,
		Type:     event.TypeToolResult,
		Content:  result.Content,
		Metadata: metadata,
	})
}

// metadataArgs renders the model's arguments for an event.
//
// A decoded object is what a client expects to find, but the model does not
// always produce one, and an event that cannot be marshalled would fail the
// write. Raw text is the fallback so that a malformed call is still recorded.
func metadataArgs(raw string) any {
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return raw
	}
	return decoded
}

func failure(content string) tool.Result {
	return tool.Result{Content: content, Success: false}
}
