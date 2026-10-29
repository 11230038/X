package turn

import (
	"context"
	"fmt"
	"sync"
	"time"

	"backend/internal/agentloop"
	"backend/internal/authz"
	"backend/internal/capability"
	"backend/internal/event"
	"backend/internal/llm"
	"backend/internal/session"
	"backend/internal/tool"
	"backend/internal/usage"
)

// terminalWriteTimeout bounds the writes a turn makes after it has stopped.
//
// They run on a context stripped of the turn's cancellation, because a turn
// that was cancelled is exactly the one that still has to record that it was.
// Stripping cancellation removes the deadline with it, so one is put back: a
// database that stops answering must not leave the turn's goroutine behind.
const terminalWriteTimeout = 30 * time.Second

// turnRun is one turn in flight: everything the runtime holds about it while it
// runs, and the steps that play it out.
//
// It is itself what ask_user calls back into, and what Cancel and SubmitReply
// reach through the Runner's table. Only the goroutine running it touches the
// turn it is carrying; the rest goes through the mutex.
type turnRun struct {
	runner  *Runner
	turn    Turn
	session session.Session
	// caller is the account this turn runs for, carried from the request rather
	// than read off the session: an administrator may run a turn in a session
	// that belongs to nobody, and the owner column would then say zero while the
	// writes this turn makes still have to be narrowed to the administrator.
	caller authz.Caller
	// question is what the user asked, kept for naming the session afterwards.
	question string
	// persona is the resolved prompt this turn runs under, and it is empty for a
	// turn that named none. It is set before the goroutine starts and read only
	// by that goroutine; the capability receives it, and nothing in the runtime
	// consults it for a permission decision.
	persona string
	// titleNeeded is whether this turn is the one that names the session: the
	// first turn of an untitled one.
	titleNeeded bool
	// declared is what the client said this turn was carrying, and material is
	// what the service made of it. Both are set before the turn's capability
	// runs and read only by the goroutine running it: the declaration arrives
	// with the request, and the material is resolved once, so the tool plane and
	// the reads that follow it cannot disagree about what this turn has.
	declared Declaration
	material Material
	// routing is how this turn came to run the capability it runs, and it is nil
	// on a turn the client named a capability for. It is set before the turn's
	// goroutine starts and read once, by begin, which is why it needs no lock:
	// the decision belongs to the request, not to the turn's progress.
	routing *Routing
	// sources counts the sources this turn has handed out, and it is what a
	// citation's number is. It belongs to the turn rather than to a read, so two
	// reads number one list between them (see number).
	sources int
	// filed counts the questions this turn has written into the bank, which is
	// the "第 N 道" a save_question result reports. It belongs to the turn
	// rather than to a call for the same reason sources does: a question's
	// number in the set is a fact about the set.
	filed int
	// unavailable counts the material this turn could not read at all — an
	// attachment that failed to parse, a collection that is gone. It sits beside
	// sources because the two are read together: how much material a turn had
	// and how much of it was never seen is what says whether an answer with no
	// evidence had nothing to lean on or was never given anything (AC-06).
	unavailable int

	ctx       context.Context
	cancel    context.CancelFunc
	emitter   *Emitter
	published resultWatch
	// work is the context the turn's own work runs on — the turn's context with
	// whatever the path claim added to it — lease is the claim as the opening
	// event reports it, release lets the path go at the end, and name gives a
	// path this turn opened its final name. All four are set once, before the
	// first event, by claimPath, and read afterwards by the one goroutine
	// running the turn.
	work    context.Context
	lease   map[string]any
	release func(context.Context) error
	name    func(context.Context, string, string) error

	mu    sync.Mutex
	reply chan string
}

// turnKey is the context key a turn puts itself under, so that the process-wide
// ask_user tool can find the turn it is being called for.
type turnKey struct{}

// newTurnRun assembles one turn about to be started, on the context it will run
// under and the cancel that stops it.
func (r *Runner) newTurnRun(
	ctx context.Context,
	cancel context.CancelFunc,
	turn Turn,
	conversation session.Session,
	caller authz.Caller,
	question string,
	persona string,
	declared Declaration,
	emitter *Emitter,
	routed *Routing,
) *turnRun {
	run := &turnRun{
		runner:      r,
		turn:        turn,
		session:     conversation,
		caller:      caller,
		question:    question,
		persona:     persona,
		titleNeeded: conversation.Title == "",
		declared:    declared,
		routing:     routed,
		// Every model call this turn makes is recorded against the account the
		// turn runs for, in this conversation, under the capability it ran
		// (§2.7). It is put on the turn's own context rather than on the context
		// of the capability run, because the turn makes calls outside that run as
		// well — naming the session afterwards, and grading an answer a tool was
		// asked to grade — and those are made for the same account, in the same
		// conversation, from the same turn.
		ctx: usage.WithCall(ctx, usage.Call{
			OwnerID:   caller.UserID,
			SessionID: conversation.ID,
			TurnID:    turn.ID,
			Source:    turn.Capability,
		}),
		cancel:  cancel,
		emitter: emitter,
	}
	run.published = resultWatch{inner: emitter, run: run}
	return run
}

