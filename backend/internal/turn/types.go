// Package turn is the runtime: it owns a turn from the moment it is queued
// until it reaches a terminal state, including the events it produces and who
// is watching them.
//
// A turn is one run of a capability in a session. Its lifetime is longer than
// any one connection's: a client may disconnect and come back to find it still
// running, which is why the goroutine running a turn belongs to this package
// and not to a request.
package turn

import (
	"errors"
	"time"
)

// Status is where a turn is in its life.
type Status string

// The statuses a turn moves through. The first three are active: a turn in one
// of them still holds its session's turn slot.
const (
	StatusQueued       Status = "queued"
	StatusRunning      Status = "running"
	StatusWaitingInput Status = "waiting_input"
	StatusCompleted    Status = "completed"
	StatusFailed       Status = "failed"
	StatusCancelled    Status = "cancelled"
)

// statuses is every status, for validation and for tests that must cover them
// all rather than the ones someone remembered.
var statuses = []Status{
	StatusQueued, StatusRunning, StatusWaitingInput,
	StatusCompleted, StatusFailed, StatusCancelled,
}

// terminal is the set of statuses a turn stops in. It is what makes a status
// answer "can this turn still change?" without asking which one it is.
var terminal = map[Status]bool{
	StatusCompleted: true,
	StatusFailed:    true,
	StatusCancelled: true,
}

// Valid reports whether the status is one this version defines.
func (s Status) Valid() bool {
	for _, candidate := range statuses {
		if candidate == s {
			return true
		}
	}
	return false
}

// Terminal reports whether the turn has stopped. A terminal turn is final:
// nothing cancels it, answers it, or brings it back.
func (s Status) Terminal() bool { return terminal[s] }

// Active reports whether the turn still holds its session's turn slot, which is
// what makes a second turn in that session a conflict.
func (s Status) Active() bool { return s.Valid() && !s.Terminal() }

var (
	// ErrNotFound reports a turn that is not stored.
	ErrNotFound = errors.New("turn: not found")
	// ErrActiveTurnExists reports a session that already has a turn which has
	// not stopped (RT-02).
	ErrActiveTurnExists = errors.New("turn: the session already has an active turn")
	// ErrStateConflict reports a turn that moved between being read and being
	// written, so the change was applied to a state that no longer holds.
	ErrStateConflict = errors.New("turn: the turn changed while this write was prepared")
	// ErrIllegalTransition reports a status change the state machine does not
	// allow.
	ErrIllegalTransition = errors.New("turn: illegal status transition")
	// ErrSeqTaken reports an event whose sequence number is already stored. It
	// means another writer holds this turn, not that the write should be retried.
	ErrSeqTaken = errors.New("turn: the event sequence is already taken")
	// ErrNotWaiting reports a reply for a turn that is not waiting for input.
	ErrNotWaiting = errors.New("turn: the turn is not waiting for input")
	// ErrInputTimeout reports a turn that waited for an answer past its
	// deadline.
	ErrInputTimeout = errors.New("turn: the input deadline passed")
)

// Turn is one run of a capability in a session.
type Turn struct {
	ID        string
	SessionID string
	Status    Status
	// Capability names what ran. It is empty until the turn starts, and stays
	// empty on a turn that failed before a capability was chosen.
	Capability string
	// StateVersion counts the writes to this turn. Every change increments it,
	// and a change is applied only if the version it was prepared from is still
	// the stored one — which is what stops two writers from both believing they
	// moved the turn.
	StateVersion int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
	FinishedAt   time.Time
	// Error is what went wrong, for an operator reading a log or a database
	// row. It is never sent to a client: a client gets FailureCode and
	// Retryable, which say what it can do about it.
	Error       string
	FailureCode string
	Retryable   bool
	// AssistantMessageID points at the message this turn produced, once it has
	// been written.
	AssistantMessageID string
}
