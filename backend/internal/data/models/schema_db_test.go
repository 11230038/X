package models

// The notebook's relationship to the conversation it came from, checked against
// a real seekdb.
//
// Migration 00010 changed two foreign keys from CASCADE to SET NULL, and that
// is a claim about what the database does when a row is deleted — which is not
// something a mapping test or a test double can attest to: both would state
// what the change was meant to do and then agree with themselves. The two
// questions worth asking are "does the delete go through at all" and "is the
// question still there afterwards", and only the engine answers either.
//
// The first is not a formality. This engine refuses to delete a parent row on a
// table whose foreign key is part of an index when that table also carries a
// vector or full-text index (ERROR 4016, measured in 00007 and recorded in V2
// §0.3 第 1 条). These six tables carry neither, so they are expected to be on
// the working side of that rule — but the foreign key columns here *are*
// indexed (`idx_notebook_entries_session_creat` and friends), which is the
// shape that made the rule visible in the first place. Until this test ran, no
// code in this repository had ever deleted a session that had produced a
// question.
//
// One database per package, so nothing here calls t.Parallel.

import (
	"testing"

	"backend/internal/data"
	"backend/internal/data/testdb"
)

// newSchemaDB hands the test an empty schema. The deletes go children first:
// the point of the test is what survives a delete, so it cannot start with rows
// that would survive it by being in the way.
func newSchemaDB(t *testing.T) *data.Resource {
	t.Helper()
	resource := testdb.Open(t, "models")
	testdb.Reset(t, resource,
		"practice_review_events", "practice_review_state", "notebook_entry_categories",
		"notebook_entries", "turns", "sessions")
	return resource
}

const (
	schemaSessionID  = "ses_schema_probe"
	schemaTurnID     = "trn_schema_probe"
	schemaEntryID    = "nbe_schema_probe"
	schemaQuestionID = "qn_schema_probe"
	schemaRequestID  = "rev_schema_probe"
)

// seedAQuestion writes one question as the V3 write path will: asked in a
// conversation, already reviewed twice, with one review event on record.
func seedAQuestion(t *testing.T, resource *data.Resource) {
	t.Helper()
	testdb.Exec(t, resource, "INSERT INTO sessions (session_id) VALUES (?)", schemaSessionID)
	testdb.Exec(t, resource,
		"INSERT INTO turns (turn_id, session_id, status) VALUES (?, ?, 'completed')",
		schemaTurnID, schemaSessionID)
	testdb.Exec(t, resource,
		`INSERT INTO notebook_entries
		    (notebook_entries_id, question_id, question, question_type, session_id, turn_id, source, material_id)
		 VALUES (?, ?, ?, 'short_answer', ?, ?, 'knowledge_base', 'doc_probe')`,
		schemaEntryID, schemaQuestionID, "这一章讲了什么？", schemaSessionID, schemaTurnID)
	testdb.Exec(t, resource,
		`INSERT INTO practice_review_state (notebook_entries_id, streak, review_count, lapses)
		 VALUES (?, 1, 2, 0)`, schemaEntryID)
	testdb.Exec(t, resource,
		`INSERT INTO practice_review_events (request_id, notebook_entries_id, user_answer, outcome_json)
		 VALUES (?, ?, '答', JSON_OBJECT('is_correct', TRUE))`, schemaRequestID, schemaEntryID)
}

// storedQuestion reads back the two columns under test.
func storedQuestion(t *testing.T, resource *data.Resource) (sessionID, turnID *string) {
	t.Helper()
	var row struct {
		SessionID *string `gorm:"column:session_id"`
		TurnID    *string `gorm:"column:turn_id"`
	}
	err := resource.GORM().Raw(
		"SELECT session_id, turn_id FROM notebook_entries WHERE notebook_entries_id = ?",
		schemaEntryID).Scan(&row).Error
	if err != nil {
		t.Fatalf("read the question back: %v", err)
	}
	return row.SessionID, row.TurnID
}

