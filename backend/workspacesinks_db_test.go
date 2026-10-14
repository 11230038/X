package main

// The four sinks against a real database.
//
// The sinks are the one place in this build where a package becomes rows, so
// what they do is a claim about storage and only a database can settle it. Three
// of the four claims are not even the sink's to make:
//
//   - The owner a row lands with is a column the sink fills in, and an
//     import that wrote another account's rows would look exactly like this one
//     in memory.
//   - The undo of each kind is one statement whose effect is a cascade spread
//     across a migration, and a double can only restate what somebody believed
//     the migration said (this is the same argument reading's and notebook's
//     delete tests make, and the reason those two methods exist at all, §2.13).
//   - A collection's documents are refused by a foreign key to `library_files`
//     when the file is not on this instance, which is a sentence in 00007 and
//     not a line of Go.
//
// One database per package, so nothing here calls t.Parallel.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"backend/internal/authz"
	"backend/internal/data"
	"backend/internal/data/testdb"
	"backend/internal/kb"
	"backend/internal/notebook"
	"backend/internal/reading"
	"backend/internal/session"
	"backend/internal/turn"
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
	if rows := rowsIn(t, fixture.resource, "messages", "session_id = ?", ref.ID); rows != 2 {
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
	if rows := rowsIn(t, fixture.resource, "reading_materials", "material_id = ?", ref.ID); rows != 1 {
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

// TestDBTheQuestionSinkFilesTheQuestionUnderItsOwnAddress pins the one thing
// this sink does that the other three do not: it goes through the bank's own
// save, so the address is computed rather than taken, and a package whose object
// is filed under an address its own text does not hash to is refused before
// anything is written.
func TestDBTheQuestionSinkFilesTheQuestionUnderItsOwnAddress(t *testing.T) {
	fixture := newSinkFixture(t)
	question := notebook.Question{
		Text: "什么是闭包", Type: notebook.QuestionChoice,
		Options: []notebook.Option{{Key: "A", Text: "一个函数"}, {Key: "B", Text: "一个变量"}},
	}
	address := notebook.QuestionID(question.Text, question.Type)
	ref := workspace.Ref{Kind: workspace.KindQuestion, ID: address}
	object := objectOf(t, ref, questionPayload{
		Entry: notebook.Entry{
			Question:   question,
			Categories: []string{"编程"},
			Answer:     notebook.Answer{Bookmarked: true},
		},
	})

	landed, err := fixture.questions.Write(context.Background(), fixture.caller, []workspace.Object{object})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if len(landed) != 1 || landed[0].ID != address {
		t.Fatalf("Write() reported %+v, want the address %s", landed, address)
	}
	if rows := rowsIn(t, fixture.resource, "notebook_entries", "question_id = ?", address); rows != 1 {
		t.Fatalf("the question landed %d times, want once", rows)
	}
	// The bookmark travels: it is the user's own filing, and a notebook that
	// arrived without it would have lost the part somebody made by hand.
	var bookmarked bool
	if err := fixture.resource.GORM().Raw("SELECT bookmarked FROM notebook_entries WHERE question_id = ?", address).
		Scan(&bookmarked).Error; err != nil {
		t.Fatalf("read the question: %v", err)
	}
	if !bookmarked {
		t.Error("the bookmark did not travel with the question")
	}

	if err := fixture.questions.Undo(context.Background(), fixture.caller, landed); err != nil {
		t.Fatalf("Undo() error = %v", err)
	}
	if rows := rowsIn(t, fixture.resource, "notebook_entries", "", nil); rows != 0 {
		t.Errorf("the question outlived its recovery: %d rows", rows)
	}
}

// TestDBTheQuestionSinkRefusesAnObjectFiledUnderTheWrongAddress is the negative
// half of the case above, and it is the one a package that was edited by hand
// would meet. Nothing lands, because the row the recovery could take back is
// addressed by the ref and the row the bank would write is addressed by the
// text.
func TestDBTheQuestionSinkRefusesAnObjectFiledUnderTheWrongAddress(t *testing.T) {
	fixture := newSinkFixture(t)
	object := objectOf(t, workspace.Ref{Kind: workspace.KindQuestion, ID: "qn_notthisaddress"},
		questionPayload{Entry: notebook.Entry{
			Question: notebook.Question{Text: "什么是闭包", Type: notebook.QuestionChoice},
		}})

	if _, err := fixture.questions.Write(context.Background(), fixture.caller, []workspace.Object{object}); !errors.Is(err, workspace.ErrBadPackage) {
		t.Fatalf("Write() error = %v, want ErrBadPackage", err)
	}
	if rows := rowsIn(t, fixture.resource, "notebook_entries", "", nil); rows != 0 {
		t.Errorf("a question filed under somebody else's address was written anyway: %d rows", rows)
	}
}

// TestDBTheKnowledgeBaseSinkCarriesTheCatalogueAndNothingElse is the case §2.13
// leaves to the indexer: the collection and what was in it, without the text,
// the chunks or the vectors — a document that lands empty and pending, which is
// the state the worker already knows how to pick up.
func TestDBTheKnowledgeBaseSinkCarriesTheCatalogueAndNothingElse(t *testing.T) {
	fixture := newSinkFixture(t)
	fileID := addSinkLibraryFile(t, fixture.resource, fixture.owner)
	ref := workspace.Ref{Kind: workspace.KindKnowledgeBase, ID: "kb_1"}
	object := objectOf(t, ref, knowledgeBasePayload{
		KnowledgeBase: kb.KnowledgeBase{
			ID: "kb_1", Name: "操作系统", Provider: kb.ProviderNative,
			EmbeddingModel: "test-embed", CreatedAt: atSecond(0),
		},
		Documents: []kb.Document{{
			ID: "doc_1", FileID: fileID, Filename: fileID + ".md", Engine: "markdown",
			// The counts and the state a package carries are about the instance
			// it came from, and the write is what replaces them.
			TextChars: 4096, VectorState: kb.VectorStateReady,
			ChunkSize: 800, ChunkOverlap: 100, ActiveRevision: 1, CreatedAt: atSecond(0),
		}},
	})

	landed, err := fixture.bases.Write(context.Background(), fixture.caller, []workspace.Object{object})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	var row struct {
		TextChars   int64  `gorm:"column:text_chars"`
		VectorState string `gorm:"column:vector_state"`
	}
	if err := fixture.resource.GORM().Raw(
		"SELECT text_chars, vector_state FROM documents WHERE document_id = ?", "doc_1").
		Scan(&row).Error; err != nil {
		t.Fatalf("read the document: %v", err)
	}
	if row.TextChars != 0 || row.VectorState != kb.VectorStatePending {
		t.Errorf("the document landed as %+v, want no text and vectors pending", row)
	}
	if rows := rowsIn(t, fixture.resource, "document_chunks", "", nil); rows != 0 {
		t.Errorf("the import wrote %d chunks, and it carried none", rows)
	}

	if err := fixture.bases.Undo(context.Background(), fixture.caller, landed); err != nil {
		t.Fatalf("Undo() error = %v", err)
	}
	for _, table := range []string{"knowledge_bases", "documents", "document_chunks"} {
		if rows := rowsIn(t, fixture.resource, table, "", nil); rows != 0 {
			t.Errorf("%s outlived the recovery: %d rows", table, rows)
		}
	}
}

// TestDBTheKnowledgeBaseSinkCannotImportADocumentWithoutItsFile is the divergence
// this stage reports rather than hides.
//
// `documents.file_id` is NOT NULL with a foreign key to `library_files` (00007),
// and a package carries no files — a file's bytes are the library's. So a
// collection whose documents name files this instance does not have cannot be
// imported at all, and the database is what says so. The preview does not warn of
// it: the plan looks at collections, and a file is not a kind. What is left
// behind is a recoverable job, which is why this case checks both halves.
func TestDBTheKnowledgeBaseSinkCannotImportADocumentWithoutItsFile(t *testing.T) {
	fixture := newSinkFixture(t)
	ref := workspace.Ref{Kind: workspace.KindKnowledgeBase, ID: "kb_1"}
	object := objectOf(t, ref, knowledgeBasePayload{
		KnowledgeBase: kb.KnowledgeBase{
			ID: "kb_1", Name: "操作系统", Provider: kb.ProviderNative,
			EmbeddingModel: "test-embed", CreatedAt: atSecond(0),
		},
		Documents: []kb.Document{{
			ID: "doc_1", FileID: "lib_00000000000000000000000000000000",
			Filename: "nowhere.md", Engine: "markdown",
			ChunkSize: 800, ChunkOverlap: 100, ActiveRevision: 1, CreatedAt: atSecond(0),
		}},
	})

	landed, err := fixture.bases.Write(context.Background(), fixture.caller, []workspace.Object{object})
	if err == nil {
		t.Fatal("a collection whose file is not here was imported")
	}
	// The base's own row is written before its documents are, and it is recorded
	// before them too — so what a recovery has to take back is exactly what the
	// sink reported on its way out.
	if len(landed) != 1 {
		t.Fatalf("Write() reported %+v, want the one row it wrote before the refusal", landed)
	}
	if rows := rowsIn(t, fixture.resource, "documents", "", nil); rows != 0 {
		t.Errorf("the refused document left %d rows behind", rows)
	}
	if err := fixture.bases.Undo(context.Background(), fixture.caller, landed); err != nil {
		t.Fatalf("Undo() error = %v", err)
	}
	if rows := rowsIn(t, fixture.resource, "knowledge_bases", "", nil); rows != 0 {
		t.Errorf("the collection outlived the recovery: %d rows", rows)
	}
}

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
				ID: "kb_1", Name: "操作系统", Provider: kb.ProviderNative, CreatedAt: atSecond(0)}}),
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
				ID: "kb_2", Name: "编译原理", Provider: kb.ProviderNative, CreatedAt: atSecond(0)}}),
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

