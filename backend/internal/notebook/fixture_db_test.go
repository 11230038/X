package notebook

// The fixture the question bank's storage tests are built on: the database this
// package owns, and the helpers that write a question at a time the caller
// chooses.
//
// Times are spelled out rather than taken from the clock because DATETIME keeps
// whole seconds: two rows written in the same second tie, and an ordering
// asserted over tied rows asserts whatever the plan did that time. The row is
// read back through a struct of its own — not through the mapping these tests are
// about — so that "what the database holds" and "what this package reads" stay
// two different claims.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"backend/internal/data"
	"backend/internal/data/testdb"
	"gorm.io/datatypes"
)

var dbTables = []string{
	"practice_review_events", "practice_review_state",
	"notebook_entry_categories", "notebook_categories", "notebook_entries",
	"turns", "sessions",
}

func newDBRepository(t *testing.T) (*GORMRepository, *data.Resource) {
	t.Helper()
	resource := testdb.Open(t, "notebook")
	testdb.Reset(t, resource, dbTables...)
	return NewGORMRepository(resource.GORM()), resource
}

func newDBService(t *testing.T) (*Service, *data.Resource) {
	t.Helper()
	repository, resource := newDBRepository(t)
	service, err := NewService(repository)
	if err != nil {
		t.Fatalf("new notebook service: %v", err)
	}
	return service, resource
}

// atSecond is a whole second on a fixed day. Timestamps are spelled out because
// DATETIME keeps whole seconds: two rows written in the same second tie, and an
// ordering asserted over tied rows asserts whatever the plan did that time.
func atSecond(second int) time.Time {
	return time.Date(2026, 10, 1, 12, 0, second, 0, time.UTC)
}

// sampleQuestion is a question with every field a caller can set filled in, so
// that a test about one of them can change that one and leave the rest.
func sampleQuestion(text string) Question {
	return Question{
		Text:            text,
		Type:            QuestionChoice,
		Options:         []Option{{Key: "A", Text: "甲"}, {Key: "B", Text: "乙"}},
		CorrectAnswer:   "A",
		Explanation:     "因为甲是对的",
		Difficulty:      "medium",
		Source:          "knowledge_base",
		MaterialID:      "doc_1",
		KnowledgePoints: []string{"闭包", "作用域"},
	}
}

func withCategories(question Question, names ...string) Question {
	question.Categories = names
	return question
}

// saveAt writes a question through the repository at a time the caller chooses,
// which is what the upsert takes instead of reading the clock itself.
func saveAt(t *testing.T, repository *GORMRepository, question Question, at time.Time) Upserted {
	t.Helper()
	entryID, err := newID(entryIDPrefix)
	if err != nil {
		t.Fatalf("generate a row id: %v", err)
	}
	question.QuestionID = QuestionID(question.Text, question.Type)
	result, err := repository.UpsertQuestion(context.Background(), entryID, question, at)
	if err != nil {
		t.Fatalf("upsert %q: %v", question.Text, err)
	}
	return result
}

func save(t *testing.T, service *Service, question Question) Upserted {
	t.Helper()
	result, err := service.Save(context.Background(), question)
	if err != nil {
		t.Fatalf("save %q: %v", question.Text, err)
	}
	return result
}

func entryOf(t *testing.T, service *Service, questionID string) Entry {
	t.Helper()
	entry, err := service.Get(context.Background(), questionID)
	if err != nil {
		t.Fatalf("read question %s: %v", questionID, err)
	}
	return entry
}

