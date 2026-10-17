package turn

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"backend/internal/authz"
	"backend/internal/capability"
	"backend/internal/llm"
	"backend/internal/session"
	"backend/internal/tool"
)

// Sessions is the part of the conversation domain the runtime uses.
//
// It is an interface rather than the service itself so that a turn can be run
// against a conversation without a database behind it, and so that what the
// runtime may do to a conversation is a list someone can read.
type Sessions interface {
	Create(ctx context.Context, caller authz.Caller) (session.Session, error)
	Get(ctx context.Context, sessionID string, caller authz.Caller) (session.Session, error)
	Rename(ctx context.Context, sessionID, title string, caller authz.Caller) (session.Session, error)
	AppendMessage(ctx context.Context, input session.NewMessage, caller authz.Caller) (session.Message, error)
	Messages(ctx context.Context, sessionID string, caller authz.Caller) ([]session.Message, error)
}

var _ Sessions = (*session.Service)(nil)

// Options is everything a Runner needs. Nothing is optional: a runtime missing
// one of these would fail in the middle of a turn, where the failure is a
// half-finished answer, rather than at startup, where it is a message.
type Options struct {
	Repository Repository
	Hub        *Hub
	Sessions   Sessions
	Materials  Materials
	// Questions is where a turn's save_question calls are stored. It is required
	// whatever the deployment runs: a runner without a bank would be a build
	// whose deep_question capability cannot store anything, and the drift check
	// that would catch that is about names, not about what they can do.
	Questions Notebook
	// Router decides which capability a turn runs when the client did not ask
	// for one. It is an optional thing here, and a nil one is this version's
	// predecessor exactly: every turn that named no capability runs the default.
	Router Router
	// Paths is the mastery path a turn works on, for the capabilities that have
	// one. It is optional in the same sense the router is: a nil one runs every
	// turn as this version's predecessor ran it, with no path and no claim.
	Paths Paths
	// Readings resolves the passage a reader had chosen, for the capabilities
	// that are about one. It is optional in the same sense: a nil one is a build
	// with no reading layer in it, where a turn that carries a selection is a
	// turn whose selection could not be read.
	Readings Readings
	// Personas resolves the saved prompt a turn runs under. It is optional in
	// the same sense: a nil one is a build with no persona store, where a turn
	// that names one is refused before it starts rather than run without the
	// prompt it asked for.
	Personas     Personas
	Capabilities *capability.Registry
	Tools        *tool.Registry
	Policy       tool.Policy
	Provider     llm.Provider
	// Titler names a session after its first exchange. A turn whose session is
	// already named does not call it.
	Titler *Titler
	Logger *slog.Logger
	// DefaultCapability runs a turn that did not ask for one.
	DefaultCapability string
	// InputTimeout bounds how long a turn waits for an answer to a question it
	// asked. A turn that waits forever holds its session's turn slot forever.
	InputTimeout time.Duration
}

// Runner owns every turn this process is running.
//
// A turn's goroutine belongs here and not to a connection: a client may
// disconnect while a turn is running and come back to find it still going, so
// nothing about a turn's lifetime follows a socket (AC-04). The runner is what
// makes that true and what a cancel has to reach.
type Runner struct {
	repository   Repository
	hub          *Hub
	sessions     Sessions
	materials    Materials
	questions    Notebook
	router       Router
	paths        Paths
	readings     Readings
	personas     Personas
	capabilities *capability.Registry
	tools        *tool.Registry
	policy       tool.Policy
	provider     llm.Provider
	titler       *Titler
	logger       *slog.Logger
	fallback     string
	inputTimeout time.Duration
	now          func() time.Time

	// rootCtx is what every turn's context derives from. Cancelling it stops
	// the process's turns, which is what a shutdown does and what a client
	// leaving must never do.
	rootCtx  context.Context
	stopRoot context.CancelFunc

	mu     sync.Mutex
	active map[string]*turnRun
	runs   sync.WaitGroup
}

// NewRunner creates a runner, refusing an incomplete one.
func NewRunner(options Options) (*Runner, error) {
	for _, missing := range []struct {
		name  string
		isset bool
	}{
		{"Repository", options.Repository != nil},
		{"Hub", options.Hub != nil},
		{"Sessions", options.Sessions != nil},
		{"Materials", options.Materials != nil},
		{"Questions", options.Questions != nil},
		{"Capabilities", options.Capabilities != nil},
		{"Tools", options.Tools != nil},
		{"Provider", options.Provider != nil},
		{"Titler", options.Titler != nil},
		{"Logger", options.Logger != nil},
	} {
		if !missing.isset {
			return nil, fmt.Errorf("turn: a runner needs %s", missing.name)
		}
	}
	if strings.TrimSpace(options.DefaultCapability) == "" {
		return nil, fmt.Errorf("turn: a runner needs a default capability")
	}
	if options.InputTimeout <= 0 {
		return nil, fmt.Errorf("turn: the input timeout must be positive, got %s", options.InputTimeout)
	}
	rootCtx, stopRoot := context.WithCancel(context.Background())
	return &Runner{
		repository:   options.Repository,
		hub:          options.Hub,
		sessions:     options.Sessions,
		materials:    options.Materials,
		questions:    options.Questions,
		router:       options.Router,
		paths:        options.Paths,
		readings:     options.Readings,
		personas:     options.Personas,
		capabilities: options.Capabilities,
		tools:        options.Tools,
		policy:       options.Policy,
		provider:     options.Provider,
		titler:       options.Titler,
		logger:       options.Logger,
		fallback:     options.DefaultCapability,
		inputTimeout: options.InputTimeout,
		now:          func() time.Time { return time.Now().UTC() },
		rootCtx:      rootCtx,
		stopRoot:     stopRoot,
		active:       make(map[string]*turnRun),
	}, nil
}

