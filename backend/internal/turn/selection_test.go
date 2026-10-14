package turn

import (
	"backend/internal/authz"
	"context"
	"errors"
	"strings"
	"testing"

	"backend/internal/capability"
	"backend/internal/event"
	"backend/internal/kb"
)

// stubReadings stands in for the reading layer: it answers one declared
// selection with the source that coordinate resolves to, or with the failure
// resolving it produced.
type stubReadings struct {
	source event.Source
	err    error
	// asked is every selection that reached the reading layer, so a test can tell
	// a selection that was refused from one that was never sent.
	asked []Selection
}

var _ Readings = (*stubReadings)(nil)

func (r *stubReadings) Passage(_ context.Context, selection Selection, _ authz.Caller) (event.Source, error) {
	r.asked = append(r.asked, selection)
	if r.err != nil {
		return event.Source{}, r.err
	}
	return r.source, nil
}

// withReadings installs a reading layer on the fixture, which is what a
// deployment with a reader in it has and what a deployment without one does not.
func withReadings(readings Readings) func(*Options) {
	return func(options *Options) { options.Readings = readings }
}

func TestTheSelectionIsWhatTheTurnWasGivenToRead(t *testing.T) {
	readings := &stubReadings{source: event.Source{
		DocumentID: "doc_a", Title: "算法笔记.md", Snippet: "快速排序每次把枢轴放到它最终的位置上",
		Locator: event.Locator{Ordinal: 7, CharStart: 120, CharEnd: 138},
	}}
	fixture := newRunnerFixture(t, withReadings(readings))
	fixture.materials.retrieved = kb.Retrieval{Sources: []event.Source{source("doc_b", "词法作用域")}}
	run := &turnRun{
		runner: fixture.runner,
		turn:   Turn{ID: "turn_1"},
		declared: Declaration{
			KnowledgeBases: []string{"kb_a"},
			Selection:      &Selection{MaterialID: "rm_a", Locator: event.Locator{Ordinal: 7, CharStart: 120, CharEnd: 138}},
		},
	}
	run.resolveMaterial(context.Background())

	grounding, err := run.ground(context.Background(), "这里为什么是 O(n log n)？")
	if err != nil {
		t.Fatalf("ground() error = %v", err)
	}

	// The reader's passage is the first thing the model is handed and the first
	// number it may cite: it is what the question is about, and the answer leans on
	// it before it leans on anything the material happened to match (§2.8).
	if len(grounding.Sources) != 2 {
		t.Fatalf("the survey found %+v", grounding.Sources)
	}
	if grounding.Sources[0].Snippet != "快速排序每次把枢轴放到它最终的位置上" {
		t.Errorf("the first source is %+v, want the reader's selection", grounding.Sources[0])
	}
	if grounding.Sources[0].Index != 1 || grounding.Sources[1].Index != 2 {
		t.Errorf("the sources are numbered %d and %d, want 1 and 2",
			grounding.Sources[0].Index, grounding.Sources[1].Index)
	}
	// The survey says why it is there at all: it is not a search result, and a
	// model that mistook it for one would ask what it matched.
	if !strings.Contains(grounding.Message.Content, "读者划出的一段原文") {
		t.Errorf("the survey does not say where the passage came from:\n%s", grounding.Message.Content)
	}
	if !strings.Contains(grounding.Message.Content, "快速排序每次把枢轴放到它最终的位置上") {
		t.Errorf("the survey does not hand the passage over:\n%s", grounding.Message.Content)
	}
	// And it is handed over whole: what the client said the text was is not what
	// the model reads, and the coordinate is not something it has to be told.
	if len(readings.asked) != 1 || readings.asked[0].MaterialID != "rm_a" {
		t.Errorf("the reading layer was asked %+v", readings.asked)
	}
	if strings.Contains(grounding.Message.Content, "rm_a") || strings.Contains(grounding.Message.Content, "doc_a") {
		t.Errorf("the survey carries this service's bookkeeping:\n%s", grounding.Message.Content)
	}
}

func TestASelectionThatCouldNotBeReadIsAGapAndNotAFailure(t *testing.T) {
	readings := &stubReadings{err: errors.New("the material has been deleted")}
	fixture := newRunnerFixture(t, withReadings(readings))
	fixture.materials.retrieved = kb.Retrieval{Sources: []event.Source{source("doc_b", "词法作用域")}}
	run := &turnRun{
		runner: fixture.runner,
		turn:   Turn{ID: "turn_1"},
		declared: Declaration{
			KnowledgeBases: []string{"kb_a"},
			Selection:      &Selection{MaterialID: "rm_gone", Locator: event.Locator{Ordinal: 1, CharStart: 0, CharEnd: 10}},
		},
	}
	run.resolveMaterial(context.Background())

	grounding, err := run.ground(context.Background(), "这段话什么意思？")
	if err != nil {
		t.Fatalf("a selection that could not be read stopped the turn: %v", err)
	}

	// The reader's question still stands, so what is lost is the grounding and not
	// the turn — the rule a failed attachment already follows (§2.3).
	if len(grounding.Sources) != 1 || grounding.Sources[0].Snippet != "词法作用域" {
		t.Errorf("the survey found %+v, want the collection's own result", grounding.Sources)
	}
	if len(grounding.Unavailable) != 1 || !strings.Contains(grounding.Unavailable[0], "读者划出的那段原文") {
		t.Fatalf("the failure was not reported as a gap: %v", grounding.Unavailable)
	}
	// The reason is this service's own trouble and stays out of the model's mouth.
	if strings.Contains(grounding.Unavailable[0], "deleted") {
		t.Errorf("the failure's own text reached the model: %q", grounding.Unavailable[0])
	}
	if !strings.Contains(grounding.Message.Content, grounding.Unavailable[0]) {
		t.Errorf("the model was not told about the gap:\n%s", grounding.Message.Content)
	}
}

