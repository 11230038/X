package session

// The conversation half of the storage contract, checked against a real seekdb:
// what a listing promises about order and paging, what a message's order is
// inside a session, the check constraint that keeps the recycle bin's two
// columns in step, and the two rules a row count cannot express — a delete that
// removed nothing is a session that is not there, while an update that changed
// nothing is a session that is.
//
// None of it is knowable from a double, which is why it is here. The fake in
// fixture_test.go stays for the service's own logic and for the storage failure
// a test has to inject.
//
// One database per package means nothing here calls t.Parallel, and every test
// starts on empty tables.

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"backend/internal/data"
	"backend/internal/data/testdb"
)

// dbTables are the tables these tests write, children first. turn_event_types is
// not one of them: the migration seeds it, and it is not this package's to
// empty.
var dbTables = []string{"turn_events", "turns", "summaries", "messages", "sessions"}

func newDBRepository(t *testing.T) (*GORMRepository, *data.Resource) {
	t.Helper()
	resource := testdb.Open(t, "session")
	testdb.Reset(t, resource, dbTables...)
	return NewGORMRepository(resource.GORM()), resource
}

// atSecond is a whole second on a fixed day. Timestamps are spelled out because
// DATETIME keeps whole seconds: two rows written in the same second tie, and an
// ordering asserted over tied rows asserts whatever the plan did that time.
func atSecond(second int) time.Time {
	return time.Date(2026, 10, 1, 12, 0, second, 0, time.UTC)
}

// storeSession creates one session as of a time the caller chooses.
func storeSession(t *testing.T, repo *GORMRepository, id string, at time.Time) Session {
	t.Helper()
	record := Session{ID: id, Title: id, CreatedAt: at, UpdatedAt: at}
	if err := repo.Create(context.Background(), record); err != nil {
		t.Fatalf("create session %s: %v", id, err)
	}
	return record
}

// appendMessage writes one message through the adapter's own path, which is what
// marks the session active as well.
func appendMessage(t *testing.T, repo *GORMRepository, id, sessionID string,
	role Role, at time.Time) {
	t.Helper()
	err := repo.AppendMessage(context.Background(), Message{
		ID: id, SessionID: sessionID, Role: role, Content: "说了一句", CreatedAt: at,
	})
	if err != nil {
		t.Fatalf("append message %s: %v", id, err)
	}
}

// storeTurn writes the row a cascade test needs to see go: turns reference a
// session, so a deleted session has to take them with it.
func storeTurn(t *testing.T, resource *data.Resource, turnID, sessionID string, at time.Time) {
	t.Helper()
	testdb.Exec(t, resource, "INSERT INTO turns "+
		"(turn_id, session_id, status, state_version, creat_time) VALUES (?, ?, 'running', 1, ?)",
		turnID, sessionID, at)
}

func countRows(t *testing.T, resource *data.Resource, table string) int64 {
	t.Helper()
	var count int64
	err := resource.GORM().Raw("SELECT COUNT(*) FROM `" + table + "`").Scan(&count).Error
	if err != nil {
		t.Fatalf("count the rows of %s: %v", table, err)
	}
	return count
}

// sessionIDs is a listing by id, which is what its order is about.
func sessionIDs(sessions []Session) []string {
	ids := make([]string, 0, len(sessions))
	for _, record := range sessions {
		ids = append(ids, record.ID)
	}
	return ids
}

func listSessions(t *testing.T, repo *GORMRepository, options ListOptions) []string {
	t.Helper()
	sessions, err := repo.List(context.Background(), options)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	return sessionIDs(sessions)
}

