package main

// The four sinks against a real database, and the two kinds whose children are
// the interesting part.
//
// The sinks are the one place in this build where a package becomes rows, so
// what they do is a claim about storage and only a database can settle it. Two
// of the four claims are not even the sink's to make:
//
//   - The owner a row lands with is a column the sink fills in, and an
//     import that wrote another account's rows would look exactly like this one
//     in memory.
//   - The undo of each kind is one statement whose effect is a cascade spread
//     across a migration, and a double can only restate what somebody believed
//     the migration said (this is the same argument reading's and notebook's
//     delete tests make, and the reason those two methods exist at all, §2.13).
//
// The fixture every case here runs on is below. The question and knowledge base
// kinds are workspacesinks_questions_db_test.go's, the plan is
// workspacesinks_plan_db_test.go's, and the turn count that guards an import is
// workspacesinks_busy_db_test.go's.
//
// One database per package, so nothing here calls t.Parallel.

import (
	"context"
	"testing"
	"time"

	"backend/internal/authz"
	"backend/internal/data"
	"backend/internal/data/testdb"
	"backend/internal/kb"
	"backend/internal/notebook"
	"backend/internal/reading"
	"backend/internal/session"
	"backend/internal/workspace"
)

// sinkTableOrder is child before parent across the four domains and the two
// tables a fixture needs besides: a package's rows hang off conversations, the
// bank, the catalogue and the knowledge base, and all of those hang off an
// account.
var sinkTableOrder = []string{
	"turn_events", "turns", "messages", "sessions",
	"reading_positions", "reading_marks", "reading_pairings", "reading_workspace_materials",
	"reading_workspace_sessions", "reading_workspaces", "reading_materials",
	"notebook_entry_categories", "practice_review_state", "practice_review_events",
	"notebook_entries", "notebook_categories",
	"document_chunks", "documents", "knowledge_bases", "library_files",
	"workspace_jobs", "users",
}

// atSecond is a whole second on a fixed day, the same helper every package's
// database tests carry. Timestamps are spelled out because DATETIME keeps whole
// seconds: two rows written in the same second tie, and a case that left the
// clock to the machine would be asserting whatever the two writes happened to
// get.
func atSecond(second int) time.Time {
	return time.Date(2026, 10, 1, 12, 0, second, 0, time.UTC)
}

// sinkFixture is the four sinks a deployment would hand the package service,
// over the four domains' own storage adapters.
//
// They are the real repositories and not doubles, which is the whole point: the
// sinks are thin enough that a double would be a copy of them, and what is worth
// checking here is every layer below.
type sinkFixture struct {
	resource  *data.Resource
	caller    authz.Caller
	owner     int64
	sessions  sessionSink
	materials materialSink
	questions questionSink
	bases     knowledgeBaseSink
}

func newSinkFixture(t *testing.T) *sinkFixture {
	t.Helper()
	resource := testdb.Open(t, "workspacesinks")
	testdb.Reset(t, resource, sinkTableOrder...)
	owner := accountFor(t, resource, "alice")
	notebookService, err := notebook.NewService(notebook.NewGORMRepository(resource.GORM()))
	if err != nil {
		t.Fatalf("build the question bank: %v", err)
	}
	return &sinkFixture{
		resource:  resource,
		owner:     owner,
		caller:    authz.Caller{UserID: owner, Role: authz.RoleUser},
		sessions:  sessionSink{store: session.NewGORMRepository(resource.GORM())},
		materials: materialSink{store: reading.NewGORMRepository(resource.GORM())},
		questions: questionSink{
			service: notebookService,
			store:   notebook.NewGORMRepository(resource.GORM()),
		},
		bases: knowledgeBaseSink{store: kb.NewGORMRepository(resource.GORM())},
	}
}

// accountFor writes the account every row here belongs to. Every one of the four
// domains has an owner column with a foreign key to `users`, so a fixture that
// skipped this would be testing the constraint rather than the sink.
func accountFor(t *testing.T, resource *data.Resource, username string) int64 {
	t.Helper()
	testdb.Exec(t, resource,
		"INSERT INTO users (username, password, learner_profile, extra) VALUES (?, 'hash', '{}', '{}')",
		username)
	var row struct {
		UserID int64 `gorm:"column:user_id"`
	}
	if err := resource.GORM().Raw("SELECT user_id FROM users WHERE username = ?", username).
		Scan(&row).Error; err != nil {
		t.Fatalf("read account %s: %v", username, err)
	}
	return row.UserID
}

// objectOf encodes one payload the way the source for that kind does, so that a
// case here is exercising the same JSON an export writes rather than a second
// shape written for the test.
func objectOf(t *testing.T, ref workspace.Ref, payload any) workspace.Object {
	t.Helper()
	encoded, err := encodePayload(payload)
	if err != nil {
		t.Fatalf("encode %s: %v", ref, err)
	}
	return workspace.Object{Ref: ref, Data: encoded}
}

