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

	"backend/internal/authz"
	"backend/internal/data"
	"backend/internal/data/testdb"
)

// dbTables are the tables these tests write, children first. turn_event_types is
// not one of them: the migration seeds it, and it is not this package's to
// empty. users comes last, because a conversation's owner column points at it.
var dbTables = []string{"turn_events", "turns", "summaries", "messages", "sessions", "users"}

func newDBRepository(t *testing.T) (*GORMRepository, *data.Resource) {
	t.Helper()
	resource := testdb.Open(t, "session")
	testdb.Reset(t, resource, dbTables...)
	seedAccounts(t, resource)
	return NewGORMRepository(resource.GORM()), resource
}

// seedAccounts writes the accounts a stored conversation can belong to.
//
// Migration 00014 made owner_id a foreign key into users, so a fixture that
// stored a conversation owned by testOwnerID and left the account unwritten
// would be refused by the database rather than reach the query under test. The
// ownerless state those tests also use is NULL, which is nobody's row and needs
// no account here.
func seedAccounts(t *testing.T, resource *data.Resource) {
	t.Helper()
	testdb.Account(t, resource, testOwnerID, "session_owner")
	testdb.Account(t, resource, testOwnerID+1, "session_other")
}

// atSecond is a whole second on a fixed day. Timestamps are spelled out because
// DATETIME keeps whole seconds: two rows written in the same second tie, and an
// ordering asserted over tied rows asserts whatever the plan did that time.
func atSecond(second int) time.Time {
	return time.Date(2026, 10, 1, 12, 0, second, 0, time.UTC)
}

// storeSession creates one session owned by the account these tests act as, as
// of a time the caller chooses.
func storeSession(t *testing.T, repo *GORMRepository, id string, at time.Time) Session {
	t.Helper()
	return storeOwnedSession(t, repo, id, testOwnerID, at)
}

// storeOwnedSession writes a session with a stated owner. An ownerID of zero is
// the NULL state §2.5 defines — a row from before V5, belonging to no account —
// and is written as such rather than as a zero the column would hold.
func storeOwnedSession(t *testing.T, repo *GORMRepository, id string, ownerID int64, at time.Time) Session {
	t.Helper()
	record := Session{ID: id, Title: id, OwnerID: ownerID, CreatedAt: at, UpdatedAt: at}
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
	}, testCaller())
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

// listSessions lists as the account these tests act as. The listing is per
// account, so a listing with no account in it is not a listing of everything:
// see listSessionsAs.
func listSessions(t *testing.T, repo *GORMRepository, options ListOptions) []string {
	t.Helper()
	return listSessionsAs(t, repo, options, testCaller())
}

