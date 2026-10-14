// Package memory owns what the account remembers.
//
// Three layers, and only two of them are tables (§2.6):
//
//   - L1 is `memory_traces`: what a surface said, once. Append-only, and a row
//     there means the system *heard* it — not that the system believes it.
//   - L2 is `memory_facts`, with `memory_fact_sources` beside it: what the
//     system admitted, and which observations it admitted it on. A fact is
//     written by the gate (admission.go) or by the user's own word, and never by
//     a model's say-so alone (MEM-03).
//   - L3 is not here at all. It is a pure function in synthesis.go that computes
//     what memory looks like now from the L2 rows. No snapshot table means
//     retracting a fact changes the synthesis in the same instant — there is no
//     second place for the old answer to hide.
//
// What this package does not know matters as much as what it does. It does not
// import mastery, reading or practice, and it knows nothing about a turn beyond
// an id it is handed. Those three domains are the writers: they notice something
// and hand it in through Observe, and the arrow only ever points that way (§1).
//
// Ownership is the account's (§2.7, MEM-04). Every read and every write takes an
// owner, and another account's row is *not found* rather than refused: a listing
// that answered "that id exists but is not yours" would answer a question about
// somebody else's memory.
package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The sentinels this package's callers switch on.
var (
	// ErrNotFound is a trace, a fact or an account's row that is not there — or
	// one belonging to another account, which from a caller's side is the same
	// answer.
	ErrNotFound = errors.New("memory: not found")
	// ErrInvalidRequest is a request the domain will not act on.
	ErrInvalidRequest = errors.New("memory: the request is not valid")
	// ErrDuplicate is a write that lost a race on a key: the same trace id, the
	// same fact id, the same piece of evidence already attached.
	ErrDuplicate = errors.New("memory: already recorded")
	// ErrStaleState is a write that lost the version guard. The fact it was
	// computed from is not the fact on the row any more, and the caller has to
	// read again rather than write.
	ErrStaleState = errors.New("memory: the fact moved on")
)

// Surface names which part of the product made an observation.
//
// It is a closed set of constants rather than a free string, and that is a
// safety decision: the gate admits a fact only when observations from two
// *different* surfaces agree, so a surface that arrives spelled two ways would
// let one voice corroborate itself. A fifth surface is a constant beside these.
//
// The three domains are §1's writers. SurfaceUser is the fourth: a fact the user
// wrote down themselves (§2.7) came from the user, not from any capability, and
// saying so keeps it from corroborating a model's observation of the same thing.
type Surface string

const (
	// SurfaceMastery is the mastery path — what a learner demonstrated.
	SurfaceMastery Surface = "mastery"
	// SurfaceReading is immersive reading — what a reader worked through.
	SurfaceReading Surface = "reading"
	// SurfacePractice is practice — how a question was answered.
	SurfacePractice Surface = "practice"
	// SurfaceUser is the account holder, writing a fact down directly.
	SurfaceUser Surface = "user"
)

// Valid reports whether the surface is one this package knows.
func (s Surface) Valid() bool {
	switch s {
	case SurfaceMastery, SurfaceReading, SurfacePractice, SurfaceUser:
		return true
	default:
		return false
	}
}

// Kind is what an observation is about — "preference", "goal", "difficulty".
//
// It is an open set on purpose, and the open half is not a hole: a kind nobody
// listed is not thereby safe, because `MEMORY_SENSITIVE_KINDS` is the deployment
// naming the kinds it *distrusts*, and everything else is admitted on the
// strength of the other two conditions. The one name this package fixes is the
// fourth path's (KindUserStated): "the user said so" is not a topic a surface
// chooses, it is the route the observation came in by.
type Kind string

// KindUserStated is the kind of an observation the user wrote down themselves.
// It is the one kind that skips the gate (§2.7), and it is a constant rather
// than a string literal because three places compare against it.
const KindUserStated Kind = "user_stated"

// Confirmer says which of the ways into L2 a fact came through.
type Confirmer string

const (
	// ConfirmerGate is the gate: confidence, corroboration across surfaces, and
	// not a sensitive kind (§2.7).
	ConfirmerGate Confirmer = "gate"
	// ConfirmerUser is the user's own word, written down or stated directly.
	ConfirmerUser Confirmer = "user"
)

// Valid reports whether the confirmer is one of the two.
func (c Confirmer) Valid() bool {
	switch c {
	case ConfirmerGate, ConfirmerUser:
		return true
	default:
		return false
	}
}

// Status is where a fact stands.
//
// Three values and not two, because the two ways a fact stops being in force are
// not one state: Retracted is the user taking it back, which Restore undoes, and
// Superseded is a dedupe merging it into another fact, which nothing undoes
// because the claim still stands — just in one row instead of two (§2.14). A
// deleted fact would be neither, and that is exactly why there is no such value:
// MEM-02 asks for actions with a record, not for rows that vanish (cleanup is
// the one exception, and it is a purge with a preview).
type Status string