// storedRow is one row read back as the database holds it, rather than through
// the mapping this package is being tested for. Columns that are nullable are
// pointers so that "written false" and "never written" can be told apart — the
// distinction the resolved column exists for.
type storedRow struct {
	NotebookEntriesID string         `gorm:"column:notebook_entries_id"`
	QuestionID        string         `gorm:"column:question_id"`
	Question          string         `gorm:"column:question"`
	QuestionType      string         `gorm:"column:question_type"`
	Options           datatypes.JSON `gorm:"column:options"`
	CorrectAnswer     *string        `gorm:"column:correct_answer"`
	Explanation       *string        `gorm:"column:explanation"`
	Difficulty        *string        `gorm:"column:difficulty"`
	Source            *string        `gorm:"column:source"`
	MaterialID        *string        `gorm:"column:material_id"`
	Extra             datatypes.JSON `gorm:"column:extra"`
	MasteryPathID     *string        `gorm:"column:mastery_path_id"`
	KnowledgePointID  *string        `gorm:"column:knowledge_point_id"`
	SessionID         *string        `gorm:"column:session_id"`
	TurnID            *string        `gorm:"column:turn_id"`
	CreatTime         *time.Time     `gorm:"column:creat_time"`
	UpdateTime        *time.Time     `gorm:"column:update_time"`
	UserAnswer        *string        `gorm:"column:user_answer"`
	IsCorrect         *bool          `gorm:"column:is_correct"`
	Result            *string        `gorm:"column:result"`
	Resolved          *bool          `gorm:"column:resolved"`
	AttemptCount      *int32         `gorm:"column:attempt_count"`
	Bookmarked        *bool          `gorm:"column:bookmarked"`
}

func readRow(t *testing.T, resource *data.Resource, questionID string) storedRow {
	t.Helper()
	var row storedRow
	err := resource.GORM().Raw(`SELECT notebook_entries_id, question_id, question, question_type,
			options, correct_answer, explanation, difficulty, source, material_id, extra,
			mastery_path_id, knowledge_point_id, session_id, turn_id, creat_time, update_time,
			user_answer, is_correct, result, resolved, attempt_count, bookmarked
		FROM notebook_entries WHERE question_id = ?`, questionID).Scan(&row).Error
	if err != nil {
		t.Fatalf("read the row of %s: %v", questionID, err)
	}
	if row.QuestionID == "" {
		t.Fatalf("no row holds question %s", questionID)
	}
	return row
}

// storedOptions reads the options column back through the same decoding the
// adapter uses, rather than matching the text the database renders it as: the
// JSON column does not keep the bytes it was given, so comparing what is stored
// to what was written is a comparison of parsed values.
func storedOptions(t *testing.T, row storedRow) []Option {
	t.Helper()
	var options []Option
	if err := json.Unmarshal(row.Options, &options); err != nil {
		t.Fatalf("the options column does not hold a list of options: %v (%s)", err, row.Options)
	}
	return options
}

// storedKnowledgePoints reads the extra column back, which is where the
// knowledge points live: there is no knowledge point table in this version.
func storedKnowledgePoints(t *testing.T, row storedRow) []string {
	t.Helper()
	var extra struct {
		KnowledgePoints []string `json:"knowledge_points"`
	}
	if err := json.Unmarshal(row.Extra, &extra); err != nil {
		t.Fatalf("the extra column does not hold knowledge points: %v (%s)", err, row.Extra)
	}
	return extra.KnowledgePoints
}

func countRows(t *testing.T, resource *data.Resource, table string, where string, args ...any) int {
	t.Helper()
	var count int64
	query := "SELECT COUNT(*) FROM `" + table + "`"
	if where != "" {
		query += " WHERE " + where
	}
	if err := resource.GORM().Raw(query, args...).Scan(&count).Error; err != nil {
		t.Fatalf("count the rows of %s: %v", table, err)
	}
	return int(count)
}

// storeConversation writes the two rows a question's session_id and turn_id can
// point at. The foreign keys still require them: what 00010 changed is what
// happens when they are deleted, not whether they have to be there.
func storeConversation(t *testing.T, resource *data.Resource, sessionID, turnID string) {
	t.Helper()
	testdb.Exec(t, resource, "INSERT INTO sessions (session_id) VALUES (?)", sessionID)
	testdb.Exec(t, resource,
		"INSERT INTO turns (turn_id, session_id, status, state_version, creat_time) "+
			"VALUES (?, ?, 'running', 1, ?)", turnID, sessionID, atSecond(0))
}
