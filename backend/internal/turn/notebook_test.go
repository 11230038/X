package turn

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"backend/internal/authz"
	"backend/internal/notebook"
	"backend/internal/session"
	"backend/internal/tool"
)

// stubQuestions stands in for the question bank: it keeps what it was asked to
// store and addresses questions by their content, the way the real bank does, so
// that "the same question twice is one row" is a rule the stub follows rather
// than a flag a test reads back.
type stubQuestions struct {
	// rows maps a content address to the row key it was first stored under.
	rows map[string]string
	// saved is every question this bank was asked to store, in order.
	saved []notebook.Question
	// err is what every save fails with.
	err error
}

var _ Notebook = (*stubQuestions)(nil)

func newStubQuestions() *stubQuestions {
	return &stubQuestions{rows: make(map[string]string)}
}

func (q *stubQuestions) Save(_ context.Context, question notebook.Question, _ authz.Caller) (notebook.Upserted, error) {
	if q.err != nil {
		return notebook.Upserted{}, q.err
	}
	q.saved = append(q.saved, question)
	questionID := notebook.QuestionID(question.Text, question.Type)
	entryID, existed := q.rows[questionID]
	if !existed {
		entryID = "ent_" + strconv.Itoa(len(q.rows)+1)
		q.rows[questionID] = entryID
	}
	return notebook.Upserted{ID: entryID, QuestionID: questionID, Created: !existed}, nil
}

// askingTurn is a turn in flight, built by hand: the two things a question is
// stamped with and the material it may be filed under are all the port reads,
// and none of them needs a socket, a model or a database to be true.
func askingTurn(t *testing.T, fixture *runnerFixture) (*turnRun, context.Context) {
	t.Helper()
	run := &turnRun{
		runner:   fixture.runner,
		turn:     Turn{ID: "turn_1", SessionID: "ses_1"},
		session:  session.Session{ID: "ses_1"},
		material: Material{Attachments: []Attachment{{Label: "1", FileID: "file_1"}}},
	}
	return run, context.WithValue(context.Background(), turnKey{}, run)
}

func TestAQuestionIsStampedWithTheTurnThatAskedIt(t *testing.T) {
	fixture := newRunnerFixture(t)
	_, ctx := askingTurn(t, fixture)

	saved, err := (Banker{}).Save(ctx, notebook.Question{
		Text: "勾股定理说的是什么？", Type: notebook.QuestionShortAnswer,
	})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if len(fixture.questions.saved) != 1 {
		t.Fatalf("the bank was given %d questions, want one", len(fixture.questions.saved))
	}
	stored := fixture.questions.saved[0]
	// The two facts only the turn knows. A model has no argument for either, and
	// a question that lost them would be one nobody could read back against the
	// conversation it was written in.
	if stored.SessionID != "ses_1" || stored.TurnID != "turn_1" {
		t.Errorf("the question was stamped (%q, %q), want this turn's session and turn",
			stored.SessionID, stored.TurnID)
	}
	if !saved.Created || saved.EntryID == "" {
		t.Errorf("Saved = %+v, want a new row reported back", saved)
	}
}

func TestTheSameQuestionTwiceIsReportedAsOneQuestion(t *testing.T) {
	fixture := newRunnerFixture(t)
	_, ctx := askingTurn(t, fixture)
	question := notebook.Question{Text: "勾股定理说的是什么？", Type: notebook.QuestionShortAnswer}

	first, err := (Banker{}).Save(ctx, question)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	second, err := (Banker{}).Save(ctx, question)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if second.Created {
		t.Error("the second save reported a new row, want the one that was already there")
	}
	if first.EntryID != second.EntryID {
		t.Errorf("the two saves landed on %s and %s, want one row", first.EntryID, second.EntryID)
	}
	// The count is the turn's, not the bank's: the model is told which question
	// in the set this one is, and a repeated question is still a call it made.
	if second.Filed != 2 {
		t.Errorf("the second save reported step %d, want 2", second.Filed)
	}
}

