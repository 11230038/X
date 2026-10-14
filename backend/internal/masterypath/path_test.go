package masterypath

// What this bridge decides, checked against a real seekdb.
//
// path.go holds one thing no double can be asked about: the mastery coordinate a
// question carries (§2.2). This bridge is the only place in the build that holds
// both halves of it at once — the path is the turn's, the knowledge point is the
// model's — so what it does with them is a claim about what ends up in
// `notebook_entries`, and the database is what settles a claim like that.
//
// The bank below is not a double of the question bank: it is the real service
// over the same database, keeping a note of what it was handed. A stub that
// answered "saved" would make every assertion in this file an assertion about
// the stub.
//
// One database for the package means nothing here calls t.Parallel, and every
// test starts on empty tables.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"backend/internal/data"
	"backend/internal/data/testdb"
	"backend/internal/mastery"
	"backend/internal/notebook"
	"backend/internal/tool"
)

// dbTables are the tables these tests write, children first. They are every
// table a path touches plus the bank's own, because the two halves of the claim
// live in different tables and either one left behind would be read as the
// other's evidence.
var dbTables = []string{
	"mastery_learning_evidences",
	"mastery_events",
	"mastery_interactions",
	"mastery_path_leases",
	"mastery_path_sessions",
	"mastery_topic_meta",
	"mastery_paths",
	"notebook_entry_categories",
	"notebook_categories",
	"notebook_entries",
	"turns",
	"sessions",
}

func atSecond(second int) time.Time {
	return time.Date(2026, 10, 1, 12, 0, second, 0, time.UTC)
}

// recordingBank is the turn runtime's side of the bank in the one respect this
// file cares about: it keeps what it was handed, and it stores it for real.
type recordingBank struct {
	bank   *notebook.Service
	handed []notebook.Question
}

var _ tool.QuestionBank = (*recordingBank)(nil)

func (b *recordingBank) Carried(context.Context) []tool.Carried { return nil }

func (b *recordingBank) Save(ctx context.Context, question notebook.Question) (tool.Saved, error) {
	b.handed = append(b.handed, question)
	result, err := b.bank.Save(ctx, question)
	if err != nil {
		return tool.Saved{}, err
	}
	return tool.Saved{EntryID: result.ID, QuestionID: result.QuestionID, Created: result.Created}, nil
}

// fixture is one conversation, one turn of it, one path, and the two services
// over one database.
type fixture struct {
	service  *mastery.Service
	notes    *notebook.Service
	bank     *recordingBank
	resource *data.Resource
	pathID   string
	actor    mastery.Actor
	version  int64
}

func (f *fixture) turnPath(held bool) *TurnPath {
	return NewTurnPath(f.service, f.bank, f.notes, nil, f.pathID, f.actor, f.version, held, mastery.Lease{})
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	resource := testdb.Open(t, "masterypath")
	testdb.Reset(t, resource, dbTables...)
	// The rows the foreign keys need: a path belongs to the conversation it was
	// started in, and every event it writes names the turn that wrote it.
	testdb.Exec(t, resource, "INSERT INTO sessions (session_id) VALUES (?)", "ses_1")
	testdb.Exec(t, resource, "INSERT INTO turns "+
		"(turn_id, session_id, status, state_version, creat_time) VALUES (?, ?, 'running', 1, ?)",
		"trn_1", "ses_1", atSecond(0))

	service, err := mastery.NewService(mastery.NewGORMRepository(resource.GORM()), mastery.Options{
		LeaseTTL:       2 * time.Minute,
		Gate:           mastery.GateOptions{Consecutive: 2, MinEvidence: 3},
		MaxActivePaths: 3,
	})
	if err != nil {
		t.Fatalf("new mastery service: %v", err)
	}
	notes, err := notebook.NewService(notebook.NewGORMRepository(resource.GORM()))
	if err != nil {
		t.Fatalf("new notebook service: %v", err)
	}
	actor := mastery.Actor{SessionID: "ses_1", TurnID: "trn_1"}
	path, err := service.Create(context.Background(), mastery.Create{
		PathID: "mp_1", Goal: "学会闭包", Actor: actor, Tree: sampleTree(),
	})
	if err != nil {
		t.Fatalf("create a path: %v", err)
	}
	return &fixture{
		service:  service,
		notes:    notes,
		bank:     &recordingBank{bank: notes},
		resource: resource,
		pathID:   path.ID,
		actor:    actor,
		version:  path.Version,
	}
}

// sampleTree is a path with two knowledge points, so that a test can name one
// that is there and one that is not.
func sampleTree() mastery.Tree {
	return mastery.Tree{Modules: []mastery.Module{{
		ID: "m_1", Name: "闭包",
		KnowledgePoints: []mastery.KnowledgePoint{
			{ID: "kp_001", Name: "闭包捕获的是变量", ModuleID: "m_1"},
			{ID: "kp_002", Name: "循环里的闭包", ModuleID: "m_1"},
		},
	}}}
}

