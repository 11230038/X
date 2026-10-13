package turn

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"backend/internal/capability"
	"backend/internal/event"
	"backend/internal/kb"
	"backend/internal/llm"
	"backend/internal/parse"
)

// surveyed is a turn run carrying one attachment and one collection, with the
// material already resolved — the state the survey is always called in.
func surveyed(t *testing.T, fixture *runnerFixture, declaration Declaration) *turnRun {
	t.Helper()
	run := &turnRun{runner: fixture.runner, turn: Turn{ID: "turn_1"}, declared: declaration}
	run.resolveMaterial(context.Background())
	return run
}

func TestTheSurveyNumbersWhatItFoundAndFoldsItIntoOneMessage(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.materials.add("lib_a", "doc_a", "笔记.md")
	fixture.materials.located = []event.Source{source("doc_a", "闭包捕获的是变量")}
	fixture.materials.retrieved = kb.Retrieval{Sources: []event.Source{source("doc_b", "词法作用域")}}
	run := surveyed(t, fixture, Declaration{Attachments: []string{"lib_a"}, KnowledgeBases: []string{"kb_a"}})

	grounding, err := run.ground(context.Background(), "闭包是什么？")
	if err != nil {
		t.Fatalf("ground() error = %v", err)
	}

	// One list, numbered once: an attachment's passage and a retrieved chunk are
	// the same list to the model, and a number in an answer has to mean one
	// passage (AIQ-02).
	if len(grounding.Sources) != 2 {
		t.Fatalf("the survey found %+v", grounding.Sources)
	}
	for index, want := range []int{1, 2} {
		if grounding.Sources[index].Index != want {
			t.Errorf("source %d is numbered %d, want %d", index, grounding.Sources[index].Index, want)
		}
	}
	if run.sources != 2 {
		t.Errorf("the turn counted %d sources, want 2", run.sources)
	}

	message := grounding.Message
	if message.Role != llm.RoleSystem {
		t.Errorf("the survey is a %s message, want the service's own account", message.Role)
	}
	for _, want := range []string{"附件 1", "笔记.md", "闭包捕获的是变量", "词法作用域", "[1]", "[2]"} {
		if !strings.Contains(message.Content, want) {
			t.Errorf("the survey message does not mention %q:\n%s", want, message.Content)
		}
	}
	if !strings.Contains(message.Content, "没有找到") && strings.Count(message.Content, "[") == 0 {
		t.Errorf("the survey message lists no sources:\n%s", message.Content)
	}
	// The locators stay on the server: what the model is handed is a number, a
	// title and the text, and nothing it could get wrong (AIQ-02).
	if strings.Contains(message.Content, "doc_a") || strings.Contains(message.Content, "chunk") {
		t.Errorf("the survey message carries this service's bookkeeping:\n%s", message.Content)
	}
}

func TestATurnCarryingNothingHasNothingToSurvey(t *testing.T) {
	fixture := newRunnerFixture(t)
	run := surveyed(t, fixture, Declaration{})

	grounding, err := run.ground(context.Background(), "在吗")
	if err != nil {
		t.Fatalf("ground() error = %v", err)
	}

	if len(grounding.Sources) != 0 || grounding.Message.Content != "" || len(grounding.Unavailable) != 0 {
		t.Errorf("a turn carrying nothing was surveyed anyway: %+v", grounding)
	}
	// Not even asked: there is no scope to search, and the library is not touched.
	if len(fixture.materials.asked) != 0 {
		t.Errorf("a turn carrying nothing reached the library: %v", fixture.materials.asked)
	}
}

