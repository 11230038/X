package turn

import (
	"backend/internal/authz"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"backend/internal/event"
	"backend/internal/llm"
	"backend/internal/session"
)

// stop ends any turn still running, so that a test failing early does not leave
// a goroutine writing into storage the test has stopped reading.

// start begins a turn and returns the ids it was stored under.

// watch subscribes to a turn from its beginning and reads it to its end.

// kindsOf lists the event types of a stream, which is what most assertions are
// about.

// only returns the single event of a kind, failing if there is not exactly one.

func TestNewRunnerNeedsItsParts(t *testing.T) {
	complete := func() (*runnerFixture, Options) {
		fixture := newRunnerFixture(t)
		fixture.stop(t)
		return nil, Options{
			Repository:        fixture.repository,
			Hub:               fixture.hub,
			Sessions:          fixture.sessions,
			Capabilities:      fixture.capabilities,
			Tools:             fixture.tools,
			Provider:          fixture.provider,
			Titler:            fixture.titler(),
			Logger:            slog.New(slog.NewTextHandler(&fixture.logs, nil)),
			DefaultCapability: "chat",
			InputTimeout:      time.Minute,
		}
	}

	cases := map[string]func(*Options){
		"no repository":     func(o *Options) { o.Repository = nil },
		"no hub":            func(o *Options) { o.Hub = nil },
		"no sessions":       func(o *Options) { o.Sessions = nil },
		"no capabilities":   func(o *Options) { o.Capabilities = nil },
		"no tools":          func(o *Options) { o.Tools = nil },
		"no provider":       func(o *Options) { o.Provider = nil },
		"no titler":         func(o *Options) { o.Titler = nil },
		"no logger":         func(o *Options) { o.Logger = nil },
		"no default":        func(o *Options) { o.DefaultCapability = " " },
		"no input deadline": func(o *Options) { o.InputTimeout = 0 },
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			_, options := complete()
			breakIt(&options)
			// A runtime missing any of these would fail mid-turn, where the
			// failure is half an answer, rather than here, where it is a
			// message at startup.
			if _, err := NewRunner(options); err == nil {
				t.Error("NewRunner() error = nil, want a rejection")
			}
		})
	}
}

func TestATurnIsPlayedOutInOrderAndEnds(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.register(t, answering("chat", "我在"))

	started := fixture.start(t, StartRequest{Content: "在吗"})
	seen := fixture.watch(t, started.Turn.ID)

	// The runtime's own events bracket the capability's, and the stream ends on
	// the event that says the turn is over (WS-07).
	want := []event.Type{
		event.TypeSession, event.TypeStageStart, event.TypeContent,
		event.TypeResult, event.TypeDone, event.TypeSessionMeta,
	}
	got := kindsOf(seen)
	if len(got) != len(want) {
		t.Fatalf("stream = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("stream = %v, want %v", got, want)
		}
	}
	for i, record := range seen {
		if record.Seq != int64(i+1) {
			t.Errorf("event %d has Seq %d, want %d", i, record.Seq, i+1)
		}
	}

	stored := fixture.repository.awaitStatus(t, started.Turn.ID, StatusCompleted)
	if stored.StateVersion != 3 {
		// The insert is version 1, then queued → running → completed.
		t.Errorf("StateVersion = %d, want 3", stored.StateVersion)
	}
	if stored.FinishedAt.IsZero() {
		t.Error("FinishedAt is zero on a finished turn")
	}
	if stored.AssistantMessageID == "" {
		t.Error("the completed turn points at no assistant message")
	}
}

func TestTheSessionIsNamedBeforeTheFirstEvent(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.register(t, answering("chat", "我在"))

	started := fixture.start(t, StartRequest{Content: "在吗"})
	first := fixture.watch(t, started.Turn.ID)[0]

	// A client that has just reconnected needs to know which conversation it
	// is watching before it can make sense of anything else on the stream.
	if first.Type != event.TypeSession {
		t.Fatalf("the first event is %q, want the session", first.Type)
	}
	if first.Content != started.Session.ID {
		t.Errorf("Content = %q, want the session id %q", first.Content, started.Session.ID)
	}
	// A client that sent no session id gets a conversation, never a writing
	// session: the writing page always creates its own before the first turn.
	if started.Session.Kind != session.KindChat {
		t.Errorf("implicit session kind = %q, want chat", started.Session.Kind)
	}
	if len(fixture.sessions.createdKinds) != 1 || fixture.sessions.createdKinds[0] != session.KindChat {
		t.Errorf("created kinds = %v, want [chat]", fixture.sessions.createdKinds)
	}
}

