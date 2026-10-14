// Package usage records what the model calls cost (§2.7).
//
// There is nothing for the code that calls a model to remember. The recorder is
// an llm.Provider, and the real provider is wrapped inside it once, where the
// process is assembled, so "the call went to the model" and "there is a row
// about it" are one statement rather than two. The other arrangement — every
// caller writing its own row after it is done — fails in a way nothing reports:
// a caller that forgets is simply absent from the numbers, and the numbers then
// look like a quiet month.
//
// The row is written in two parts, and that is deliberate as well. The
// identifying half goes in before the request is sent, the numbers are filled
// in after it comes back, and a call that never came back is therefore a row
// with no finished_at — a state this table can be asked about. Written whole at
// the end, such a call would not be a row at all.
//
// The dependency runs one way: this package knows llm, and llm does not know it
// exists (§1.2).
package usage

import (
	"context"
	"errors"
	"strings"
	"time"

	"backend/internal/llm"
)

// Source names the part of the product a call belongs to. It is what §17.1
// aggregates by, so it is a vocabulary rather than a label: the same call site
// always writes the same word, and the words here are the ones the call sites
// outside a capability use.
//
// A turn's own calls are the exception — their source is the capability that
// ran, which a capability names for itself — and these are the calls the
// runtime makes around them and outside them.
const (
	SourceTurnTitle        = "turn.title"
	SourcePracticeJudge    = "practice.judge"
	SourceMasteryJudge     = "mastery.judge"
	SourceReadingExtension = "reading.extension"
)

// Call is one model call as the code making it knows it: on whose behalf, in
// which conversation, and from which part of the product.
//
// OwnerID and Source are required. A call with no account behind it would be a
// call §2.9's quota never charges anybody for, and one with no source could not
// be aggregated at all; the recorder refuses both rather than making the call
// and leaving it out of the record.
//
// SessionID and TurnID are not required. Judging a review and running a reading
// extension are calls made from a route that is not a conversation, and a
// conversation that is not there is stored as NULL rather than as an empty name
// (migration 00017).
type Call struct {
	OwnerID   int64
	SessionID string
	TurnID    string
	Source    string
}

// WithCall marks ctx as work done for an account, so that every model call made
// under it is recorded against that account.
//
// It belongs at the boundary where the account is known — the runtime that
// starts a turn, the service that takes a request — and not next to the call
// itself: a component deep enough to call a model is usually too deep to know
// whose session it is in.
func WithCall(ctx context.Context, call Call) context.Context {
	return context.WithValue(ctx, callContextKey{}, call)
}

// WithSource marks what the calls made under ctx are, keeping the account and
// the conversation the work already declared.
//
// It is for the call a component makes on its own behalf inside somebody else's
// work: naming a conversation is part of a turn, and running an extension is
// part of a mastery path, but neither is what the capability did, and a usage
// table that could not tell them apart could not answer what they cost.
func WithSource(ctx context.Context, source string) context.Context {
	call, _ := CallFrom(ctx)
	call.Source = strings.TrimSpace(source)
	return WithCall(ctx, call)
}

// CallFrom returns the call ctx carries.
func CallFrom(ctx context.Context) (Call, bool) {
	call, ok := ctx.Value(callContextKey{}).(Call)
	return call, ok
}

// callContextKey is unexported and empty, so that nothing outside this package
// can put a call into a context in the shape the recorder reads.
type callContextKey struct{}

// Outcome is how a call ended. It is the empty string while the call is open,
// and one of three words once it is not; the vocabulary is closed by a check
// constraint (migration 00015) for the same reason the audit's results are: an
// outcome nobody can enumerate is one nothing can count.
type Outcome string

// The outcomes a finished call can have.
const (
	OutcomeOK        Outcome = "ok"
	OutcomeError     Outcome = "error"
	OutcomeCancelled Outcome = "cancelled"
)

// OpenCall is the first half of the record: everything about a call that is
// known before it is made.
type OpenCall struct {
	CallID string
	Call   Call
	// Provider and Model are what was called. Model is the one from the request
	// rather than the one the provider answered with, because a provider that
	// substituted a model is not a thing any of them do, and the request is what
	// this code knows.
	Provider  string
	Model     string
	StartedAt time.Time
}

// Completion is the second half: what the call produced and what it cost.
type Completion struct {
	FinishedAt time.Time
	Usage      llm.Usage
	DurationMs int64
	TTFTMs     int64
	Outcome    Outcome
	// FinishReason is the provider's own word for why it stopped. It has no
	// column, and it is the reason usage_json is kept: a field an upstream
	// answers with that nothing had thought to index stays in the original
	// rather than being dropped (§2.8).
	FinishReason string
}

// Store is the usage table as the recorder reaches it: one row opened, and the
// same row completed.
//
// It is two methods rather than one "write" because the two writes are two
// moments, and the table's most interesting state — a call that was never
// finished — exists only between them.
type Store interface {
	Open(ctx context.Context, open OpenCall) error
	Complete(ctx context.Context, callID string, completion Completion) error
}

// ErrUnattributed is a model call with no account behind it, or with no source.
// The recorder refuses to make one: a call nobody would be charged for is
// exactly the failure §2.7 is written against, and it is invisible from the
// outside — the call works, the answer is right, and the numbers are small.
var ErrUnattributed = errors.New("usage: a model call has to be made on behalf of an account")

// ErrNotConfigured is a recorder with no provider or no store behind it, which
// is a wiring mistake rather than a call that should be made.
var ErrNotConfigured = errors.New("usage: the recorder is not configured")