func TestASelectionOnATurnCarryingNothingIsStillSurveyed(t *testing.T) {
	readings := &stubReadings{source: event.Source{DocumentID: "doc_a", Snippet: "快速排序"}}
	fixture := newRunnerFixture(t, withReadings(readings))
	run := &turnRun{
		runner: fixture.runner,
		turn:   Turn{ID: "turn_1"},
		declared: Declaration{
			Selection: &Selection{MaterialID: "rm_a", Locator: event.Locator{Ordinal: 1, CharStart: 0, CharEnd: 4}},
		},
	}
	run.resolveMaterial(context.Background())

	grounding, err := run.ground(context.Background(), "这段什么意思？")
	if err != nil {
		t.Fatalf("ground() error = %v", err)
	}

	// This is exactly the turn immersive_reading is: a reader with a passage and
	// nothing attached. It is surveyed even though it carries nothing, because it
	// does carry something — the material is the passage (§2.8).
	if len(grounding.Sources) != 1 || grounding.Sources[0].Index != 1 {
		t.Errorf("the survey found %+v", grounding.Sources)
	}
	if grounding.Message.Content == "" {
		t.Error("a turn carrying a selection got no survey message")
	}
	// Everything else is untouched: no scope was searched, because there is none.
	if len(fixture.materials.asked) != 0 {
		t.Errorf("a turn carrying only a selection reached the library: %v", fixture.materials.asked)
	}
}

func TestASelectionThatCannotBeASpanIsRefusedBeforeTheTurnExists(t *testing.T) {
	for _, test := range []struct {
		name      string
		selection Selection
		refused   bool
	}{
		{
			name:      "a coordinate no text has",
			selection: Selection{MaterialID: "rm_a", Locator: event.Locator{CharStart: 20, CharEnd: 10}},
			refused:   true,
		},
		{
			name:      "a start before the text does",
			selection: Selection{MaterialID: "rm_a", Locator: event.Locator{CharStart: -1, CharEnd: 4}},
			refused:   true,
		},
		{
			name:      "a selection that names no material",
			selection: Selection{Locator: event.Locator{CharStart: 0, CharEnd: 4}},
			refused:   true,
		},
		{
			name: "a reader who asked with nothing selected",
			// Where the reader is looking and what they selected are one field on
			// the wire, and asking about the unit they are reading is the ordinary
			// way to use this: the reading layer resolves the point to the text
			// there, so the runtime must not refuse it here.
			selection: Selection{MaterialID: "rm_a", Locator: event.Locator{Ordinal: 3, CharStart: 120, CharEnd: 120}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRunnerFixture(t, withReadings(&stubReadings{
				source: event.Source{DocumentID: "doc_a", Snippet: "快速排序"},
			}))
			fixture.register(t, answering("chat", "好"))

			started, err := fixture.runner.Start(context.Background(), StartRequest{
				Content: "这段什么意思？", Selection: &test.selection,
			})
			if test.refused {
				if !errors.Is(err, ErrInvalidSelection) {
					t.Fatalf("Start() error = %v, want a refused selection", err)
				}
				// Refused before the row exists: a turn that was created and then
				// reported the coordinate would be a user waiting on an answer that
				// was never going to come.
				if started.Turn.ID != "" {
					t.Errorf("a refused selection still created the turn %s", started.Turn.ID)
				}
				return
			}
			if err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			fixture.repository.awaitStatus(t, started.Turn.ID, StatusCompleted)
		})
	}
}

func TestTheReaderSelectionReachesTheModel(t *testing.T) {
	fixture := newRunnerFixture(t, withReadings(&stubReadings{source: event.Source{
		DocumentID: "doc_a", Title: "算法笔记.md", Snippet: "快速排序每次把枢轴放到它最终的位置上",
	}}))
	fixture.register(t, stubCapability{
		manifest: capability.Manifest{Name: "chat", Description: "answers", Stages: []string{"responding"}},
		run: func(ctx context.Context, rc capability.RunContext) error {
			grounding, err := rc.Ground(ctx, "这里为什么是 O(n log n)？")
			if err != nil {
				return err
			}
			if len(grounding.Sources) != 1 {
				return errors.New("the survey did not hand over the selection")
			}
			return rc.Emitter().Emit(ctx, event.Event{
				Type: event.TypeResult, Stage: "responding", Content: "因为它每次把问题对半分 [1]。",
			})
		},
	})

	started := fixture.start(t, StartRequest{
		Content:   "这里为什么是 O(n log n)？",
		Selection: &Selection{MaterialID: "rm_a", Locator: event.Locator{Ordinal: 7, CharStart: 120, CharEnd: 138}},
	})
	result := only(t, fixture.watch(t, started.Turn.ID), event.TypeResult)

	// The accounting is the runtime's, and a selection counts: an answer citing
	// [1] is citing the passage it was handed, and a turn that counted zero would
	// report the citation as unresolved (AC-06, AIQ-02).
	grounding, ok := result.Metadata[event.MetaGrounding].(map[string]any)
	if !ok || grounding["source_count"] != 1 {
		t.Fatalf("grounding = %v, want one source", result.Metadata[event.MetaGrounding])
	}
	if _, reported := result.Metadata[event.MetaUnresolvedCitations]; reported {
		t.Errorf("the answer's own citation was called unresolved: %+v", result.Metadata)
	}
}
