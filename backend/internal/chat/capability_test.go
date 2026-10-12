package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"backend/internal/agentloop"
	"backend/internal/event"
	"backend/internal/llm"
	"backend/internal/tool"
)

// fakeRunContext stands in for the turn runtime. It records what the capability
// asked for and answers with a scripted result, which is the only way to test a
// capability without a model.
type fakeRunContext struct {
	emitter  *recordingEmitter
	tools    []tool.Definition
	messages []llm.Message
	requests []agentloop.Request
	result   agentloop.Result
	err      error
}

func (f *fakeRunContext) Emitter() event.Emitter   { return f.emitter }
func (f *fakeRunContext) Tools() []tool.Definition { return f.tools }
func (f *fakeRunContext) Messages() []llm.Message  { return f.messages }

func (f *fakeRunContext) RunLoop(_ context.Context, req agentloop.Request) (agentloop.Result, error) {
	f.requests = append(f.requests, req)
	return f.result, f.err
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

func (e *recordingEmitter) types() []event.Type {
	kinds := make([]event.Type, 0, len(e.events))
	for _, published := range e.events {
		kinds = append(kinds, published.Type)
	}
	return kinds
}

func (e *recordingEmitter) find(kind event.Type) (event.Event, bool) {
	for _, published := range e.events {
		if published.Type == kind {
			return published, true
		}
	}
	return event.Event{}, false
}

func testSettings() Settings {
	return Settings{
		Model:               "deepseek-chat",
		Temperature:         0.7,
		ContextBudgetTokens: 32000,
		MaxRounds:           4,
		StageMaxTokens:      map[string]int{StageExploring: 1024, StageResponding: 4096},
	}
}

func newTestCapability(t *testing.T, settings Settings) *Capability {
	t.Helper()
	created, err := New(settings)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return created
}

func newRunContext() *fakeRunContext {
	return &fakeRunContext{
		emitter:  &recordingEmitter{},
		tools:    []tool.Definition{{Name: tool.AskUserName, Description: "ask"}},
		messages: []llm.Message{{Role: llm.RoleUser, Content: "什么是闭包？"}},
		result: agentloop.Result{
			Content: "闭包是捕获了外部变量的函数。",
			Usage:   llm.Usage{PromptTokens: 30, CompletionTokens: 12, TotalTokens: 42},
			Rounds:  1,
		},
	}
}

func TestManifestDeclaresTheConfiguredStages(t *testing.T) {
	manifest := Manifest()

	if err := manifest.Validate(); err != nil {
		t.Fatalf("the manifest would not register: %v", err)
	}
	if manifest.Name != Name {
		t.Errorf("Name = %q, want %q", manifest.Name, Name)
	}
	// CFG-01 configures a token limit per stage name, so the names here and the
	// names in the environment have to be the same list.
	if strings.Join(manifest.Stages, ",") != "exploring,responding" {
		t.Errorf("Stages = %v", manifest.Stages)
	}
	// ask_user is mounted by the always-on tier; declaring it here as well would
	// make the manifest claim something the mount policy decides.
	if len(manifest.Tools) != 0 {
		t.Errorf("Tools = %v, want the always-on set to cover them", manifest.Tools)
	}
}

func TestRunWalksTheStagesInOrder(t *testing.T) {
	rc := newRunContext()

	if err := newTestCapability(t, testSettings()).Run(context.Background(), rc); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	want := []event.Type{
		event.TypeStageStart,
		event.TypeStageEnd,
		event.TypeStageStart,
		event.TypeResult,
		event.TypeStageEnd,
	}
	got := rc.emitter.types()
	if len(got) != len(want) {
		t.Fatalf("published %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("published %v, want %v", got, want)
		}
	}
	if rc.emitter.events[0].Stage != StageExploring || rc.emitter.events[2].Stage != StageResponding {
		t.Errorf("stage boundaries named the wrong stages: %+v", rc.emitter.events)
	}
}

func TestRunDoesNotCallTheModelWhileExploring(t *testing.T) {
	rc := newRunContext()

	if err := newTestCapability(t, testSettings()).Run(context.Background(), rc); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(rc.requests) != 1 {
		t.Fatalf("the loop ran %d times, want only the answering stage", len(rc.requests))
	}
	if rc.requests[0].Stage != StageResponding {
		t.Errorf("the loop ran stage %q", rc.requests[0].Stage)
	}
}

func TestRunSendsTheStageSettings(t *testing.T) {
	rc := newRunContext()

	if err := newTestCapability(t, testSettings()).Run(context.Background(), rc); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	sent := rc.requests[0]
	if sent.Model != "deepseek-chat" || sent.Temperature != 0.7 {
		t.Errorf("request = %+v", sent)
	}
	// The limit is the answering stage's, not the exploring stage's.
	if sent.MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d, want the responding limit", sent.MaxTokens)
	}
	if sent.MaxRounds != 4 || sent.ContextBudgetTokens != 32000 {
		t.Errorf("request = %+v", sent)
	}
	if len(sent.Tools) != 1 || sent.Tools[0].Name != tool.AskUserName {
		t.Errorf("Tools = %+v, want the plane the runtime composed", sent.Tools)
	}
}

