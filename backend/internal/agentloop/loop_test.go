package agentloop

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"backend/internal/event"
	"backend/internal/llm"
	"backend/internal/tool"
)

// step is one scripted answer from the fake provider.
type step struct {
	deltas   []llm.Delta
	response llm.Response
	err      error
}

// scriptedProvider stands in for a model. Replaying a written script is what
// makes multi-round behaviour testable; the runtime always uses the real
// provider.
type scriptedProvider struct {
	steps    []step
	requests []llm.Request
}

func (p *scriptedProvider) Stream(_ context.Context, req llm.Request, onDelta func(llm.Delta) error) (llm.Response, error) {
	p.requests = append(p.requests, req)
	if len(p.requests) > len(p.steps) {
		return llm.Response{}, errors.New("the script ran out of answers")
	}
	current := p.steps[len(p.requests)-1]
	for _, delta := range current.deltas {
		if err := onDelta(delta); err != nil {
			return llm.Response{}, err
		}
	}
	return current.response, current.err
}

type stubTool struct {
	name     string
	result   tool.Result
	err      error
	panics   bool
	calls    int
	lastCall tool.Call
}

func (s *stubTool) Definition() tool.Definition {
	return tool.Definition{Name: s.name, Description: "stub"}
}

func (s *stubTool) Invoke(_ context.Context, call tool.Call) (tool.Result, error) {
	s.calls++
	s.lastCall = call
	if s.panics {
		panic("a tool defect")
	}
	return s.result, s.err
}

// stubPlane is the authorized tool plane: only what it holds may run, whatever
// the model was shown.
type stubPlane struct {
	tools map[string]tool.Tool
}

func newPlane(tools ...tool.Tool) *stubPlane {
	plane := &stubPlane{tools: make(map[string]tool.Tool, len(tools))}
	for _, t := range tools {
		plane.tools[t.Definition().Name] = t
	}
	return plane
}

func (p *stubPlane) Tool(name string) (tool.Tool, bool) {
	t, ok := p.tools[name]
	return t, ok
}

type recordingEmitter struct {
	events []event.Event
	err    error
}

func (e *recordingEmitter) Emit(_ context.Context, published event.Event) error {
	if e.err != nil {
		return e.err
	}
	e.events = append(e.events, published)
	return nil
}

func (e *recordingEmitter) ofType(kind event.Type) []event.Event {
	var matched []event.Event
	for _, published := range e.events {
		if published.Type == kind {
			matched = append(matched, published)
		}
	}
	return matched
}

func (e *recordingEmitter) types() []event.Type {
	kinds := make([]event.Type, 0, len(e.events))
	for _, published := range e.events {
		kinds = append(kinds, published.Type)
	}
	return kinds
}

// newLoop wires a loop over a script with one tool in the plane.
func newLoop(t *testing.T, provider llm.Provider, emitter event.Emitter, tools ...tool.Tool) *Loop {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
	return New(provider, emitter, newPlane(tools...), logger, "chat")
}

// newTestLoop also returns the log, for tests that assert a failure was
// recorded rather than swallowed.
func newTestLoop(t *testing.T, provider llm.Provider, emitter event.Emitter, tools ...tool.Tool) (*Loop, *strings.Builder) {
	t.Helper()
	var logs strings.Builder
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	return New(provider, emitter, newPlane(tools...), logger, "chat"), &logs
}

func answer(content string) llm.Response {
	return llm.Response{Content: content, FinishReason: "stop", Usage: llm.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12}}
}

func asksFor(calls ...llm.ToolCall) llm.Response {
	return llm.Response{ToolCalls: calls, FinishReason: "tool_calls", Usage: llm.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12}}
}

func basicRequest() Request {
	return Request{
		Stage:               "responding",
		Model:               "deepseek-chat",
		Messages:            []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		Temperature:         0.7,
		MaxTokens:           1024,
		MaxRounds:           4,
		ContextBudgetTokens: 32000,
	}
}

