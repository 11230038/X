package usage

// The quota (§2.9).
//
// The quota asks "how much has this account used", and the answer already
// exists: it is the sum over the rows of llm_calls with this owner in the
// period. So there is no quotas table and no counter to update after a call —
// a second source of truth for a number this table already holds is the most
// expensive kind of debt, and here the drift would be systematic rather than
// occasional: the rows are written one call at a time and a tally is
// accumulated, so one crash or one replay moves the tally and leaves the rows
// alone.
//
// What that buys is the thing §2.9 is actually about: the number a client is
// refused on and the number it reads on its own usage screen come from the same
// read. A quota computed by some second query would disagree with the screen
// exactly when somebody was close to the limit and looking at both.

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// The two items an account is measured on, and the names a refusal uses for
// them.
//
// A name rather than a sentence, because "which item ran out" has a reader that
// is not a person: the client draws the period it may come back in, and it can
// only do that for a name it knows. The sentence is what a person who has
// neither reads.
const (
	// ItemDailyCalls is how many calls an account may start in one UTC day. It
	// counts calls rather than tokens because it is the bound on how often the
	// product may be asked to do something, whatever it does.
	ItemDailyCalls = "daily_calls"
	// ItemMonthlyTokens is how many tokens an account may spend in one UTC
	// calendar month. It is the bound on how much the product may spend, and it
	// is a different question from the one above on purpose: a month of long
	// conversations and a day of many short ones are the two ways to be
	// expensive.
	ItemMonthlyTokens = "monthly_tokens"
)

// ErrQuotaExceeded is the refusal's identity, and it is what a caller wraps and
// checks for when it does not need which item ran out. The refusal itself is
// *Exceeded, which carries the three facts a client needs.
var ErrQuotaExceeded = errors.New("usage: this account has used up its quota")

// Limits is what one account may spend in a period.
//
// A zero is no limit at all rather than a limit of zero, and that is what a
// deployment starts from: §23.5 第 25 条 has not chosen a number yet, and a
// build that refused everybody until somebody chose one would be a worse
// default than one that charges nobody.
type Limits struct {
	// DailyCalls and MonthlyTokens are the two numbers above, and either may be
	// zero, which means that period is not bounded.
	DailyCalls    int64
	MonthlyTokens int64
}

// Reader is the usage table as the quota reaches it: one window of one
// account's calls. It is the Ledger's own read, narrowed to what a count needs.
//
// It is deliberately not a second query with a COUNT in it. The reasoning is
// the file's opening: two reads of one number are two answers, and the one the
// account is refused on is the one nobody can check.
type Reader interface {
	Read(ctx context.Context, query Query) (Report, error)
}

// Quota decides whether one more call may be made.
type Quota struct {
	reader Reader
	limits Limits
	// now is the clock the periods are read off. It is a field rather than a
	// call to time.Now because the interesting cases are the boundaries — the
	// call that exactly reaches the limit, and the one a moment after — and
	// those are not reachable by waiting.
	now func() time.Time
}

// NewQuota builds the quota over a reader and the deployment's limits.
//
// A reader is required. A quota that could not read the table would have to
// answer from nothing, and both answers it could give are wrong: refusing every
// call turns a database that is down into an account that has spent its
// allowance, and allowing every call is the one thing this type exists to
// prevent. The mistake belongs at startup, where it is a refused deployment.
func NewQuota(reader Reader, limits Limits) (*Quota, error) {
	if reader == nil {
		return nil, ErrNotConfigured
	}
	return &Quota{
		reader: reader,
		limits: limits,
		now:    func() time.Time { return time.Now().UTC() },
	}, nil
}