func TestTheTurnSaysHowManyQuestionsItSetOutToWrite(t *testing.T) {
	fixture := newRunnerFixture(t)
	_, ctx := askingTurn(t, fixture)
	question := notebook.Question{Text: "勾股定理说的是什么？", Type: notebook.QuestionShortAnswer}

	// A turn that never laid out a plan has no denominator, and zero is how that
	// is said: "第 1 道" rather than "第 1/0 道".
	planless, err := (Banker{}).Save(ctx, question)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if planless.Planned != 0 {
		t.Errorf("Planned = %d, want no denominator when no plan was laid out", planless.Planned)
	}

	planned, err := (Banker{}).Save(tool.WithQuestionSet(ctx, 10), question)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if planned.Planned != 10 {
		t.Errorf("Planned = %d, want the count the turn set out to write", planned.Planned)
	}
}

func TestTheMaterialAQuestionMayBeFiledUnderIsThisTurns(t *testing.T) {
	fixture := newRunnerFixture(t)
	run, ctx := askingTurn(t, fixture)
	run.material.Attachments = append(run.material.Attachments,
		Attachment{Label: "2", FileID: "file_2", DocumentID: "doc_2"})

	carried := (Banker{}).Carried(ctx)
	if len(carried) != 2 {
		t.Fatalf("Carried() = %+v, want this turn's two attachments", carried)
	}
	if carried[0].Label != "1" || carried[0].MaterialID != "file_1" {
		t.Errorf("Carried()[0] = %+v, want the first attachment under its own label", carried[0])
	}
	// The stored file and not the parsed document: a material is what the user
	// attached, and the file exists whether or not the parse worked.
	if carried[1].MaterialID != "file_2" {
		t.Errorf("Carried()[1] = %+v, want the file id rather than the document id", carried[1])
	}

	// A turn that carries nothing says so, which is what makes the tool refuse a
	// label by naming what the turn does have — "本轮没有附件" being the answer
	// when there is nothing to name.
	empty := &turnRun{runner: fixture.runner, turn: Turn{ID: "turn_2"}}
	if got := (Banker{}).Carried(context.WithValue(context.Background(), turnKey{}, empty)); len(got) != 0 {
		t.Errorf("Carried() = %+v, want nothing for a turn that carries nothing", got)
	}
}

func TestAFailedSaveIsNotCountedAsAQuestion(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.questions.err = errors.New("the question bank is not answering")
	_, ctx := askingTurn(t, fixture)

	if _, err := (Banker{}).Save(ctx, notebook.Question{
		Text: "勾股定理说的是什么？", Type: notebook.QuestionShortAnswer,
	}); err == nil {
		t.Fatal("Save() error = nil, want the storage failure to stay one")
	}
	// The failure belongs to the turn's caller and not to the model: a bank that
	// is not answering is not something a model can fix by writing the question
	// differently.
	if _, err := (Banker{}).Save(tool.WithQuestionSet(ctx, 3), notebook.Question{
		Text: "勾股定理说的是什么？", Type: notebook.QuestionShortAnswer,
	}); err == nil {
		t.Fatal("Save() error = nil after the bank failed")
	}
}

func TestSaveQuestionOutsideATurnIsRefused(t *testing.T) {
	// The tool is registered once for the process, so the only thing that says
	// which conversation a question belongs to is the context it arrived on. A
	// call without one is this service's own mistake rather than the model's.
	if _, err := (Banker{}).Save(context.Background(), notebook.Question{
		Text: "勾股定理说的是什么？", Type: notebook.QuestionShortAnswer,
	}); err == nil {
		t.Error("Save() outside a turn error = nil, want a refusal")
	}
	if got := (Banker{}).Carried(context.Background()); got != nil {
		t.Errorf("Carried() outside a turn = %+v, want nothing", got)
	}
}