// Cancel stops a running turn.
//
// It only signals: the goroutine running the turn is the one that writes the
// terminal state and publishes done, so there is exactly one writer and exactly
// one ending, whether the turn was cancelled or finished on its own (WS-08).
func (r *Runner) Cancel(ctx context.Context, turnID string) error {
	r.mu.Lock()
	run, running := r.active[turnID]
	r.mu.Unlock()

	if running {
		run.cancel()
		return nil
	}

	record, err := r.repository.Turn(ctx, turnID)
	if err != nil {
		return err
	}
	if record.Status.Terminal() {
		// Cancelling a turn that has already stopped is what a client does when
		// its cancel and the turn's own ending cross. There is nothing to undo,
		// so there is nothing to report.
		return nil
	}
	return r.abandon(ctx, record)
}

// abandon ends a turn that is stored as active with nothing running it.
//
// It is what a previous process left behind: the turn was stored, the process
// died before finishing it, and no goroutine in this one will ever move it. Left
// alone it holds its session's turn slot, so the session can never be used
// again. Nothing is watching the turn's stream either, which is why this writes
// the state without publishing an ending.
func (r *Runner) abandon(ctx context.Context, record Turn) error {
	r.logger.WarnContext(ctx, "cancelling a turn no process is running",
		"turn_id", record.ID, "session_id", record.SessionID, "status", record.Status)

	final, err := record.Fail(Failure{
		Error:     "the process running this turn stopped",
		Code:      CodeAbandoned,
		Retryable: true,
	}, r.now())
	if err != nil {
		return err
	}
	return r.repository.Save(ctx, final, record.Status)
}

// SubmitReply answers the question a turn is waiting on.
func (r *Runner) SubmitReply(_ context.Context, turnID, answer string) error {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" {
		return ErrEmptyReply
	}
	r.mu.Lock()
	run := r.active[turnID]
	r.mu.Unlock()
	if run == nil {
		return ErrNotWaiting
	}
	return run.submit(trimmed)
}

// Turn returns one turn, whatever state it is in.
func (r *Runner) Turn(ctx context.Context, turnID string) (Turn, error) {
	return r.repository.Turn(ctx, turnID)
}

// ActiveTurn returns the session's unfinished turn, or ErrNotFound.
func (r *Runner) ActiveTurn(ctx context.Context, sessionID string) (Turn, error) {
	return r.repository.ActiveTurn(ctx, sessionID)
}

// LastSeq returns how far a turn's event stream has got, or zero if it has
// produced nothing yet.
//
// It is what a client that is coming back needs: the position it last saw is
// only useful compared with where the turn actually is, and a client that
// guessed would either skip events or be sent ones it already had.
func (r *Runner) LastSeq(ctx context.Context, turnID string) (int64, error) {
	return r.repository.LastSeq(ctx, turnID)
}

// Subscribe attaches to a turn's stream from a position in it.
//
// Reading the turn's events and reading whether it has already ended are the
// same call, so a client learns where a turn got to and that it is over without
// a second round trip in which the answer could change.
func (r *Runner) Subscribe(ctx context.Context, turnID string, afterSeq int64) (*Subscription, error) {
	return r.hub.Subscribe(ctx, turnID, afterSeq)
}

// Close stops the process's turns and waits for them to record how they ended.
//
// Cancelling makes them end as cancelled, which is what they are: the process
// is going away, not the client's intent. Waiting is what stops a shutdown from
// leaving turns stored as active with nothing running them — the state abandon
// exists to clean up.
func (r *Runner) Close(ctx context.Context) error {
	r.stopRoot()

	stopped := make(chan struct{})
	go func() {
		r.runs.Wait()
		close(stopped)
	}()
	select {
	case <-stopped:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%w: %d turns were still running", ctx.Err(), r.running())
	}
}

// Running is how many turns are in flight right now.
//
// It is exported for the status route (§2.15), and it is a count rather than a
// list on purpose: what an operator asks is whether the process is loaded, and
// which sessions those turns belong to is what the sessions routes answer to
// the accounts that own them.
func (r *Runner) Running() int { return r.running() }

// running counts the turns still in flight.
func (r *Runner) running() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.active)
}
