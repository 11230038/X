package deepquestion

import (
	"context"
	"strings"
	"sync"
	"testing"

	"backend/internal/agentloop"
	"backend/internal/capability"
	"backend/internal/event"
	"backend/internal/llm"
	"backend/internal/tool"
)

// fakeRun is one turn as the capability sees it: the conversation, whatever the
// runtime's survey found, and a model that always answers the same thing.
//
// Every call the capability makes is written down — the events it published, the
// survey it asked for, the model calls it ran — because the things this package
// has to get right are all about which of them happened and in what order.
type fakeRun struct {
	// visible is the conversation the runtime hands over.
	visible []llm.Message
	// grounding is what the survey returns; groundErr is what it fails with.
	grounding capability.Grounding
	groundErr error
	// result is what the model answers with.
	result agentloop.Result

	events   []event.Event
	queries  []string
	requests []agentloop.Request
}

var _ capability.RunContext = (*fakeRun)(nil)

func (f *fakeRun) Emitter() event.Emitter { return f }
func (f *fakeRun) Emit(_ context.Context, record event.Event) error {
	f.events = append(f.events, record)
	return nil
}
func (f *fakeRun) Tools() []tool.Definition { return nil }
func (f *fakeRun) Messages() []llm.Message  { return f.visible }

func (f *fakeRun) RunLoop(_ context.Context, request agentloop.Request) (agentloop.Result, error) {
	f.requests = append(f.requests, request)
	return f.result, nil
}

func (f *fakeRun) Ground(_ context.Context, query string) (capability.Grounding, error) {
	f.queries = append(f.queries, query)
	return f.grounding, f.groundErr
}

// kinds lists the event types a run published, in order.
func (f *fakeRun) kinds() []event.Type {
	types := make([]event.Type, 0, len(f.events))
	for _, record := range f.events {
		types = append(types, record.Type)
	}
	return types
}

// of returns the one event of a type, failing if there is not exactly one.
func (f *fakeRun) of(t *testing.T, kind event.Type) event.Event {
	t.Helper()
	found := make([]event.Event, 0, 1)
	for _, record := range f.events {
		if record.Type == kind {
			found = append(found, record)
		}
	}
	if len(found) != 1 {
		t.Fatalf("the run published %d %s events, want one", len(found), kind)
	}
	return found[0]
}

// testCapability is the capability under test, built the way the process builds
// it: every declared stage has a token limit, including the one that calls no
// model — the manifest declares the stages the deployment's tuning is written
// against, and a stage left out of the tuning is a capability the process
// refuses to start with (CFG-01).
func testCapability(t *testing.T, limits Limits) *Capability {
	t.Helper()
	built, err := New(capability.Settings{
		Model:               "test-model",
		Temperature:         0.6,
		ContextBudgetTokens: 32000,
		MaxRounds:           3,
		StageMaxTokens: map[string]int{
			StageIdeation:   1024,
			StageGeneration: 4096,
		},
	}, limits)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return built
}

// testRun is a turn asking for ten multiple-choice questions about a chapter,
// with two passages of material to write them from.
func testRun(asked string) *fakeRun {
	return &fakeRun{
		visible: []llm.Message{
			{Role: llm.RoleUser, Content: "先聊聊别的"},
			{Role: llm.RoleAssistant, Content: "好"},
			{Role: llm.RoleUser, Content: asked},
		},
		grounding: capability.Grounding{
			Sources: []event.Source{
				{Index: 1, FileID: "file_1", DocumentID: "doc_1", Title: "第三章讲义.md", Snippet: "…"},
				{Index: 2, FileID: "file_1", DocumentID: "doc_1", Title: "第三章讲义.md", Snippet: "…"},
			},
			Message: llm.Message{Role: llm.RoleSystem, Content: "本轮携带的材料：第三章讲义.md"},
		},
		result: agentloop.Result{Content: "十道题都出好了。"},
	}
}

func TestTheStagesAreTheOnesTheProductFixed(t *testing.T) {
	manifest := Manifest()
	if manifest.Name != Name {
		t.Errorf("Manifest().Name = %q, want %q", manifest.Name, Name)
	}
	// The two names are configured against, so their order and their spelling are
	// part of the deployment's contract and not this package's business.
	if got := strings.Join(manifest.Stages, " → "); got != "ideation → generation" {
		t.Errorf("Manifest().Stages = %q, want ideation → generation", got)
	}
	if len(manifest.Tools) != 1 || manifest.Tools[0] != tool.SaveQuestionName {
		t.Errorf("Manifest().Tools = %v, want save_question and nothing else", manifest.Tools)
	}
}

