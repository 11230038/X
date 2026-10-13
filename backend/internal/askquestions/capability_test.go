package askquestions

import (
	"context"
	"errors"
	"strings"
	"testing"

	"backend/internal/agentloop"
	"backend/internal/capability"
	"backend/internal/event"
	"backend/internal/llm"
	"backend/internal/tool"
)

// fakeRunContext stands in for the turn runtime: it records what the capability
// asked for and answers with a scripted result, which is the only way to test a
// capability without a model.
type fakeRunContext struct {
	emitter  *recordingEmitter
	tools    []tool.Definition
	messages []llm.Message
	requests []agentloop.Request
	result   agentloop.Result
	err      error
	// surveyed records that the capability reached for the turn's material.
	surveyed bool
}

func (f *fakeRunContext) Emitter() event.Emitter   { return f.emitter }
func (f *fakeRunContext) Tools() []tool.Definition { return f.tools }
func (f *fakeRunContext) Messages() []llm.Message  { return f.messages }

func (f *fakeRunContext) RunLoop(_ context.Context, req agentloop.Request) (agentloop.Result, error) {
	f.requests = append(f.requests, req)
	return f.result, f.err
}

// Ground records that it was asked. Where a turn's material is surveyed is the
// general assistant's business, so this capability reaching for it would be a
// behaviour change rather than a detail — one test asserts the call never comes.
func (f *fakeRunContext) Ground(context.Context, string) (capability.Grounding, error) {
	f.surveyed = true
	return capability.Grounding{}, nil
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

func testSettings() capability.Settings {
	return capability.Settings{
		Model:               "deepseek-chat",
		Temperature:         0.7,
		ContextBudgetTokens: 32000,
		MaxRounds:           4,
		StageMaxTokens:      map[string]int{StageResponding: 4096},
	}
}

func newTestCapability(t *testing.T, settings capability.Settings) *Capability {
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
		messages: []llm.Message{{Role: llm.RoleUser, Content: "帮我把这个函数改快一点。"}},
		result: agentloop.Result{
			Content: "你关注的是延迟还是吞吐？",
			Usage:   llm.Usage{PromptTokens: 30, CompletionTokens: 12, TotalTokens: 42},
			Rounds:  2,
		},
	}
}

func TestManifestIsASingleConfiguredStage(t *testing.T) {
	manifest := Manifest()

	if err := manifest.Validate(); err != nil {
		t.Fatalf("the manifest would not register: %v", err)
	}
	if manifest.Name != Name {
		t.Errorf("Name = %q, want %q", manifest.Name, Name)
	}
	// The stage list is what the environment's per-stage token limits are keyed
	// by (CFG-01), so one stage here means one limit to configure.
	if strings.Join(manifest.Stages, ",") != StageResponding {
		t.Errorf("Stages = %v", manifest.Stages)
	}
	// What it asks with is the always-on ask_user; declaring it here would make
	// the manifest claim a mount the policy decides.
	if len(manifest.Tools) != 0 {
		t.Errorf("Tools = %v, want the always-on set to cover them", manifest.Tools)
	}
}

func TestRunIsOneAnsweringStage(t *testing.T) {
	rc := newRunContext()

	if err := newTestCapability(t, testSettings()).Run(context.Background(), rc); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	want := []event.Type{event.TypeStageStart, event.TypeResult, event.TypeStageEnd}
	got := rc.emitter.types()
	if len(got) != len(want) {
		t.Fatalf("published %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("published %v, want %v", got, want)
		}
	}
	for _, published := range rc.emitter.events {
		if published.Stage != StageResponding {
			t.Errorf("an event was published under stage %q", published.Stage)
		}
	}
	if len(rc.requests) != 1 {
		t.Fatalf("the loop ran %d times", len(rc.requests))
	}
	// A turn that stops to ask does it inside this one run: the question is a
	// tool call, and the runtime hands the reply back as that call's result. So
	// the pause must not cost a second run, which would call the model twice for
	// one answer.
	if rc.requests[0].Stage != StageResponding {
		t.Errorf("the loop ran stage %q", rc.requests[0].Stage)
	}
}

func TestRunSendsTheInstructionBeforeTheHistory(t *testing.T) {
	rc := newRunContext()

	if err := newTestCapability(t, testSettings()).Run(context.Background(), rc); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	sent := rc.requests[0].Messages
	if len(sent) != 2 {
		t.Fatalf("sent %d messages, want the instruction and the history: %+v", len(sent), sent)
	}
	if sent[0].Role != llm.RoleSystem || sent[0].Content != systemPrompt {
		t.Errorf("the first message was %+v", sent[0])
	}
	if sent[1].Content != "帮我把这个函数改快一点。" {
		t.Errorf("the history was %+v", sent[1])
	}
	// Prepended per call, so the stored conversation stays a record of what was
	// said rather than of what this version's prompt asked for.
	if len(rc.messages) != 1 {
		t.Errorf("the capability rewrote its own history: %+v", rc.messages)
	}
}

