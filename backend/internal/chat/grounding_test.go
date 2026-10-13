package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"backend/internal/capability"
	"backend/internal/event"
	"backend/internal/llm"
)

// surveyedGrounding is what the runtime answers with for a turn that carries
// material: two passages and one thing it could not read.
func surveyedGrounding() capability.Grounding {
	return capability.Grounding{
		Sources: []event.Source{
			{Index: 1, Title: "笔记.md", Snippet: "闭包捕获的是变量"},
			{Index: 2, Title: "讲义.pdf", Snippet: "词法作用域"},
		},
		Message:     llm.Message{Role: llm.RoleSystem, Content: "本轮携带的材料：\n- 附件 1《笔记.md》\n"},
		Unavailable: []string{"附件 2《图.png》没有读到：当前模型不支持读图"},
	}
}

func TestExploringPublishesWhatTheSurveyFound(t *testing.T) {
	rc := newRunContext()
	rc.grounding = surveyedGrounding()

	if err := newTestCapability(t, testSettings()).Run(context.Background(), rc); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// The order is the design's: the stage opens, the sources are shown, what
	// they amount to is shown, and only then does the stage end.
	want := []event.Type{
		event.TypeStageStart, event.TypeSources, event.TypeObservation, event.TypeStageEnd,
		event.TypeStageStart, event.TypeResult, event.TypeStageEnd,
	}
	got := rc.emitter.types()
	if len(got) != len(want) {
		t.Fatalf("published %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("published %v, want %v", got, want)
		}
	}
	for _, record := range rc.emitter.events[:3] {
		if record.Stage != StageExploring {
			t.Errorf("a survey event was published under stage %q", record.Stage)
		}
	}

	sources, ok := rc.emitter.events[1].Metadata[event.MetaSources].([]event.Source)
	if !ok || len(sources) != 2 || sources[1].Index != 2 {
		t.Errorf("the sources event carries %+v", rc.emitter.events[1].Metadata)
	}
	grounding, ok := rc.emitter.events[2].Metadata[event.MetaGrounding].(map[string]any)
	if !ok {
		t.Fatalf("the observation event carries %+v", rc.emitter.events[2].Metadata)
	}
	if grounding["source_count"] != 2 {
		t.Errorf("the observation reports %v sources, want 2", grounding["source_count"])
	}
	// What could not be read reaches the client as the sentences themselves, not
	// as a count: they are what a client shows, and the model was given the same
	// ones (ATT-04).
	unavailable, ok := grounding["unavailable"].([]string)
	if !ok || len(unavailable) != 1 || !strings.Contains(unavailable[0], "没有读到") {
		t.Errorf("the observation reports unavailable = %v", grounding["unavailable"])
	}
	if summary, _ := grounding["summary"].(string); !strings.Contains(summary, "2") {
		t.Errorf("the summary = %q, want it to say how much was found", summary)
	}
}

// TestATurnWithNoMaterialPublishesNothingExtra is the regression half: a turn
// that carries nothing gets the stream it got before there was a survey, and the
// model gets the messages it got.
func TestATurnWithNoMaterialPublishesNothingExtra(t *testing.T) {
	rc := newRunContext()

	if err := newTestCapability(t, testSettings()).Run(context.Background(), rc); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	for _, kind := range []event.Type{event.TypeSources, event.TypeObservation} {
		if _, ok := rc.emitter.find(kind); ok {
			t.Errorf("a turn carrying nothing published a %s event", kind)
		}
	}
	if len(rc.queries) != 1 {
		t.Fatalf("the runtime was asked to survey %d times", len(rc.queries))
	}
	if len(rc.requests[0].Messages) != 2 {
		t.Errorf("the model was sent %+v, want the prompt and the history", rc.requests[0].Messages)
	}
}

// TestASurveyThatFoundNothingStillSaysItLooked is the third case between the
// other two: the turn carries material, so the survey ran, and it found nothing.
// That is a fact a client has to be able to show — it is how "the material does
// not answer this" is told apart from a turn that was never given material at
// all (AC-06).
func TestASurveyThatFoundNothingStillSaysItLooked(t *testing.T) {
	rc := newRunContext()
	rc.grounding = capability.Grounding{
		Message: llm.Message{Role: llm.RoleSystem, Content: "本轮携带的材料：\n- 附件 1《笔记.md》\n"},
	}

	if err := newTestCapability(t, testSettings()).Run(context.Background(), rc); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	sources, ok := rc.emitter.events[1].Metadata[event.MetaSources].([]event.Source)
	if !ok || sources == nil {
		t.Fatalf("an empty survey serialises as %+v, want an empty list",
			rc.emitter.events[1].Metadata)
	}
	if len(sources) != 0 {
		t.Errorf("the survey found %+v", sources)
	}
	grounding, ok := rc.emitter.events[2].Metadata[event.MetaGrounding].(map[string]any)
	if !ok {
		t.Fatalf("the observation carries %+v", rc.emitter.events[2].Metadata)
	}
	if summary, _ := grounding["summary"].(string); !strings.Contains(summary, "没有找到") {
		t.Errorf("the summary = %q, want it to say the material was looked at", summary)
	}
}

func TestTheSurveyIsPutBetweenThePromptAndTheHistory(t *testing.T) {
	rc := newRunContext()
	rc.grounding = surveyedGrounding()

	if err := newTestCapability(t, testSettings()).Run(context.Background(), rc); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	sent := rc.requests[0].Messages
	if len(sent) != 3 {
		t.Fatalf("sent %d messages, want the prompt, the survey and the history: %+v", len(sent), sent)
	}
	if sent[0].Content != systemPrompt || sent[1].Content != rc.grounding.Message.Content {
		t.Errorf("the survey is not where the material belongs: %+v", sent)
	}
	// A system message is what the loop never trims, so the material survives a
	// conversation that outgrows its budget.
	if sent[1].Role != llm.RoleSystem {
		t.Errorf("the survey was sent as a %s message", sent[1].Role)
	}
	if sent[2].Content != "什么是闭包？" {
		t.Errorf("the history was %+v", sent[2])
	}
	// The survey is this turn's material, not part of what was said, so it is not
	// written back into the conversation.
	if len(rc.messages) != 1 {
		t.Errorf("the capability rewrote its own history: %+v", rc.messages)
	}
}

func TestTheSurveyIsAskedTheNewestThingTheUserSaid(t *testing.T) {
	rc := newRunContext()
	rc.messages = []llm.Message{
		{Role: llm.RoleUser, Content: "第一个问题"},
		{Role: llm.RoleAssistant, Content: "第一个回答"},
		{Role: llm.RoleUser, Content: "那闭包呢？"},
	}

	if err := newTestCapability(t, testSettings()).Run(context.Background(), rc); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(rc.queries) != 1 || rc.queries[0] != "那闭包呢？" {
		t.Errorf("the survey was asked %v, want this turn's own question", rc.queries)
	}
}

func TestExploringReturnsTheTurnsOwnFailure(t *testing.T) {
	// The survey fails only when the turn does — a cancellation, say — so the
	// capability must not swallow it and answer anyway.
	for _, want := range []error{context.Canceled, errors.New("the turn stopped")} {
		rc := newRunContext()
		rc.groundErr = want

		err := newTestCapability(t, testSettings()).Run(context.Background(), rc)
		if !errors.Is(err, want) {
			t.Errorf("Run() error = %v, want %v", err, want)
		}
		if len(rc.requests) != 0 {
			t.Error("the model was called although the survey could not be run")
		}
		if _, ok := rc.emitter.find(event.TypeResult); ok {
			t.Error("a failed survey still produced a result")
		}
	}
}