func TestDBTheListingIsByActivityThenID(t *testing.T) {
	repo, resource := newDBRepository(t)
	storeSession(t, repo, "s_old", atSecond(1))
	storeSession(t, repo, "s_b", atSecond(5))
	storeSession(t, repo, "s_c", atSecond(5))

	// What was stored is what the fixture said: without this the ordering below
	// would be asserting against a clock the fixture does not control.
	stored, err := repo.Session(context.Background(), "s_b")
	if err != nil {
		t.Fatalf("read the session back: %v", err)
	}
	if !stored.UpdatedAt.Equal(atSecond(5)) || !stored.CreatedAt.Equal(atSecond(5)) {
		t.Fatalf("the stored session is %+v, want the times the fixture wrote", stored)
	}

	// Most recently active first, and the two that share a second are decided by
	// id: without that, a listing whose order changed between identical requests
	// would make paging skip rows and repeat others.
	want := []string{"s_b", "s_c", "s_old"}
	if got := listSessions(t, repo, ListOptions{}); !slices.Equal(got, want) {
		t.Errorf("the listing is %v, want %v", got, want)
	}

	// A zero limit is the default page rather than no rows.
	if got := listSessions(t, repo, ListOptions{Limit: 0}); len(got) != 3 {
		t.Errorf("a listing with no limit returned %d sessions, want all 3", len(got))
	}
	if got := listSessions(t, repo, ListOptions{Limit: 2}); !slices.Equal(got, []string{"s_b", "s_c"}) {
		t.Errorf("the first page is %v, want the two most recent", got)
	}
	if got := listSessions(t, repo, ListOptions{Limit: 2, Offset: 1}); !slices.Equal(got, []string{"s_c", "s_old"}) {
		t.Errorf("the second page is %v, want the two after the first", got)
	}

	// A message appended to the oldest session moves it to the front: this is
	// the transaction AppendMessage is, seen from the listing.
	appendMessage(t, repo, "msg_1", "s_old", RoleUser, atSecond(9))
	want = []string{"s_old", "s_b", "s_c"}
	if got := listSessions(t, repo, ListOptions{}); !slices.Equal(got, want) {
		t.Errorf("after a message the listing is %v, want %v", got, want)
	}

	// The bin is a listing of its own: a client showing both would have to
	// filter every row to know which actions to offer.
	if err := repo.SetDeleted(context.Background(), "s_b", true, atSecond(10)); err != nil {
		t.Fatalf("move a session to the bin: %v", err)
	}
	want = []string{"s_old", "s_c"}
	if got := listSessions(t, repo, ListOptions{}); !slices.Equal(got, want) {
		t.Errorf("the live listing is %v, want %v", got, want)
	}
	if got := listSessions(t, repo, ListOptions{Deleted: true}); !slices.Equal(got, []string{"s_b"}) {
		t.Errorf("the bin holds %v, want the deleted session", got)
	}
	// And the session itself is still readable, in the state it is in: the
	// service decides what that state allows, not the adapter.
	binned, err := repo.Session(context.Background(), "s_b")
	if err != nil {
		t.Fatalf("read a session in the bin: %v", err)
	}
	if !binned.Deleted() || !binned.DeletedAt.Equal(atSecond(10)) {
		t.Errorf("the session in the bin is %+v, want the moment it went in", binned)
	}
	if got := countRows(t, resource, "sessions"); got != 3 {
		t.Errorf("%d sessions are stored, want all 3: the bin is a state, not a delete", got)
	}
}

func TestDBMessagesAreOldestFirstThenByID(t *testing.T) {
	repo, resource := newDBRepository(t)
	storeSession(t, repo, "s_1", atSecond(1))

	// Two of the three share a second — the same second the session was created
	// in — so the order inside it is decided by id. Written out of order on
	// purpose: what a model is shown has to be the order the messages happened
	// in, and the id is what carries that when the clock cannot.
	appendMessage(t, repo, "msg_b", "s_1", RoleUser, atSecond(1))
	appendMessage(t, repo, "msg_a", "s_1", RoleAssistant, atSecond(1))
	appendMessage(t, repo, "msg_late", "s_1", RoleUser, atSecond(4))

	messages, err := repo.Messages(context.Background(), "s_1")
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	want := []string{"msg_a", "msg_b", "msg_late"}
	ids := make([]string, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.ID)
	}
	if !slices.Equal(ids, want) {
		t.Errorf("the messages are %v, want %v", ids, want)
	}
	if len(messages) > 0 && messages[1].Capability != "" {
		t.Errorf("a message written with no capability carries %q", messages[1].Capability)
	}
	if len(messages) > 2 && messages[2].Content != "说了一句" {
		t.Errorf("the last message reads %q, want what was appended", messages[2].Content)
	}

	// A session that is not there is not a session with no messages: appending
	// to it is refused, and the message goes with the refusal rather than
	// describing a session nobody can find.
	err = repo.AppendMessage(context.Background(), Message{
		ID: "msg_orphan", SessionID: "s_missing", Role: RoleUser,
		Content: "说给一个不存在的会话", CreatedAt: atSecond(5),
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("appending to a session that is not there: %v, want %v", err, ErrNotFound)
	}
	if got := countRows(t, resource, "messages"); got != 3 {
		t.Errorf("%d messages are stored, want the refused one rolled back", got)
	}

	// And a session with nothing appended has no messages and no error.
	if messages, err := repo.Messages(context.Background(), "s_1"); err != nil || len(messages) != 3 {
		t.Errorf("the messages are %d long (err %v), want 3", len(messages), err)
	}
}

