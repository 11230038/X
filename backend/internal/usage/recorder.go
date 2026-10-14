package usage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"backend/internal/llm"
)

// callIDPrefix marks a usage row's id. It is the same shape every other id in
// this repository has, and it is a plain random value rather than a counter or
// a hash of the request: `call_id` is the primary key, which is what makes
// §17.3's "deduplicate by call id" free — there is nothing to deduplicate when
// the id cannot repeat.
const callIDPrefix = "call_"

// Recorder is the decorator: an llm.Provider with the usage table behind it.
//
// It is one value wrapped around one provider, which is the whole of §2.7's
// structural claim. Nothing prevents a second provider from existing somewhere
// that is not this one, and a call made through that one would leave no row —
// which is why there is exactly one place that builds a provider, and why the
// test that says so uses an undecorated provider to prove the difference.
type Recorder struct {
	provider llm.Provider
	store    Store
	quota    *Quota
	name     string
	logger   *slog.Logger
	// now and newID are the two things a test cannot otherwise pin: a clock that
	// moves when the test says so, and an id that says which call it is. They
	// are options rather than interfaces because there is nothing else about
	// time or naming this package needs.
	now   func() time.Time
	newID func() (string, error)
}

// Options configures a recorder.
type Options struct {
	// Provider is the configured provider's name — the one written on every row
	// and aggregated by. It is required and comes from configuration rather than
	// from the provider itself, because no OpenAI-compatible client is told
	// which service it is talking to.
	Provider string
	// Quota is asked before every call, and it is required. A recorder built
	// without one is a recorder whose calls nobody is charged for, which is
	// precisely the arrangement §2.7 exists to make impossible, and the mistake
	// would be invisible from outside: the calls work, the answers are right and
	// the numbers are small.
	Quota *Quota
	// Logger receives the one failure this recorder does not turn into a failed
	// call: the second half of a write that could not be stored. It may be nil.
	Logger *slog.Logger
	// Now and NewID default to the clock and to a random id.
	Now   func() time.Time
	NewID func() (string, error)
}

// New wraps a provider in the usage record.
func New(provider llm.Provider, store Store, options Options) (*Recorder, error) {
	if provider == nil {
		return nil, fmt.Errorf("usage: a recorder needs a provider")
	}
	if store == nil {
		return nil, fmt.Errorf("usage: a recorder needs a store")
	}
	if options.Quota == nil {
		return nil, fmt.Errorf("usage: a recorder needs a quota")
	}
	name := strings.TrimSpace(options.Provider)
	if name == "" {
		return nil, fmt.Errorf("usage: a recorder needs the provider's name")
	}
	recorder := &Recorder{
		provider: provider,
		store:    store,
		quota:    options.Quota,
		name:     name,
		logger:   options.Logger,
		now:      options.Now,
		newID:    options.NewID,
	}
	if recorder.now == nil {
		recorder.now = func() time.Time { return time.Now().UTC() }
	}
	if recorder.newID == nil {
		recorder.newID = newCallID
	}
	return recorder, nil
}

// Stream runs one completion and writes both halves of its record.
//
// It is also the one gate every call passes: the account's quota is asked here
// and nowhere else, so a call site cannot forget to ask and a call site cannot
// ask twice.
// The first half is written before the provider is called, and a failure to
// write it stops the call: a call that cannot be recorded is not made, because
// the alternative is a model call that happened and left nothing behind, which
// is the failure this whole arrangement exists to make impossible.
//
// The second half is not symmetric. By the time it is written the answer is in
// hand and already paid for, so a failure to store the numbers is logged and the
// answer is returned: throwing away a completion because the row could not be
// updated would charge the account twice for one answer. The row is left open,
// which reads as a call that did not come back — a known and stated difference
// between what happened and what the table says, and the lesser of the two.
func (r *Recorder) Stream(
	ctx context.Context, req llm.Request, onDelta func(llm.Delta) error,
) (llm.Response, error) {
	if r == nil || r.provider == nil || r.store == nil {
		return llm.Response{}, ErrNotConfigured
	}
	call, err := attributed(ctx)
	if err != nil {
		return llm.Response{}, err
	}

	// The quota is asked here: after the account is known, and before anything
	// at all is written or sent. Nothing about this call exists yet — no id, no
	// row, no request — so a refusal leaves no trace of a call that did not
	// happen (§8 第 10 条), and the next call an account is allowed looks
	// exactly like its first. A failure to read the table is not a refusal: it
	// stops the call too, because the alternative is spending an allowance
	// nobody could count.
	if err := r.quota.Allow(ctx, call.OwnerID); err != nil {
		return llm.Response{}, err
	}

	callID, err := r.newID()
	if err != nil {
		return llm.Response{}, fmt.Errorf("usage: could not name a call: %w", err)
	}
	started := r.now()
	if err := r.store.Open(ctx, OpenCall{
		CallID:    callID,
		Call:      call,
		Provider:  r.name,
		Model:     req.Model,
		StartedAt: started,
	}); err != nil {
		return llm.Response{}, fmt.Errorf("usage: could not open the record of a call: %w", err)
	}

	// The first fragment is what the time to first token is measured to, and it
	// is taken here rather than from the provider's own accounting: a provider
	// that reported it would be reporting a number this code would have to trust
	// anyway, and one that does not would leave the column empty.
	var first time.Time
	response, callErr := r.provider.Stream(ctx, req, func(delta llm.Delta) error {
		if first.IsZero() {
			first = r.now()
		}
		return onDelta(delta)
	})
	finished := r.now()

	if err := r.store.Complete(ctx, callID, completionOf(started, finished, first, response, callErr, ctx)); err != nil {
		if r.logger != nil {
			r.logger.ErrorContext(ctx, "could not complete the record of a model call",
				"call_id", callID, "source", call.Source, "error", err)
		}
	}
	return response, callErr
}

// attributed is the identity a call would be recorded under, or the refusal
// that stops one.
func attributed(ctx context.Context) (Call, error) {
	call, ok := CallFrom(ctx)
	if !ok {
		return Call{}, ErrUnattributed
	}
	if call.OwnerID <= 0 {
		return Call{}, fmt.Errorf("%w: this call named no account", ErrUnattributed)
	}
	call.Source = strings.TrimSpace(call.Source)
	if call.Source == "" {
		return Call{}, fmt.Errorf("%w: this call named no source", ErrUnattributed)
	}
	return call, nil
}

// completionOf reads the call's outcome off how it ended.
//
// A call that ended because the caller's context did is the caller's own
// cancellation coming back, whichever error the provider wrapped it in, and it
// is told apart from a provider failure for the reason the turn runtime tells
// them apart: only one of the two is worth retrying, and only one of them is
// about the model.
func completionOf(
	started, finished, first time.Time,
	response llm.Response,
	callErr error,
	ctx context.Context,
) Completion {
	outcome := OutcomeOK
	if callErr != nil {
		outcome = OutcomeError
		if ctx.Err() != nil {
			outcome = OutcomeCancelled
		}
	}
	var ttft time.Duration
	if !first.IsZero() {
		ttft = first.Sub(started)
	}
	return Completion{
		FinishedAt:   finished,
		Usage:        response.Usage,
		DurationMs:   finished.Sub(started).Milliseconds(),
		TTFTMs:       ttft.Milliseconds(),
		Outcome:      outcome,
		FinishReason: response.FinishReason,
	}
}

// newCallID makes one call's id.
func newCallID() (string, error) {
	var buffer [16]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return "", err
	}
	return callIDPrefix + hex.EncodeToString(buffer[:]), nil
}

var _ llm.Provider = (*Recorder)(nil)
