package turn

// Starting a turn: what the client asks for, everything that is checked before
// the turn row exists, and the handover to the goroutine that runs it.
//
// The checks live on this side of the insert on purpose. A request that names a
// persona the caller does not have, or a selection that cannot be a span of any
// text, is refused while it is still a request; a turn that started and then
// discovered either would be a stream the client subscribed to that could only
// report the problem.

import (
	"context"
	"fmt"
	"strings"

	"backend/internal/authz"
	"backend/internal/session"
)

// StartRequest is what a client asks for when it starts a turn.
type StartRequest struct {
	// OwnerID is whose turn this is, and it is zero for a turn with no account
	// behind it — a test, or a connection nobody has signed in on (§2.7).
	//
	// The runner does not know what the account is for. It knows that a turn has
	// a master, and the master is who the Memory it observes belongs to; a turn
	// with no master writes no Memory at all, which is a narrower and safer rule
	// than writing it somewhere shared. The id arrives from the authenticated
	// websocket connection, because that boundary is the only layer that knows who
	// is on the other end of it.
	OwnerID int64
	// SessionID continues an existing conversation. Empty starts a new one.
	SessionID string
	// Capability is what should run. Empty runs the default.
	Capability string
	// PersonaID is the saved prompt this turn should run under. Empty runs it
	// under none.
	//
	// It is one turn's, like a selection: the conversation stores what was said
	// and not the voice it was said in, so switching personas midway through
	// leaves a history that is still just the messages. The resolved text goes
	// to the model and is never written into a message row.
	PersonaID string
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
	// Selection is the passage the reader had chosen in a reading material, and
	// it is empty for every turn that is not an immersive reading one (§2.8). It
	// is one turn's material and not a conversation's: it is what the reader was
	// looking at when they asked, and it is not stored — a saved selection would
	// be somebody else's highlight in the second tab.
	Selection *Selection
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
	// The account travels with the turn from here on, because everything the
	// turn writes is one person's: the conversation it appends to, the questions
	// it files (notebook_entries carries its own owner) and the path it claims.
	// A turn started with no account is a turn whose work belongs to nobody, and
	// nobody is the administrator — which is what a connection that was never
	// signed in on gets, and what the ws boundary must therefore always fill in.
	account := authz.Caller{UserID: request.OwnerID}
	// The persona is resolved before the turn exists for the same reason the
	// selection is checked before it: a turn that started and then found it had
	// no prompt would be a user waiting on an answer to a request nobody could
	// serve. It is also read through the caller, so another account's persona is
	// an id this turn does not have — never one it silently runs without.
	personaText, err := r.persona(ctx, request.PersonaID, account)
	if err != nil {
		return Started{}, err
	}
	// A selection is checked before the turn exists, because what can be wrong
	// with one is wrong with the request: a coordinate that cannot be a span of
	// any text is the client's to fix, and a turn that starts and then reports it
	// is a user waiting on an answer that was never going to come.
	if request.Selection != nil {
		if err := request.Selection.Validate(); err != nil {
			return Started{}, err
		}
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

	conversation, err := r.conversation(ctx, request.SessionID, account)
	if err != nil {
		return Started{}, err
	}
	if _, err := r.sessions.AppendMessage(ctx, session.NewMessage{
		SessionID:  conversation.ID,
		Role:       session.RoleUser,
		Content:    content,
		Capability: capabilityName,
	}, account); err != nil {
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

	r.launch(r.newTurnRun(runCtx, cancel, record, conversation, account, content, personaText,
		declarationOf(request.Attachments, request.KnowledgeBases, request.Selection), emitter, routed))
	return Started{Session: conversation, Turn: record}, nil
}

// persona resolves the saved prompt a turn was asked to run under.
//
// An empty id is no persona and not an error. A named one this account does not
// have is ErrPersonaNotFound, whether it never existed or belongs to somebody
// else: the two are one answer, and the second is not a fact an account is
// entitled to learn. A store that could not answer is returned unchanged, which
// stops the turn for a reason that is not the caller's to fix.
func (r *Runner) persona(ctx context.Context, personaID string, caller authz.Caller) (string, error) {
	id := strings.TrimSpace(personaID)
	if id == "" {
		return "", nil
	}
	if r.personas == nil {
		return "", fmt.Errorf("%w: %s", ErrPersonaNotFound, id)
	}
	instruction, found, err := r.personas.Instruction(ctx, id, caller)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("%w: %s", ErrPersonaNotFound, id)
	}
	return instruction, nil
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
//
// Either way the answer is one the caller may write in. Continuing somebody
// else's conversation fails here, as a session that is not found, before the
// turn row exists — a turn that started and then discovered it had no session
// would be a stream the client subscribed to that could only report the problem.
func (r *Runner) conversation(ctx context.Context, sessionID string, caller authz.Caller) (session.Session, error) {
	if sessionID == "" {
		// An implicit session is a chat session: the writing page creates its
		// session explicitly so it can name the kind, and a client that sends
		// no id at all is the old chat protocol.
		return r.sessions.Create(ctx, caller, session.KindChat)
	}
	return r.sessions.Get(ctx, sessionID, caller)
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