func TestRunAnswersWithoutTools(t *testing.T) {
	provider := &scriptedProvider{steps: []step{{
		deltas:   []llm.Delta{{Content: "你好"}, {Content: "，世界"}},
		response: answer("你好，世界"),
	}}}
	emitter := &recordingEmitter{}
	loop := newLoop(t, provider, emitter)

	result, err := loop.Run(context.Background(), basicRequest())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.Content != "你好，世界" {
		t.Errorf("Content = %q", result.Content)
	}
	if result.Rounds != 1 {
		t.Errorf("Rounds = %d, want 1", result.Rounds)
	}
	if result.Usage.TotalTokens != 12 {
		t.Errorf("Usage = %+v", result.Usage)
	}

	// The deltas become content events as they arrive, stamped with the
	// capability and the stage they belong to.
	contents := emitter.ofType(event.TypeContent)
	if len(contents) != 2 {
		t.Fatalf("published %d content events, want 2: %v", len(contents), emitter.types())
	}
	if contents[0].Content != "你好" || contents[1].Content != "，世界" {
		t.Errorf("content events = %q, %q", contents[0].Content, contents[1].Content)
	}
	if contents[0].Source != "chat" || contents[0].Stage != "responding" {
		t.Errorf("content event = %+v", contents[0])
	}
}

// TestAnAnswerThatCallsNoToolIsHeldBackAndAskedAgain is the mastery failure this
// option was added for: a model that narrates a tool call it never made. The
// first answer is not streamed and not kept — the reader would take it as the
// turn's result — and the model is told, at the end of the conversation, that it
// must call something.
func TestAnAnswerThatCallsNoToolIsHeldBackAndAskedAgain(t *testing.T) {
	const nudge = "这一轮没有调用工具，重新回答。"
	provider := &scriptedProvider{steps: []step{
		{
			deltas:   []llm.Delta{{Content: "题目已经存进题库了"}},
			response: answer("题目已经存进题库了"),
		},
		{
			deltas: []llm.Delta{{Content: "先读状态"}},
			response: llm.Response{
				Content:      "先读状态",
				ToolCalls:    []llm.ToolCall{{ID: "call_1", Name: "state", Arguments: "{}"}},
				FinishReason: "tool_calls",
				Usage:        llm.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12},
			},
		},
		{
			deltas:   []llm.Delta{{Content: "好了"}},
			response: answer("好了"),
		},
	}}
	tool := &stubTool{name: "state", result: tool.Result{Success: true, Content: "ok"}}
	emitter := &recordingEmitter{}
	logger := slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
	loop := New(provider, emitter, newPlane(tool), logger, "mastery_path")

	request := basicRequest()
	request.NudgeWithoutToolCall = nudge
	result, err := loop.Run(context.Background(), request)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(provider.requests) != 3 {
		t.Fatalf("the model was called %d times, want the discarded answer plus two rounds", len(provider.requests))
	}
	// The note is the last thing the model reads, which is where a model follows
	// instructions from.
	second := provider.requests[1].Messages
	if last := second[len(second)-1]; last.Role != llm.RoleSystem || last.Content != nudge {
		t.Errorf("the second call ends with %+v, want the nudge", last)
	}
	// The discarded attempt leaves no trace in the answer or in the stream.
	if result.Content != "先读状态好了" {
		t.Errorf("Content = %q, want the answer after the nudge", result.Content)
	}
	contents := emitter.ofType(event.TypeContent)
	if len(contents) != 2 || contents[0].Content != "先读状态" || contents[1].Content != "好了" {
		t.Errorf("published content = %v, want only what came after the nudge", contents)
	}
	if tool.calls != 1 {
		t.Errorf("the tool ran %d times, want once", tool.calls)
	}
	// The discarded call's tokens were still spent, so they are still counted.
	if result.Rounds != 3 || result.Usage.TotalTokens != 36 {
		t.Errorf("Rounds = %d, Usage = %+v, want three calls and their full cost", result.Rounds, result.Usage)
	}
}

// TestTheNudgeIsGivenOnlyOnce keeps the guard from becoming a loop: a model that
// answers without tools twice is allowed to finish, because asking a third time
// cannot make it act.
func TestTheNudgeIsGivenOnlyOnce(t *testing.T) {
	provider := &scriptedProvider{steps: []step{
		{deltas: []llm.Delta{{Content: "第一次"}}, response: answer("第一次")},
		{deltas: []llm.Delta{{Content: "第二次"}}, response: answer("第二次")},
	}}
	emitter := &recordingEmitter{}
	logger := slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
	loop := New(provider, emitter, newPlane(), logger, "mastery_path")

	request := basicRequest()
	request.NudgeWithoutToolCall = "这一轮没有调用工具，重新回答。"
	result, err := loop.Run(context.Background(), request)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(provider.requests) != 2 {
		t.Errorf("the model was called %d times, want exactly one nudge", len(provider.requests))
	}
	if result.Content != "第二次" {
		t.Errorf("Content = %q", result.Content)
	}
	contents := emitter.ofType(event.TypeContent)
	if len(contents) != 1 || contents[0].Content != "第二次" {
		t.Errorf("published content = %v, want only the answer after the nudge", contents)
	}
}

