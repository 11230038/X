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
	// ErrEmptyMessage reports a turn asked to start with nothing to answer.
	ErrEmptyMessage = errors.New("turn: the message is empty")
	// ErrEmptyReply reports an answer to a question that says nothing.
	ErrEmptyReply = errors.New("turn: the reply is empty")
)

// The failure codes a turn can end with.
//
// A client reads these rather than the error text: the text is for an operator
// and may name a provider, a host or an internal step, while a code is what
// tells a client whether to offer a retry, to change what it asked for, or to
// stop. They are stored in turns.failure_code.
const (
	// CodeUnknownCapability means the turn named a capability this service does
	// not offer. Retrying it will not help.
	CodeUnknownCapability = "unknown_capability"
	// CodeInputTimeout means the turn waited for an answer past its deadline.
	CodeInputTimeout = "input_timeout"
	// CodeRoundsExhausted means the model kept calling tools until the round
	// limit stopped it.
	CodeRoundsExhausted = "rounds_exhausted"
	// CodeTimeout means the provider did not answer in time.
	CodeTimeout = "timeout"
	// CodeAbandoned means the process running the turn stopped before it
	// finished, so the turn was ended by whoever found it next.
	CodeAbandoned = "abandoned"
	// CodeInternalError is everything else: a failure this service did not
	// classify. It is the default rather than an absence, so a client never has
	// to interpret a missing code.
	CodeInternalError = "internal_error"
)

// InitialStateVersion is the version a turn is stored at when it is created.
//
// The version counts writes to the turn, and the insert is the first of them,
// so a turn that has never moved is at one and its first transition takes it to
// two. The column requires at least one, which is the same statement made where
// it cannot be forgotten.
const InitialStateVersion int64 = 1

// Turn is one run of a capability in a session.
type Turn struct {
	ID        string
	SessionID string
	Status    Status
	// Capability names what ran. It is empty until the turn starts, and stays
	// empty on a turn that failed before a capability was chosen.
	Capability string
	// StateVersion counts the writes to this turn, starting at
	// InitialStateVersion. Every change increments it, and a change is applied
	// only if the version it was prepared from is still the stored one — which
	// is what stops two writers from both believing they moved the turn.
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

// Routing is where a turn that did not ask for a capability was sent, and on
// what grounds.
//
// The four fields are not the runtime's own invention: they are the payload the
// turn's first event carries, so that "how did this conversation end up writing
// questions" is answerable from the stream a client already has, without a
// second lookup or a column on the turn.
type Routing struct {
	// Capability is what ran.
	Capability string
	// By is how it was decided. A rule table is the only thing this version
	// decides by, and a decision made some other way would be told apart here.
	By string
	// Reason is which rule read the sentence, in one line.
	Reason string
	// Confidence is that rule's own weight: not a model's estimate and not
	// anything measured.
	Confidence float64
}

// Router decides which capability a turn that did not ask for one should run.
//
// It is one method over one sentence, and it is declared here rather than in the
// package that implements it for the same reason the source and question tools
// are declared as interfaces in ports.go: the runtime is told what to run and
// never learns the rules. The rules are a property of the product's language,
// they change for product reasons, and a runtime that imported them would make
// every change to them a change to the turn lifecycle.
//
// A Router may be nil, and that is the version before this one: a turn that did
// not ask for a capability runs the default.
type Router interface {
	Route(content string) (Routing, bool)
}

// RouterFunc lets a function be a Router.
//
// The wiring in assemble.go has a rule table that knows nothing about this
// package — it answers with its own decision type — and one place where the two
// are joined. Being able to pass the closure that does the joining is what keeps
// that seam to a handful of lines instead of a type that exists only to be
// adapted.
type RouterFunc func(content string) (Routing, bool)

// Route implements Router.
func (f RouterFunc) Route(content string) (Routing, bool) { return f(content) }

// metadata is the decision as the turn's first event carries it.
func (r Routing) metadata() map[string]any {
	return map[string]any{
		"capability": r.Capability,
		"by":         r.By,
		"reason":     r.Reason,
		"confidence": r.Confidence,
	}
}
