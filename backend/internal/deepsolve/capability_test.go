package deepsolve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
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
	// script is what the model does during the loop. It is handed the context the
	// loop was called with, which is the seam the solve tools are found through:
	// driving them from here is what a model calling them looks like.
	script func(context.Context) error
}

func (f *fakeRunContext) Emitter() event.Emitter   { return f.emitter }
func (f *fakeRunContext) Tools() []tool.Definition { return f.tools }
func (f *fakeRunContext) Messages() []llm.Message  { return f.messages }

func (f *fakeRunContext) RunLoop(ctx context.Context, req agentloop.Request) (agentloop.Result, error) {
	f.requests = append(f.requests, req)
	if f.err != nil {
		return agentloop.Result{}, f.err
	}
	if f.script != nil {
		if err := f.script(ctx); err != nil {
			return agentloop.Result{}, err
		}
	}
	return f.result, nil
}

// Ground is not reached by this capability, and the zero grounding says so: a
// plan is not built out of the turn's material.
func (f *fakeRunContext) Ground(context.Context, string) (capability.Grounding, error) {
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

func (e *recordingEmitter) all() []event.Event { return e.events }

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

// progressEvents is every progress event, which on a turn that plans is the
// record of the plan moving.
func (e *recordingEmitter) progressEvents() []event.Event {
	reported := make([]event.Event, 0, len(e.events))
	for _, published := range e.events {
		if published.Type == event.TypeProgress {
			reported = append(reported, published)
		}
	}
	return reported
}

func contains(text, want string) bool { return strings.Contains(text, want) }

func testSettings() capability.Settings {
	return capability.Settings{
		Model:               "deepseek-chat",
		Temperature:         0.6,
		ContextBudgetTokens: 64000,
		MaxRounds:           12,
		StageMaxTokens:      map[string]int{StageResponding: 8192},
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

// solveTools is the plane the runtime composes for this capability, in the order
// the manifest declares it.
func solveTools() []tool.Definition {
	return []tool.Definition{
		{Name: tool.SolvePlanName, Description: "plan"},
		{Name: tool.SolveStepName, Description: "step"},
		{Name: tool.SolveReplanName, Description: "replan"},
		{Name: tool.SolveFinishName, Description: "finish"},
	}
}

func newRunContext() *fakeRunContext {
	return &fakeRunContext{
		emitter:  &recordingEmitter{},
		tools:    solveTools(),
		messages: []llm.Message{{Role: llm.RoleUser, Content: "把这三份报价里的总价算出来，再判断哪份最便宜。"}},
		result: agentloop.Result{
			Content: "最便宜的是第二份。",
			Usage:   llm.Usage{PromptTokens: 120, CompletionTokens: 40, TotalTokens: 160},
			Rounds:  5,
		},
	}
}

// script is one turn's use of the tools: plan, walk every step, finish. It is
// what the model does between the stage's start and its result, so it runs inside
// the loop, on the loop's context, exactly as a dispatched call does.
//
// A refusal is returned as an error rather than tolerated: these are the calls a
// model would make, so the plan refusing one of them is the test failing to
// describe a turn that could happen.
func script(steps []string) func(context.Context) error {
	return func(ctx context.Context) error {
		plan, err := json.Marshal(map[string]any{"steps": steps})
		if err != nil {
			return err
		}
		moves := []tool.Call{{Name: tool.SolvePlanName, Arguments: plan}}
		for range steps {
			moves = append(moves, tool.Call{Name: tool.SolveStepName, Arguments: json.RawMessage(`{}`)})
		}
		moves = append(moves, tool.Call{
			Name:      tool.SolveFinishName,
			Arguments: json.RawMessage(`{"summary": "算完了"}`),
		})

		for _, move := range moves {
			result, err := invocation(move.Name).Invoke(ctx, move)
			if err != nil {
				return fmt.Errorf("%s: %w", move.Name, err)
			}
			if !result.Success {
				return fmt.Errorf("%s was refused: %s", move.Name, result.Content)
			}
		}
		return nil
	}
}

// invocation returns the tool a call names, as the runtime's registry would.
func invocation(name string) tool.Tool {
	switch name {
	case tool.SolvePlanName:
		return tool.NewSolvePlanTool()
	case tool.SolveStepName:
		return tool.NewSolveStepTool()
	case tool.SolveReplanName:
		return tool.NewSolveReplanTool()
	default:
		return tool.NewSolveFinishTool()
	}
}

func TestManifestIsOneStageAndTheSolveTools(t *testing.T) {
	manifest := Manifest()

	if err := manifest.Validate(); err != nil {
		t.Fatalf("the manifest would not register: %v", err)
	}
	if manifest.Name != Name || Name != "deep_solve" {
		t.Errorf("Name = %q", manifest.Name)
	}
	// One stage, so one token limit to configure: the steps of a plan are not
	// stages, they are progress inside this one (CFG-01).
	if strings.Join(manifest.Stages, ",") != StageResponding {
		t.Errorf("Stages = %v", manifest.Stages)
	}
	// The tools are declared rather than left to the policy: a plan is meaningless
	// outside this capability's prompt, so a turn running anything else must not be
	// offered solve_plan.
	want := strings.Join([]string{tool.SolvePlanName, tool.SolveStepName, tool.SolveReplanName, tool.SolveFinishName}, ",")
	if got := strings.Join(manifest.Tools, ","); got != want {
		t.Errorf("Tools = %v, want %v", manifest.Tools, want)
	}
}

func TestRunIsOneAnsweringStageWithTheInstructionFirst(t *testing.T) {
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
	sent := rc.requests[0].Messages
	if len(sent) != 2 || sent[0].Role != llm.RoleSystem || sent[0].Content != systemPrompt {
		t.Fatalf("the instruction was not sent before the history: %+v", sent)
	}
	if sent[1].Content != "把这三份报价里的总价算出来，再判断哪份最便宜。" {
		t.Errorf("the history was %+v", sent[1])
	}
}

func TestATurnThatDoesNotPlanPublishesNoProgress(t *testing.T) {
	// A simple question is answered without the tools, which the prompt says as
	// much as the model does. Nothing here may publish a step of a plan that was
	// never made.
	rc := newRunContext()

	if err := newTestCapability(t, testSettings()).Run(context.Background(), rc); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if reported := rc.emitter.progressEvents(); len(reported) != 0 {
		t.Errorf("a turn with no plan published %d progress events", len(reported))
	}
}

func TestTheSolveToolsFindThePlanOfTheTurnRunningThem(t *testing.T) {
	rc := newRunContext()
	rc.script = script([]string{"读三份报价", "算总价", "比出最便宜的"})

	if err := newTestCapability(t, testSettings()).Run(context.Background(), rc); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// One event per move, and each one says where the turn is: the plan opened at
	// the first step, walked forward once per solve_step, and stayed put for the
	// calls that took no step — the third solve_step, which found nothing left to
	// mark, and the closing solve_finish. A plan never reports a step past its
	// last one.
	want := []struct {
		step, total int
		label       string
	}{
		{1, 3, "读三份报价"},
		{2, 3, "算总价"},
		{3, 3, "比出最便宜的"},
		{3, 3, "比出最便宜的"},
		{3, 3, "比出最便宜的"},
	}
	reported := rc.emitter.progressEvents()
	if len(reported) != len(want) {
		t.Fatalf("published %d progress events, want %d: %v", len(reported), len(want), rc.emitter.types())
	}
	for index, expected := range want {
		metadata := reported[index].Metadata
		if metadata[event.MetaStep] != expected.step ||
			metadata[event.MetaTotal] != expected.total ||
			metadata[event.MetaLabel] != expected.label {
			t.Errorf("event %d carries %+v, want %d/%d %s",
				index, metadata, expected.step, expected.total, expected.label)
		}
	}
	// The plan is published between the call and its result, so a client reading
	// the stream in order sees the step before the model is told it may move on.
	if kinds := rc.emitter.types(); kinds[0] != event.TypeStageStart || kinds[len(kinds)-1] != event.TypeStageEnd {
		t.Errorf("the events are %v", kinds)
	}
}

func TestTwoTurnsDoNotShareAPlan(t *testing.T) {
	// The capability is one per process while a plan is one per turn, which is the
	// mistake this pins: a plan on a field would be two concurrent turns writing
	// into the same steps, and each user would watch the other's progress.
	capabilityUnderTest := newTestCapability(t, testSettings())
	plans := [][]string{
		{"读三份报价", "算总价"},
		{"查一下汇率"},
		{"列出条件", "查政策", "写结论"},
	}

	contexts := make([]*fakeRunContext, len(plans))
	var wg sync.WaitGroup
	for index, steps := range plans {
		contexts[index] = newRunContext()
		contexts[index].script = script(steps)
		wg.Add(1)
		go func(rc *fakeRunContext) {
			defer wg.Done()
			if err := capabilityUnderTest.Run(context.Background(), rc); err != nil {
				t.Errorf("Run() error = %v", err)
			}
		}(contexts[index])
	}
	wg.Wait()

	for index, steps := range plans {
		reported := contexts[index].emitter.progressEvents()
		if len(reported) == 0 {
			t.Fatalf("turn %d published nothing", index)
		}
		for _, published := range reported {
			label, _ := published.Metadata[event.MetaLabel].(string)
			if published.Metadata[event.MetaTotal] != len(steps) || !slices.Contains(steps, label) {
				t.Fatalf("turn %d reports %v, which is not a step of its own %v",
					index, published.Metadata, steps)
			}
		}
	}
}

func TestRunSendsThePlaneTheRuntimeComposed(t *testing.T) {
	// The four solve tools arrive from the runtime, which composes them from the
	// manifest; the capability itself mounts nothing. A capability that narrowed
	// the plane here would be hiding a tool the manifest promised.
	rc := newRunContext()
	rc.tools = append(solveTools(), tool.Definition{Name: tool.AskUserName, Description: "ask"})

	if err := newTestCapability(t, testSettings()).Run(context.Background(), rc); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

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

func TestRunReturnsTheLoopFailureUntouched(t *testing.T) {
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