func TestTheAnswerBecomesPartOfTheConversation(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.register(t, answering("chat", "我在"))

	started := fixture.start(t, StartRequest{Content: "在吗"})
	fixture.repository.awaitStatus(t, started.Turn.ID, StatusCompleted)

	messages, err := fixture.sessions.Messages(context.Background(), started.Session.ID, authz.Caller{})
	if err != nil {
		t.Fatalf("Messages() error = %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("the session holds %d messages, want the question and the answer", len(messages))
	}
	if messages[0].Role != session.RoleUser || messages[0].Content != "在吗" {
		t.Errorf("message 0 = %+v, want the user's question", messages[0])
	}
	if messages[1].Role != session.RoleAssistant || messages[1].Content != "我在" {
		t.Errorf("message 1 = %+v, want the assistant's answer", messages[1])
	}
	if messages[1].Capability != "chat" {
		t.Errorf("the answer was recorded for %q, want the capability that produced it", messages[1].Capability)
	}
}

func TestASessionIsNamedFromItsFirstTurn(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.naming.responses = []llm.Response{{Content: "在不在"}}
	fixture.register(t, answering("chat", "我在"))

	started := fixture.start(t, StartRequest{Content: "在吗"})
	seen := fixture.watch(t, started.Turn.ID)

	if got := only(t, seen, event.TypeSessionMeta).Content; got != "在不在" {
		t.Errorf("session_meta = %q, want the generated name", got)
	}
	renamed, err := fixture.sessions.Get(context.Background(), started.Session.ID, authz.Caller{})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if renamed.Title != "在不在" {
		t.Errorf("the session is titled %q, want the generated name stored", renamed.Title)
	}
}

func TestASessionIsNotRenamedByItsSecondTurn(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.naming.responses = []llm.Response{{Content: "在不在"}}
	fixture.register(t, answering("chat", "我在"))

	started := fixture.start(t, StartRequest{Content: "在吗"})
	fixture.watch(t, started.Turn.ID)

	fixture.naming.responses = []llm.Response{{Content: "另一个名字"}}
	second := fixture.start(t, StartRequest{SessionID: started.Session.ID, Content: "再说一次"})
	seen := fixture.watch(t, second.Turn.ID)

	// A name that changed under the second turn would move the conversation in
	// the list while the user was reading it.
	if got := only(t, seen, event.TypeSessionMeta).Content; got != "在不在" {
		t.Errorf("session_meta = %q, want the name the session already had", got)
	}
	if asked := len(fixture.naming.asked()); asked != 1 {
		t.Errorf("the namer was asked %d times, want only the session's first turn", asked)
	}
}

func TestASessionMetaEndsTheStreamEvenWhenNamingFails(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.naming.err = errors.New("the naming model is unreachable")
	fixture.register(t, answering("chat", "我在"))

	started := fixture.start(t, StartRequest{Content: "在吗"})
	seen := fixture.watch(t, started.Turn.ID)

	// The event that ends the stream is not optional: a subscriber left waiting
	// on a turn that has ended is worse than one given a stale title.
	if got := only(t, seen, event.TypeSessionMeta).Content; got != "" {
		t.Errorf("session_meta = %q, want the empty name the session still has", got)
	}
	if !strings.Contains(fixture.logs.String(), "could not name a session") {
		t.Error("a failed naming was not reported")
	}
}

func TestAnEmptyMessageIsRefused(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.register(t, answering("chat", "我在"))

	_, err := fixture.runner.Start(context.Background(), StartRequest{Content: "   "})
	if !errors.Is(err, ErrEmptyMessage) {
		t.Errorf("Start() error = %v, want ErrEmptyMessage", err)
	}
	if fixture.sessions.created != 0 {
		t.Error("a refused turn still created a session")
	}
}

func TestASessionRunsOneTurnAtATime(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.provider.block = make(chan struct{})
	fixture.register(t, looping("chat", agentRequest()))

	started := fixture.start(t, StartRequest{Content: "第一句"})
	_, err := fixture.runner.Start(context.Background(), StartRequest{
		SessionID: started.Session.ID, Content: "第二句",
	})

	// Two turns answering into one conversation would interleave their messages
	// and both hold the session (RT-02).
	if !errors.Is(err, ErrActiveTurnExists) {
		t.Errorf("Start() error = %v, want ErrActiveTurnExists", err)
	}
	close(fixture.provider.block)
	fixture.repository.awaitStatus(t, started.Turn.ID, StatusCompleted)
}

func TestAnUnknownCapabilityEndsTheTurnAsFailed(t *testing.T) {
	fixture := newRunnerFixture(t)

	started := fixture.start(t, StartRequest{Content: "在吗", Capability: "deep_solve"})
	seen := fixture.watch(t, started.Turn.ID)

	// Asking for something this service does not have is an ordinary ending,
	// not a request that hangs: the client gets a terminal error and the turn
	// still records what happened (AC-02).
	failed := only(t, seen, event.TypeError)
	if got := failed.Metadata[event.MetaErrorCode]; got != CodeUnknownCapability {
		t.Errorf("error_code = %v, want %q", got, CodeUnknownCapability)
	}
	if got := failed.Metadata[event.MetaTurnTerminal]; got != true {
		t.Errorf("turn_terminal = %v, want a terminal error", got)
	}
	done := only(t, seen, event.TypeDone)
	if got := done.Metadata[event.MetaStatus]; got != string(StatusFailed) {
		t.Errorf("done status = %v, want %q", got, StatusFailed)
	}

	stored := fixture.repository.awaitStatus(t, started.Turn.ID, StatusFailed)
	if stored.FailureCode != CodeUnknownCapability {
		t.Errorf("FailureCode = %q, want %q", stored.FailureCode, CodeUnknownCapability)
	}
	if stored.Retryable {
		t.Error("Retryable is true for a capability this service does not offer")
	}
	// Nothing may still hold the session, or the conversation is unusable.
	if _, err := fixture.runner.ActiveTurn(context.Background(), started.Session.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("ActiveTurn() error = %v, want the session to be free", err)
	}
}

func TestTheDefaultCapabilityIsUsedWhenNoneIsAskedFor(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.register(t, answering("fallback", "我在"))
	fixture.runner.fallback = "fallback"

	started := fixture.start(t, StartRequest{Content: "在吗"})
	if started.Turn.Capability != "fallback" {
		t.Errorf("Capability = %q, want the runner's default", started.Turn.Capability)
	}
	fixture.repository.awaitStatus(t, started.Turn.ID, StatusCompleted)
}

func TestATurnOutlivesTheRequestThatStartedIt(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.provider.block = make(chan struct{})
	fixture.provider.responses = []llm.Response{{Content: "我在"}}
	fixture.register(t, looping("chat", agentRequest()))

	ctx, cancel := context.WithCancel(context.Background())
	started, err := fixture.runner.Start(ctx, StartRequest{Content: "在吗"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	// The client hung up. The turn is not the client's: it carries on and is
	// still there when the client comes back (AC-04).
	cancel()
	close(fixture.provider.block)

	stored := fixture.repository.awaitStatus(t, started.Turn.ID, StatusCompleted)
	if stored.Error != "" {
		t.Errorf("Error = %q, want a turn that ran to its end", stored.Error)
	}
}

func TestClosingTheRunnerStopsItsTurns(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.provider.block = make(chan struct{})
	fixture.register(t, looping("chat", agentRequest()))

	started := fixture.start(t, StartRequest{Content: "在吗"})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := fixture.runner.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Shutting down is not a failure of the turn, and it must not leave the
	// turn stored as running with nothing to finish it.
	stored := fixture.repository.awaitStatus(t, started.Turn.ID, StatusCancelled)
	if stored.Error != "" {
		t.Errorf("Error = %q, want a shutdown to be recorded without a failure", stored.Error)
	}
}

func TestCancellingATurnNoProcessIsRunningFreesItsSession(t *testing.T) {
	fixture := newRunnerFixture(t)
	// A turn left behind by a process that stopped: stored, active, and with no
	// goroutine here that will ever move it.
	orphan := Turn{
		ID: "turn_orphan", SessionID: "sess_orphan", Status: StatusRunning,
		Capability: "chat", CreatedAt: time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC),
	}
	if err := fixture.repository.Start(context.Background(), orphan); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if err := fixture.runner.Cancel(context.Background(), orphan.ID); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}

	// Left alone it holds the session's turn slot forever, and that
	// conversation could never be used again.
	stored := fixture.repository.awaitStatus(t, orphan.ID, StatusFailed)
	if stored.FailureCode != CodeAbandoned {
		t.Errorf("FailureCode = %q, want %q", stored.FailureCode, CodeAbandoned)
	}
	if !stored.Retryable {
		t.Error("Retryable is false for a turn that nothing ran")
	}
}

func TestCancellingATurnThatHasEndedIsNotAnError(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.register(t, answering("chat", "我在"))

	started := fixture.start(t, StartRequest{Content: "在吗"})
	fixture.repository.awaitStatus(t, started.Turn.ID, StatusCompleted)

	// This is what happens when a client's cancel crosses the turn's own
	// ending. There is nothing to undo, so there is nothing to report.
	if err := fixture.runner.Cancel(context.Background(), started.Turn.ID); err != nil {
		t.Errorf("Cancel() error = %v, want a cancel with nothing to do", err)
	}
	if fixture.repository.mustTurn(t, started.Turn.ID).Status != StatusCompleted {
		t.Error("cancelling a finished turn changed how it ended")
	}
}

func TestCancellingATurnThatDoesNotExistIsReported(t *testing.T) {
	fixture := newRunnerFixture(t)

	if err := fixture.runner.Cancel(context.Background(), "turn_nothing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Cancel() error = %v, want ErrNotFound", err)
	}
}