func TestTheTurnLaysThePlanOutBeforeItWritesAnything(t *testing.T) {
	run := testRun("针对第三章出 10 道选择题，难度中等")
	capabilityUnderTest := testCapability(t, Limits{Default: 5, Max: 30})
	if err := capabilityUnderTest.Run(context.Background(), run); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	want := []event.Type{
		event.TypeStageStart, event.TypeSources, event.TypeObservation, event.TypeStageEnd,
		event.TypeStageStart, event.TypeResult, event.TypeStageEnd,
	}
	if got := run.kinds(); len(got) != len(want) {
		t.Fatalf("the run published %v, want %v", got, want)
	} else {
		for index, kind := range want {
			if got[index] != kind {
				t.Fatalf("the run published %v, want %v", got, want)
			}
		}
	}
	// The stages the boundaries belong to are the capability's two, in order:
	// a client reading the stream sees the plan's stage close before the writing
	// one opens.
	boundaries := []struct {
		kind  event.Type
		stage string
	}{
		{event.TypeStageStart, StageIdeation},
		{event.TypeSources, StageIdeation},
		{event.TypeObservation, StageIdeation},
		{event.TypeStageEnd, StageIdeation},
		{event.TypeStageStart, StageGeneration},
		{event.TypeResult, StageGeneration},
		{event.TypeStageEnd, StageGeneration},
	}
	for index, boundary := range boundaries {
		if run.events[index].Stage != boundary.stage {
			t.Errorf("event %d (%s) is published under %q, want %q",
				index, boundary.kind, run.events[index].Stage, boundary.stage)
		}
	}

	// One model call, and it is the writing stage: the plan was worked out by
	// rules and the survey, so nothing about it is worth a round trip.
	if len(run.requests) != 1 {
		t.Fatalf("the turn made %d model calls, want one", len(run.requests))
	}
	if run.requests[0].Stage != StageGeneration {
		t.Errorf("the model was called in %q, want %q", run.requests[0].Stage, StageGeneration)
	}
	// And the survey was asked the user's own question — the newest thing they
	// said, not the conversation's opening line.
	if len(run.queries) != 1 || run.queries[0] != "针对第三章出 10 道选择题，难度中等" {
		t.Errorf("the survey was asked %v, want this turn's question", run.queries)
	}
}

func TestThePlanTravelsWithTheObservation(t *testing.T) {
	run := testRun("针对第三章出 10 道选择题，难度中等")
	if err := testCapability(t, Limits{Default: 5, Max: 30}).Run(context.Background(), run); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	observation := run.of(t, event.TypeObservation)
	plan, ok := observation.Metadata[event.MetaQuestionPlan].(map[string]any)
	if !ok {
		t.Fatalf("the observation carries no question_plan: %+v", observation.Metadata)
	}
	for key, want := range map[string]any{
		"count":          10,
		"difficulty":     "medium",
		"material_id":    "file_1",
		"material_title": "第三章讲义.md",
	} {
		if got := plan[key]; got != want {
			t.Errorf("question_plan[%q] = %v, want %v", key, got, want)
		}
	}
	if got := plan["question_types"]; !equalStrings(got, []string{"choice"}) {
		t.Errorf("question_plan[question_types] = %v, want the kind that was asked for", got)
	}
	if got := plan["categories"]; !equalStrings(got, []string{"第三章"}) {
		t.Errorf("question_plan[categories] = %v, want the chapter the request named", got)
	}
	// The survey rides along in the same payload, in the shape the general
	// assistant publishes it in, so a client reads one observation shape.
	grounding, ok := observation.Metadata[event.MetaGrounding].(map[string]any)
	if !ok || grounding["source_count"] != 2 {
		t.Errorf("the observation's grounding = %v, want the survey's own summary", observation.Metadata)
	}
	// And the same thing in a sentence, for a client that shows what the turn is
	// about to do without reading the structured form.
	if !strings.Contains(observation.Content, "我准备出 10 道题") ||
		!strings.Contains(observation.Content, "取材于《第三章讲义.md》") {
		t.Errorf("the observation says %q, want the plan in one line", observation.Content)
	}
}

func TestThePlanIsComputedAndNotAskedFor(t *testing.T) {
	// A model that answers with a set of its own does not get to change one: the
	// plan is the payload of the observation and the number the questions are
	// counted against, and if it came from the model it would be a sentence
	// nothing could be checked against.
	run := testRun("出 10 道题")
	run.result = agentloop.Result{Content: "我只出了 3 道，而且我想改成判断题。"}
	if err := testCapability(t, Limits{Default: 5, Max: 30}).Run(context.Background(), run); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	plan := run.of(t, event.TypeObservation).Metadata[event.MetaQuestionPlan].(map[string]any)
	if plan["count"] != 10 {
		t.Errorf("question_plan[count] = %v, want the count the request named", plan["count"])
	}
	// The same number is what the model is told to write, and it is told before
	// it answers — the instruction is built from the plan, not from the answer.
	instruction := run.requests[0].Messages[0]
	if instruction.Role != llm.RoleSystem || !strings.Contains(instruction.Content, "数量：10 道") {
		t.Errorf("the writing stage was instructed with %q, want the plan's own count", instruction.Content)
	}
}