// TestDBTheBusyPortCountsTheTurnsInFlight is AC-09's half of §2.13's second
// discipline, and it is a claim about a SQL predicate: the three statuses that
// mean "something is happening" are strings in a column, and a count that named
// the wrong ones would let an import run over a turn that is mid-sentence — or
// refuse every import for ever because it counted finished turns too.
func TestDBTheBusyPortCountsTheTurnsInFlight(t *testing.T) {
	fixture := newSinkFixture(t)
	store := session.NewGORMRepository(fixture.resource.GORM())
	if err := store.Create(context.Background(), session.Session{ID: "sess_busy", OwnerID: fixture.owner}); err != nil {
		t.Fatalf("write the conversation the turns hang off: %v", err)
	}
	// A turn hangs off a conversation by a foreign key (00002), so the four
	// below are four states of one conversation rather than four fixtures.
	for index, status := range []turn.Status{
		turn.StatusQueued, turn.StatusRunning, turn.StatusWaitingInput,
		turn.StatusCompleted, turn.StatusFailed, turn.StatusCancelled,
	} {
		testdb.Exec(t, fixture.resource, "INSERT INTO turns "+
			"(turn_id, session_id, status, state_version, creat_time) VALUES (?, ?, ?, 1, ?)",
			fmt.Sprintf("turn_%d", index), "sess_busy", string(status), atSecond(index))
	}

	running, err := runningTurns{db: fixture.resource.GORM()}.Running(context.Background())
	if err != nil {
		t.Fatalf("Running() error = %v", err)
	}
	if running != 3 {
		t.Errorf("Running() = %d, want the three turns that have not finished", running)
	}
}

// addSinkLibraryFile writes the row a document's file foreign key needs.
//
// The path and the digest are not decoration: migration 00005 checks that the
// path is md/lib_<32 lowercase hex digits>.md with the id as the name before the
// extension, and that the digest is sixty-four lowercase hex digits — so a
// document fixture cannot invent a file id, it has to be one the library itself
// would have stored.
func addSinkLibraryFile(t *testing.T, resource *data.Resource, owner int64) string {
	t.Helper()
	id := fmt.Sprintf("lib_%032x", 1)
	sum := sha256.Sum256([]byte(id))
	testdb.Exec(t, resource, "INSERT INTO library_files "+
		"(id, sha256, filename, mime_type, size_bytes, library_path, is_deleted, owner_id) "+
		"VALUES (?, ?, ?, 'text/markdown', 0, ?, FALSE, ?)",
		id, hex.EncodeToString(sum[:]), id+".md", "md/"+id+".md", owner)
	return id
}