const (
	// StatusActive is a fact in force.
	StatusActive Status = "active"
	// StatusRetracted is a fact the user took back. Its observations stay in L1.
	StatusRetracted Status = "retracted"
	// StatusSuperseded is a fact that dedupe merged into another one.
	StatusSuperseded Status = "superseded"
)

// Valid reports whether the status is one of the three.
func (s Status) Valid() bool {
	switch s {
	case StatusActive, StatusRetracted, StatusSuperseded:
		return true
	default:
		return false
	}
}

// Fingerprint is what makes two statements the same statement.
//
// It is the SHA-256 of the statement with runs of whitespace folded to one space
// and the ends trimmed. Nothing else is folded: not case, not punctuation. The
// trade is V3's `question_id` trade, taken again on purpose — the two mistakes
// are not symmetric. Treating two statements as different when they are the same
// costs one duplicate fact, which dedupe merges and the user can see. Treating
// two as the same when they are different silently merges "I prefer tea" with "I
// prefer coffee", and a merged fact cannot be unmerged because the observation
// that distinguished them was never recorded as separate.
//
// Folding whitespace is worth its own line because it is the case that actually
// happens: the same sentence arrives from a model with a newline where the last
// one had a space.
func Fingerprint(statement string) string {
	sum := sha256.Sum256([]byte(normalizeStatement(statement)))
	return hex.EncodeToString(sum[:])
}

func normalizeStatement(statement string) string {
	return strings.Join(strings.Fields(statement), " ")
}

// Trace is one observation as it is stored: L1.
//
// There is no confidence column and no `Scope` here, and their absence is the
// layer's meaning rather than an omission. A trace records *that* something was
// said, from where, and in which conversation — a confidence is a claim made at
// one moment by one surface, and where a claim is settled is L2: the fact keeps
// the confidence it was admitted at. The scope a fact belongs to is likewise the
// fact's; an observation that stays in L1 never had one (§3).
//
// SessionID and TurnID are the conversation the observation was made in. They
// are not foreign keys: deleting a conversation must not destroy what was
// observed in it. TurnID is also what stops one turn from corroborating itself —
// two statements in one turn are one voice, however many traces they made.
type Trace struct {
	ID        string
	OwnerID   int64
	Surface   Surface
	Kind      Kind
	SessionID string
	TurnID    string
	Statement string
	// Fingerprint is derived from the statement (Fingerprint). It is stored
	// because it is what the gate reads: the query "what has this account already
	// heard that says the same thing" is an index lookup on it.
	Fingerprint string
	// Payload is the observation's own JSON, kept verbatim. It is where a surface
	// puts what it saw beyond the sentence — a score, a source id, a locator.
	Payload   []byte
	CreatedAt time.Time
}

// Observation is one thing a surface noticed, handed to Observe.
//
// It is the request and not the row, which is why it is a second type: it
// carries the confidence the surface claimed for it (a claim that is spent on
// the decision and is not stored) and the scope it would belong to if it became
// a fact. Both belong to the act of handing the observation in.
type Observation struct {
	OwnerID int64
	Surface Surface
	Kind    Kind
	// SessionID and TurnID may be empty: an observation made outside any
	// conversation is one no turn can be said to have repeated.
	SessionID string
	TurnID    string
	Statement string
	// Confidence is the number the surface reports, 0–100. The gate compares it
	// against MEMORY_CONFIDENCE_THRESHOLD, and it is the first thing §2.7 says
	// about a model's output: the model talking about itself, which is why it is
	// the weakest of the three conditions and not the strongest.
	Confidence int
	// Scope is where the fact this observation may become belongs in the
	// synthesis — a grouping the surface chooses, or "" for none. It is spent on
	// admission: an observation that stays in L1 has no scope.
	Scope   string
	Payload []byte
}

// writtenByUser reports whether this observation is the user writing a fact
// down themselves — the fourth path of §2.7.
//
// Both halves are required and neither is the route alone. The surface is the
// half a model cannot forge: a capability's surface is a constant in the package
// that wrote it, while the kind is part of what a model reported. A gate that
// took KindUserStated as the route would be a gate that a prompt could open by
// saying the user had said something, and the whole of MEM-03 is that a model's
// say-so is not the user's.
func (o Observation) writtenByUser() bool {
	return o.Surface == SurfaceUser && o.Kind == KindUserStated
}

// Validate reports whether the observation is fit to store.
func (o Observation) Validate() error {
	if o.OwnerID <= 0 {
		return fmt.Errorf("%w: the owner is %d", ErrInvalidRequest, o.OwnerID)
	}
	if !o.Surface.Valid() {
		return fmt.Errorf("%w: %q is not a surface", ErrInvalidRequest, o.Surface)
	}
	if strings.TrimSpace(string(o.Kind)) == "" {
		return fmt.Errorf("%w: the kind is blank", ErrInvalidRequest)
	}
	if strings.TrimSpace(o.Statement) == "" {
		return fmt.Errorf("%w: the statement is blank", ErrInvalidRequest)
	}
	if o.Confidence < 0 || o.Confidence > 100 {
		return fmt.Errorf("%w: confidence %d is not between 0 and 100", ErrInvalidRequest, o.Confidence)
	}
	if len(o.Payload) > 0 && !json.Valid(o.Payload) {
		return fmt.Errorf("%w: the payload is not JSON", ErrInvalidRequest)
	}
	return nil
}