func TestRunReportsReasoningAsThinking(t *testing.T) {
	provider := &scriptedProvider{steps: []step{{
		deltas:   []llm.Delta{{Reasoning: "先想一想"}, {Content: "答案"}},
		response: answer("答案"),
	}}}
	emitter := &recordingEmitter{}

	if _, err := newLoop(t, provider, emitter).Run(context.Background(), basicRequest()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	thinking := emitter.ofType(event.TypeThinking)
	if len(thinking) != 1 || thinking[0].Content != "先想一想" {
		t.Errorf("thinking events = %+v", thinking)
	}
	// Reasoned text is shown while the model works, but it is not the answer.
	if contents := emitter.ofType(event.TypeContent); len(contents) != 1 {
		t.Errorf("published %d content events, want only the answer", len(contents))
	}
}

func TestRunStopsAtTheRoundLimit(t *testing.T) {
	ask := &stubTool{name: "ask_user", result: tool.Result{Content: "ok", Success: true}}
	provider := &scriptedProvider{steps: []step{
		{response: asksFor(llm.ToolCall{ID: "call_1", Name: "ask_user", Arguments: `{}`})},
		{response: asksFor(llm.ToolCall{ID: "call_2", Name: "ask_user", Arguments: `{}`})},
		{response: asksFor(llm.ToolCall{ID: "call_3", Name: "ask_user", Arguments: `{}`})},
	}}
	req := basicRequest()
	req.MaxRounds = 2

	_, err := newLoop(t, provider, &recordingEmitter{}, ask).Run(context.Background(), req)
	if !errors.Is(err, ErrRoundsExhausted) {
		t.Fatalf("Run() error = %v, want ErrRoundsExhausted", err)
	}
	if len(provider.requests) != 2 {
		t.Errorf("the model was called %d times, want the limit of 2", len(provider.requests))
	}
}

func TestRunNeedsAtLeastOneRound(t *testing.T) {
	req := basicRequest()
	req.MaxRounds = 0

	provider := &scriptedProvider{}
	_, err := newLoop(t, provider, &recordingEmitter{}).Run(context.Background(), req)
	if err == nil {
		t.Fatal("Run() error = nil, want a rejection")
	}
	if len(provider.requests) != 0 {
		t.Error("the model was called despite there being no round to run")
	}
}

func TestRunPassesProviderFailuresThrough(t *testing.T) {
	providerErr := errors.New("the provider is unavailable")
	provider := &scriptedProvider{steps: []step{{err: providerErr}}}

	_, err := newLoop(t, provider, &recordingEmitter{}).Run(context.Background(), basicRequest())
	if !errors.Is(err, providerErr) {
		t.Errorf("Run() error = %v, want %v", err, providerErr)
	}
}

func TestRunFailsWhenAnEventCannotBeRecorded(t *testing.T) {
	// An event that was never written would be missing from a replay, so the
	// turn must end rather than carry on with a stream that has a hole in it.
	emitterErr := errors.New("the event store is unavailable")
	provider := &scriptedProvider{steps: []step{
		{deltas: []llm.Delta{{Content: "x"}}, response: answer("x")},
	}}

	_, err := newLoop(t, provider, &recordingEmitter{err: emitterErr}).Run(context.Background(), basicRequest())
	if !errors.Is(err, emitterErr) {
		t.Errorf("Run() error = %v, want %v", err, emitterErr)
	}
}

func TestRunDoesNotCallOutOnceCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ask := &stubTool{name: "ask_user"}
	provider := &scriptedProvider{steps: []step{
		{response: asksFor(llm.ToolCall{ID: "call_1", Name: "ask_user", Arguments: `{}`})},
	}}

	_, err := newLoop(t, provider, &recordingEmitter{}, ask).Run(ctx, basicRequest())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if len(provider.requests) != 0 {
		t.Error("the model was called on a cancelled turn")
	}
	if ask.calls != 0 {
		t.Error("a tool ran on a cancelled turn")
	}
}