// Allow answers whether the account may make one more call.
//
// A nil error is a yes and the only yes; every no is an *Exceeded, and a read
// that failed is neither: it is an error that stops the call without pretending
// to be a limit the account reached. The three are told apart because the
// client is told about the second and not about the third.
func (q *Quota) Allow(ctx context.Context, ownerID int64) error {
	if q == nil || q.reader == nil {
		return ErrNotConfigured
	}
	if ownerID <= 0 {
		return fmt.Errorf("%w: a quota is about one account's calls", ErrUnattributed)
	}
	now := q.now()

	// The day is asked about before the month, and when both are exhausted the
	// answer is the day's: it is the one with the sooner way out, and a client
	// told "tomorrow" is not also told "next month".
	if q.limits.DailyCalls > 0 {
		start, end := dayWindow(now)
		report, err := q.reader.Read(ctx, Query{OwnerID: ownerID, Since: start, Until: end})
		if err != nil {
			return fmt.Errorf("usage: could not count today's calls: %w", err)
		}
		// Every row that started today counts, finished or not. A call that did
		// not come back was still a call that was made and still cost what it
		// cost, and an account that hung twenty calls would otherwise be
		// allowed twenty more.
		used := report.Finished.Calls + report.Unfinished
		if used >= q.limits.DailyCalls {
			return &Exceeded{
				Item: ItemDailyCalls, Limit: q.limits.DailyCalls, Used: used, ResetsAt: end,
			}
		}
	}

	if q.limits.MonthlyTokens > 0 {
		start, end := monthWindow(now)
		report, err := q.reader.Read(ctx, Query{OwnerID: ownerID, Since: start, Until: end})
		if err != nil {
			return fmt.Errorf("usage: could not count this month's tokens: %w", err)
		}
		// Only the calls that came back have tokens to count: a call that is
		// still open has no usage yet, and it has already been counted against
		// the day, which is the limit that stops it from running at all.
		if used := report.Finished.TotalTokens; used >= q.limits.MonthlyTokens {
			return &Exceeded{
				Item: ItemMonthlyTokens, Limit: q.limits.MonthlyTokens, Used: used, ResetsAt: end,
			}
		}
	}
	return nil
}

// Exceeded is the refusal: which limit ran out, where the account stands, and
// when the count starts over.
//
// It is a type rather than a sentence (§2.9) because the three facts have three
// readers: a client draws where to look at its usage and when it may come back,
// an operator reads which number is too low, and the sentence is for whoever has
// neither.
type Exceeded struct {
	// Item is which limit this is — one of the two names above.
	Item string
	// Limit is what the deployment allows and Used is what the account reached,
	// in the item's own unit.
	Limit int64
	Used  int64
	// ResetsAt is when the period ends and the count starts from zero. It is a
	// moment rather than a duration on purpose: "in three hours" is a sentence
	// that goes stale between the answer and the reading of it, and the client
	// holding it has no way to know when it was written.
	ResetsAt time.Time
}

func (e *Exceeded) Error() string {
	if e == nil {
		return ErrQuotaExceeded.Error()
	}
	return fmt.Sprintf("%s: %s reached %d of %d, and it starts over at %s",
		ErrQuotaExceeded, e.Item, e.Used, e.Limit, e.ResetsAt.UTC().Format(time.RFC3339))
}

// Unwrap makes errors.Is(err, ErrQuotaExceeded) true for a refusal, without
// every caller having to name the type.
func (e *Exceeded) Unwrap() error { return ErrQuotaExceeded }

// Refused marks this failure as one that stopped work before it began, as
// opposed to work that began and did not finish.
//
// It exists for one boundary. The reading registry runs an extension and reports
// everything that went wrong with it as a skipped outcome rather than a failed
// request, and that is right for an extension that could not answer: one broken
// panel must not fail the page. It is wrong for an account that may not make the
// call at all — a refusal is about the request, not about the extension — so the
// refusal says so here and the registry lets it through. The interface is
// unexported at both ends and found structurally, the way the turn runtime finds
// a failure that knows whether retrying it could ever work.
func (e *Exceeded) Refused() bool { return true }

// dayWindow is the UTC calendar day the moment falls in: the start inclusive,
// the end exclusive.
//
// A calendar day rather than "the last 24 hours", and the difference is the
// whole reason a client can be told when to come back: a rolling window has no
// moment at which it clears, so the only honest answer it supports is "wait a
// while". It is also what makes the account's own usage screen agree with the
// quota, since "what did I use today" is a question that already has this
// answer.
//
// The day is the UTC one rather than the zone the deployment runs in: the
// moment an account may come back is answered by whichever instance served the
// request, and two instances in different zones would give it two answers.
// Everything recorded on a row is a UTC instant already, so the window the
// count is taken over is the same statement.
func dayWindow(now time.Time) (time.Time, time.Time) {
	now = now.UTC()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return start, start.AddDate(0, 0, 1)
}

// monthWindow is the UTC calendar month the moment falls in, on the same terms.
//
// The day of the month is set to one rather than counted back from, so the end
// is the first of the next month at midnight whatever length the month is.
func monthWindow(now time.Time) (time.Time, time.Time) {
	now = now.UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	return start, start.AddDate(0, 1, 0)
}

// The marker above, stated where the compiler checks it. Nothing in this
// repository names the interface — internal/reading declares its own copy so
// that the two packages do not have to know about each other — so this line is
// what keeps the method from being renamed away with nothing to notice.
var _ interface{ Refused() bool } = (*Exceeded)(nil)
