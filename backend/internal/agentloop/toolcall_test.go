package agentloop

import (
	"context"
	"errors"
	"strings"
	"testing"

	"backend/internal/event"
	"backend/internal/llm"
	"backend/internal/tool"
)

func TestRunFeedsToolResultsBackToTheModel(t *testing.T) {
	ask := &stubTool{name: "ask_user", result: tool.Result{Content: "用 Redis", Success: true}}
	provider := &scriptedProvider{steps: []step{
		{response: asksFor(llm.ToolCall{ID: "call_1", Name: "ask_user", Arguments: `{"question":"用什么缓存？"}`})},
		{deltas: []llm.Delta{{Content: "好的"}}, response: answer("好的")},
	}}
	emitter := &recordingEmitter{}

	result, err := newLoop(t, provider, emitter, ask).Run(context.Background(), basicRequest())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.Rounds != 2 || result.Content != "好的" {
		t.Errorf("Rounds = %d, Content = %q", result.Rounds, result.Content)
	}
	// Usage is the whole run, not just the last call.
	if result.Usage.TotalTokens != 24 {
		t.Errorf("Usage = %+v, want both calls counted", result.Usage)
	}
	if ask.calls != 1 || ask.lastCall.ID != "call_1" {
		t.Errorf("tool saw %d calls, last %+v", ask.calls, ask.lastCall)
	}
	if !strings.Contains(string(ask.lastCall.Arguments), "用什么缓存") {
		t.Errorf("tool arguments = %s", ask.lastCall.Arguments)
	}

	// The next request must carry both the model's own call and its answer, or
	// the provider would reject the conversation as incomplete.
	sent := provider.requests[1].Messages
	if len(sent) != 3 {
		t.Fatalf("second request carried %d messages, want 3: %+v", len(sent), sent)
	}
	if sent[1].Role != llm.RoleAssistant || len(sent[1].ToolCalls) != 1 || sent[1].ToolCalls[0].ID != "call_1" {
		t.Errorf("assistant message = %+v", sent[1])
	}
	if sent[2].Role != llm.RoleTool || sent[2].ToolCallID != "call_1" || sent[2].Content != "用 Redis" {
		t.Errorf("tool message = %+v", sent[2])
	}

	// A call and its result are both published, so a client can show the work.
	calls := emitter.ofType(event.TypeToolCall)
	results := emitter.ofType(event.TypeToolResult)
	if len(calls) != 1 || calls[0].Content != "ask_user" {
		t.Fatalf("tool_call events = %+v", calls)
	}
	if args, ok := calls[0].Metadata[event.MetaArgs].(map[string]any); !ok || args["question"] != "用什么缓存？" {
		t.Errorf("tool_call metadata = %+v", calls[0].Metadata)
	}
	if len(results) != 1 || results[0].Content != "用 Redis" || results[0].Metadata["success"] != true {
		t.Errorf("tool_result events = %+v", results)
	}
}

func TestRunRefusesAToolThatIsNotInThePlane(t *testing.T) {
	// The model was shown a schema for it, but authorization is decided here.
	inPlane := &stubTool{name: "ask_user", result: tool.Result{Content: "ok", Success: true}}
	provider := &scriptedProvider{steps: []step{
		{response: asksFor(llm.ToolCall{ID: "call_1", Name: "exec", Arguments: `{"cmd":"rm -rf /"}`})},
		{deltas: []llm.Delta{{Content: "x"}}, response: answer("x")},
	}}
	emitter := &recordingEmitter{}

	result, err := newLoop(t, provider, emitter, inPlane).Run(context.Background(), basicRequest())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Content != "x" {
		t.Errorf("Content = %q, want the run to continue", result.Content)
	}
	if inPlane.calls != 0 {
		t.Errorf("the authorized tool was invoked %d times", inPlane.calls)
	}

	results := emitter.ofType(event.TypeToolResult)
	if len(results) != 1 || results[0].Metadata["success"] != false {
		t.Fatalf("tool_result events = %+v", results)
	}
	if !strings.Contains(results[0].Content, "exec") {
		t.Errorf("refusal text = %q, want it to name the tool", results[0].Content)
	}
}