func TestTheMaterialGoesInFrontOfTheModel(t *testing.T) {
	run := testRun("出 10 道题")
	if err := testCapability(t, Limits{Default: 5, Max: 30}).Run(context.Background(), run); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// Instruction, then the survey of this turn's material, then the
	// conversation: the two system messages are what the answer stands on, and
	// the loop's trimming drops the oldest end first — which is why they are not
	// stored with the history.
	messages := run.requests[0].Messages
	if len(messages) != 5 {
		t.Fatalf("the model was given %d messages, want instruction, survey and three of history", len(messages))
	}
	if messages[1].Content != "本轮携带的材料：第三章讲义.md" {
		t.Errorf("the second message is %q, want the survey", messages[1].Content)
	}
	if messages[2].Content != "先聊聊别的" {
		t.Errorf("the third message is %q, want the conversation", messages[2].Content)
	}
}

func TestATurnWithNothingToReadPublishesNoSources(t *testing.T) {
	// The rule the general assistant's exploring stage already follows: a turn
	// carrying nothing gets no sources event, because an empty citation list is a
	// list to cite from. The plan is published either way — it exists whether or
	// not there was material to ground it.
	run := testRun("出 5 道填空题")
	run.grounding = capability.Grounding{}
	if err := testCapability(t, Limits{Default: 5, Max: 30}).Run(context.Background(), run); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	for _, record := range run.events {
		if record.Type == event.TypeSources {
			t.Fatalf("a turn with no material published a sources event: %+v", record)
		}
	}
	observation := run.of(t, event.TypeObservation)
	plan := observation.Metadata[event.MetaQuestionPlan].(map[string]any)
	if plan["count"] != 5 || plan["material_id"] != "" || plan["material_title"] != "" {
		t.Errorf("question_plan = %v, want the request's count and no material", plan)
	}
	if strings.Contains(observation.Content, "取材于") {
		t.Errorf("the observation says %q, want it not to name material nobody read", observation.Content)
	}
	// No material also means no survey message in front of the model: a blank
	// system turn would be this capability saying something it does not know.
	if got := run.requests[0].Messages; len(got) != 4 {
		t.Errorf("the model was given %d messages, want instruction and history", len(got))
	}
}

func TestAnIdleStageWithNothingToAskIsNotInvented(t *testing.T) {
	// ideation asks the survey one question and the model none, so a turn with
	// no conversation to read must still produce a plan out of the defaults
	// rather than failing or calling a model to make one up.
	run := &fakeRun{}
	if err := testCapability(t, Limits{Default: 7, Max: 30}).Run(context.Background(), run); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	plan := run.of(t, event.TypeObservation).Metadata[event.MetaQuestionPlan].(map[string]any)
	if plan["count"] != 7 {
		t.Errorf("question_plan[count] = %v, want the deployment's default", plan["count"])
	}
}

func TestTwoTurnsDoNotShareAPlan(t *testing.T) {
	// The capability is registered once for the process while a plan belongs to
	// one turn, so a plan kept on a field would be one plan that every concurrent
	// turn wrote into — and two users would watch each other's questions. The
	// test is the same shape the other two capabilities' are, because the mistake
	// is the same one and it is invisible until two turns overlap.
	shared := testCapability(t, Limits{Default: 5, Max: 30})
	turns := []*fakeRun{testRun("出 3 道填空题"), testRun("出 8 道选择题")}
	wanted := []int{3, 8}

	var wg sync.WaitGroup
	for index, run := range turns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := shared.Run(context.Background(), run); err != nil {
				t.Errorf("Run() error = %v", err)
				return
			}
			_ = index
		}()
	}
	wg.Wait()

	for index, run := range turns {
		plan := run.of(t, event.TypeObservation).Metadata[event.MetaQuestionPlan].(map[string]any)
		if plan["count"] != wanted[index] {
			t.Errorf("turn %d's plan says %v questions, want %d", index, plan["count"], wanted[index])
		}
	}
}

func TestALimitsPairWrittenTheWrongWayRoundIsRefused(t *testing.T) {
	// The service would hand out a default it then refuses, and the request that
	// asked for nothing in particular is the one that gets the error.
	if _, err := New(capability.Settings{
		Model: "test-model", ContextBudgetTokens: 1000, MaxRounds: 1,
		StageMaxTokens: map[string]int{StageIdeation: 1, StageGeneration: 1},
	}, Limits{Default: 30, Max: 10}); err == nil {
		t.Error("New() with a maximum below the default error = nil, want a refusal")
	}
}

// equalStrings reports whether a metadata value is the list that was expected.
// Metadata travels as `any`, so a list is compared as one rather than asserted
// into a type the test then has to be sure about.
func equalStrings(value any, wanted []string) bool {
	list, ok := value.([]string)
	if !ok {
		return false
	}
	if len(list) != len(wanted) {
		return false
	}
	for index := range list {
		if list[index] != wanted[index] {
			return false
		}
	}
	return true
}
