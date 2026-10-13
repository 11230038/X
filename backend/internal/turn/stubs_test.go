package turn

import (
	"context"
	"sync"
	"testing"
	"time"

	"backend/internal/event"
)

// watcher is the "something happened, look again" mechanism the stubs share.
// A test waits on what it can observe rather than sleeping and hoping.
type watcher struct {
	mu     sync.Mutex
	notify chan struct{}
}

func newWatcher() *watcher {
	return &watcher{notify: make(chan struct{}, 1)}
}

func (w *watcher) touch() {
	select {
	case w.notify <- struct{}{}:
	default:
	}
}

// await polls under the lock until the condition holds. The condition reads the
// guarded fields directly; the lock is already held.
func (w *watcher) await(t *testing.T, condition func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		w.mu.Lock()
		held := condition()
		w.mu.Unlock()
		if held {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		select {
		case <-w.notify:
		case <-time.After(time.Millisecond):
		}
	}
}

// stubRepository is storage in a slice, so a test can read what was written
// without a database.
type stubRepository struct {
	*watcher
	lastSeq   int64
	lastErr   error
	appendErr error
	attempts  int
	appended  []event.Event
	// trace, when set, records each call so that a test can assert on the order
	// of two different collaborators.
	trace *[]string
	// eventsHold, when set, is waited on before a history read answers. It is
	// how a test holds a replay open and decides what happens beside it.
	eventsHold chan struct{}
	// lastEventHold, when set, is waited on before the end-of-stream question is
	// answered. It is how a test puts an event in the window between a history
	// read and that question, which is where the runtime stores a turn's last
	// event before it publishes it.
	lastEventHold chan struct{}
}

func newStubRepository() *stubRepository {
	return &stubRepository{watcher: newWatcher()}
}

func (s *stubRepository) Start(context.Context, Turn) error { return nil }

func (s *stubRepository) Turn(context.Context, string) (Turn, error) {
	return Turn{}, ErrNotFound
}

func (s *stubRepository) ActiveTurn(context.Context, string) (Turn, error) {
	return Turn{}, ErrNotFound
}

func (s *stubRepository) Save(context.Context, Turn, Status) error { return nil }

func (s *stubRepository) AppendEvent(_ context.Context, record event.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts++
	if s.trace != nil {
		*s.trace = append(*s.trace, "store:"+string(record.Type))
	}
	s.touch()
	if s.appendErr != nil {
		return s.appendErr
	}
	s.appended = append(s.appended, record)
	return nil
}

func (s *stubRepository) Events(_ context.Context, turnID string, afterSeq int64) ([]event.Event, error) {
	if s.eventsHold != nil {
		<-s.eventsHold
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var history []event.Event
	for _, record := range s.appended {
		if record.TurnID == turnID && record.Seq > afterSeq {
			history = append(history, record)
		}
	}
	return history, nil
}

func (s *stubRepository) LastEvent(_ context.Context, turnID string) (event.Event, error) {
	if s.lastEventHold != nil {
		<-s.lastEventHold
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.appended) - 1; i >= 0; i-- {
		if s.appended[i].TurnID == turnID {
			return s.appended[i], nil
		}
	}
	return event.Event{}, ErrNotFound
}

func (s *stubRepository) LastSeq(context.Context, string) (int64, error) {
	if s.lastErr != nil {
		return 0, s.lastErr
	}
	return s.lastSeq, nil
}

// stored returns what the emitter wrote, oldest first.
func (s *stubRepository) stored() []event.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]event.Event(nil), s.appended...)
}

// seed puts events into the store as if a turn had already produced them.
func (s *stubRepository) seed(records ...event.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appended = append(s.appended, records...)
	s.touch()
}

// awaitAttempts waits until storage has been asked to write this many times.
func (s *stubRepository) awaitAttempts(t *testing.T, count int) {
	t.Helper()
	s.await(t, func() bool { return s.attempts >= count }, "storage to be written")
}

// recordingBroadcaster keeps what subscribers were told.
type recordingBroadcaster struct {
	*watcher
	published []event.Event
	trace     *[]string
}

func newRecordingBroadcaster() *recordingBroadcaster {
	return &recordingBroadcaster{watcher: newWatcher()}
}

func (b *recordingBroadcaster) Publish(_ string, record event.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.trace != nil {
		*b.trace = append(*b.trace, "publish:"+string(record.Type))
	}
	b.published = append(b.published, record)
	b.touch()
}

func (b *recordingBroadcaster) told() []event.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]event.Event(nil), b.published...)
}

func (b *recordingBroadcaster) awaitPublished(t *testing.T, count int) {
	t.Helper()
	b.await(t, func() bool { return len(b.published) >= count }, "subscribers to be told")
}
