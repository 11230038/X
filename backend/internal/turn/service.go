package turn

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

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
	Create(ctx context.Context) (session.Session, error)
	Get(ctx context.Context, sessionID string) (session.Session, error)
	Rename(ctx context.Context, sessionID, title string) (session.Session, error)
	AppendMessage(ctx context.Context, input session.NewMessage) (session.Message, error)
	Messages(ctx context.Context, sessionID string) ([]session.Message, error)
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
	// for one. It is the one optional thing here, and a nil one is this version's
	// predecessor exactly: every turn that named no capability runs the default.
	Router       Router
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

// StartRequest is what a client asks for when it starts a turn.
type StartRequest struct {
	// SessionID continues an existing conversation. Empty starts a new one.
	SessionID string
	// Capability is what should run. Empty runs the default.
	Capability string
	// Content is what the user said.
	Content string
	// Attachments are the stored files this turn carries, in the order they were
	// given. The order is what the turn's attachment numbers are: the first file
	// here is the one the model reads as "1".
	Attachments []string
	// KnowledgeBases are the collections this turn may search. Empty means the
	// turn carries none, which is not the same as carrying a default one: a turn
	// that named no collection searches nothing (KB-07).
	KnowledgeBases []string
}

// Started is what starting a turn produced.
//
// The session comes back with the turn because a client that started a new
// conversation needs its id to continue it, and the turn id names the stream it
// should now subscribe to.
type Started struct {
	Session session.Session
	Turn    Turn
}

// Start creates the turn and runs it in the background.
//
// It returns as soon as the turn is stored, before any of it has run: the model
// call takes seconds, and a client that waited for it would be a client that
// could not cancel it, because the id it would cancel by has not reached it.
func (r *Runner) Start(ctx context.Context, request StartRequest) (Started, error) {
	content := strings.TrimSpace(request.Content)
	if content == "" {
		return Started{}, ErrEmptyMessage
	}
	// What runs is decided here, before the turn row exists, which is the whole
	// point of the position: a turn is created already knowing its capability, so
	// nothing downstream has to ask again, and a routed turn is stored as one
	// (TOOL-08). An explicit capability is never rewritten — pre-routing fills a
	// blank and does not overrule an answer.
	capabilityName := strings.TrimSpace(request.Capability)
	var routed *Routing
	if capabilityName == "" {
		capabilityName = r.fallback
		if decision, matched := r.route(ctx, content); matched {
			routed, capabilityName = &decision, decision.Capability
		}
	}

	conversation, err := r.conversation(ctx, request.SessionID)
	if err != nil {
		return Started{}, err
	}
	if _, err := r.sessions.AppendMessage(ctx, session.NewMessage{
		SessionID:  conversation.ID,
		Role:       session.RoleUser,
		Content:    content,
		Capability: capabilityName,
	}); err != nil {
		return Started{}, err
	}

	turnID, err := newID(turnIDPrefix)
	if err != nil {
		return Started{}, fmt.Errorf("generate turn id: %w", err)
	}
	record := Turn{
		ID:         turnID,
		SessionID:  conversation.ID,
		Status:     StatusQueued,
		Capability: capabilityName,
		// The insert is the first write to the turn, so this is where the
		// version count starts. Leaving it at the zero value would store a turn
		// below the first version the column accepts, and every turn would fail
		// to start.
		StateVersion: InitialStateVersion,
		CreatedAt:    r.now(),
		UpdatedAt:    r.now(),
	}

	// From here the turn has a context of its own, derived from the runner's
	// rather than from the request's, so that a client that goes away does not
	// take the turn with it.
	runCtx, cancel := context.WithCancel(r.rootCtx)
	// The emitter keeps a context that reaches past the turn being cancelled:
	// it is what its background flush writes on, and a cancel racing that flush
	// would look like a storage failure rather than the turn ending normally.
	emitter, err := NewEmitter(context.WithoutCancel(runCtx), turnID, r.repository, r.hub)
	if err != nil {
		cancel()
		return Started{}, err
	}
	if err := r.repository.Start(ctx, record); err != nil {
		cancel()
		return Started{}, err
	}

	r.launch(r.newTurnRun(runCtx, cancel, record, conversation, content,
		declarationOf(request.Attachments, request.KnowledgeBases), emitter, routed))
	return Started{Session: conversation, Turn: record}, nil
}

// route asks the router where this turn should go, and checks the answer is one
// this process can run.
//
// The check is here because a rule table is a property of the build while the
// capabilities a process registers are a property of the deployment. A
// deployment that took the question writer out would otherwise turn every
// question-shaped request into a turn that failed with unknown_capability —
// a mistake in the wiring, reported to the user as if the runtime could not read
// it (AC-02). Falling back to the default is exactly what the same request would
// have got before this version; the log is what tells an operator that the table
// and the registration drifted apart.
func (r *Runner) route(ctx context.Context, content string) (Routing, bool) {
	if r.router == nil {
		return Routing{}, false
	}
	decision, matched := r.router.Route(content)
	if !matched {
		return Routing{}, false
	}
	if _, registered := r.capabilities.Lookup(decision.Capability); !registered {
		r.logger.WarnContext(ctx, "a routing rule named a capability this build does not register",
			"capability", decision.Capability, "reason", decision.Reason, "by", decision.By)
		return Routing{}, false
	}
	return decision, true
}

// conversation continues one that exists, or starts one.
func (r *Runner) conversation(ctx context.Context, sessionID string) (session.Session, error) {
	if sessionID == "" {
		return r.sessions.Create(ctx)
	}
	return r.sessions.Get(ctx, sessionID)
}

// launch puts a turn in the table and starts its goroutine.
//
// The table is written before the goroutine starts, so a cancel arriving the
// moment Start returns finds something to cancel rather than concluding the
// turn is not here.
func (r *Runner) launch(run *turnRun) {
	r.mu.Lock()
	r.active[run.turn.ID] = run
	r.runs.Add(1)
	r.mu.Unlock()

	go run.run()
}

// finished removes a turn once its goroutine has stopped.
func (r *Runner) finished(turnID string) {
	r.runs.Done()
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.active, turnID)
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

// running counts the turns still in flight.
func (r *Runner) running() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.active)
}