func TestRunPutsThePromptBeforeTheHistory(t *testing.T) {
	rc := newRunContext()

	if err := newTestCapability(t, testSettings()).Run(context.Background(), rc); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	sent := rc.requests[0].Messages
	if len(sent) != 2 {
		t.Fatalf("sent %d messages, want the prompt and the history: %+v", len(sent), sent)
	}
	if sent[0].Role != llm.RoleSystem || sent[0].Content != systemPrompt {
		t.Errorf("the first message was %+v", sent[0])
	}
	if sent[1].Role != llm.RoleUser || sent[1].Content != "什么是闭包？" {
		t.Errorf("the history was %+v", sent[1])
	}
	// The prompt is prepended per call, so the stored history stays a record of
	// what was actually said.
	if len(rc.messages) != 1 {
		t.Errorf("the capability rewrote its own history: %+v", rc.messages)
	}
}

func TestRunReportsTheAnswerAndItsCost(t *testing.T) {
	rc := newRunContext()

	if err := newTestCapability(t, testSettings()).Run(context.Background(), rc); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	result, ok := rc.emitter.find(event.TypeResult)
	if !ok {
		t.Fatalf("no result event: %v", rc.emitter.types())
	}
	if result.Content != "闭包是捕获了外部变量的函数。" {
		t.Errorf("result content = %q", result.Content)
	}
	usage, ok := result.Metadata[event.MetaUsageSummary].(map[string]any)
	if !ok {
		t.Fatalf("result metadata = %+v", result.Metadata)
	}
	if usage["total_tokens"] != 42 || usage["prompt_tokens"] != 30 || usage["rounds"] != 1 {
		t.Errorf("usage summary = %+v", usage)
	}
}

func TestRunReturnsTheLoopFailureUntouched(t *testing.T) {
	// The runtime turns these into a terminal state and reports which one it
	// was, so the capability must not wrap or replace them.
	for _, want := range []error{context.Canceled, agentloop.ErrRoundsExhausted} {
		rc := newRunContext()
		rc.err = want

		err := newTestCapability(t, testSettings()).Run(context.Background(), rc)
		if !errors.Is(err, want) {
			t.Errorf("Run() error = %v, want %v", err, want)
		}
		// A failed stage has no answer to report and no end to announce. The
		// exploring stage did finish, so it is the responding boundary that
		// must be missing.
		if _, ok := rc.emitter.find(event.TypeResult); ok {
			t.Error("a failed run published a result")
		}
		for _, published := range rc.emitter.events {
			if published.Type == event.TypeStageEnd && published.Stage == StageResponding {
				t.Error("a failed run announced the end of the stage that failed")
			}
		}
	}
}

func TestRunStopsWhenAnEventCannotBeRecorded(t *testing.T) {
	emitterErr := errors.New("the event store is unavailable")
	rc := newRunContext()
	rc.emitter.err = emitterErr

	err := newTestCapability(t, testSettings()).Run(context.Background(), rc)
	if !errors.Is(err, emitterErr) {
		t.Fatalf("Run() error = %v, want %v", err, emitterErr)
	}
	if len(rc.requests) != 0 {
		t.Error("the model was called despite the turn being unable to record its events")
	}
}

func TestNewRejectsTuningItCouldNotRunWith(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Settings)
	}{
		{"no model", func(s *Settings) { s.Model = "" }},
		{"blank model", func(s *Settings) { s.Model = "  " }},
		{"negative temperature", func(s *Settings) { s.Temperature = -0.1 }},
		{"temperature above the range", func(s *Settings) { s.Temperature = 2.1 }},
		{"no context budget", func(s *Settings) { s.ContextBudgetTokens = 0 }},
		{"no round limit", func(s *Settings) { s.MaxRounds = 0 }},
		{"exploring has no token limit", func(s *Settings) { delete(s.StageMaxTokens, StageExploring) }},
		{"responding has no token limit", func(s *Settings) { delete(s.StageMaxTokens, StageResponding) }},
		{"a stage limit of zero", func(s *Settings) { s.StageMaxTokens[StageResponding] = 0 }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := testSettings()
			test.change(&settings)

			if _, err := New(settings); !errors.Is(err, ErrInvalidSettings) {
				t.Errorf("New() error = %v, want ErrInvalidSettings", err)
			}
		})
	}
}

func TestNewAcceptsCompleteTuning(t *testing.T) {
	if _, err := New(testSettings()); err != nil {
		t.Errorf("New() error = %v", err)
	}
}
