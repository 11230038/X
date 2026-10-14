package audit

// The vocabulary of the audit, and the rules about what one entry may say (§2.6).
//
// The shape here is the argument. §13.3 asks for an audit that records the
// actor, action, target, timestamp and result "but not passwords, tokens or the
// whole prompt", and the second half of that sentence is not a filter applied to
// the first — it is arranged so that there is nothing to filter. There is no
// field that holds a body of text: a target is a *pointer* (a kind and an id),
// the actor's name is a snapshot taken at write time, and the detail is a
// closed set of scalars each of which has to be a code.
//
// That is why Detail is a struct with named fields rather than a map. A map
// would accept a prompt the first time somebody passed one, and the entry would
// look perfectly well formed.

import (
	"errors"
	"fmt"
	"time"
)

// Actor is who the entry is about, with the name and role as they were when it
// was written.
//
// The snapshot is the point (§2.6): an audit answers "who was it *then*". An
// account renamed in March must still show its February name on February's
// entries, and an account deleted must leave entries that can still be read —
// which is why actor_id carries no foreign key in 00016.
type Actor struct {
	ID   int64
	Name string
	Role string
}

// Ref is a pointer into the product: what kind of thing, and which one.
//
// ID is an identifier and never a body. A session's entry says
// `session / ses_abc`, not the session; a library file's says
// `library_file / lib_abc`, not the file. Nothing downstream has to remember not
// to copy the material, because no column could hold it.
type Ref struct {
	Kind TargetKind `json:"kind"`
	ID   string     `json:"id"`
}

// TargetKind names what a Ref points at. It is a closed set for the same reason
// Action is: a target kind nobody can spell the same way twice is a kind nobody
// can narrow a read by, and the mistake is silent — the row is written, and the
// query that should have found it finds nothing.
type TargetKind string

const (
	// TargetAccount is an account as the thing that was acted on.
	TargetAccount TargetKind = "account"
	// TargetGrant is one of an account's per-item decisions.
	TargetGrant TargetKind = "grant"
	// TargetGuardianLink is a relationship between two accounts.
	TargetGuardianLink TargetKind = "guardian_link"
	// TargetGuardianPermission is one permission on a relationship.
	TargetGuardianPermission TargetKind = "guardian_permission"
	// TargetSession is a conversation.
	TargetSession TargetKind = "session"
	// TargetLibraryFile is a file in the library.
	TargetLibraryFile TargetKind = "library_file"
	// TargetWorkspaceJob is one move of a workspace in or out.
	TargetWorkspaceJob TargetKind = "workspace_job"
	// TargetSurface is an area of the product. It is what a refusal points at:
	// what was attempted was a route, and the route belongs to a surface.
	TargetSurface TargetKind = "surface"
)

// TargetKinds is every kind this build writes, in one place so that the check
// and the list cannot drift.
var TargetKinds = []TargetKind{
	TargetAccount, TargetGrant, TargetGuardianLink, TargetGuardianPermission,
	TargetSession, TargetLibraryFile, TargetWorkspaceJob, TargetSurface,
}

// KnownTargetKind reports whether a name is one this build writes.
func KnownTargetKind(kind TargetKind) bool { return contains(TargetKinds, kind) }

// Result is how it ended. It is the three the DDL's CHECK allows and no more.
type Result string

const (
	// ResultOK is an action that happened.
	ResultOK Result = "ok"
	// ResultDenied is an action that was refused. It is a result and not an
	// error, because "somebody asked and was told no" is the thing this table
	// exists to be able to answer.
	ResultDenied Result = "denied"
	// ResultFailed is an action that was attempted, was allowed, and did not
	// work. It is separate from Denied because the two are somebody else's
	// problem: a denial is a policy, and a failure is the process.
	ResultFailed Result = "failed"
)

// Results is every result, in the order 00016's CHECK lists them.
var Results = []Result{ResultOK, ResultDenied, ResultFailed}

// KnownResult reports whether a result is one the table accepts.
func KnownResult(result Result) bool { return contains(Results, result) }

// Detail is what an entry may say beyond its columns: ids, counts and reason
// codes, and nothing else (§2.6).
//
// Every field is a code or a number, and every code is checked to be shaped like
// one. What that buys is the refusal rather than the silence: an entry that
// tried to carry a sentence is *rejected*, so the attempt is visible, where a
// version that quietly dropped the field would let the caller believe the audit
// had recorded what it asked it to.
// The JSON tags are the column's shape and the wire's shape at once, and they
// are written once because they are one thing: what an entry may say does not
// change between being stored and being served, and two spellings of it would be
// two chances for a reader to look for a field that is not there.
type Detail struct {
	// Reason is the policy's reason code for a decision, e.g. baseline_closed.
	Reason string `json:"reason,omitempty"`
	// Layer is which of the four layers decided, e.g. guardian.
	Layer string `json:"layer,omitempty"`
	// Counts is what a bulk action reports, e.g. how many rows an import wrote.
	Counts map[string]int `json:"counts,omitempty"`
	// Items are the ids a bulk action touched, when naming them is the point.
	Items []Ref `json:"items,omitempty"`
}

// Empty reports whether there is nothing to write, which is how the column ends
// up NULL rather than `{}`.
func (d Detail) Empty() bool {
	return d.Reason == "" && d.Layer == "" && len(d.Counts) == 0 && len(d.Items) == 0
}

// Event is one row of audit_events.
type Event struct {
	ID        int64
	Actor     Actor
	Action    Action
	Target    Ref
	SubjectID int64 // 0 means no subject, which is written as NULL
	Result    Result
	Detail    Detail
	RequestID string
	CreatedAt time.Time
}

