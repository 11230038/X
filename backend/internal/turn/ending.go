package turn

// How a turn ends: what it stores, what state it reaches, and what it tells whoever
// is watching.
//
// It is apart from runner.go because the ordering here is the whole of the file.
// The answer is stored, then the turn reaches its final state, then the client is
// told — and a client told about an ending that was never stored would have seen a
// different turn from the one in the database. Reading that order, and the reasons
// each step is where it is, is easier when the file holds nothing that runs before
// an ending.

import (
	"context"
	"errors"
	"strings"
	"time"

	"backend/internal/event"
	"backend/internal/session"
	"backend/internal/usage"
)

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
	}, r.caller)
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
		// A quota refusal says which limit and until when, because the code
		// alone cannot: a client that knows only "out of something" can tell the
		// user to wait without being able to say how long or where to look.
		if failure.Quota != nil {
			metadata[event.MetaQuotaItem] = failure.Quota.Item
			metadata[event.MetaQuotaResetsAt] = failure.Quota.ResetsAt.UTC().Format(time.RFC3339)
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
	// The naming call is the turn's — same account, same conversation — but it
	// is not part of what the capability did, and it is recorded under its own
	// source so that what naming costs is a question the usage table can answer.
	ctx = usage.WithSource(ctx, usage.SourceTurnTitle)

	title := r.session.Title
	if r.titleNeeded && completed {
		generated, err := r.runner.titler.Title(ctx, r.question, r.published.answer)
		if err != nil {
			r.runner.logger.WarnContext(ctx, "could not name a session",
				"session_id", r.session.ID, "error", err)
		} else if renamed, err := r.runner.sessions.Rename(ctx, r.session.ID, generated, r.caller); err != nil {
			r.runner.logger.WarnContext(ctx, "could not store a session name",
				"session_id", r.session.ID, "error", err)
		} else {
			title = renamed.Title
		}
	}
	r.report(ctx, event.Event{Type: event.TypeSessionMeta, Content: title})
}
