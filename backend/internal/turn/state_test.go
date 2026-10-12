package turn

import (
	"errors"
	"testing"
	"time"
)

func TestEveryStatusIsEitherActiveOrTerminal(t *testing.T) {
	// The two questions a status answers are "is it over?" and "does it hold
	// the session's turn slot?". Exactly one answer has to be yes, or a turn is
	// either stuck forever or stops holding a slot it still needs.
	for _, status := range statuses {
		if !status.Valid() {
			t.Errorf("status %q is listed but not valid", status)
			continue
		}
		if status.Active() == status.Terminal() {
			t.Errorf("status %q: active = %v, terminal = %v, want exactly one",
				status, status.Active(), status.Terminal())
		}
	}
}

func TestNoStatusIsListedTwice(t *testing.T) {
	seen := make(map[Status]bool, len(statuses))
	for _, status := range statuses {
		if seen[status] {
			t.Errorf("status %q is listed twice", status)
		}
		seen[status] = true
	}
}

func TestAStatusThisVersionDoesNotDefineIsNothing(t *testing.T) {
	// The empty status is what a Turn has before anything has been stored, and
	// the capitalised one is what a caller would write if it confused a status
	// with a display name. Neither may pass for a real state.
	for _, status := range []Status{"", "Queued", "paused", "done", "RUNNING"} {
		if status.Valid() {
			t.Errorf("status %q is valid", status)
		}
		if status.Terminal() {
			t.Errorf("status %q is terminal", status)
		}
		if status.Active() {
			t.Errorf("status %q is active", status)
		}
	}
}

func TestEveryStatusHasDecidedWhereItMayGo(t *testing.T) {
	// A status added without an entry would silently allow nothing, which reads
	// as "this status is final" rather than as the omission it is.
	for _, status := range statuses {
		if _, decided := transitions[status]; !decided {
			t.Errorf("status %q has no transition entry", status)
		}
	}
	for status := range transitions {
		if !status.Valid() {
			t.Errorf("there are transitions for the unknown status %q", status)
		}
	}
}

func TestATerminalTurnGoesNowhere(t *testing.T) {
	for _, from := range statuses {
		if !from.Terminal() {
			continue
		}
		for _, to := range statuses {
			if CanTransition(from, to) {
				t.Errorf("%s may move to %s, want it to be final", from, to)
			}
		}
	}
}

func TestEveryActiveTurnCanBeGivenUpOn(t *testing.T) {
	// Whatever a turn is doing, a storage failure or a client that stops
	// waiting has to be able to end it — otherwise it keeps its session's turn
	// slot forever and that session can never be used again.
	for _, from := range statuses {
		if !from.Active() {
			continue
		}
		for _, to := range []Status{StatusFailed, StatusCancelled} {
			if !CanTransition(from, to) {
				t.Errorf("%s may not move to %s", from, to)
			}
		}
	}
}

func TestATurnCannotCompleteWithoutHavingRun(t *testing.T) {
	// A queued turn has produced nothing, so there is no answer to call
	// complete, and nothing can have asked it a question yet.
	for _, to := range []Status{StatusCompleted, StatusWaitingInput} {
		if CanTransition(StatusQueued, to) {
			t.Errorf("a queued turn may move to %s", to)
		}
	}
	if !CanTransition(StatusQueued, StatusRunning) {
		t.Error("a queued turn may not start")
	}
}

func TestWaitingForInputIsNotFinishing(t *testing.T) {
	// The model has not answered yet, so the only way on is back to running.
	if CanTransition(StatusWaitingInput, StatusCompleted) {
		t.Error("a turn waiting for input may complete without resuming")
	}
	if !CanTransition(StatusWaitingInput, StatusRunning) {
		t.Error("a turn waiting for input may not resume")
	}
}

