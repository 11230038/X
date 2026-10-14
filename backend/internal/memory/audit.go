package memory

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// The audit trail (MEM-02): one row per action taken on memory.
//
// The table is append-only and the enforcement is here rather than in the
// schema: this engine has no table property that forbids an UPDATE, so the
// guarantee lives in the interface a caller is given — Repository has an append
// and a read over `memory_audit` and no third method. A test asserts that shape,
// which is the only place the guarantee can be checked at all.
//
// What is *not* here is as much a decision as what is, and the biggest omission
// is the gate. An admission writes no audit row: the fact it wrote names its own
// evidence in `memory_fact_sources`, which answers "why is this here" better than
// a log line can, and the counts a turn wants go out in its `result` metadata
// (§4). An observation that fails the gate writes no row either — the trace in L1
// is already the record that it was heard. What is left, and what this file is
// for, is the actions somebody asked for: the user stating, editing, retracting,
// restoring, merging, purging or exporting.

// Actor is who asked for an action.
//
// Today it is always the account holder: every audited action arrives over HTTP
// on the user's behalf. The column is a word rather than a boolean because a
// second actor is entirely imaginable — a tool changing memory in a turn — and
// naming one then is a constant beside this one.
type Actor string

// ActorUser is the account holder.
const ActorUser Actor = "user"

// Action is what was done.
//
// The vocabulary is dotted, as the mastery events' is, and closed: these are the
// words this package writes, and a database CHECK does not hold them because
// adding a word should not be a migration.
type Action string

const (
	// ActionFactStated is the user writing a fact down themselves (§2.7).
	ActionFactStated Action = "fact.stated"
	// ActionFactEdited is a statement or scope being changed.
	ActionFactEdited Action = "fact.edited"
	// ActionFactRetracted is a fact being taken back. The row stays.
	ActionFactRetracted Action = "fact.retracted"
	// ActionFactRestored is a retraction being undone.
	ActionFactRestored Action = "fact.restored"
	// ActionFactMerged is a dedupe folding one fact into another.
	ActionFactMerged Action = "fact.merged"
	// ActionFactPurged is a cleanup removing a fact outright.
	ActionFactPurged Action = "fact.purged"
	// ActionMemoryExported is the account being handed its memory.
	ActionMemoryExported Action = "memory.exported"
)

// Valid reports whether the action is one this package writes.
func (a Action) Valid() bool {
	switch a {
	case ActionFactStated, ActionFactEdited, ActionFactRetracted, ActionFactRestored,
		ActionFactMerged, ActionFactPurged, ActionMemoryExported:
		return true
	default:
		return false
	}
}

// TargetKind is what an action was about.
type TargetKind string

const (
	// TargetFact is one fact, named by id.
	TargetFact TargetKind = "fact"
	// TargetMemory is the account's memory as a whole — an export, which names no
	// row. It is why TargetID is nullable: an action about everything has no one
	// id to point at.
	TargetMemory TargetKind = "memory"
)

// Valid reports whether the target kind is one this package writes.
func (t TargetKind) Valid() bool {
	switch t {
	case TargetFact, TargetMemory:
		return true
	default:
		return false
	}
}

// Result is how an action ended.
type Result string

const (
	// ResultDone is the action doing what it was asked to.
	ResultDone Result = "done"
	// ResultPartial is a batch that did most of what it was asked and lost a race
	// on the rest — a dedupe group whose survivor was edited while the merge was
	// running. It is recorded rather than retried because the state the caller
	// sees is "this one did not merge", and the next run will find it again.
	ResultPartial Result = "partial"
)

// Valid reports whether the result is one of the two.
func (r Result) Valid() bool {
	switch r {
	case ResultDone, ResultPartial:
		return true
	default:
		return false
	}
}

// AuditQuery is a listing of the audit trail: one account's actions, newest
// first, optionally the ones of one action (§2.14's `GET /memory/audit`).
//
// There is no filter by target, and that is the read being honest about what it
// is for: the question the trail answers is "what has been done to my memory and
// when", and "what was done to this fact" is answered by the fact's own row set —
// `memory_fact_sources` for what it rests on, and the row itself for what state
// it is in.
type AuditQuery struct {
	OwnerID int64
	// Action filters the read; empty means every action.
	Action Action
	Limit  int
}

// validate reports whether the question is one worth asking.
func (q AuditQuery) validate() error {
	if q.OwnerID <= 0 {
		return fmt.Errorf("%w: the owner is %d", ErrInvalidRequest, q.OwnerID)
	}
	if q.Action != "" && !q.Action.Valid() {
		return fmt.Errorf("%w: %q is not an action", ErrInvalidRequest, q.Action)
	}
	if q.Limit < 0 {
		return fmt.Errorf("%w: a limit of %d is not a limit", ErrInvalidRequest, q.Limit)
	}
	return nil
}

// Audit is one action, as it is stored.
type Audit struct {
	ID      string
	OwnerID int64
	Actor   Actor
	Action  Action
	// TargetKind and TargetID name what the action was about. TargetID is empty
	// for an action about the account as a whole.
	TargetKind TargetKind
	TargetID   string
	Result     Result
	// Detail is the action's own JSON: for a merge, the fingerprint and which
	// fact absorbed which. It is what makes the row answer "what exactly
	// happened" and not only "something did".
	Detail    []byte
	CreatedAt time.Time
}

// Validate reports whether the audit row is fit to store.
func (a Audit) Validate() error {
	if strings.TrimSpace(a.ID) == "" {
		return fmt.Errorf("%w: the audit id is blank", ErrInvalidRequest)
	}
	if a.OwnerID <= 0 {
		return fmt.Errorf("%w: the owner is %d", ErrInvalidRequest, a.OwnerID)
	}
	if strings.TrimSpace(string(a.Actor)) == "" {
		return fmt.Errorf("%w: the actor is blank", ErrInvalidRequest)
	}
	if !a.Action.Valid() {
		return fmt.Errorf("%w: %q is not an action", ErrInvalidRequest, a.Action)
	}
	if !a.TargetKind.Valid() {
		return fmt.Errorf("%w: %q is not a target kind", ErrInvalidRequest, a.TargetKind)
	}
	if !a.Result.Valid() {
		return fmt.Errorf("%w: %q is not a result", ErrInvalidRequest, a.Result)
	}
	if len(a.Detail) > 0 && !json.Valid(a.Detail) {
		return fmt.Errorf("%w: the audit detail is not JSON", ErrInvalidRequest)
	}
	return nil
}
