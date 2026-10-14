package turn

import (
	"fmt"
	"time"

	"backend/internal/usage"
)

// transitions is the lifecycle: what a turn in each status may become.
//
// It is written out rather than derived from Active, because the interesting
// rules are the ones between active states. A turn may not go from queued
// straight to completed — nothing ran — and a turn that is waiting for input
// does not complete; it goes back to running first, because the model has not
// finished answering.
var transitions = map[Status]map[Status]bool{
	// A queued turn can start, be cancelled before it does, or fail before it
	// gets that far — an unknown capability is a failure of the turn that was
	// asked for, and it still has to reach a terminal state and say so.
	StatusQueued: {
		StatusRunning:   true,
		StatusCancelled: true,
		StatusFailed:    true,
	},
	StatusRunning: {
		StatusWaitingInput: true,
		StatusCompleted:    true,
		StatusFailed:       true,
		StatusCancelled:    true,
	},
	StatusWaitingInput: {
		StatusRunning:   true,
		StatusFailed:    true,
		StatusCancelled: true,
	},
	// Terminal: nothing leads out of them.
	StatusCompleted: {},
	StatusFailed:    {},
	StatusCancelled: {},
}

// CanTransition reports whether the state machine allows a move.
func CanTransition(from, to Status) bool {
	return transitions[from][to]
}

// Transition returns the turn as it stands after moving to a new status.
//
// It returns the result rather than changing the receiver: the caller decides
// what to do with a turn that cannot move, and a half-applied change is exactly
// the state this package exists to prevent.
func (t Turn) Transition(to Status, now time.Time) (Turn, error) {
	if !to.Valid() {
		return Turn{}, fmt.Errorf("%w: %q is not a status", ErrIllegalTransition, to)
	}
	if !CanTransition(t.Status, to) {
		return Turn{}, fmt.Errorf("%w: %s to %s", ErrIllegalTransition, t.Status, to)
	}
	moved := t
	moved.Status = to
	moved.StateVersion = t.StateVersion + 1
	moved.UpdatedAt = now
	if to.Terminal() {
		moved.FinishedAt = now
	}
	return moved, nil
}

// Fail returns the turn as it stands after a failure.
//
// The status change and the reason are one operation: a turn that failed and a
// turn that failed for a reason are never stored apart, so no reader has to
// handle the case of a failed turn with nothing to say.
func (t Turn) Fail(failure Failure, now time.Time) (Turn, error) {
	failed, err := t.Transition(StatusFailed, now)
	if err != nil {
		return Turn{}, err
	}
	failed.Error = failure.Error
	failed.FailureCode = failure.Code
	failed.Retryable = failure.Retryable
	return failed, nil
}

// Failure is why a turn did not finish, split into what an operator needs and
// what a client is told.
type Failure struct {
	// Error is the internal reason. It stays in the log and the database.
	Error string
	// Code is the structured reason a client acts on.
	Code string
	// Retryable says whether repeating the turn could plausibly work.
	Retryable bool
	// PartialResponse says whether any answer text had already reached the
	// client, so it knows what it is looking at when the turn ends.
	PartialResponse bool
	// Quota is the refusal behind a CodeQuotaExceeded failure, when there was
	// one, and nil for every other failure.
	//
	// It has no column: the code, the account and the moment are what the turn
	// row keeps, and the row is not the place to copy a number out of another
	// table. What it is for is the error event — a client told its allowance is
	// used up is told which one and when it comes back, and neither of those two
	// has a column here to travel on.
	Quota *usage.Exceeded
}