func TestDBASessionCannotBeHalfDeleted(t *testing.T) {
	repo, resource := newDBRepository(t)
	storeSession(t, repo, "s_1", atSecond(1))

	// The two columns say one thing between them, and the schema is what keeps
	// them saying it: a row marked deleted with no moment, or a moment with no
	// mark, is refused rather than stored as a state nothing can interpret.
	half := []struct{ name, query string }{
		{"deleted without a moment",
			"UPDATE sessions SET is_deleted = TRUE, deleted_at = NULL WHERE session_id = 's_1'"},
		{"a moment without the mark",
			"UPDATE sessions SET is_deleted = FALSE, deleted_at = NOW() WHERE session_id = 's_1'"},
	}
	for _, scenario := range half {
		if err := testdb.Try(resource, scenario.query); err == nil {
			t.Errorf("%s was accepted, want the schema to refuse it", scenario.name)
		}
	}

	// The coherent version of the first one goes through, so what refused the
	// others is the constraint and not the statement.
	err := testdb.Try(resource, "UPDATE sessions SET is_deleted = TRUE, deleted_at = NOW() "+
		"WHERE session_id = 's_1'")
	if err != nil {
		t.Fatalf("a coherent delete was refused: %v", err)
	}

	// The adapter writes both columns together, which is the schema's rule kept
	// from the other side: taking a session out of the bin clears both.
	if err := repo.SetDeleted(context.Background(), "s_1", false, atSecond(3)); err != nil {
		t.Fatalf("restore the session: %v", err)
	}
	var row struct {
		IsDeleted bool       `gorm:"column:is_deleted"`
		DeletedAt *time.Time `gorm:"column:deleted_at"`
	}
	err = resource.GORM().Raw(
		"SELECT is_deleted, deleted_at FROM sessions WHERE session_id = 's_1'").Scan(&row).Error
	if err != nil {
		t.Fatalf("read the two columns: %v", err)
	}
	if row.IsDeleted || row.DeletedAt != nil {
		t.Errorf("the restored session is deleted=%v at %v, want both cleared", row.IsDeleted, row.DeletedAt)
	}
}

func TestDBDeletingASessionTakesItsMessagesAndTurns(t *testing.T) {
	repo, resource := newDBRepository(t)
	storeSession(t, repo, "s_1", atSecond(1))
	storeSession(t, repo, "s_2", atSecond(1))
	appendMessage(t, repo, "msg_1", "s_1", RoleUser, atSecond(2))
	appendMessage(t, repo, "msg_2", "s_1", RoleAssistant, atSecond(3))
	appendMessage(t, repo, "msg_3", "s_2", RoleUser, atSecond(2))
	storeTurn(t, resource, "turn_1", "s_1", atSecond(4))

	// The delete takes the session's messages and turns with it through the
	// foreign keys, and leaves the other session's alone: what a permanent
	// delete has to be is the whole of one conversation and nothing of another.
	if err := repo.Delete(context.Background(), "s_1"); err != nil {
		t.Fatalf("delete the session: %v", err)
	}
	if got := countRows(t, resource, "messages"); got != 1 {
		t.Errorf("%d messages are left, want the other session's one", got)
	}
	if got := countRows(t, resource, "turns"); got != 0 {
		t.Errorf("%d turns are left, want the deleted session's gone", got)
	}
	if got := countRows(t, resource, "sessions"); got != 1 {
		t.Errorf("%d sessions are left, want the other one: a delete is for good", got)
	}

	// A delete reports the rows it removed, so this one has an answer to give
	// and it does not have to read the table to find it — unlike an update,
	// where a row written back its own values reports nothing changed.
	if err := repo.Delete(context.Background(), "s_1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting a session twice: %v, want %v", err, ErrNotFound)
	}
}

func TestDBAnUpdateThatChangesNothingIsNotAMissingSession(t *testing.T) {
	repo, resource := newDBRepository(t)
	storeSession(t, repo, "s_1", atSecond(1))

	// The row count an update reports is the number of rows whose values
	// changed. Writing a session back the values it already holds is such an
	// update, and it is what this database answers for one.
	result := resource.GORM().Exec(
		"UPDATE sessions SET title = ?, update_time = ? WHERE session_id = ?",
		"s_1", atSecond(1), "s_1")
	if result.Error != nil {
		t.Fatalf("write the row back to itself: %v", result.Error)
	}
	if result.RowsAffected != 0 {
		t.Fatalf("writing a row back its own values reported %d changed rows, want none: "+
			"the read in requireSession is only needed because of this", result.RowsAffected)
	}

	// Which is why the adapter reads the session instead of believing that
	// count: renaming a session to the title it already has, in the second it
	// was last active, succeeds.
	if err := repo.Rename(context.Background(), "s_1", "s_1", atSecond(1)); err != nil {
		t.Errorf("renaming a session to what it is already called: %v", err)
	}
	// Nor is a message appended in the same second as the session's own write a
	// message for a session that has gone.
	appendMessage(t, repo, "msg_1", "s_1", RoleUser, atSecond(1))
	if got := countRows(t, resource, "messages"); got != 1 {
		t.Errorf("%d messages are stored, want the one appended", got)
	}

	// A session that really is not there is still reported as missing, which is
	// the answer the read is there to give.
	if err := repo.Rename(context.Background(), "s_missing", "任何名字", atSecond(2)); !errors.Is(err, ErrNotFound) {
		t.Errorf("renaming a session that is not there: %v, want %v", err, ErrNotFound)
	}
	if err := repo.SetDeleted(context.Background(), "s_missing", true, atSecond(2)); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting a session that is not there: %v, want %v", err, ErrNotFound)
	}
}
