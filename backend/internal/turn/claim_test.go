package turn

import (
	"context"
	"errors"
	"testing"

	"backend/internal/event"
)

// The path claim, from the runtime's side.
//
// §7.2 第 13 条 asks for this by name: a turn that is on a mastery path says so
// in its **first** event, in `metadata.lease`, and that event is the only place
// it can say it — the client's acknowledgement of the command that started the
// turn travels on a short-lived table and is gone after a reconnect, while an
// event is replayable and always in the same place.
//
// What is pinned here is the runtime's whole knowledge of the path, which is
// three things and no more: it asks whoever was assembled into `Options.Paths`,
// it publishes what comes back without reading it, and a build with no `Paths`
// at all runs every turn the way every turn ran before this version.

// stubPaths answers with whatever a test set it to, and writes down what it was
// asked. The record is the half that matters: "was this turn on a path" is
// decided by the capability name, and the runtime is not supposed to know which
// names those are.
type stubPaths struct {
	lease    map[string]any
	work     context.Context
	failure  error
	requests []ClaimRequest
}

var _ Paths = (*stubPaths)(nil)

func (s *stubPaths) Claim(_ context.Context, request ClaimRequest) (Claimed, error) {
	s.requests = append(s.requests, request)
	if s.failure != nil {
		return Claimed{}, s.failure
	}
	return Claimed{Context: s.work, Lease: s.lease}, nil
}

// aLease is a claim as the domain reports it, spelled out rather than imported:
// the runtime carries these four fields and has no opinion about any of them.
func aLease() map[string]any {
	return map[string]any{
		"path_id":     "mp_1",
		"turn_id":     "t_1",
		"expires_at":  "2026-10-01T10:00:00Z",
		"took_over":   false,
		"holder_name": "另一个窗口",
	}
}

// claimedFixture is a runtime with a path port wired in and two capabilities
// registered, one of which is the mastery path's.
func claimedFixture(t *testing.T, paths Paths) *runnerFixture {
	t.Helper()
	fixture := newRunnerFixture(t, func(options *Options) { options.Paths = paths })
	fixture.register(t,
		answering("chat", "我在"),
		answering("mastery_path", "这条路走到这里了"),
	)
	return fixture
}

func TestATurnOnAPathSaysWhichOneInItsFirstEvent(t *testing.T) {
	paths := &stubPaths{lease: aLease(), work: context.Background()}
	fixture := claimedFixture(t, paths)

	started := fixture.start(t, StartRequest{Capability: "mastery_path", Content: "接着上次的闭包"})
	fixture.repository.awaitStatus(t, started.Turn.ID, StatusCompleted)

	// The claim is asked before anything is said, and it is asked with the turn's
	// own four facts — the capability name travels as the string it already is,
	// because which capabilities work on a path is the capability's knowledge and
	// not the runtime's.
	if len(paths.requests) != 1 {
		t.Fatalf("the path was claimed %d times, want once", len(paths.requests))
	}
	asked := paths.requests[0]
	if asked.Capability != "mastery_path" || asked.SessionID != started.Turn.SessionID ||
		asked.TurnID != started.Turn.ID || asked.Goal != "接着上次的闭包" {
		t.Errorf("the claim was asked %+v, want this turn's own four facts", asked)
	}

	// And the claim rides on the session event, which is the first event of the
	// stream — read by subscribing from the beginning, which is what a client that
	// reconnected would do.
	records := fixture.watch(t, started.Turn.ID)
	if len(records) == 0 || records[0].Type != event.TypeSession {
		t.Fatalf("the stream opens with %v, want the session event", kindsOf(records))
	}
	opening := only(t, records, event.TypeSession)
	lease, ok := opening.Metadata[event.MetaLease].(map[string]any)
	if !ok {
		t.Fatalf("the opening event carries no lease: %+v", opening.Metadata)
	}
	for key, want := range aLease() {
		if lease[key] != want {
			t.Errorf("lease[%q] = %v, want %v", key, lease[key], want)
		}
	}
}

func TestATurnThatIsOnNoPathCarriesNoLease(t *testing.T) {
	// A turn the port has nothing to say about — a plain conversation, or a claim
	// that found no path — is announced without a lease rather than with an empty
	// one. The two are different answers to a client: absent means "this turn is
	// not writing a path", and an empty object would mean "a path, and nobody
	// knows who holds it".
	paths := &stubPaths{}
	fixture := claimedFixture(t, paths)

	started := fixture.start(t, StartRequest{Capability: "chat", Content: "随便聊聊"})
	fixture.repository.awaitStatus(t, started.Turn.ID, StatusCompleted)

	opening := only(t, fixture.watch(t, started.Turn.ID), event.TypeSession)
	if _, present := opening.Metadata[event.MetaLease]; present {
		t.Errorf("a turn on no path carries a lease: %+v", opening.Metadata)
	}

	// And a build with no path port at all is the same answer by a different road:
	// the port is optional, and a nil one is a deployment without the mastery path
	// in it.
	plain := newRunnerFixture(t)
	plain.register(t, answering("chat", "我在"))
	started = plain.start(t, StartRequest{Capability: "chat", Content: "随便聊聊"})
	plain.repository.awaitStatus(t, started.Turn.ID, StatusCompleted)
	opening = only(t, plain.watch(t, started.Turn.ID), event.TypeSession)
	if _, present := opening.Metadata[event.MetaLease]; present {
		t.Errorf("a build with no path port carries a lease: %+v", opening.Metadata)
	}
}

func TestATurnWhosePathCannotBeClaimedNeverStarts(t *testing.T) {
	// The claim happens before the first event, so a claim that fails is a turn
	// that never announces itself at all: no session event, and the failure
	// reported the way any failure before the first event is. That is the whole
	// reason the claim is where it is — a turn that had already said "here I am"
	// would have said it about a path it could not open.
	paths := &stubPaths{failure: errors.New("the storage is not answering")}
	fixture := claimedFixture(t, paths)

	started := fixture.start(t, StartRequest{Capability: "mastery_path", Content: "接着上次的闭包"})
	fixture.repository.awaitStatus(t, started.Turn.ID, StatusFailed)

	for _, record := range fixture.watch(t, started.Turn.ID) {
		if record.Type == event.TypeSession {
			t.Errorf("a turn that could not claim its path announced itself: %+v", record)
		}
	}
}