func TestTheSurveyReportsWhatCouldNotBeRead(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.materials.fileErrs["lib_bad"] = &parse.Error{
		Code:    parse.CodeParserUnavailable,
		Message: "这台服务器上的 PDF 解析服务当前不可用，请稍后重试或改用文本文件。",
	}
	fixture.materials.add("lib_a", "doc_a", "笔记.md")
	fixture.materials.located = []event.Source{source("doc_a", "闭包捕获的是变量")}
	// A collection that is gone is reported by the search, not by an error.
	fixture.materials.retrieved = kb.Retrieval{Unavailable: []string{"kb_gone"}}
	run := surveyed(t, fixture, Declaration{
		Attachments:    []string{"lib_a", "lib_bad"},
		KnowledgeBases: []string{"kb_x", "kb_gone"},
	})

	grounding, err := run.ground(context.Background(), "闭包是什么？")
	if err != nil {
		t.Fatalf("ground() error = %v", err)
	}

	if len(grounding.Unavailable) != 2 {
		t.Fatalf("the survey reports %v", grounding.Unavailable)
	}
	// The parse failure explains itself (ATT-04), and the deleted collection is
	// named by the only name it still has (KB-07). Neither takes another's place.
	if !strings.Contains(strings.Join(grounding.Unavailable, "\n"), "PDF 解析服务") {
		t.Errorf("the attachment's reason was rewritten: %v", grounding.Unavailable)
	}
	if !strings.Contains(strings.Join(grounding.Unavailable, "\n"), "kb_gone") {
		t.Errorf("the survey does not name the collection that is gone: %v", grounding.Unavailable)
	}
	// The model has to hear both, or it will say the material had nothing to
	// offer when the truth is that part of it was never seen.
	for _, note := range grounding.Unavailable {
		if !strings.Contains(grounding.Message.Content, note) {
			t.Errorf("the model was not told %q:\n%s", note, grounding.Message.Content)
		}
	}
	// And the turn reports the same count, which is what says an answer with no
	// evidence was short of material rather than short of work (AC-06).
	if run.unavailable != 2 {
		t.Errorf("the turn counted %d unreadable materials, want 2", run.unavailable)
	}
}

func TestASearchThatCouldNotRunIsAGapAndNotAFailure(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.materials.add("lib_a", "doc_a", "笔记.md")
	fixture.materials.located = []event.Source{source("doc_a", "闭包捕获的是变量")}
	fixture.materials.retrieveErr = errors.New("the knowledge base store is unreachable")
	run := surveyed(t, fixture, Declaration{Attachments: []string{"lib_a"}, KnowledgeBases: []string{"kb_a"}})

	grounding, err := run.ground(context.Background(), "闭包是什么？")
	if err != nil {
		t.Fatalf("a search that failed stopped the turn: %v", err)
	}

	// The attachment was still read: one search failing does not cost the material
	// the turn did manage to look at.
	if len(grounding.Sources) != 1 {
		t.Errorf("the survey found %+v", grounding.Sources)
	}
	if len(grounding.Unavailable) != 1 {
		t.Fatalf("the failure was not reported as a gap: %v", grounding.Unavailable)
	}
	// The reason stays out of the model's mouth: a storage failure repeated as
	// though it were a fact about the material is worse than a gap.
	if strings.Contains(grounding.Unavailable[0], "unreachable") {
		t.Errorf("the failure's own text reached the model: %q", grounding.Unavailable[0])
	}
	if !strings.Contains(grounding.Message.Content, grounding.Unavailable[0]) {
		t.Errorf("the model was not told about the gap:\n%s", grounding.Message.Content)
	}
}

func TestAShortQuestionIsReportedTheWayTheToolsReportIt(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.materials.retrieveErr = fmt.Errorf("%w: it needs at least 2 characters", kb.ErrQueryTooShort)
	run := surveyed(t, fixture, Declaration{KnowledgeBases: []string{"kb_a"}})

	grounding, err := run.ground(context.Background(), "嗯")
	if err != nil {
		t.Fatalf("ground() error = %v", err)
	}

	// A question the index cannot answer is the model's to fix, so it hears it as
	// the refusal it is rather than as a service that is unwell — and it is the
	// same sentence the tool would have given it, because there is one wording of
	// "ask differently" and not two.
	if len(grounding.Unavailable) != 1 || !strings.Contains(grounding.Unavailable[0], "更长") {
		t.Errorf("a short question was reported as %v", grounding.Unavailable)
	}
}

func TestASurveyAsksNothingWhenThereIsNoQuestion(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.materials.add("lib_a", "doc_a", "笔记.md")
	run := surveyed(t, fixture, Declaration{Attachments: []string{"lib_a"}, KnowledgeBases: []string{"kb_a"}})

	grounding, err := run.ground(context.Background(), "  ")
	if err != nil {
		t.Fatalf("ground() error = %v", err)
	}

	// No question, nothing to search with: searching anyway would put a document's
	// opening — what a search with no terms degenerates to — in front of the model
	// as though it were an answer to something.
	for _, asked := range fixture.materials.asked {
		if strings.HasPrefix(asked, "locate") || strings.HasPrefix(asked, "retrieve") {
			t.Errorf("a survey with no question searched anyway: %v", fixture.materials.asked)
		}
	}
	// The material itself is still what the model is told about: the numbers an
	// attachment is read by come from here and nowhere else.
	if !strings.Contains(grounding.Message.Content, "附件 1") {
		t.Errorf("the survey does not list the material:\n%s", grounding.Message.Content)
	}
	if strings.Contains(grounding.Message.Content, "没有找到") {
		t.Errorf("the survey claims to have looked and found nothing:\n%s", grounding.Message.Content)
	}
}