// run plays the turn out and records how it ended.
//
// Every path out of here goes through endTurn, including the ones that fail
// before a capability is reached: a turn that stopped still has to say so, or
// it holds its session's turn slot forever.
func (r *turnRun) run() {
	defer r.runner.finished(r.turn.ID)

	// The path is claimed before anything is said, because the turn announces it
	// in its first event — which path this is, and whether this turn may write
	// it. A claim that fails is a turn that never starts, reported the same way
	// any failure before the first event is.
	err := r.claimPath()
	if err == nil {
		err = r.begin()
	}
	if err == nil {
		// The material is resolved before the tool plane is composed, because the
		// plane is what the material mounts: which source tools this turn is
		// offered is decided by what it turned out to be carrying.
		r.resolveMaterial(r.working())
		err = r.runCapability()
	}

	// The writes that follow must survive the cancellation that may have
	// caused them, so they run on the turn's context without its cancellation.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.ctx), terminalWriteTimeout)
	defer cancel()
	r.endTurn(ctx, err)
}

// claimPath settles which path this turn is on, if any, and decorates the
// context the tools will find it in.
//
// It is the runtime's whole knowledge of the mastery path: it hands over the
// three facts a path claim needs — which capability is running, whose session
// and turn this is, what the user asked — and gets back a context and a claim.
// It never learns what a path is.
func (r *turnRun) claimPath() error {
	if r.runner.paths == nil {
		return nil
	}
	claimed, err := r.runner.paths.Claim(r.ctx, ClaimRequest{
		Capability: r.turn.Capability,
		SessionID:  r.session.ID,
		TurnID:     r.turn.ID,
		Goal:       r.question,
		Caller:     r.caller,
	})
	if err != nil {
		return err
	}
	r.work = claimed.Context
	r.lease = claimed.Lease
	r.release = claimed.Release
	r.name = claimed.Name
	return nil
}

// working is the context the turn's own work runs on: the turn's context, plus
// the path if this turn is on one.
func (r *turnRun) working() context.Context {
	if r.work != nil {
		return r.work
	}
	return r.ctx
}

// begin moves the turn to running and tells the client which conversation it is
// in, before anything else of the turn is produced.
//
// It is also where a routed turn says so, and where a turn on a path says which
// one and who is writing it. Both ride on the first event of the stream rather
// than on the turn's row or on the acknowledgement of the command that started
// it: an acknowledgement travels on a short-lived in-memory table, so a client
// that reconnects and replays from a position would never see it, while an event
// is replayable and always in the same place — before anything the turn produced
// (TOOL-08).
func (r *turnRun) begin() error {
	opening := event.Event{Type: event.TypeSession, Content: r.session.ID}
	metadata := make(map[string]any, 2)
	if r.routing != nil {
		metadata[event.MetaRouting] = r.routing.metadata()
	}
	if r.lease != nil {
		metadata[event.MetaLease] = r.lease
	}
	if len(metadata) > 0 {
		opening.Metadata = metadata
	}
	if err := r.emit(r.ctx, opening); err != nil {
		return err
	}
	moved, err := r.turn.Transition(StatusRunning, r.runner.now())
	if err != nil {
		return err
	}
	if err := r.runner.repository.Save(r.ctx, moved, r.turn.Status); err != nil {
		return err
	}
	r.turn = moved
	return nil
}

// runCapability composes what the capability is allowed to use and runs it.
//
// A name that is not registered fails here rather than at the request, so that
// asking for a capability this service does not have ends as a turn that failed
// — recorded, and reported the same way as any other failure (AC-02).
func (r *turnRun) runCapability() error {
	implementation, ok := r.runner.capabilities.Resolve(r.turn.Capability)
	if !ok {
		return fmt.Errorf("%w: %s", capability.ErrUnknownCapability, r.turn.Capability)
	}
	manifest := implementation.Manifest()

	// What the model is shown and what it may call are decided together and
	// once: the plane below answers from this same list, so a tool the model
	// was not offered cannot be called however it was named (TOOL-07).
	names := r.runner.policy.Compose(tool.Request{
		Capability: manifest.Tools,
		Flags:      r.material.flags(),
	})
	definitions, err := r.runner.tools.Definitions(names)
	if err != nil {
		return err
	}
	plane := mountedPlane{registry: r.runner.tools, mounted: names}

	// The turn's own work runs on the working context, which is where the path
	// claim put what the tools need to find it: the tools are reached through
	// the loop the capability runs, so whatever the claim added travels with it
	// and the capability itself never has to be told about a path.
	work := context.WithValue(r.working(), turnKey{}, r)
	messages, err := r.messages(work)
	if err != nil {
		return err
	}
	loop := agentloop.New(r.runner.provider, &r.published, plane, r.runner.logger, manifest.Name)
	return implementation.Run(work, runContext{
		emitter:  &r.published,
		tools:    definitions,
		messages: messages,
		persona:  r.persona,
		loop:     loop,
		run:      r,
	})
}

// messages reads the conversation so far.
//
// Every turn re-reads it rather than carrying it in memory, so that a turn
// started in a session someone else has been writing in sees what is there now.
func (r *turnRun) messages(ctx context.Context) ([]llm.Message, error) {
	history, err := r.runner.sessions.Messages(ctx, r.session.ID, r.caller)
	if err != nil {
		return nil, err
	}
	messages := make([]llm.Message, 0, len(history))
	for _, message := range history {
		messages = append(messages, llm.Message{
			Role:    llm.Role(message.Role),
			Content: message.Content,
		})
	}
	return messages, nil
}

func (r *turnRun) emit(ctx context.Context, record event.Event) error {
	record.Source = event.SourceTurnRuntime
	return r.emitter.Emit(ctx, record)
}

// report sends an event whose failure is not worth stopping for: the turn is
// over, and the only thing left to get wrong is telling the client so.
func (r *turnRun) report(ctx context.Context, record event.Event) {
	if err := r.emit(ctx, record); err != nil {
		r.runner.logger.ErrorContext(ctx, "could not publish the end of a turn",
			"turn_id", r.turn.ID, "type", record.Type, "error", err)
	}
}
