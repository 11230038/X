package usage

// The quota, without a database.
//
// What is pinned here is the arithmetic of the two periods and the shape of the
// refusal: which window is asked about, what a zero limit does, the call that
// exactly reaches the limit against the one a call below it, and the three
// answers an account can get — yes, a refusal, and a read that failed, which is
// neither. That the count itself comes off the real table is
// repository_db_test.go's, where the rows are real rows.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// ledgerStub is the usage table as a quota reaches it. It answers with what the
// case told it to, and it writes down every window it was asked about, because
// half of these cases are about the question rather than about the answer.
//
// The zero value answers an empty window, which is what a case that is about the
// limit needs and not about what has been used.
type ledgerStub struct {
	answer func(Query) (Report, error)
	asked  []Query
}

func (s *ledgerStub) Read(_ context.Context, query Query) (Report, error) {
	s.asked = append(s.asked, query)
	if s.answer == nil {
		return Report{}, nil
	}
	return s.answer(query)
}

// usedToday is a ledger that has already recorded what the case wants counted:
// calls that came back, calls that did not, and the tokens the first group cost.
func usedToday(calls, unfinished, tokens int64) *ledgerStub {
	return &ledgerStub{answer: func(Query) (Report, error) {
		return Report{
			Finished:   Totals{Calls: calls, TotalTokens: tokens},
			Unfinished: unfinished,
		}, nil
	}}
}

// frozen is a clock that does not move, so that "which day is this" is a
// question the case answers rather than waits for.
func frozen(at time.Time) func() time.Time { return func() time.Time { return at } }

// at builds the quota the cases in this file run against, with the clock they
// named.
func at(t *testing.T, now time.Time, limits Limits, reader Reader) *Quota {
	t.Helper()
	quota, err := NewQuota(reader, limits)
	if err != nil {
		t.Fatalf("NewQuota() error = %v", err)
	}
	quota.now = frozen(now)
	return quota
}

// freeQuota is the gate the recorder's own cases pass: no limits at all, so
// nothing is ever refused and nothing is ever read.
func freeQuota(t *testing.T) *Quota {
	t.Helper()
	return at(t, time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC), Limits{}, &ledgerStub{})
}

// noon is the moment most cases here happen at, far enough from either boundary
// that a case about a boundary has to say so.
var noon = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

// TestAZeroLimitIsNoLimitAtAll: §23.5 第 25 条 has not chosen a number, so the
// default deployment must charge nobody. The proof is not that a call is
// allowed — a quota that read the table and found nothing used would allow it
// too — but that the table is never asked: a zero limit is a deployment saying
// this period is not bounded, not a bound of zero that happens to pass.
func TestAZeroLimitIsNoLimitAtAll(t *testing.T) {
	ledger := &ledgerStub{answer: func(Query) (Report, error) {
		t.Error("a limit of zero read the table")
		return Report{}, nil
	}}
	quota := at(t, noon, Limits{}, ledger)

	for range 1000 {
		if err := quota.Allow(context.Background(), 7); err != nil {
			t.Fatalf("Allow() error = %v, want every call allowed", err)
		}
	}
	if len(ledger.asked) != 0 {
		t.Errorf("the ledger was asked %d questions, want none", len(ledger.asked))
	}
}

// TestTheDayIsTheUTCDay: the window is the calendar day, and it is the UTC one.
// The moment below is the evening of the 14th in +08:00 and the afternoon of the
// 13th in UTC, so a window built out of the clock's own zone would count a
// different day's calls than the one the answer names.
func TestTheDayIsTheUTCDay(t *testing.T) {
	east := time.FixedZone("+08:00", 8*60*60)
	ledger := &ledgerStub{}
	quota := at(t, time.Date(2026, 10, 14, 0, 30, 0, 0, east), Limits{DailyCalls: 5}, ledger)

	if err := quota.Allow(context.Background(), 7); err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	want := Query{
		OwnerID: 7,
		Since:   time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC),
		Until:   time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC),
	}
	if len(ledger.asked) != 1 || ledger.asked[0] != want {
		t.Errorf("the day was read as %+v, want %+v", ledger.asked, want)
	}
}

