package session

import (
	"context"
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