func TestRunTurnsBadArgumentsIntoAFailureTheModelCanRead(t *testing.T) {
	ask := &stubTool{name: "ask_user", result: tool.Result{Content: "ok", Success: true}}
	provider := &scriptedProvider{steps: []step{
		{response: asksFor(llm.ToolCall{ID: "call_1", Name: "ask_user", Arguments: `{"question":`})},
		{deltas: []llm.Delta{{Content: "x"}}, response: answer("x")},
	}}
	emitter := &recordingEmitter{}

	result, err := newLoop(t, provider, emitter, ask).Run(context.Background(), basicRequest())
	if err != nil {
		t.Fatalf("Run() error = %v, want the run to continue", err)
	}
	if result.Content != "x" {
		t.Errorf("Content = %q", result.Content)
	}
	if ask.calls != 0 {
		t.Errorf("the tool was invoked %d times with unusable arguments", ask.calls)
	}
	results := emitter.ofType(event.TypeToolResult)
	if len(results) != 1 || !strings.Contains(results[0].Content, "not valid JSON") {
		t.Fatalf("tool_result events = %+v", results)
	}
	// A malformed call must still be recorded, including its arguments.
	calls := emitter.ofType(event.TypeToolCall)
	if len(calls) != 1 {
		t.Fatalf("tool_call events = %+v", calls)
	}
	if _, ok := calls[0].Metadata[event.MetaArgs].(string); !ok {
		t.Errorf("the malformed arguments were recorded as %T, want text so the event still marshals",
			calls[0].Metadata[event.MetaArgs])
	}
}

func TestRunSurvivesAToolThatFails(t *testing.T) {
	failing := &stubTool{name: "ask_user", err: errors.New("the database is unreachable")}
	provider := &scriptedProvider{steps: []step{
		{response: asksFor(llm.ToolCall{ID: "call_1", Name: "ask_user", Arguments: `{}`})},
		{deltas: []llm.Delta{{Content: "x"}}, response: answer("x")},
	}}
	emitter := &recordingEmitter{}
	loop, logs := newTestLoop(t, provider, emitter, failing)

	if _, err := loop.Run(context.Background(), basicRequest()); err != nil {
		t.Fatalf("Run() error = %v, want the run to continue", err)
	}

	results := emitter.ofType(event.TypeToolResult)
	if len(results) != 1 || results[0].Metadata["success"] != false {
		t.Fatalf("tool_result events = %+v", results)
	}
	// The model is told the tool failed; the operator gets the reason.
	if strings.Contains(results[0].Content, "database") {
		t.Errorf("the model was shown an internal failure: %q", results[0].Content)
	}
	if !strings.Contains(logs.String(), "the database is unreachable") {
		t.Errorf("the failure was not recorded: %s", logs.String())
	}
}

func TestRunSurvivesAToolThatPanics(t *testing.T) {
	panicking := &stubTool{name: "ask_user", panics: true}
	provider := &scriptedProvider{steps: []step{
		{response: asksFor(llm.ToolCall{ID: "call_1", Name: "ask_user", Arguments: `{}`})},
		{deltas: []llm.Delta{{Content: "x"}}, response: answer("x")},
	}}
	emitter := &recordingEmitter{}
	loop, logs := newTestLoop(t, provider, emitter, panicking)

	result, err := loop.Run(context.Background(), basicRequest())
	if err != nil {
		t.Fatalf("Run() error = %v, want a tool defect not to end the turn", err)
	}
	if result.Content != "x" {
		t.Errorf("Content = %q", result.Content)
	}

	results := emitter.ofType(event.TypeToolResult)
	if len(results) != 1 || results[0].Metadata["success"] != false {
		t.Fatalf("tool_result events = %+v", results)
	}
	if strings.Contains(results[0].Content, "a tool defect") {
		t.Errorf("the panic value reached the client: %q", results[0].Content)
	}
	if !strings.Contains(logs.String(), "a tool defect") {
		t.Errorf("the panic was not recorded: %s", logs.String())
	}
}

func TestRunNamesAnEmptyToolName(t *testing.T) {
	provider := &scriptedProvider{steps: []step{
		{response: asksFor(llm.ToolCall{ID: "call_1", Name: "", Arguments: `{}`})},
		{deltas: []llm.Delta{{Content: "x"}}, response: answer("x")},
	}}
	emitter := &recordingEmitter{}

	result, err := newLoop(t, provider, emitter).Run(context.Background(), basicRequest())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Content != "x" {
		t.Errorf("Content = %q", result.Content)
	}
	if results := emitter.ofType(event.TypeToolResult); len(results) != 1 || results[0].Metadata["success"] != false {
		t.Errorf("tool_result events = %+v", results)
	}
}

func TestRunRejectsAToolCallWithNoID(t *testing.T) {
	// Without the id the call cannot be answered, and the provider would reject
	// the next request rather than explain why.
	provider := &scriptedProvider{steps: []step{
		{response: asksFor(llm.ToolCall{Name: "ask_user", Arguments: `{}`})},
	}}

	_, err := newLoop(t, provider, &recordingEmitter{}).Run(context.Background(), basicRequest())
	if !errors.Is(err, ErrToolCallWithoutID) {
		t.Fatalf("Run() error = %v, want ErrToolCallWithoutID", err)
	}
}