// TestTheMonthIsTheCalendarMonth: the end of the window is the first of the next
// month whatever the length of this one, which is what lets a refusal name a
// moment a client can print. February is the case that catches a window built by
// adding thirty days, and December catches one built by adding a month to the
// year's last.
func TestTheMonthIsTheCalendarMonth(t *testing.T) {
	ledger := &ledgerStub{}
	quota := at(t, time.Date(2026, 12, 15, 9, 0, 0, 0, time.UTC), Limits{MonthlyTokens: 100}, ledger)

	if err := quota.Allow(context.Background(), 7); err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	want := Query{
		OwnerID: 7,
		Since:   time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		Until:   time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	if len(ledger.asked) != 1 || ledger.asked[0] != want {
		t.Errorf("the month was read as %+v, want %+v", ledger.asked, want)
	}
}

// TestTheCallThatExactlyReachesTheLimitIsTheLastOne: the boundary, both sides of
// it. A limit of two allows the call that takes the account to two and refuses
// the one after it — "at the limit" is spent, not one short of it — and the
// counts the refusal carries are the ones it was decided on.
func TestTheCallThatExactlyReachesTheLimitIsTheLastOne(t *testing.T) {
	quota := at(t, noon, Limits{DailyCalls: 2}, usedToday(1, 0, 0))
	if err := quota.Allow(context.Background(), 7); err != nil {
		t.Fatalf("Allow() error = %v, want the second call of two allowed", err)
	}

	quota = at(t, noon, Limits{DailyCalls: 2}, usedToday(2, 0, 0))
	err := quota.Allow(context.Background(), 7)

	var exceeded *Exceeded
	if !errors.As(err, &exceeded) {
		t.Fatalf("Allow() error = %v, want a refusal", err)
	}
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("errors.Is(%v, ErrQuotaExceeded) = false, want true", err)
	}
	if exceeded.Item != ItemDailyCalls || exceeded.Used != 2 || exceeded.Limit != 2 {
		t.Errorf("refusal = %+v, want two of two daily calls", exceeded)
	}
	if want := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC); !exceeded.ResetsAt.Equal(want) {
		t.Errorf("ResetsAt = %v, want %v", exceeded.ResetsAt, want)
	}
	// §7: the refusal says which item and when it recovers, and this is the
	// sentence a person reads when there is nobody to draw it for them.
	for _, want := range []string{ItemDailyCalls, "2 of 2", "2026-10-15T00:00:00Z"} {
		if !strings.Contains(exceeded.Error(), want) {
			t.Errorf("Error() = %q, want it to carry %q", exceeded.Error(), want)
		}
	}
}

// TestTheUnfinishedCallsCountTowardsTheDay: a call that did not come back was
// still a call that was made. Counting only the calls that finished would let an
// account make as many calls as it liked by never waiting for one, which is the
// failure the limit exists to prevent and the one an account would find first.
func TestTheUnfinishedCallsCountTowardsTheDay(t *testing.T) {
	quota := at(t, noon, Limits{DailyCalls: 2}, usedToday(1, 1, 0))

	var exceeded *Exceeded
	if err := quota.Allow(context.Background(), 7); !errors.As(err, &exceeded) {
		t.Fatalf("Allow() error = %v, want a refusal", err)
	}
	if exceeded.Used != 2 {
		t.Errorf("Used = %d, want the call that never came back counted too", exceeded.Used)
	}
}

