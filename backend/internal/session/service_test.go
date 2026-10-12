package session

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeRepository is storage in a map. The service's rules are about which
// operations a session's state allows, and those are testable without a
// database; the adapter that talks to seekdb is exercised separately.

func TestNewServiceNeedsARepository(t *testing.T) {
	if _, err := NewService(nil); err == nil {
		t.Fatal("NewService(nil) error = nil, want a rejection")
	}
}

func TestCreateStartsAnUntitledSession(t *testing.T) {
	service, repository := newTestService(t)

	created, err := service.Create(context.Background())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if !strings.HasPrefix(created.ID, sessionIDPrefix) || len(created.ID) <= len(sessionIDPrefix) {
		t.Errorf("ID = %q, want a generated id with the session prefix", created.ID)
	}
	// An empty title is what lets the naming step recognise a session it has not
	// named yet; a placeholder would be indistinguishable from a real title.
	if created.Title != "" {
		t.Errorf("Title = %q, want it left to be generated", created.Title)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Errorf("timestamps = %v / %v", created.CreatedAt, created.UpdatedAt)
	}
	if created.Deleted() {
		t.Error("a new session is already in the recycle bin")
	}
	if _, stored := repository.sessions[created.ID]; !stored {
		t.Error("the session was returned without being stored")
	}
}

func TestCreateGivesEachSessionItsOwnID(t *testing.T) {
	service, _ := newTestService(t)

	first, err := service.Create(context.Background())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	second, err := service.Create(context.Background())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if first.ID == second.ID {
		t.Errorf("two sessions share the id %q", first.ID)
	}
}

func TestGetHidesTheRecycleBin(t *testing.T) {
	service, repository := newTestService(t)
	ctx := context.Background()
	repository.seed(Session{ID: "sess_live"})
	repository.seed(Session{ID: "sess_binned", DeletedAt: time.Now().UTC()})

	if _, err := service.Get(ctx, "sess_live"); err != nil {
		t.Fatalf("Get(live) error = %v", err)
	}
	// A session in the bin may not be started in or read, so as far as this
	// operation is concerned it is not there.
	if _, err := service.Get(ctx, "sess_binned"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(binned) error = %v, want ErrNotFound", err)
	}
	if _, err := service.Get(ctx, "sess_missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing) error = %v, want ErrNotFound", err)
	}
}

func TestListAsksForTheBinOrTheLiveSessions(t *testing.T) {
	service, repository := newTestService(t)

	if _, err := service.List(context.Background(), ListOptions{Limit: 10, Offset: 20}); err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if _, err := service.List(context.Background(), ListOptions{Deleted: true}); err != nil {
		t.Fatalf("List() error = %v", err)
	}

	if len(repository.lists) != 2 {
		t.Fatalf("storage was asked %d times", len(repository.lists))
	}
	if repository.lists[0].Deleted || repository.lists[0].Limit != 10 || repository.lists[0].Offset != 20 {
		t.Errorf("live listing = %+v", repository.lists[0])
	}
	if !repository.lists[1].Deleted {
		t.Errorf("bin listing = %+v", repository.lists[1])
	}
}

func TestRenameTitlesALiveSession(t *testing.T) {
	service, repository := newTestService(t)
	repository.seed(Session{ID: "sess_1"})

	renamed, err := service.Rename(context.Background(), "sess_1", "  闭包与作用域  ")
	if err != nil {
		t.Fatalf("Rename() error = %v", err)
	}

	if renamed.Title != "闭包与作用域" {
		t.Errorf("Title = %q, want the surrounding space trimmed", renamed.Title)
	}
	if len(repository.renames) != 1 || repository.renames[0].title != "闭包与作用域" {
		t.Fatalf("storage saw %+v", repository.renames)
	}
	if repository.renames[0].now.IsZero() {
		t.Error("the change was not stamped")
	}
	// The returned session has to agree with what was stored, or a client that
	// renders the response shows something different from the list.
	if stored := repository.sessions["sess_1"]; stored.Title != renamed.Title || !stored.UpdatedAt.Equal(renamed.UpdatedAt) {
		t.Errorf("stored = %+v, returned = %+v", stored, renamed)
	}
}

