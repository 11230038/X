package turn

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"backend/internal/agentloop"
	"backend/internal/capability"
	"backend/internal/event"
	"backend/internal/llm"
	"backend/internal/session"
	"backend/internal/tool"
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
	// question is what the user asked, kept for naming the session afterwards.
	question string
	// titleNeeded is whether this turn is the one that names the session: the
	// first turn of an untitled one.
	titleNeeded bool

	ctx       context.Context
	cancel    context.CancelFunc
	emitter   *Emitter
	published resultWatch

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
	question string,
	emitter *Emitter,
) *turnRun {
	run := &turnRun{
		runner:      r,
		turn:        turn,
		session:     conversation,
		question:    question,
		titleNeeded: conversation.Title == "",
		ctx:         ctx,
		cancel:      cancel,
		emitter:     emitter,
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

	err := r.begin()
	if err == nil {
		err = r.runCapability()
	}

	// The writes that follow must survive the cancellation that may have
	// caused them, so they run on the turn's context without its cancellation.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.ctx), terminalWriteTimeout)
	defer cancel()
	r.endTurn(ctx, err)
}

// begin moves the turn to running and tells the client which conversation it is
// in, before anything else of the turn is produced.
func (r *turnRun) begin() error {
	if err := r.emit(r.ctx, event.Event{Type: event.TypeSession, Content: r.session.ID}); err != nil {
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
	names := r.runner.policy.Compose(tool.Request{Capability: manifest.Tools})
	definitions, err := r.runner.tools.Definitions(names)
	if err != nil {
		return err
	}
	plane := mountedPlane{registry: r.runner.tools, mounted: names}

	messages, err := r.messages(r.ctx)
	if err != nil {
		return err
	}
	loop := agentloop.New(r.runner.provider, &r.published, plane, r.runner.logger, manifest.Name)
	return implementation.Run(context.WithValue(r.ctx, turnKey{}, r), runContext{
		emitter:  &r.published,
		tools:    definitions,
		messages: messages,
		loop:     loop,
	})
}

// messages reads the conversation so far.
//
// Every turn re-reads it rather than carrying it in memory, so that a turn
// started in a session someone else has been writing in sees what is there now.
func (r *turnRun) messages(ctx context.Context) ([]llm.Message, error) {
	history, err := r.runner.sessions.Messages(ctx, r.session.ID)
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

// endTurn records how the turn ended and tells whoever is watching.
//
// The order is fixed and matters: the answer is stored, then the turn reaches
// its final state, then the client is told. A client told about an ending that
// was never stored would see a different turn from the one in the database.
func (r *turnRun) endTurn(ctx context.Context, failure error) {
	outcome, turnFailure := r.outcome(failure)
	r.storeAnswer(ctx)
	if err := r.record(ctx, outcome, turnFailure); err != nil {
		r.runner.logger.ErrorContext(ctx, "could not record how a turn ended",
			"turn_id", r.turn.ID, "error", err)
	}

	r.announce(ctx, outcome, turnFailure)
	r.nameSession(ctx, turnFailure == nil)
	if err := r.emitter.Close(ctx); err != nil {
		r.runner.logger.ErrorContext(ctx, "could not flush the end of a turn",
			"turn_id", r.turn.ID, "error", err)
	}
}

// outcome decides which status the turn stops in.
//
// Cancellation is read off the turn's own context rather than off the error:
// a provider that failed because the turn was cancelled reports a failure, and
// reporting that as one would call a client's own cancel an error.
func (r *turnRun) outcome(failure error) (Status, *Failure) {
	if r.ctx.Err() != nil {
		// A cancel explains a call that ended because of it, and nothing else.
		// Anything other than that is a failure that happened to land in the
		// same moment, and a cancelled turn reports no failure to a client —
		// so this log is the only place it exists.
		if failure != nil && !errors.Is(failure, context.Canceled) && !errors.Is(failure, context.DeadlineExceeded) {
			r.runner.logger.ErrorContext(r.ctx, "a turn was cancelled while a failure was in flight",
				"turn_id", r.turn.ID, "capability", r.turn.Capability, "error", failure)
		}
		return StatusCancelled, nil
	}
	if failure == nil {
		return StatusCompleted, nil
	}
	described := describe(failure)
	described.PartialResponse = r.published.answered
	r.runner.logger.ErrorContext(r.ctx, "a turn failed",
		"turn_id", r.turn.ID, "capability", r.turn.Capability, "error", failure)
	return StatusFailed, &described
}

// storeAnswer writes the assistant's message, which is what makes the answer
// part of the conversation rather than something only the live stream carried.
func (r *turnRun) storeAnswer(ctx context.Context) {
	answer := strings.TrimSpace(r.published.answer)
	if answer == "" {
		return
	}
	message, err := r.runner.sessions.AppendMessage(ctx, session.NewMessage{
		SessionID:  r.session.ID,
		Role:       session.RoleAssistant,
		Content:    answer,
		Capability: r.turn.Capability,
	})
	if err != nil {
		// The turn still ends and the client still hears about it: the answer
		// has already been streamed, so failing the turn now would tell the
		// client to retry something it has already seen.
		r.runner.logger.ErrorContext(ctx, "could not store an answer",
			"turn_id", r.turn.ID, "session_id", r.session.ID, "error", err)
		return
	}
	r.turn.AssistantMessageID = message.ID
}

// record reaches the turn's final state.
func (r *turnRun) record(ctx context.Context, outcome Status, failure *Failure) error {
	var (
		final Turn
		err   error
	)
	if failure == nil {
		final, err = r.turn.Transition(outcome, r.runner.now())
	} else {
		final, err = r.turn.Fail(*failure, r.runner.now())
	}
	if err != nil {
		return err
	}
	if err := r.runner.repository.Save(ctx, final, r.turn.Status); err != nil {
		return err
	}
	r.turn = final
	return nil
}

// announce tells whoever is watching that the turn is over.
//
// A failure is announced by an error event and then the same done event every
// turn ends with, so a client has one thing to wait for and one thing to read
// the outcome from. A cancellation is not a failure and gets only the done.
func (r *turnRun) announce(ctx context.Context, outcome Status, failure *Failure) {
	if failure != nil {
		metadata := map[string]any{
			event.MetaErrorCode:       failure.Code,
			event.MetaRetryable:       failure.Retryable,
			event.MetaPartialResponse: failure.PartialResponse,
			event.MetaTurnTerminal:    true,
			event.MetaStatus:          string(outcome),
		}
		message, ok := clientMessages[failure.Code]
		if !ok {
			message = clientMessages[CodeInternalError]
		}
		r.report(ctx, event.Event{Type: event.TypeError, Content: message, Metadata: metadata})
	}
	r.report(ctx, event.Event{
		Type: event.TypeDone,
		Metadata: map[string]any{
			event.MetaStatus:          string(outcome),
			event.MetaPartialResponse: r.published.answered,
		},
	})
}

// nameSession generates the session's title and publishes the event that ends
// the turn's stream (WS-07).
//
// The event is sent whatever happened, including when there is no title to
// give: it is what tells a subscriber the stream is over, and a subscriber left
// waiting on a turn that has ended is worse than one given a stale title.
func (r *turnRun) nameSession(ctx context.Context, completed bool) {
	title := r.session.Title
	if r.titleNeeded && completed {
		generated, err := r.runner.titler.Title(ctx, r.question, r.published.answer)
		if err != nil {
			r.runner.logger.WarnContext(ctx, "could not name a session",
				"session_id", r.session.ID, "error", err)
		} else if renamed, err := r.runner.sessions.Rename(ctx, r.session.ID, generated); err != nil {
			r.runner.logger.WarnContext(ctx, "could not store a session name",
				"session_id", r.session.ID, "error", err)
		} else {
			title = renamed.Title
		}
	}
	r.report(ctx, event.Event{Type: event.TypeSessionMeta, Content: title})
}

// askUser pauses the turn to ask the user something.
//
// It is what ask_user calls, through the context the turn put itself in. The
// wait is opened before the question is published, and holds one answer, so a
// user who answers the instant the question appears is not told the turn was
// not listening — which is exactly what a plain unbuffered send would say, in
// the moment between the question being published and this goroutine parking on
// it.
func (r *turnRun) askUser(ctx context.Context, question string) (string, error) {
	if err := r.pause(ctx); err != nil {
		return "", err
	}
	replies := make(chan string, 1)
	r.await(replies)

	if err := r.emit(ctx, event.Event{Type: event.TypeWaitForInput, Content: question}); err != nil {
		return "", err
	}

	select {
	case answer := <-replies:
		if err := r.resume(ctx); err != nil {
			return "", err
		}
		return answer, nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(r.runner.inputTimeout):
		return "", ErrInputTimeout
	}
}

// pause stores the turn as waiting for an answer and moves it on in memory with
// it. The two must not drift: a turn that moved in storage while this copy still
// said running would be refused every later write as a conflict, and it would
// wait for an answer it could never record the end of.
func (r *turnRun) pause(ctx context.Context) error {
	paused, err := r.turn.Transition(StatusWaitingInput, r.runner.now())
	if err != nil {
		return err
	}
	if err := r.runner.repository.Save(ctx, paused, r.turn.Status); err != nil {
		return err
	}
	r.turn = paused
	return nil
}

// resume picks the turn back up after the answer arrived.
func (r *turnRun) resume(ctx context.Context) error {
	resumed, err := r.turn.Transition(StatusRunning, r.runner.now())
	if err != nil {
		return err
	}
	if err := r.runner.repository.Save(ctx, resumed, r.turn.Status); err != nil {
		return err
	}
	r.turn = resumed
	return nil
}

// await opens this turn's wait for one answer, replacing whatever was there.
//
// A fresh channel per question is what keeps answers with their questions: once
// a question has been answered its channel is replaced, so an answer left in
// the old one is unreachable rather than waiting to satisfy the next question
// nobody has asked.
func (r *turnRun) await(replies chan string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reply = replies
}

// submit hands an answer to the turn if it is waiting for one.
//
// The send does not wait: an answer that arrives when the turn is not waiting
// is refused rather than held until it is (WS-09).
func (r *turnRun) submit(answer string) error {
	r.mu.Lock()
	replies := r.reply
	r.mu.Unlock()

	if replies == nil {
		return ErrNotWaiting
	}
	select {
	case replies <- answer:
		return nil
	default:
		return ErrNotWaiting
	}
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
