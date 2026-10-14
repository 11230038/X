package session

import (
	"context"
	"fmt"
	"testing"
	"time"

	"backend/internal/authz"
)

// The account these tests act as, and the one that holds every row the fixtures
// seed. It is a number rather than a name because an account id is what the
// column holds, and the point of the constant is that the seeded rows and the
// caller agree about it — a fixture that seeded ownerless rows and then called
// as this account would fail every test here for a reason that has nothing to do
// with what any of them are about.
const testOwnerID int64 = 7

func testCaller() authz.Caller {
	return authz.Caller{UserID: testOwnerID, Role: authz.RoleUser}
}

// otherCaller is a second account, for the one thing a single-account fixture
// cannot show: that a row belonging to somebody else is not there.
func otherCaller() authz.Caller {
	return authz.Caller{UserID: testOwnerID + 1, Role: authz.RoleUser}
}

func adminCaller() authz.Caller {
	return authz.Caller{UserID: testOwnerID + 2, Role: authz.RoleAdmin}
}

type fakeRepository struct {
	sessions map[string]Session
	messages map[string][]Message

	// err, when set, is what every method returns.
	err error

	renames  []renameCall
	binCalls []binCall
	deleted  []string
	appended []Message
	read     []string
}

type renameCall struct {
	id    string
	title string
	now   time.Time
}

type binCall struct {
	id      string
	deleted bool
	now     time.Time
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{
		sessions: make(map[string]Session),
		messages: make(map[string][]Message),
	}
}

func (f *fakeRepository) seed(record Session) {
	if record.OwnerID == 0 {
		record.OwnerID = testOwnerID
	}
	f.sessions[record.ID] = record
}

// visible mirrors what OwnerScope does in storage, so that a service test with
// two accounts behaves the way the database would instead of the way a map of
// everything would. It is a means and not evidence: what the real predicate does
// to a real table is the contract test's business, and this is a copy of it that
// can drift.
func (f *fakeRepository) visible(record Session, caller authz.Caller) bool {
	if caller.UserID > 0 && record.OwnerID == caller.UserID {
		return true
	}
	// A row that belongs to nobody is the administrator's, and nobody else's.
	return caller.IsAdmin() && record.OwnerID == 0
}

// owned returns the stored session when the caller may see it. A session that
// exists and is somebody else's is not a different answer from one that does not
// exist: both are ErrNotFound, which is the whole of what AUTH-06 asks for.
func (f *fakeRepository) owned(sessionID string, caller authz.Caller) (Session, error) {
	record, ok := f.sessions[sessionID]
	if !ok || !f.visible(record, caller) {
		return Session{}, fmt.Errorf("%w: %s", ErrNotFound, sessionID)
	}
	return record, nil
}

func (f *fakeRepository) Create(_ context.Context, record Session) error {
	if f.err != nil {
		return f.err
	}
	f.sessions[record.ID] = record
	return nil
}

func (f *fakeRepository) Session(_ context.Context, sessionID string, caller authz.Caller) (Session, error) {
	if f.err != nil {
		return Session{}, f.err
	}
	return f.owned(sessionID, caller)
}

func (f *fakeRepository) List(_ context.Context, options ListOptions) ([]Session, error) {
	if f.err != nil {
		return nil, f.err
	}
	var listed []Session
	for _, record := range f.sessions {
		if record.Deleted() == options.Deleted && f.visible(record, options.Caller) {
			listed = append(listed, record)
		}
	}
	return listed, nil
}

func (f *fakeRepository) Rename(_ context.Context, sessionID, title string, now time.Time, caller authz.Caller) error {
	f.renames = append(f.renames, renameCall{id: sessionID, title: title, now: now})
	if f.err != nil {
		return f.err
	}
	record, err := f.owned(sessionID, caller)
	if err != nil {
		return err
	}
	record.Title = title
	record.UpdatedAt = now
	f.sessions[sessionID] = record
	return nil
}

func (f *fakeRepository) SetDeleted(_ context.Context, sessionID string, deleted bool, now time.Time, caller authz.Caller) error {
	f.binCalls = append(f.binCalls, binCall{id: sessionID, deleted: deleted, now: now})
	if f.err != nil {
		return f.err
	}
	record, err := f.owned(sessionID, caller)
	if err != nil {
		return err
	}
	record.DeletedAt = time.Time{}
	if deleted {
		record.DeletedAt = now
	}
	record.UpdatedAt = now
	f.sessions[sessionID] = record
	return nil
}

func (f *fakeRepository) Delete(_ context.Context, sessionID string, caller authz.Caller) error {
	f.deleted = append(f.deleted, sessionID)
	if f.err != nil {
		return f.err
	}
	if _, err := f.owned(sessionID, caller); err != nil {
		return err
	}
	delete(f.sessions, sessionID)
	return nil
}

// Claim follows the real adapter's shape rather than a shorter one: the write is
// conditional on the row having no owner, and the answer to a write that changed
// nothing is read back. A fake that simply wrote the caller's id would let a
// service test pass while the real statement refused the same call.
func (f *fakeRepository) Claim(_ context.Context, sessionID string, caller authz.Caller) error {
	if f.err != nil {
		return f.err
	}
	record, ok := f.sessions[sessionID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, sessionID)
	}
	if record.OwnerID == 0 {
		record.OwnerID = caller.UserID
		f.sessions[sessionID] = record
		return nil
	}
	if _, err := f.owned(sessionID, caller); err != nil {
		return err
	}
	return nil
}

func (f *fakeRepository) AppendMessage(_ context.Context, record Message, caller authz.Caller) error {
	f.appended = append(f.appended, record)
	if f.err != nil {
		return f.err
	}
	// Checked the same way the real adapter checks it, by looking for the
	// session, because what an update of it reports cannot answer this.
	record2, err := f.owned(record.SessionID, caller)
	if err != nil {
		return err
	}
	record2.UpdatedAt = record.CreatedAt
	f.sessions[record.SessionID] = record2
	f.messages[record.SessionID] = append(f.messages[record.SessionID], record)
	return nil
}

func (f *fakeRepository) Messages(_ context.Context, sessionID string, caller authz.Caller) ([]Message, error) {
	f.read = append(f.read, sessionID)
	if f.err != nil {
		return nil, f.err
	}
	if _, err := f.owned(sessionID, caller); err != nil {
		return nil, err
	}
	return f.messages[sessionID], nil
}

func newTestService(t *testing.T) (*Service, *fakeRepository) {
	t.Helper()
	repository := newFakeRepository()
	service, err := NewService(repository)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service, repository
}