// TestTheTokensAreTheFinishedCallss: the month counts tokens, and a call that is
// still open has none — it has a row and no usage, and the day's limit, which
// counts rows, is what stops it from running at all.
func TestTheTokensAreTheFinishedCalls(t *testing.T) {
	allowed := at(t, noon, Limits{MonthlyTokens: 100}, usedToday(3, 2, 99))
	if err := allowed.Allow(context.Background(), 7); err != nil {
		t.Fatalf("Allow() error = %v, want the month's last call allowed", err)
	}

	refused := at(t, noon, Limits{MonthlyTokens: 100}, usedToday(3, 2, 100))
	err := refused.Allow(context.Background(), 7)

	var exceeded *Exceeded
	if !errors.As(err, &exceeded) {
		t.Fatalf("Allow() error = %v, want a refusal", err)
	}
	if exceeded.Item != ItemMonthlyTokens || exceeded.Used != 100 || exceeded.Limit != 100 {
		t.Errorf("refusal = %+v, want a hundred of a hundred tokens", exceeded)
	}
	if want := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC); !exceeded.ResetsAt.Equal(want) {
		t.Errorf("ResetsAt = %v, want the end of the month %v", exceeded.ResetsAt, want)
	}
}

// TestTheDayIsAnsweredBeforeTheMonth: when both are spent the answer is the
// day's, because it is the one with the sooner way out — a client told to come
// back tomorrow has been told something true, and told more than it would have
// been by the month. The month is not even read once the day has refused, which
// is the same statement made about the work.
func TestTheDayIsAnsweredBeforeTheMonth(t *testing.T) {
	quota := at(t, noon, Limits{DailyCalls: 2, MonthlyTokens: 10}, usedToday(5, 0, 500))

	var exceeded *Exceeded
	if err := quota.Allow(context.Background(), 7); !errors.As(err, &exceeded) {
		t.Fatalf("Allow() error = %v, want a refusal", err)
	}
	if exceeded.Item != ItemDailyCalls {
		t.Errorf("Item = %q, want %q", exceeded.Item, ItemDailyCalls)
	}
	if want := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC); !exceeded.ResetsAt.Equal(want) {
		t.Errorf("ResetsAt = %v, want the sooner of the two, %v", exceeded.ResetsAt, want)
	}
	if asked := quota.reader.(*ledgerStub).asked; len(asked) != 1 {
		t.Errorf("the ledger was asked %d questions, want only the day's", len(asked))
	}
}

// TestAReadThatFailedIsNotARefusal is the third answer. The call is still not
// made — a table nobody can read is not an allowance anybody can spend — but the
// account has done nothing and must not be told that it has: the failure comes
// back as itself, and the surfaces answer it as a failure of the service rather
// than as a limit.
func TestAReadThatFailedIsNotARefusal(t *testing.T) {
	broken := errors.New("the database is gone")
	quota := at(t, noon, Limits{DailyCalls: 2},
		&ledgerStub{answer: func(Query) (Report, error) { return Report{}, broken }})

	err := quota.Allow(context.Background(), 7)
	if !errors.Is(err, broken) {
		t.Fatalf("Allow() error = %v, want the read's own failure in it", err)
	}
	if errors.Is(err, ErrQuotaExceeded) {
		t.Error("a table that could not be read was reported as a limit the account reached")
	}
	var exceeded *Exceeded
	if errors.As(err, &exceeded) {
		t.Errorf("Allow() error = %v, want no refusal", err)
	}
}

// TestTheQuotaNeedsAReaderAndAnAccount: the two things a quota cannot answer
// without. A reader is refused at construction, where it is a deployment that
// does not start; an account is refused per call, where it is a call that named
// nobody and must not be charged to account zero — which every anonymous call
// would share, turning a per-account quota into a global one.
func TestTheQuotaNeedsAReaderAndAnAccount(t *testing.T) {
	if _, err := NewQuota(nil, Limits{DailyCalls: 1}); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("NewQuota(nil) error = %v, want %v", err, ErrNotConfigured)
	}

	ledger := &ledgerStub{}
	quota := at(t, noon, Limits{DailyCalls: 1}, ledger)
	if err := quota.Allow(context.Background(), 0); !errors.Is(err, ErrUnattributed) {
		t.Errorf("Allow(0) error = %v, want %v", err, ErrUnattributed)
	}
	if len(ledger.asked) != 0 {
		t.Error("a quota about nobody's calls read the table")
	}
}