func sampleAsked() notebook.Question {
	return notebook.Question{
		Text:          "闭包捕获的是变量还是值？",
		Type:          notebook.QuestionShortAnswer,
		CorrectAnswer: "变量",
		Explanation:   "循环里的函数看到的是同一个变量。",
	}
}

// TestAQuestionAskedOnAPathIsFiledOnIt is §2.2's filling of the two columns,
// stated as a row: the coordinate reaches the bank with the question, and it is
// still there when the question is read back.
func TestAQuestionAskedOnAPathIsFiledOnIt(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()

	saved, err := fixture.turnPath(true).Ask(ctx, sampleAsked(), "kp_001")
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	// What the bank was handed is what this bridge decided; what the bank stored
	// is what the decision left behind. Both, or the test would pass on a write
	// the bank never made.
	if len(fixture.bank.handed) != 1 {
		t.Fatalf("the bank was handed %d questions, want one", len(fixture.bank.handed))
	}
	handed := fixture.bank.handed[0]
	if handed.MasteryPathID != "mp_1" || handed.KnowledgePointID != "kp_001" {
		t.Errorf("the question went to the bank filed as %q/%q, want mp_1/kp_001",
			handed.MasteryPathID, handed.KnowledgePointID)
	}
	entry, err := fixture.notes.Get(ctx, saved.QuestionID)
	if err != nil {
		t.Fatalf("read the question back: %v", err)
	}
	if entry.Question.MasteryPathID != "mp_1" || entry.Question.KnowledgePointID != "kp_001" {
		t.Errorf("the stored question reads back filed as %q/%q, want mp_1/kp_001",
			entry.Question.MasteryPathID, entry.Question.KnowledgePointID)
	}
	// The question is one row — a path's questions are the bank's questions and
	// not a second set of them (§2.5) — and the interaction the path wrote names
	// that row, which is the record that has to agree with the coordinate.
	var stored int
	err = fixture.resource.GORM().Raw("SELECT COUNT(*) FROM notebook_entries").Scan(&stored).Error
	if err != nil {
		t.Fatalf("count the bank: %v", err)
	}
	if stored != 1 {
		t.Errorf("the bank holds %d rows, want the one question", stored)
	}
	var asked struct {
		PathID       string `gorm:"column:path_id"`
		QuestionJSON []byte `gorm:"column:question_json"`
	}
	err = fixture.resource.GORM().Raw("SELECT path_id, question_json " +
		"FROM mastery_interactions WHERE result_json IS NULL").Scan(&asked).Error
	if err != nil {
		t.Fatalf("read the open interaction: %v", err)
	}
	if asked.PathID != "mp_1" || !strings.Contains(string(asked.QuestionJSON), saved.QuestionID) {
		t.Errorf("the path recorded %s against %s, want the question the bank wrote",
			asked.PathID, asked.QuestionJSON)
	}
}

// TestAQuestionIsNotWrittenAgainstAKnowledgePointThePathDoesNotHave is the other
// half of writing the coordinate on the way in: an id that is not in the tree
// never becomes one of those columns.
//
// The column holds the id for good once it is written — it is the first write
// that decides it — so a model that named the wrong knowledge point once would
// leave a question filed on a coordinate nothing resolves. The refusal happens
// before the bank is reached, which is what makes that impossible rather than
// merely unlikely.
func TestAQuestionIsNotWrittenAgainstAKnowledgePointThePathDoesNotHave(t *testing.T) {
	fixture := newFixture(t)

	_, err := fixture.turnPath(true).Ask(context.Background(), sampleAsked(), "kp_404")
	if err == nil {
		t.Fatal("a question was filed against a knowledge point that is not in the path")
	}
	if !errors.Is(err, mastery.ErrNotFound) {
		t.Errorf("Ask() error = %v, want a missing knowledge point", err)
	}
	if len(fixture.bank.handed) != 0 {
		t.Errorf("the bank was handed %+v", fixture.bank.handed)
	}
	var stored int
	if err := fixture.resource.GORM().Raw("SELECT COUNT(*) FROM notebook_entries").
		Scan(&stored).Error; err != nil {
		t.Fatalf("count the bank: %v", err)
	}
	if stored != 0 {
		t.Errorf("the bank holds %d rows, want none", stored)
	}
}

// TestATurnThatDoesNotHoldThePathCannotFileAQuestion pins the lease in front of
// the bank write: a question filed by a turn that may not write the path would be
// a question in the bank that the path never recorded, and the model would be
// told it asked something the path knows nothing about.
func TestATurnThatDoesNotHoldThePathCannotFileAQuestion(t *testing.T) {
	fixture := newFixture(t)

	_, err := fixture.turnPath(false).Ask(context.Background(), sampleAsked(), "kp_001")
	var refusal *tool.Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("Ask() error = %v, want a refusal the model can read", err)
	}
	if refusal.Note == "" {
		t.Error("the refusal says nothing about why the turn cannot write the path")
	}
	if len(fixture.bank.handed) != 0 {
		t.Errorf("a refused question reached the bank: %+v", fixture.bank.handed)
	}
}