// TestASurveyedTurnReportsItsGroundingAndItsUnresolvedNumbers runs the whole
// runtime: a capability surveys its material, answers citing one source it was
// given and one it was not, and the result carries both facts.
func TestASurveyedTurnReportsItsGroundingAndItsUnresolvedNumbers(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.materials.retrieved = kb.Retrieval{Sources: []event.Source{source("doc_a", "闭包捕获的是变量")}}
	// The turn carries a collection, so the plane it is offered includes retrieve
	// and the registry has to have it: what is under test is the survey, not a
	// tool the model never calls.
	if err := fixture.tools.Register(&stubTool{name: "retrieve"}); err != nil {
		t.Fatalf("registering retrieve: %v", err)
	}
	fixture.register(t, stubCapability{
		manifest: capability.Manifest{Name: "chat", Description: "answers", Stages: []string{"responding"}},
		run: func(ctx context.Context, rc capability.RunContext) error {
			grounding, err := rc.Ground(ctx, "闭包是什么？")
			if err != nil {
				return err
			}
			if len(grounding.Sources) != 1 || grounding.Message.Content == "" {
				return fmt.Errorf("the survey came back as %+v", grounding)
			}
			return rc.Emitter().Emit(ctx, event.Event{
				Type:    event.TypeResult,
				Stage:   "responding",
				Content: "闭包捕获的是变量 [1]，另一个说法见 [7]。",
				Metadata: map[string]any{
					event.MetaUsageSummary: map[string]any{"total_tokens": 12},
				},
			})
		},
	})

	started := fixture.start(t, StartRequest{Content: "闭包是什么？", KnowledgeBases: []string{"kb_a"}})
	result := only(t, fixture.watch(t, started.Turn.ID), event.TypeResult)

	// The capability's own payload is untouched, and the turn's accounting is
	// added beside it: only the runtime knows how much material the whole turn
	// handed out.
	if _, ok := result.Metadata[event.MetaUsageSummary]; !ok {
		t.Errorf("the capability's own metadata was lost: %+v", result.Metadata)
	}
	grounding, ok := result.Metadata[event.MetaGrounding].(map[string]any)
	if !ok {
		t.Fatalf("the result reports no grounding: %+v", result.Metadata)
	}
	if grounding["source_count"] != 1 || grounding["unavailable_count"] != 0 {
		t.Errorf("grounding = %+v, want one source and nothing unreadable", grounding)
	}
	// A number naming no source is recorded, and the answer is left alone: the
	// model's own words are not edited to make a list add up (AIQ-02).
	unresolved, ok := result.Metadata[event.MetaUnresolvedCitations].([]int)
	if !ok || len(unresolved) != 1 || unresolved[0] != 7 {
		t.Errorf("unresolved citations = %v", result.Metadata[event.MetaUnresolvedCitations])
	}
	if !strings.Contains(result.Content, "[7]") {
		t.Errorf("the answer was rewritten: %q", result.Content)
	}
}

// TestATurnWithNoSourcesReportsNoUnresolvedNumbers is the other side of it: a
// bracketed number in a turn that was handed no source at all is a list marker,
// not a citation, and reporting it would fill the diagnosis with noise.
func TestATurnWithNoSourcesReportsNoUnresolvedNumbers(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.register(t, answering("chat", "第一点如下 [1]，第二点如下 [2]。"))

	started := fixture.start(t, StartRequest{Content: "列一下"})
	result := only(t, fixture.watch(t, started.Turn.ID), event.TypeResult)

	if _, ok := result.Metadata[event.MetaUnresolvedCitations]; ok {
		t.Errorf("a turn handed no source reported citations: %+v", result.Metadata)
	}
	grounding, ok := result.Metadata[event.MetaGrounding].(map[string]any)
	if !ok || grounding["source_count"] != 0 {
		t.Errorf("grounding = %v, want the turn's own count of zero", result.Metadata[event.MetaGrounding])
	}
}