// rowsIn counts a table, which is how "the review history survived" is asked.
func rowsIn(t *testing.T, resource *data.Resource, table string) int64 {
	t.Helper()
	var count int64
	if err := resource.GORM().Raw("SELECT COUNT(*) FROM " + table).Scan(&count).Error; err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

// A question is a shared asset, so the turn that first produced it may go
// without taking it along — and without taking its review state or its review
// history, which cascade from the question in turn.
func TestDBDeletingATurnLeavesTheQuestionBehind(t *testing.T) {
	resource := newSchemaDB(t)
	seedAQuestion(t, resource)

	if err := testdb.Try(resource, "DELETE FROM turns WHERE turn_id = ?", schemaTurnID); err != nil {
		t.Fatalf("delete the turn: %v", err)
	}

	sessionID, turnID := storedQuestion(t, resource)
	if turnID != nil {
		t.Errorf("the question still names turn %q, want no turn", *turnID)
	}
	// The session is the other key and the other question: deleting a turn is
	// not deleting the conversation, so this one must be untouched.
	if sessionID == nil || *sessionID != schemaSessionID {
		t.Errorf("the question names session %v, want %q", sessionID, schemaSessionID)
	}
	if got := rowsIn(t, resource, "practice_review_state"); got != 1 {
		t.Errorf("%d review states survived, want 1", got)
	}
	if got := rowsIn(t, resource, "practice_review_events"); got != 1 {
		t.Errorf("%d review events survived, want 1", got)
	}
}

// The same, one level up: a conversation can go and leave the questions it
// produced with everything that was learned from them. Both foreign keys are
// SET NULL, and deleting a session cascades to its turns first, so this is the
// case where the two of them are exercised in one delete.
func TestDBDeletingASessionLeavesTheQuestionAndItsHistoryBehind(t *testing.T) {
	resource := newSchemaDB(t)
	seedAQuestion(t, resource)

	if err := testdb.Try(resource, "DELETE FROM sessions WHERE session_id = ?", schemaSessionID); err != nil {
		t.Fatalf("delete the session: %v", err)
	}

	sessionID, turnID := storedQuestion(t, resource)
	if sessionID != nil {
		t.Errorf("the question still names session %q, want no session", *sessionID)
	}
	if turnID != nil {
		t.Errorf("the question still names turn %q, want no turn", *turnID)
	}
	if got := rowsIn(t, resource, "notebook_entries"); got != 1 {
		t.Errorf("%d questions survived, want 1", got)
	}

	// The review history is the point of the whole arrangement: a question that
	// outlives its conversation but loses its review record would be a question
	// the user has to start over on.
	if got := rowsIn(t, resource, "practice_review_state"); got != 1 {
		t.Errorf("%d review states survived, want 1", got)
	}
	if got := rowsIn(t, resource, "practice_review_events"); got != 1 {
		t.Errorf("%d review events survived, want 1", got)
	}

	// The question's own columns are untouched by any of this: the answer half
	// is the user's work, and deleting a conversation is not an edit of it.
	var row struct {
		Question   string  `gorm:"column:question"`
		MaterialID *string `gorm:"column:material_id"`
		Source     *string `gorm:"column:source"`
	}
	err := resource.GORM().Raw(
		`SELECT question, material_id, source FROM notebook_entries WHERE notebook_entries_id = ?`,
		schemaEntryID).Scan(&row).Error
	if err != nil {
		t.Fatalf("read the question's own columns: %v", err)
	}
	if row.Question == "" {
		t.Error("the question's text was emptied")
	}
	if row.MaterialID == nil || *row.MaterialID != "doc_probe" {
		t.Errorf("material_id = %v, want the document the question came from", row.MaterialID)
	}
	if row.Source == nil || *row.Source != "knowledge_base" {
		t.Errorf("source = %v, want knowledge_base", row.Source)
	}
}