func TestRunStopsBetweenRoundsWhenCancelled(t *testing.T) {
	// The turn is cancelled while the model is still answering, and the answer
	// then asks for a tool. Cancelling must stop the dispatch rather than let it
	// run, because the tool was authorized for a turn that has ended.
	ctx, cancel := context.WithCancel(context.Background())
	ask := &stubTool{name: "ask_user"}
	provider := &scriptedProvider{steps: []step{
		{
			deltas:   []llm.Delta{{Content: "叫我"}},
			response: asksFor(llm.ToolCall{ID: "call_1", Name: "ask_user", Arguments: `{}`}),
		},
		{deltas: []llm.Delta{{Content: "x"}}, response: answer("x")},
	}}
	emitter := &recordingEmitter{}
	loop := newLoop(t, provider, &cancellingEmitter{inner: emitter, cancel: cancel}, ask)

	_, err := loop.Run(ctx, basicRequest())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if len(provider.requests) != 1 {
		t.Errorf("the model was called %d times, want 1", len(provider.requests))
	}
	if ask.calls != 0 {
		t.Errorf("the tool ran %d times after cancellation", ask.calls)
	}
}

// cancellingEmitter cancels the turn as the first event is published, which is
// mid-response: the model is still streaming when the cancel arrives.
type cancellingEmitter struct {
	inner  *recordingEmitter
	cancel context.CancelFunc
}

func (e *cancellingEmitter) Emit(ctx context.Context, published event.Event) error {
	err := e.inner.Emit(ctx, published)
	e.cancel()
	return err
}

func TestRunSendsTheStageSettings(t *testing.T) {
	provider := &scriptedProvider{steps: []step{{deltas: []llm.Delta{{Content: "x"}}, response: answer("x")}}}
	req := basicRequest()
	req.Tools = []tool.Definition{{Name: "ask_user", Description: "ask", Parameters: []byte(`{"type":"object"}`)}}

	if _, err := newLoop(t, provider, &recordingEmitter{}).Run(context.Background(), req); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	sent := provider.requests[0]
	if sent.Model != "deepseek-chat" || sent.Temperature != 0.7 || sent.MaxTokens != 1024 {
		t.Errorf("request = %+v", sent)
	}
	if len(sent.Tools) != 1 || sent.Tools[0].Name != "ask_user" || sent.Tools[0].Description != "ask" {
		t.Errorf("tools = %+v", sent.Tools)
	}
	if string(sent.Tools[0].Parameters) != `{"type":"object"}` {
		t.Errorf("tool parameters = %s", sent.Tools[0].Parameters)
	}
}

func TestRunRechecksTheBudgetEachRound(t *testing.T) {
	// A large tool result grows the conversation between rounds, so the budget
	// has to be applied per call rather than once at the start.
	ask := &stubTool{name: "ask_user", result: tool.Result{Content: strings.Repeat("x", 600), Success: true}}
	provider := &scriptedProvider{steps: []step{
		{response: asksFor(llm.ToolCall{ID: "call_1", Name: "ask_user", Arguments: `{}`})},
		{deltas: []llm.Delta{{Content: "ok"}}, response: answer("ok")},
	}}
	req := basicRequest()
	req.Messages = []llm.Message{
		{Role: llm.RoleSystem, Content: "prompt"},
		{Role: llm.RoleUser, Content: strings.Repeat("old", 200)},
		{Role: llm.RoleAssistant, Content: strings.Repeat("old", 200)},
		{Role: llm.RoleUser, Content: "the current question"},
	}
	req.ContextBudgetTokens = 300

	if _, err := newLoop(t, provider, &recordingEmitter{}, ask).Run(context.Background(), req); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(provider.requests) != 2 {
		t.Fatalf("the model was called %d times, want 2", len(provider.requests))
	}

	// The oldest droppable message was over budget, so it went first and the
	// trim stopped there rather than emptying the conversation.
	first := provider.requests[0].Messages
	if len(first) != 3 {
		t.Fatalf("first call sent %d messages, want the prompt, the old answer and the question", len(first))
	}
	if first[0].Role != llm.RoleSystem || first[1].Role != llm.RoleAssistant || first[2].Content != "the current question" {
		t.Errorf("first call sent %+v", first)
	}
	if first[1].Content != strings.Repeat("old", 200) {
		t.Errorf("the wrong message was dropped: %+v", first[1])
	}

	// The tool result then grew the conversation, and the second call is trimmed
	// again rather than reusing the first call's messages.
	second := provider.requests[1].Messages
	if len(second) != 4 {
		t.Fatalf("second call sent %d messages, want the prompt, the question, the call and its result: %+v",
			len(second), second)
	}
	if second[0].Role != llm.RoleSystem || second[1].Content != "the current question" {
		t.Errorf("second call = %+v", second)
	}
	if second[2].Role != llm.RoleAssistant || len(second[2].ToolCalls) != 1 {
		t.Errorf("the model's own call was not replayed: %+v", second[2])
	}
	if second[3].Role != llm.RoleTool || len(second[3].Content) != 600 {
		t.Errorf("the tool result was not replayed whole: %+v", second[3])
	}
}