func TestRunUsesThePlaneTheRuntimeComposed(t *testing.T) {
	rc := newRunContext()
	rc.tools = []tool.Definition{
		{Name: tool.AskUserName, Description: "ask"},
		{Name: tool.ReadSourceName, Description: "read"},
	}

	if err := newTestCapability(t, testSettings()).Run(context.Background(), rc); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// The capability neither adds to nor narrows the plane: it declares no tools,
	// so what the model may call is ask_user plus whatever this turn's material
	// mounts, and both arrive from the runtime.
	sent := rc.requests[0].Tools
	if len(sent) != len(rc.tools) {
		t.Fatalf("Tools = %+v, want the plane the runtime composed", sent)
	}
	for index := range rc.tools {
		if sent[index].Name != rc.tools[index].Name {
			t.Errorf("Tools = %+v", sent)
		}
	}
}

func TestRunSendsTheConfiguredStageSettings(t *testing.T) {
	rc := newRunContext()

	if err := newTestCapability(t, testSettings()).Run(context.Background(), rc); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	sent := rc.requests[0]
	if sent.Model != "deepseek-chat" || sent.Temperature != 0.7 || sent.MaxTokens != 4096 {
		t.Errorf("request = %+v", sent)
	}
	if sent.MaxRounds != 4 || sent.ContextBudgetTokens != 32000 {
		t.Errorf("request = %+v", sent)
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
	if result.Content != "你关注的是延迟还是吞吐？" {
		t.Errorf("result content = %q", result.Content)
	}
	usage, ok := result.Metadata[event.MetaUsageSummary].(map[string]any)
	if !ok {
		t.Fatalf("result metadata = %+v", result.Metadata)
	}
	// Two rounds: the question and the answer. Both are the turn's cost, and the
	// caller is told so.
	if usage["total_tokens"] != 42 || usage["rounds"] != 2 {
		t.Errorf("usage summary = %+v", usage)
	}
}

func TestRunReturnsTheLoopFailureUntouched(t *testing.T) {
	// These are the runtime's to turn into a terminal state and to report which
	// one it was, so the capability must not wrap or replace them.
	for _, want := range []error{context.Canceled, agentloop.ErrRoundsExhausted} {
		rc := newRunContext()
		rc.err = want

		err := newTestCapability(t, testSettings()).Run(context.Background(), rc)
		if !errors.Is(err, want) {
			t.Errorf("Run() error = %v, want %v", err, want)
		}
		if _, ok := rc.emitter.find(event.TypeResult); ok {
			t.Error("a failed run published a result")
		}
		if _, ok := rc.emitter.find(event.TypeStageEnd); ok {
			t.Error("a failed run announced the end of the stage that failed")
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
		change func(*capability.Settings)
	}{
		{"no model", func(s *capability.Settings) { s.Model = "" }},
		{"no context budget", func(s *capability.Settings) { s.ContextBudgetTokens = 0 }},
		{"no round limit", func(s *capability.Settings) { s.MaxRounds = 0 }},
		{"responding has no token limit", func(s *capability.Settings) { delete(s.StageMaxTokens, StageResponding) }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := testSettings()
			test.change(&settings)

			if _, err := New(settings); !errors.Is(err, capability.ErrInvalidSettings) {
				t.Errorf("New() error = %v, want ErrInvalidSettings", err)
			}
		})
	}
}

// TestThisCapabilityDoesNotSurveyTheTurnsMaterial pins the one thing this
// capability does *not* do with a turn's material. The general assistant
// surveys it in its exploring stage and publishes the sources that came of it
// (CHAT-09); here the material reaches the model the other way, through the
// tools this turn's attachments mount.
func TestThisCapabilityDoesNotSurveyTheTurnsMaterial(t *testing.T) {
	rc := newRunContext()

	if err := newTestCapability(t, testSettings()).Run(context.Background(), rc); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if rc.surveyed {
		t.Error("the capability surveyed the turn's material")
	}
	for _, kind := range []event.Type{event.TypeSources, event.TypeObservation} {
		if _, ok := rc.emitter.find(kind); ok {
			t.Errorf("published a %s event, which no stage of this capability is for", kind)
		}
	}
}