func rowsIn(t *testing.T, resource *data.Resource, table, column string, value any) int64 {
	t.Helper()
	var count int64
	query := resource.GORM().Table(table)
	if column != "" {
		query = query.Where(column+" = ?", value)
	}
	if err := query.Count(&count).Error; err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func sessionRef() workspace.Ref {
	return workspace.Ref{Kind: workspace.KindSession, ID: "sess_test1"}
}

// TestDBTheSessionSinkWritesWhatThePackageHeldAndTakesItBack is the round trip
// for the kind with children: one row and its messages in, the same out.
func TestDBTheSessionSinkWritesWhatThePackageHeldAndTakesItBack(t *testing.T) {
	fixture := newSinkFixture(t)
	ref := sessionRef()
	object := objectOf(t, ref, sessionPayload{
		Session: session.Session{ID: "sess_test1", Title: "为什么天是蓝的"},
		Messages: []session.Message{
			{ID: "msg_1", SessionID: "sess_test1", Role: session.RoleUser, Content: "为什么天是蓝的"},
			{ID: "msg_2", SessionID: "sess_test1", Role: session.RoleAssistant, Content: "因为瑞利散射"},
		},
	})

	landed, err := fixture.sessions.Write(context.Background(), fixture.caller, []workspace.Object{object})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if len(landed) != 1 || landed[0] != (workspace.Written{Kind: workspace.KindSession, ID: ref.ID}) {
		t.Fatalf("Write() reported %+v", landed)
	}
	if rows := rowsIn(t, fixture.resource, "messages", "session_id", ref.ID); rows != 2 {
		t.Errorf("the conversation holds %d messages, want the two the package carried", rows)
	}
	// The owner is the caller's and not the payload's: a package names the work,
	// and the account it lands in is the one that asked for the import.
	var owner *int64
	if err := fixture.resource.GORM().Raw("SELECT owner_id FROM sessions WHERE session_id = ?", ref.ID).
		Scan(&owner).Error; err != nil {
		t.Fatalf("read the session's owner: %v", err)
	}
	if owner == nil || *owner != fixture.owner {
		t.Fatalf("the session landed owned by %v, want account %d", owner, fixture.owner)
	}

	if err := fixture.sessions.Undo(context.Background(), fixture.caller, landed); err != nil {
		t.Fatalf("Undo() error = %v", err)
	}
	if rows := rowsIn(t, fixture.resource, "sessions", "", nil); rows != 0 {
		t.Errorf("the conversation outlived its recovery: %d rows", rows)
	}
	if rows := rowsIn(t, fixture.resource, "messages", "", nil); rows != 0 {
		t.Errorf("the messages outlived the conversation: %d rows", rows)
	}
	// A second undo is not an error: recovery runs on a job that failed, and a
	// batch that failed halfway may have written less than it reported. The two
	// answers are the same answer to the person pressing the button.
	if err := fixture.sessions.Undo(context.Background(), fixture.caller, landed); err != nil {
		t.Errorf("the second Undo() error = %v, want the row simply not being there", err)
	}
}

// TestDBTheSessionSinkPutsAConversationBackInTheBin covers the one state that is
// not a column of its own: the recycle bin is two columns the sink applies after
// the insert, and the check constraint on them is the database's.
func TestDBTheSessionSinkPutsAConversationBackInTheBin(t *testing.T) {
	fixture := newSinkFixture(t)
	ref := sessionRef()
	object := objectOf(t, ref, sessionPayload{
		Session: session.Session{ID: ref.ID, Title: "扔掉的", DeletedAt: atSecond(30)},
	})
	if _, err := fixture.sessions.Write(context.Background(), fixture.caller, []workspace.Object{object}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	var row struct {
		IsDeleted bool `gorm:"column:is_deleted"`
	}
	if err := fixture.resource.GORM().Raw("SELECT is_deleted FROM sessions WHERE session_id = ?", ref.ID).
		Scan(&row).Error; err != nil {
		t.Fatalf("read the session: %v", err)
	}
	if !row.IsDeleted {
		t.Error("a conversation that was in the bin landed live")
	}
}

// TestDBTheMaterialSinkWritesAndTakesBackAMaterial is the kind whose children
// are four tables in two migrations.
func TestDBTheMaterialSinkWritesAndTakesBackAMaterial(t *testing.T) {
	fixture := newSinkFixture(t)
	documentID := "doc_1"
	ref := workspace.Ref{Kind: workspace.KindMaterial, ID: "mat_1"}
	object := objectOf(t, ref, materialPayload{
		Material: reading.Material{
			MaterialID: "mat_1", ContentID: documentID, Filename: "算法笔记.md",
			SourceType: "library", CreatedAt: atSecond(0),
		},
	})
	landed, err := fixture.materials.Write(context.Background(), fixture.caller, []workspace.Object{object})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	// A row that hangs off the material, so that the undo has something to take
	// with it: a mark is the smallest of the four children. Its session is NULL
	// rather than a made-up id — the mark's session is a foreign key to
	// conversations (00012), and this fixture has no conversation.
	testdb.Exec(t, fixture.resource, "INSERT INTO reading_marks "+
		"(mark_id, material_id, session_id, kind, locator_json, body, revision, creat_time) "+
		"VALUES (?, ?, NULL, ?, ?, ?, ?, ?)",
		"mark_1", ref.ID, "note", `{"start":0,"end":3}`, "here", 1, atSecond(0))
	if rows := rowsIn(t, fixture.resource, "reading_materials", "material_id", ref.ID); rows != 1 {
		t.Fatalf("the material landed %d times, want once", rows)
	}
	if err := fixture.materials.Undo(context.Background(), fixture.caller, landed); err != nil {
		t.Fatalf("Undo() error = %v", err)
	}
	if rows := rowsIn(t, fixture.resource, "reading_materials", "", nil); rows != 0 {
		t.Errorf("the material outlived its recovery: %d rows", rows)
	}
	if rows := rowsIn(t, fixture.resource, "reading_marks", "", nil); rows != 0 {
		t.Errorf("a mark outlived the material it hung off: %d rows", rows)
	}
}