func listSessionsAs(t *testing.T, repo *GORMRepository, options ListOptions, caller authz.Caller) []string {
	t.Helper()
	options.Caller = caller
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
	stored, err := repo.Session(context.Background(), "s_b", testCaller())
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
	if err := repo.SetDeleted(context.Background(), "s_b", true, atSecond(10), testCaller()); err != nil {
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
	binned, err := repo.Session(context.Background(), "s_b", testCaller())
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

	messages, err := repo.Messages(context.Background(), "s_1", testCaller())
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
	}, testCaller())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("appending to a session that is not there: %v, want %v", err, ErrNotFound)
	}
	if got := countRows(t, resource, "messages"); got != 3 {
		t.Errorf("%d messages are stored, want the refused one rolled back", got)
	}

	// And a session with nothing appended has no messages and no error.
	if messages, err := repo.Messages(context.Background(), "s_1", testCaller()); err != nil || len(messages) != 3 {
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
	if err := repo.SetDeleted(context.Background(), "s_1", false, atSecond(3), testCaller()); err != nil {
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
	if err := repo.Delete(context.Background(), "s_1", testCaller()); err != nil {
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
	if err := repo.Delete(context.Background(), "s_1", testCaller()); !errors.Is(err, ErrNotFound) {
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
	if err := repo.Rename(context.Background(), "s_1", "s_1", atSecond(1), testCaller()); err != nil {
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
	if err := repo.Rename(context.Background(), "s_missing", "任何名字", atSecond(2), testCaller()); !errors.Is(err, ErrNotFound) {
		t.Errorf("renaming a session that is not there: %v, want %v", err, ErrNotFound)
	}
	if err := repo.SetDeleted(context.Background(), "s_missing", true, atSecond(2), testCaller()); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting a session that is not there: %v, want %v", err, ErrNotFound)
	}
}

// TestDBOneAccountsConversationsAreNotInAnothers is the contract the whole of V5
// rests on, checked where it is really enforced: the SQL. Everything above this
// line would pass against a database with no owner column at all, which is why
// none of it is the evidence for this.
func TestDBOneAccountsConversationsAreNotInAnothers(t *testing.T) {
	repo, resource := newDBRepository(t)
	mine := testCaller()
	theirs := otherCaller()

	storeSession(t, repo, "s_mine", atSecond(1))
	storeOwnedSession(t, repo, "s_theirs", theirs.UserID, atSecond(2))
	// A row from before V5: written by migration 00014's era, belonging to nobody.
	storeOwnedSession(t, repo, "s_legacy", 0, atSecond(3))

	// The listing is per account. s_theirs is not missing from it in the sense of
	// having been filtered out after the fact — the row is not in the query's
	// result, which is what makes this a scope rather than a check.
	if got := listSessions(t, repo, ListOptions{}); !slices.Equal(got, []string{"s_mine"}) {
		t.Errorf("my listing is %v, want only my own session", got)
	}
	if got := listSessionsAs(t, repo, ListOptions{}, theirs); !slices.Equal(got, []string{"s_theirs"}) {
		t.Errorf("their listing is %v, want only theirs", got)
	}
	// The administrator is the one account that sees a row belonging to nobody.
	// Reading somebody else's conversation is not what that buys: adminCaller sees
	// s_legacy and not s_theirs, and not s_mine either.
	if got := listSessionsAs(t, repo, ListOptions{}, adminCaller()); !slices.Equal(got, []string{"s_legacy"}) {
		t.Errorf("the administrator's listing is %v, want the ownerless row alone", got)
	}

	// Every read and every write of a row that is somebody else's answers the way
	// it answers an id that was never issued (AUTH-06). The important one is the
	// first: it is not that the write is refused, it is that there is nothing to
	// refuse.
	if _, err := repo.Session(context.Background(), "s_theirs", mine); !errors.Is(err, ErrNotFound) {
		t.Errorf("reading somebody else's session: %v, want %v", err, ErrNotFound)
	}
	if err := repo.Rename(context.Background(), "s_theirs", "我的了", atSecond(4), mine); !errors.Is(err, ErrNotFound) {
		t.Errorf("renaming somebody else's session: %v, want %v", err, ErrNotFound)
	}
	if err := repo.SetDeleted(context.Background(), "s_theirs", true, atSecond(4), mine); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting somebody else's session: %v, want %v", err, ErrNotFound)
	}
	if err := repo.Delete(context.Background(), "s_theirs", mine); !errors.Is(err, ErrNotFound) {
		t.Errorf("erasing somebody else's session: %v, want %v", err, ErrNotFound)
	}
	if err := repo.AppendMessage(context.Background(), Message{
		ID: "msg_intruder", SessionID: "s_theirs", Role: RoleUser,
		Content: "写进别人的会话", CreatedAt: atSecond(4),
	}, mine); !errors.Is(err, ErrNotFound) {
		t.Errorf("writing into somebody else's session: %v, want %v", err, ErrNotFound)
	}
	// Messages are reached through their session, so the same filter has to hold
	// one table away. Empty and refused are different answers here: an empty
	// history is a conversation nobody has written in.
	if _, err := repo.Messages(context.Background(), "s_theirs", mine); !errors.Is(err, ErrNotFound) {
		t.Errorf("reading somebody else's history: %v, want %v", err, ErrNotFound)
	}

	// And none of it wrote: sqlite-style "refused" that left a row behind would
	// pass every error check above.
	if got := countRows(t, resource, "messages"); got != 0 {
		t.Errorf("%d messages are stored, want none: every write above was for a row that is not there", got)
	}
	if got := countRows(t, resource, "sessions"); got != 3 {
		t.Errorf("%d sessions are stored, want all 3 still there", got)
	}
	untouched, err := repo.Session(context.Background(), "s_theirs", theirs)
	if err != nil {
		t.Fatalf("reading their own session: %v", err)
	}
	if untouched.Title != "s_theirs" || untouched.Deleted() {
		t.Errorf("their session is now %+v, want it as it was written", untouched)
	}
}

// TestDBClaimingIsHowAnOwnerlessSessionFindsAnAccount covers the exit from the
// state migration 00014 left every earlier row in (§2.5). It is a write whose
// whole decision is in its WHERE clause, so the two ways it can come up empty —
// the row is somebody else's, the row was never there — are one answer, and the
// one way it can come up full is a row that now belongs to the claimer.
func TestDBClaimingIsHowAnOwnerlessSessionFindsAnAccount(t *testing.T) {
	repo, resource := newDBRepository(t)
	mine := testCaller()
	theirs := otherCaller()

	storeOwnedSession(t, repo, "s_legacy", 0, atSecond(1))
	storeSession(t, repo, "s_mine", atSecond(2))

	// Claiming somebody else's session changes nothing. It is refused as a row
	// that is not there rather than as a conflict, because the second fact — that
	// the row exists and belongs to somebody — is not one an account is entitled
	// to learn.
	if err := repo.Claim(context.Background(), "s_mine", theirs); !errors.Is(err, ErrNotFound) {
		t.Errorf("claiming somebody's session: %v, want %v", err, ErrNotFound)
	}
	if err := repo.Claim(context.Background(), "s_missing", mine); !errors.Is(err, ErrNotFound) {
		t.Errorf("claiming a session that was never there: %v, want %v", err, ErrNotFound)
	}
	var owner *int64
	if err := resource.GORM().Raw(
		"SELECT owner_id FROM sessions WHERE session_id = 's_mine'").Scan(&owner).Error; err != nil {
		t.Fatalf("read the owner of their session: %v", err)
	}
	if owner == nil || *owner != mine.UserID {
		t.Errorf("their session is owned by %v, want it untouched at %d", owner, mine.UserID)
	}

	// Before a claim, the ownerless row is the administrator's alone: it is not
	// that the user is refused it, it is that there is no row for them to be
	// refused about.
	if _, err := repo.Session(context.Background(), "s_legacy", mine); !errors.Is(err, ErrNotFound) {
		t.Errorf("reading a session belonging to nobody: %v, want %v", err, ErrNotFound)
	}
	if _, err := repo.Session(context.Background(), "s_legacy", adminCaller()); err != nil {
		t.Fatalf("the administrator reading a session belonging to nobody: %v", err)
	}

	// The claim itself, and then the two views of it: the claimer has it, and the
	// administrator has it as the claimer's.
	if err := repo.Claim(context.Background(), "s_legacy", mine); err != nil {
		t.Fatalf("claiming a session belonging to nobody: %v", err)
	}
	claimed, err := repo.Session(context.Background(), "s_legacy", mine)
	if err != nil {
		t.Fatalf("reading back the session just claimed: %v", err)
	}
	if claimed.OwnerID != mine.UserID {
		t.Errorf("the claimed session is owned by %d, want %d", claimed.OwnerID, mine.UserID)
	}
	if _, err := repo.Session(context.Background(), "s_legacy", adminCaller()); !errors.Is(err, ErrNotFound) {
		t.Errorf("the administrator after the claim: %v, want the row to be somebody's now", err)
	}
	// Claiming what is already yours is the outcome the caller asked for, and a
	// second click is not an error. It is also not a second write: the row is
	// unchanged.
	if err := repo.Claim(context.Background(), "s_legacy", mine); err != nil {
		t.Errorf("claiming a session that is already mine: %v", err)
	}
	if got := countRows(t, resource, "sessions"); got != 2 {
		t.Errorf("%d sessions are stored, want both: a claim renames an owner, it does not move a row", got)
	}
}