func TestRenameRejectsTitlesItCouldNotReadBack(t *testing.T) {
	tests := []struct {
		name  string
		title string
	}{
		{"blank", "   "},
		{"empty", ""},
		{"too long", strings.Repeat("字", TitleMaxRunes+1)},
		{"a newline", "two\nrows"},
		{"a control character", "bell\x07"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, repository := newTestService(t)
			repository.seed(Session{ID: "sess_1"})

			if _, err := service.Rename(context.Background(), "sess_1", test.title); !errors.Is(err, ErrInvalidTitle) {
				t.Errorf("Rename() error = %v, want ErrInvalidTitle", err)
			}
			if len(repository.renames) != 0 {
				t.Errorf("a rejected title was written: %+v", repository.renames)
			}
		})
	}
}

func TestRenameAcceptsATitleAtTheLimit(t *testing.T) {
	service, repository := newTestService(t)
	repository.seed(Session{ID: "sess_1"})

	title := strings.Repeat("字", TitleMaxRunes)
	if _, err := service.Rename(context.Background(), "sess_1", title); err != nil {
		t.Errorf("Rename() error = %v", err)
	}
}

func TestSoftDeleteMovesALiveSessionToTheBin(t *testing.T) {
	service, repository := newTestService(t)
	repository.seed(Session{ID: "sess_1"})

	if err := service.SoftDelete(context.Background(), "sess_1"); err != nil {
		t.Fatalf("SoftDelete() error = %v", err)
	}

	if len(repository.binCalls) != 1 || !repository.binCalls[0].deleted {
		t.Fatalf("storage saw %+v", repository.binCalls)
	}
	if !repository.sessions["sess_1"].Deleted() {
		t.Error("the session is still live")
	}
	// A binned session is gone from the live listing straight away.
	if _, err := service.Get(context.Background(), "sess_1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get() after deletion error = %v", err)
	}
}

func TestSoftDeleteRefusesASessionAlreadyInTheBin(t *testing.T) {
	service, repository := newTestService(t)
	repository.seed(Session{ID: "sess_1", DeletedAt: time.Now().UTC()})

	if err := service.SoftDelete(context.Background(), "sess_1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("SoftDelete() error = %v, want ErrNotFound", err)
	}
	// Deleting twice would move deleted_at, and the bin is ordered by it.
	if len(repository.binCalls) != 0 {
		t.Errorf("storage was written anyway: %+v", repository.binCalls)
	}
}

func TestRestoreTakesASessionBackOutOfTheBin(t *testing.T) {
	service, repository := newTestService(t)
	repository.seed(Session{ID: "sess_1", DeletedAt: time.Now().UTC()})

	if err := service.Restore(context.Background(), "sess_1"); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	if len(repository.binCalls) != 1 || repository.binCalls[0].deleted {
		t.Fatalf("storage saw %+v", repository.binCalls)
	}
	if repository.sessions["sess_1"].Deleted() {
		t.Error("the session is still in the bin")
	}
	if _, err := service.Get(context.Background(), "sess_1"); err != nil {
		t.Errorf("Get() after restoring error = %v", err)
	}
}

func TestRestoreRefusesASessionThatIsNotInTheBin(t *testing.T) {
	service, repository := newTestService(t)
	repository.seed(Session{ID: "sess_1"})

	if err := service.Restore(context.Background(), "sess_1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Restore() error = %v, want ErrNotFound", err)
	}
	if len(repository.binCalls) != 0 {
		t.Errorf("storage was written anyway: %+v", repository.binCalls)
	}
}

func TestDeletePermanentlyRequiresTheBin(t *testing.T) {
	service, repository := newTestService(t)
	repository.seed(Session{ID: "sess_binned", DeletedAt: time.Now().UTC()})
	repository.seed(Session{ID: "sess_live"})

	if err := service.DeletePermanently(context.Background(), "sess_binned"); err != nil {
		t.Fatalf("DeletePermanently(binned) error = %v", err)
	}
	if len(repository.deleted) != 1 || repository.deleted[0] != "sess_binned" {
		t.Fatalf("storage saw %+v", repository.deleted)
	}

	// A live session is one click from the user's point of view; it has to pass
	// through the bin first (CHAT-06).
	if err := service.DeletePermanently(context.Background(), "sess_live"); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeletePermanently(live) error = %v, want ErrNotFound", err)
	}
	if len(repository.deleted) != 1 {
		t.Errorf("a live session was deleted: %+v", repository.deleted)
	}
}