// Observed is what Observe did with one observation.
//
// The three states it can report are the three §4 puts in a turn's
// `metadata.memory`: observed is one per call, admitted is the ones that became
// (or joined) a fact, and proposed is the rest — the observations that are
// sitting in L1 waiting for corroboration, which is the state MEM-03 says the
// interface has to be able to show.
type Observed struct {
	// TraceID is the L1 row. It is written whatever happened next: hearing
	// something and not believing it is still hearing it.
	TraceID string
	// FactID is the fact this observation is now evidence for — the one it was
	// admitted as, or the one that already stood and it joined. Empty when the
	// observation stayed in L1.
	FactID string
	// Admitted is true when the observation is evidence for a fact.
	Admitted bool
	// Proposed is Admitted's negation, spelled out because a caller counting
	// `observed / admitted / proposed` reads better than `!Admitted`, and because
	// "waiting for corroboration" is a state and not an absence.
	Proposed bool
}

// Fact is one thing the system is prepared to act on: L2.
type Fact struct {
	ID      string
	OwnerID int64
	// Surface is where the observation that admitted it came from.
	Surface Surface
	// Scope is the grouping the admitting observation named, or "" for none.
	Scope     string
	Statement string
	// Fingerprint is recomputed whenever the statement changes, so that a fact
	// edited into a duplicate is a duplicate the next dedupe finds.
	Fingerprint string
	// Confidence is the number the admitting observation reported, kept as it was
	// reported so that what the gate decided can still be explained.
	Confidence int
	Status     Status
	// Sensitive is true when the kind it was admitted under is one of the
	// deployment's sensitive kinds. A sensitive kind never passes the gate, so a
	// sensitive fact is one the user wrote (§2.7) — and marking it is how the
	// reading surfaces know to treat it as one.
	Sensitive bool
	// ConfirmedBy is the route it came in by.
	ConfirmedBy Confirmer
	// Version is bumped by every write. It is what a write that read the fact
	// first has to match, so that two writers cannot both be right.
	Version int32
	// CreatedAt is when it was admitted. UpdatedAt is when it last changed, and
	// is the zero time while it never has. RetractTime is when it was taken back,
	// and is the zero time while it is in force — restoring clears it, which is
	// what makes "retracted" a state rather than a mark.
	CreatedAt   time.Time
	UpdatedAt   time.Time
	RetractTime time.Time
}

// InForce reports whether the fact is one a reading surface should show.
func (f Fact) InForce() bool {
	return f.Status == StatusActive
}

// FactSource is one piece of evidence for one fact (MEM-01).
//
// The pair is the identity — one observation can support two facts and one fact
// is admitted on several observations — so there is no id of its own. This type
// exists for the evidence chain read and for the merge dedupe performs; the
// adapter writes rows rather than calling the domain back.
type FactSource struct {
	FactID    string
	TraceID   string
	CreatedAt time.Time
}

// FactWithEvidence is a fact and the L1 observations it rests on.
//
// The two are read in one call because the question they answer is one question:
// "why does the system think this?" A fact whose evidence were a second request
// away would be a fact the interface shows as an assertion.
type FactWithEvidence struct {
	Fact   Fact
	Traces []Trace
}

// FactGroup is several facts that say the same thing, oldest first.
//
// It is what a dedupe reads and what its report is built from, which is why the
// order is part of the type: the oldest fact of a group is the one the others
// fold into, and a group whose order was whatever the engine chose would merge
// into a different fact each time it was asked.
type FactGroup struct {
	Fingerprint string
	Facts       []Fact
}

// TraceQuery is what a listing of L1 takes: one account's observations, and
// optionally one surface's, one kind's, or one stretch of time (§2.14).
//
// A zero Since or Until is unbounded, and a zero Limit is all of them. The zero
// values mean "no filter" rather than "none" because a query with no filter is
// the ordinary way to ask a question about an account's whole memory, and the
// sizes involved here are one person's.
type TraceQuery struct {
	OwnerID int64
	// Fingerprint is what the gate reads by. It is a filter like the others and
	// exists here rather than as a method of its own because it is the same read.
	Fingerprint string
	Surface     Surface
	Kind        Kind
	Since       time.Time
	Until       time.Time
	Limit       int
}

// FactQuery is a listing of L2: one account's facts, optionally of the given
// statuses and surface.
type FactQuery struct {
	OwnerID int64
	// Statuses filters the read; empty means every status. It is a slice because
	// the honest question is "what is in force", which is one status, against
	// "what have I ever had", which is three.
	Statuses []Status
	Surface  Surface
	Limit    int
}