// Filter narrows a read. A zero field means "not about that".
//
// Every field is a single value rather than a list, and that is deliberate: the
// three questions asked of an audit are "what did this account do", "what
// happened to this account" and "what happened to this thing", each of which is
// one value. A list-valued filter is how an audit query becomes an index scan
// of the whole table, which on this table is the one thing it must never be.
type Filter struct {
	ActorID    int64
	SubjectID  int64
	Action     Action
	TargetKind TargetKind
	TargetID   string
	Since      time.Time
	Until      time.Time
	// Limit is how many entries to return. Zero means "as many as this
	// deployment serves in one page", which is a cap and not a default: there is
	// no number here that the code invented, and a caller asking for more than
	// the cap is refused rather than handed a shorter list than it asked for.
	Limit int
}

// Errors the audit refuses with.
var (
	// ErrUnknownAction is what an action outside the closed set is refused with.
	ErrUnknownAction = errors.New("audit: not an action this build records")
	// ErrUnknownResult is what a result the CHECK does not allow is refused
	// with. It cannot reach the database as an error — the CHECK would refuse
	// it — but reaching the database to find out is a failure at the wrong
	// layer, and one that leaves nothing to map to.
	ErrUnknownResult = errors.New("audit: not a result this table accepts")
	// ErrUnknownTargetKind is what a target kind outside the closed set is
	// refused with.
	ErrUnknownTargetKind = errors.New("audit: not a target this build records")
	// ErrNotACode is what a detail that carries free text is refused with. It is
	// the refusal §2.6 needs: the field table has nowhere to put a sentence, and
	// a value that looks like one is answered rather than dropped.
	ErrNotACode = errors.New("audit: the detail must be ids, counts and reason codes")
	// ErrPageTooLarge is what a read asking for more than one page is refused
	// with.
	ErrPageTooLarge = errors.New("audit: the page size exceeds this deployment's limit")
)

// codeMax is how long a code may be. It is the width of the narrowest column a
// code has to fit in (action and reason are VARCHAR(64) in 00016), so a value
// longer than this is a value that was never a code.
const codeMax = 64

// idMax is how long an identifier may be: the width of target_id.
const idMax = 128

// Validate refuses an entry this build may not write.
//
// It is exported because the repository must not be the only caller: a service
// that validated and a store that did not would be a store somebody writes to
// directly, and the check would hold exactly as long as everybody remembered it.
func (e Event) Validate() error {
	if !KnownAction(e.Action) {
		return fmt.Errorf("%w: %q", ErrUnknownAction, e.Action)
	}
	if !KnownResult(e.Result) {
		return fmt.Errorf("%w: %q", ErrUnknownResult, e.Result)
	}
	if !KnownTargetKind(e.Target.Kind) {
		return fmt.Errorf("%w: %q", ErrUnknownTargetKind, e.Target.Kind)
	}
	if !isIdentifier(e.Target.ID, idMax) {
		return fmt.Errorf("%w: the target id is not an identifier", ErrNotACode)
	}
	if err := e.Detail.validate(); err != nil {
		return err
	}
	return nil
}

// validate checks that every scalar in the detail is a code or a count.
func (d Detail) validate() error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{"reason", d.Reason},
		{"layer", d.Layer},
	} {
		if field.value != "" && !isCode(field.value) {
			return fmt.Errorf("%w: %s", ErrNotACode, field.name)
		}
	}
	for name, count := range d.Counts {
		if !isCode(name) {
			return fmt.Errorf("%w: a count is named by something that is not a code", ErrNotACode)
		}
		if count < 0 {
			return fmt.Errorf("%w: the count %s is negative", ErrNotACode, name)
		}
	}
	for _, item := range d.Items {
		if !KnownTargetKind(item.Kind) {
			return fmt.Errorf("%w: %q", ErrUnknownTargetKind, item.Kind)
		}
		if !isIdentifier(item.ID, idMax) {
			return fmt.Errorf("%w: an item id is not an identifier", ErrNotACode)
		}
	}
	return nil
}

// isCode reports whether a value is shaped like a code: lower-case words joined
// by the separators the reason codes and action names use, and nothing else.
//
// It is deliberately narrower than "no whitespace". A rule that refused only
// spaces would accept a prompt in a language that does not use them, and this
// check has to refuse the first attempt rather than the one that happens to
// contain an ASCII space.
func isCode(value string) bool {
	if value == "" || len(value) > codeMax {
		return false
	}
	for index := 0; index < len(value); index++ {
		if !isCodeByte(value[index]) {
			return false
		}
	}
	return true
}

func isCodeByte(character byte) bool {
	switch {
	case character >= 'a' && character <= 'z':
		return true
	case character >= '0' && character <= '9':
		return true
	}
	switch character {
	case '_', '.', '-', ':':
		return true
	}
	return false
}

// isIdentifier reports whether a value is shaped like one of this product's ids.
//
// It is wider than isCode because ids carry capital letters from time to time
// and are compared, never interpreted. It is still a shape and not a filter: a
// sentence fails it whichever language it is in, and an empty value passes it
// because half the item kinds of a grant are spelled with an empty id that means
// "every one of them" (§2.2).
func isIdentifier(value string, max int) bool {
	if len(value) > max {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9':
			continue
		}
		switch character {
		case '_', '.', '-', ':', '/', '@':
			continue
		}
		return false
	}
	return true
}

func contains[T comparable](values []T, wanted T) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
