package usage

// The quota, against a real seekdb.
//
// What quota_test.go cannot speak for is the one claim the whole of §2.9 rests
// on: that the number an account is refused on is the number in the table. The
// windows below are a day and a month apart from each other, so a query that
// forgot its bound — or that took the wrong column, or that summed another
// account's rows in — answers a number that is wrong in a way no fake would
// ever produce: the rows are real rows, written at instants the test chose.
//
// The last case is the acceptance criterion itself (§8 第 10 条) run end to end:
// two calls allowed, the third refused, and no row for the third.
//
// Nothing here calls t.Parallel: one database per package, emptied at each test.

import (
	"context"
	"errors"
	"testing"
	"time"

	"backend/internal/data"
)

// lastMonth is a moment in September: outside the day and the month the cases
// happen in, and inside the table.
var lastMonth = time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)

// newDBQuota is the quota over the real ledger, with the clock pinned to a moment
// the case chose. The rows are written at instants a day or a month away from it,
// and a quota reading the wall clock could not tell them apart from one that
// ignored its window entirely.
//
// The ledger comes back with it because a case that asks the same window a second
// question — the same rows with a different limit — has to be over the same
// table: building a second quota would start by emptying it.
func newDBQuota(t *testing.T, now time.Time, limits Limits) (*Quota, *GORMLedger, *data.Resource, int64) {
	t.Helper()
	ledger, resource, owner := newDBLedger(t)
	quota := quotaOver(t, ledger, now, limits)
	return quota, ledger, resource, owner
}

// quotaOver is one quota over a ledger a case already has.
func quotaOver(t *testing.T, ledger *GORMLedger, now time.Time, limits Limits) *Quota {
	t.Helper()
	quota, err := NewQuota(ledger, limits)
	if err != nil {
		t.Fatalf("NewQuota() error = %v", err)
	}
	quota.now = frozen(now)
	return quota
}

// TestDBTheDayCountsTodayAndThisAccountAndDoesNotWaitForACallToComeBack is the
// three narrowings of the day's count in one window of real rows: yesterday's
// calls are not today's, another account's calls are not this one's, and a call
// that has not come back was still a call that was made.
func TestDBTheDayCountsTodayAndThisAccountAndDoesNotWaitForACallToComeBack(t *testing.T) {
	quota, ledger, resource, owner := newDBQuota(t, atTen, Limits{DailyCalls: 3})
	stranger := createAccount(t, resource, "bob")

	insertRow(t, resource, aFinishedCall("call_a", owner, atNine))
	insertRow(t, resource, aFinishedCall("call_b", owner, atNine30))
	insertRow(t, resource, anOpenCall("call_c", owner, atNine30))
	// Yesterday, and today for somebody else: two rows that a query missing
	// either narrowing would count.
	insertRow(t, resource, aFinishedCall("call_yesterday", owner, lastMonth))
	insertRow(t, resource, aFinishedCall("call_stranger", stranger, atNine))

	// Three of a limit of three is spent, so the fourth call is the refused one
	// — which is the row above that must not have been counted.
	var exceeded *Exceeded
	err := quota.Allow(context.Background(), owner)
	if !errors.As(err, &exceeded) {
		t.Fatalf("Allow() error = %v, want a refusal at three counted calls", err)
	}
	if exceeded.Used != 3 || exceeded.Limit != 3 {
		t.Errorf("refusal = %+v, want three of three", exceeded)
	}
	if want := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC); !exceeded.ResetsAt.Equal(want) {
		t.Errorf("ResetsAt = %v, want the end of the day %v", exceeded.ResetsAt, want)
	}

	// And the fourth of a limit of four is allowed, which is the same window
	// read with room in it: five rows exist and three of them are the account's
	// day, so a count that missed a narrowing would refuse this one too.
	roomy := quotaOver(t, ledger, atTen, Limits{DailyCalls: 4})
	if err := roomy.Allow(context.Background(), owner); err != nil {
		t.Errorf("Allow() error = %v, want the fourth call allowed", err)
	}
}

