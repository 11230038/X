package main

// The plan against the rows themselves: a conflict for what is here and a create
// for what is not, per kind — which is §7.3's eighth item.
//
// The negative half is the more valuable one: the plan writes nothing, and that
// is checked by counting every table the four sinks write to.

import (
	"context"
	"testing"

	"backend/internal/authz"
	"backend/internal/kb"
	"backend/internal/notebook"
	"backend/internal/reading"
	"backend/internal/session"
	"backend/internal/workspace"
)

// TestDBAPlanSaysConflictForWhatIsHereAndCreateForWhatIsNot is §7.3's eighth
// item, per kind, against the rows themselves — and it is the negative as much
// as the positive: the plan writes nothing, which is checked by counting every
// table the four sinks write to.
func TestDBAPlanSaysConflictForWhatIsHereAndCreateForWhatIsNot(t *testing.T) {
	fixture := newSinkFixture(t)
	caller := fixture.caller
	question := notebook.Question{Text: "什么是闭包", Type: notebook.QuestionChoice}
	address := notebook.QuestionID(question.Text, question.Type)

	// One row of every kind is written first, through the same sinks the plan is
	// being asked about — so what the plan sees is what an earlier import of the
	// same package left.
	present := map[workspace.Kind]workspace.Object{
		workspace.KindSession: objectOf(t, sessionRef(), sessionPayload{
			Session: session.Session{ID: "sess_test1"}}),
		workspace.KindMaterial: objectOf(t, workspace.Ref{Kind: workspace.KindMaterial, ID: "mat_1"},
			materialPayload{Material: reading.Material{MaterialID: "mat_1", CreatedAt: atSecond(0)}}),
		workspace.KindQuestion: objectOf(t, workspace.Ref{Kind: workspace.KindQuestion, ID: address},
			questionPayload{Entry: notebook.Entry{Question: question}}),
		workspace.KindKnowledgeBase: objectOf(t, workspace.Ref{Kind: workspace.KindKnowledgeBase, ID: "kb_1"},
			knowledgeBasePayload{KnowledgeBase: kb.KnowledgeBase{
				ID: "kb_1", Name: "操作系统", Provider: kb.ProviderNative,
				EmbeddingModel: "test-embed", CreatedAt: atSecond(0)}}),
	}
	fresh := map[workspace.Kind]workspace.Object{
		workspace.KindSession: objectOf(t, workspace.Ref{Kind: workspace.KindSession, ID: "sess_test2"},
			sessionPayload{Session: session.Session{ID: "sess_test2"}}),
		workspace.KindMaterial: objectOf(t, workspace.Ref{Kind: workspace.KindMaterial, ID: "mat_2"},
			materialPayload{Material: reading.Material{MaterialID: "mat_2", CreatedAt: atSecond(0)}}),
		workspace.KindQuestion: objectOf(t, workspace.Ref{Kind: workspace.KindQuestion, ID: notebook.QuestionID("什么是装饰器", notebook.QuestionChoice)},
			questionPayload{Entry: notebook.Entry{Question: notebook.Question{
				Text: "什么是装饰器", Type: notebook.QuestionChoice}}}),
		workspace.KindKnowledgeBase: objectOf(t, workspace.Ref{Kind: workspace.KindKnowledgeBase, ID: "kb_2"},
			knowledgeBasePayload{KnowledgeBase: kb.KnowledgeBase{
				ID: "kb_2", Name: "编译原理", Provider: kb.ProviderNative,
				EmbeddingModel: "test-embed", CreatedAt: atSecond(0)}}),
	}
	for _, kind := range workspace.Kinds {
		if _, err := fixture.write(caller, kind, present[kind]); err != nil {
			t.Fatalf("write the fixture's %s: %v", kind, err)
		}
	}

	before := fixture.counts(t)
	for _, kind := range workspace.Kinds {
		sink := fixture.sink(kind)
		items, err := sink.Plan(context.Background(), caller, []workspace.Object{present[kind], fresh[kind]})
		if err != nil {
			t.Fatalf("Plan(%s) error = %v", kind, err)
		}
		if len(items) != 2 {
			t.Fatalf("Plan(%s) answered %d items for two objects", kind, len(items))
		}
		if items[0].Action != workspace.ActionConflict || items[0].Reason != workspace.ReasonAlreadyPresent {
			t.Errorf("Plan(%s) called the row that is here %q/%q", kind, items[0].Action, items[0].Reason)
		}
		if items[1].Action != workspace.ActionCreate || items[1].Reason != "" {
			t.Errorf("Plan(%s) called the row that is not here %q/%q", kind, items[1].Action, items[1].Reason)
		}
	}
	// Zero writes, per table: the plan is a read, and the discipline is only
	// worth anything if the read really is one.
	after := fixture.counts(t)
	for table, rows := range before {
		if after[table] != rows {
			t.Errorf("planning changed %s: %d rows, was %d", table, after[table], rows)
		}
	}
}

// sink is one kind's sink, as the plan cases reach it without repeating which
// one is which.
func (f *sinkFixture) sink(kind workspace.Kind) workspace.Sink {
	switch kind {
	case workspace.KindSession:
		return f.sessions
	case workspace.KindMaterial:
		return f.materials
	case workspace.KindQuestion:
		return f.questions
	default:
		return f.bases
	}
}

// write is the fixture's own write, for the same reason sink exists.
func (f *sinkFixture) write(caller authz.Caller, kind workspace.Kind, object workspace.Object) ([]workspace.Written, error) {
	return f.sink(kind).Write(context.Background(), caller, []workspace.Object{object})
}

// counts is every table the four sinks write to, which is what "the plan wrote
// nothing" has to be said in.
func (f *sinkFixture) counts(t *testing.T) map[string]int64 {
	t.Helper()
	counted := make(map[string]int64, len(sinkTableOrder))
	for _, table := range sinkTableOrder {
		counted[table] = rowsIn(t, f.resource, table, "", nil)
	}
	return counted
}
