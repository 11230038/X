package session

import (
	"context"
	"fmt"
	"testing"
	"time"
)

type fakeRepository struct {
	sessions map[string]Session
	messages map[string][]Message

	// err, when set, is what every method returns.
	err error

	lists    []ListOptions
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
	f.sessions[record.ID] = record
}

func (f *fakeRepository) Create(_ context.Context, record Session) error {
	if f.err != nil {
		return f.err
	}
	f.sessions[record.ID] = record
	return nil
}

func (f *fakeRepository) Session(_ context.Context, sessionID string) (Session, error) {
	if f.err != nil {
		return Session{}, f.err
	}
	record, ok := f.sessions[sessionID]
	if !ok {
		return Session{}, fmt.Errorf("%w: %s", ErrNotFound, sessionID)
	}
	return record, nil
}

func (f *fakeRepository) List(_ context.Context, options ListOptions) ([]Session, error) {
	f.lists = append(f.lists, options)
	if f.err != nil {
		return nil, f.err
	}
	var listed []Session
	for _, record := range f.sessions {
		if record.Deleted() == options.Deleted {
			listed = append(listed, record)
		}
	}
	return listed, nil
}

func (f *fakeRepository) Rename(_ context.Context, sessionID, title string, now time.Time) error {
	f.renames = append(f.renames, renameCall{id: sessionID, title: title, now: now})
	if f.err != nil {
		return f.err
	}
	record, ok := f.sessions[sessionID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, sessionID)
	}
	record.Title = title
	record.UpdatedAt = now
	f.sessions[sessionID] = record
	return nil
}

func (f *fakeRepository) SetDeleted(_ context.Context, sessionID string, deleted bool, now time.Time) error {
	f.binCalls = append(f.binCalls, binCall{id: sessionID, deleted: deleted, now: now})
	if f.err != nil {
		return f.err
	}
	record, ok := f.sessions[sessionID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, sessionID)
	}
	record.DeletedAt = time.Time{}
	if deleted {
		record.DeletedAt = now
	}
	record.UpdatedAt = now
	f.sessions[sessionID] = record
	return nil
}

func (f *fakeRepository) Delete(_ context.Context, sessionID string) error {
	f.deleted = append(f.deleted, sessionID)
	if f.err != nil {
		return f.err
	}
	if _, ok := f.sessions[sessionID]; !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, sessionID)
	}
	delete(f.sessions, sessionID)
	return nil
}

func (f *fakeRepository) AppendMessage(_ context.Context, record Message) error {
	f.appended = append(f.appended, record)
	if f.err != nil {
		return f.err
	}
	// The real adapter reaches this the same way: its transaction marks the
	// session active, and a session that is not there updates no rows.
	record2, ok := f.sessions[record.SessionID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, record.SessionID)
	}
	record2.UpdatedAt = record.CreatedAt
	f.sessions[record.SessionID] = record2
	f.messages[record.SessionID] = append(f.messages[record.SessionID], record)
	return nil
}

func (f *fakeRepository) Messages(_ context.Context, sessionID string) ([]Message, error) {
	f.read = append(f.read, sessionID)
	if f.err != nil {
		return nil, f.err
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