func TestTransitionMovesTheTurnAndCountsTheWrite(t *testing.T) {
	now := time.Date(2026, 10, 12, 9, 30, 0, 0, time.UTC)
	turn := Turn{ID: "turn_1", Status: StatusQueued, StateVersion: 4}

	moved, err := turn.Transition(StatusRunning, now)
	if err != nil {
		t.Fatalf("Transition() error = %v", err)
	}

	if moved.Status != StatusRunning {
		t.Errorf("Status = %q", moved.Status)
	}
	// The version is what a concurrent writer compares against, so a move that
	// did not count itself would let the next write overwrite it.
	if moved.StateVersion != 5 {
		t.Errorf("StateVersion = %d, want 5", moved.StateVersion)
	}
	if !moved.UpdatedAt.Equal(now) {
		t.Errorf("UpdatedAt = %v", moved.UpdatedAt)
	}
	// A turn still running has not finished, and a finish time on it would be a
	// claim about the future.
	if !moved.FinishedAt.IsZero() {
		t.Errorf("FinishedAt = %v, want it unset while running", moved.FinishedAt)
	}
	if turn.Status != StatusQueued || turn.StateVersion != 4 {
		t.Errorf("the original turn was changed: %+v", turn)
	}
}

func TestTransitionStampsTheEndOfATerminalTurn(t *testing.T) {
	now := time.Date(2026, 10, 12, 9, 30, 0, 0, time.UTC)

	for _, to := range []Status{StatusCompleted, StatusFailed, StatusCancelled} {
		moved, err := Turn{Status: StatusRunning}.Transition(to, now)
		if err != nil {
			t.Fatalf("Transition(%s) error = %v", to, err)
		}
		if !moved.FinishedAt.Equal(now) {
			t.Errorf("Transition(%s) FinishedAt = %v", to, moved.FinishedAt)
		}
	}
}

func TestTransitionRefusesAMoveTheLifecycleDoesNotAllow(t *testing.T) {
	now := time.Now()
	turn := Turn{ID: "turn_1", Status: StatusCompleted, StateVersion: 7}

	if _, err := turn.Transition(StatusRunning, now); !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("Transition() error = %v, want ErrIllegalTransition", err)
	}
}

func TestTransitionRefusesAStatusThatDoesNotExist(t *testing.T) {
	if _, err := (Turn{Status: StatusQueued}).Transition("paused", time.Now()); !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("Transition() error = %v, want ErrIllegalTransition", err)
	}
}

func TestFailRecordsWhyWithoutLosingTheStep(t *testing.T) {
	now := time.Date(2026, 10, 12, 9, 30, 0, 0, time.UTC)
	turn := Turn{ID: "turn_1", Status: StatusRunning, StateVersion: 2}

	failed, err := turn.Fail(Failure{
		Error:     "deepseek returned 503",
		Code:      "provider_unavailable",
		Retryable: true,
	}, now)
	if err != nil {
		t.Fatalf("Fail() error = %v", err)
	}

	// The status and the reason are one write: a failed turn with nothing to
	// say would be a row no reader could act on.
	if failed.Error != "deepseek returned 503" {
		t.Errorf("Error = %q", failed.Error)
	}
	if failed.FailureCode != "provider_unavailable" {
		t.Errorf("FailureCode = %q", failed.FailureCode)
	}
	if !failed.Retryable {
		t.Error("Retryable = false, want the provider's answer kept")
	}
	if failed.StateVersion != 3 {
		t.Errorf("StateVersion = %d, want the step counted", failed.StateVersion)
	}
	if !failed.FinishedAt.Equal(now) {
		t.Errorf("FinishedAt = %v", failed.FinishedAt)
	}
}

func TestFailCannotRescueATurnThatAlreadyEnded(t *testing.T) {
	turn := Turn{ID: "turn_1", Status: StatusCancelled, StateVersion: 1}

	if _, err := turn.Fail(Failure{Code: "provider_error"}, time.Now()); !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("Fail() error = %v, want ErrIllegalTransition", err)
	}
}