func TestAppendMessageRecordsWhoSaidWhat(t *testing.T) {
	service, repository := newTestService(t)
	repository.seed(Session{ID: "sess_1"})

	appended, err := service.AppendMessage(context.Background(), NewMessage{
		SessionID: "sess_1", Role: RoleUser, Content: "什么是闭包？",
	})
	if err != nil {
		t.Fatalf("AppendMessage() error = %v", err)
	}

	if !strings.HasPrefix(appended.ID, messageIDPrefix) {
		t.Errorf("ID = %q, want a generated message id", appended.ID)
	}
	if appended.CreatedAt.IsZero() {
		t.Error("the message was not stamped")
	}
	if len(repository.appended) != 1 || repository.appended[0].Content != "什么是闭包？" {
		t.Fatalf("storage saw %+v", repository.appended)
	}
	if repository.appended[0].Role != RoleUser {
		t.Errorf("Role = %q", repository.appended[0].Role)
	}
}

func TestAppendMessageRejectsARoleThisVersionDoesNotWrite(t *testing.T) {
	// Tool traffic is a turn's context, not something said in the conversation,
	// so a stored message is never one.
	for _, role := range []Role{"", "tool", "system", "User"} {
		service, repository := newTestService(t)
		repository.seed(Session{ID: "sess_1"})

		_, err := service.AppendMessage(context.Background(), NewMessage{SessionID: "sess_1", Role: role})
		if !errors.Is(err, ErrInvalidMessage) {
			t.Errorf("AppendMessage(role=%q) error = %v, want ErrInvalidMessage", role, err)
		}
		if len(repository.appended) != 0 {
			t.Errorf("role %q was stored anyway", role)
		}
	}
}

func TestAppendMessageReportsAnUnknownSession(t *testing.T) {
	// The message table's foreign key would reject this too, but as a storage
	// error nobody can act on. The caller should hear that the session is gone.
	service, _ := newTestService(t)

	_, err := service.AppendMessage(context.Background(), NewMessage{SessionID: "sess_missing", Role: RoleUser})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("AppendMessage() error = %v, want ErrNotFound", err)
	}
}

func TestMessagesReturnsHistoryForALiveSessionOnly(t *testing.T) {
	service, repository := newTestService(t)
	ctx := context.Background()
	repository.seed(Session{ID: "sess_live"})
	repository.seed(Session{ID: "sess_binned", DeletedAt: time.Now().UTC()})
	repository.messages["sess_live"] = []Message{{ID: "msg_1", SessionID: "sess_live", Role: RoleUser, Content: "hi"}}

	messages, err := service.Messages(ctx, "sess_live")
	if err != nil {
		t.Fatalf("Messages() error = %v", err)
	}
	if len(messages) != 1 || messages[0].Content != "hi" {
		t.Errorf("Messages() = %+v", messages)
	}

	// A binned session's history reads as an empty conversation rather than
	// missing, which is indistinguishable from a session nobody wrote in.
	if _, err := service.Messages(ctx, "sess_binned"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Messages(binned) error = %v, want ErrNotFound", err)
	}
	if len(repository.read) != 1 {
		t.Errorf("history was read for a session that is not live: %+v", repository.read)
	}
}

func TestStorageFailuresReachTheCaller(t *testing.T) {
	storageErr := errors.New("the database is unreachable")
	repository := newFakeRepository()
	repository.err = storageErr
	service, err := NewService(repository)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	calls := map[string]func() error{
		"Create":   func() error { _, err := service.Create(context.Background()); return err },
		"List":     func() error { _, err := service.List(context.Background(), ListOptions{}); return err },
		"Get":      func() error { _, err := service.Get(context.Background(), "sess_1"); return err },
		"Messages": func() error { _, err := service.Messages(context.Background(), "sess_1"); return err },
		"AppendMessage": func() error {
			_, err := service.AppendMessage(context.Background(), NewMessage{SessionID: "sess_1", Role: RoleUser})
			return err
		},
		"SoftDelete":        func() error { return service.SoftDelete(context.Background(), "sess_1") },
		"Restore":           func() error { return service.Restore(context.Background(), "sess_1") },
		"DeletePermanently": func() error { return service.DeletePermanently(context.Background(), "sess_1") },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, storageErr) {
			t.Errorf("%s error = %v, want the storage failure", name, err)
		}
	}
}