// TestDBTheMonthCountsThisMonthsTokensAndOnlyTheFinishedCalls: the month's number
// is tokens, so a call that has not come back contributes none — it has no usage
// yet — and last month's calls contribute none of theirs however many they were.
func TestDBTheMonthCountsThisMonthsTokensAndOnlyTheFinishedCalls(t *testing.T) {
	quota, ledger, resource, owner := newDBQuota(t, atTen, Limits{MonthlyTokens: 141})
	insertRow(t, resource, aFinishedCall("call_a", owner, atNine)) // 140 tokens
	insertRow(t, resource, anOpenCall("call_b", owner, atNine30))  // no tokens yet
	insertRow(t, resource, aFinishedCall("call_old", owner, lastMonth))
	insertRow(t, resource, aFinishedCall("call_older", owner, lastMonth))

	if err := quota.Allow(context.Background(), owner); err != nil {
		t.Fatalf("Allow() error = %v, want the month's 140 tokens to be under 141", err)
	}

	// The same rows at the exact limit: 140 tokens spent is the last call, and
	// the numbers the refusal carries are the ones the table answered with.
	tight := quotaOver(t, ledger, atTen, Limits{MonthlyTokens: 140})
	var exceeded *Exceeded
	err := tight.Allow(context.Background(), owner)
	if !errors.As(err, &exceeded) {
		t.Fatalf("Allow() error = %v, want a refusal at exactly the limit", err)
	}
	if exceeded.Item != ItemMonthlyTokens || exceeded.Used != 140 {
		t.Errorf("refusal = %+v, want the month's tokens and %q", exceeded, ItemMonthlyTokens)
	}
	if want := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC); !exceeded.ResetsAt.Equal(want) {
		t.Errorf("ResetsAt = %v, want the end of the month %v", exceeded.ResetsAt, want)
	}
}

// TestDBTheThirdCallOfTwoIsRefusedAndLeavesNoRow is §8 第 10 条 end to end: the
// recorder over the real store, the quota over the real ledger, and a deployment
// that allows two calls a day. The third call is not made, and — the half of the
// criterion that only a real table can answer — the refusal left nothing behind,
// so the two rows that exist are the two calls that happened.
//
// It ends by setting the limit to zero and calling again, which is the last
// clause of the same criterion: a deployment that stops charging must recover at
// once, without waiting for the day the refused account was told about.
func TestDBTheThirdCallOfTwoIsRefusedAndLeavesNoRow(t *testing.T) {
	ledger, resource, owner := newDBLedger(t)
	quota, err := NewQuota(ledger, Limits{DailyCalls: 2})
	if err != nil {
		t.Fatalf("NewQuota() error = %v", err)
	}
	quota.now = frozen(atTen)

	recorder, err := New(&scriptProvider{response: aResponse()}, NewGORMStore(resource.GORM()), Options{
		Provider: "deepseek",
		Quota:    quota,
		Now:      frozen(atTen),
		NewID:    naming("call_1", "call_2", "call_3"),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := WithCall(context.Background(), Call{OwnerID: owner, Source: SourceTurnTitle})

	for call := 1; call <= 2; call++ {
		if _, err := recorder.Stream(ctx, aRequest(), collect(new([]string))); err != nil {
			t.Fatalf("call %d of two was refused: %v", call, err)
		}
	}
	var exceeded *Exceeded
	if _, err := recorder.Stream(ctx, aRequest(), collect(new([]string))); !errors.As(err, &exceeded) {
		t.Fatalf("the third call's error = %v, want a refusal", err)
	}
	if exceeded.Item != ItemDailyCalls {
		t.Errorf("the third call was refused over %q, want %q", exceeded.Item, ItemDailyCalls)
	}

	if rows := countCalls(t, resource, owner); rows != 2 {
		t.Errorf("the table holds %d rows, want the two calls that happened", rows)
	}

	// The same deployment with the limit taken away: the two rows are still
	// there, and the call goes through, because zero is no limit at all.
	free, err := NewQuota(ledger, Limits{})
	if err != nil {
		t.Fatalf("NewQuota() error = %v", err)
	}
	free.now = frozen(atTen)
	relaxed, err := New(&scriptProvider{response: aResponse()}, NewGORMStore(resource.GORM()), Options{
		Provider: "deepseek", Quota: free, Now: frozen(atTen), NewID: naming("call_4"),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := relaxed.Stream(ctx, aRequest(), collect(new([]string))); err != nil {
		t.Errorf("a limit of zero refused a call: %v", err)
	}
	if rows := countCalls(t, resource, owner); rows != 3 {
		t.Errorf("the table holds %d rows, want the three calls that happened", rows)
	}
}

// countCalls is what the table says, asked of the table rather than of the store
// that wrote it.
func countCalls(t *testing.T, resource *data.Resource, owner int64) int64 {
	t.Helper()
	var row struct {
		Rows int64 `gorm:"column:rows"`
	}
	if err := resource.GORM().Raw(
		"SELECT COUNT(*) AS rows FROM llm_calls WHERE owner_id = ?", owner).Scan(&row).Error; err != nil {
		t.Fatalf("count the calls: %v", err)
	}
	return row.Rows
}
