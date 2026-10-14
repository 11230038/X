package usage

// The broken-down report, against a real seekdb.
//
// A breakdown is two GROUP BYs over the same window — one for the calls that
// came back and one for the calls that did not — merged in Go. What only a real
// database can say is what those two queries return: that a NULL session groups
// as one bucket rather than as a row with no name, that a day label comes out of
// the database already formatted, and that a bucket whose every call hung is one
// the first query never mentions and which the second one still produces.
//
// The claim the merge is built for is the last case's: the whole is the sum of
// the parts. It is structural — a report's totals are added up *from* the
// buckets — and it is checked anyway, over every breakdown, because "it cannot
// fail" is the kind of sentence worth one test that would notice if it did.

import (
	"context"
	"sort"
	"testing"

	"backend/internal/data"
)

// theWindow writes the four rows most cases below are built from.
//
// They are chosen so that no two of them agree on everything a report can break
// a window down by: a breakdown that read the wrong column would produce
// different buckets rather than the same ones by luck. The last call never came
// back, which is what makes it the interesting row — it is a bucket of its own
// in three of the five breakdowns, and each of those buckets has to appear with
// no totals at all.
func theWindow(t *testing.T, resource *data.Resource, owner int64) {
	t.Helper()
	first := "ses_1"
	second := "ses_2"

	chat := aFinishedCall("call_chat_ok", owner, atNine)
	chat.SessionID = &first
	chat.Source = "chat"

	failed := aFinishedCall("call_chat_error", owner, atNine)
	failed.SessionID = &first
	failed.Source, failed.Outcome = "chat", OutcomeError
	failed.Prompt, failed.Completion, failed.Total = 20, 0, 20

	judged := aFinishedCall("call_judged", owner, nextDay)
	judged.Source, judged.Model = "practice.judge", "deepseek-reasoner"
	judged.Prompt, judged.Completion, judged.Total = 10, 5, 15

	hung := anOpenCall("call_hung", owner, nextDay)
	hung.SessionID = &second
	hung.Source, hung.Provider, hung.Model = "mastery.judge", "other", "other-model"

	for _, row := range []callRow{chat, failed, judged, hung} {
		insertRow(t, resource, row)
	}
}

// labels is one bucket's key with its two numbers, for a comparison that reads
// like the report it is about.
type labels struct {
	Key        string
	Calls      int64
	Unfinished int64
}

func keysOf(groups []Group) []labels {
	listed := make([]labels, 0, len(groups))
	for _, group := range groups {
		listed = append(listed, labels{group.Key, group.Finished.Calls, group.Unfinished})
	}
	return listed
}

func TestDBABreakdownByModel(t *testing.T) {
	ledger, resource, owner := newDBLedger(t)
	theWindow(t, resource, owner)

	report := read(t, ledger, Query{OwnerID: owner, GroupBy: GroupByModel})

	want := []labels{
		{"deepseek-chat", 2, 0},
		{"deepseek-reasoner", 1, 0},
		{"other-model", 0, 1},
	}
	assertBuckets(t, report.Groups, want)
}

func TestDBABreakdownByProvider(t *testing.T) {
	ledger, resource, owner := newDBLedger(t)
	theWindow(t, resource, owner)

	report := read(t, ledger, Query{OwnerID: owner, GroupBy: GroupByProvider})

	assertBuckets(t, report.Groups, []labels{{"deepseek", 3, 0}, {"other", 0, 1}})
}

func TestDBABreakdownBySource(t *testing.T) {
	ledger, resource, owner := newDBLedger(t)
	theWindow(t, resource, owner)

	report := read(t, ledger, Query{OwnerID: owner, GroupBy: GroupBySource})

	assertBuckets(t, report.Groups, []labels{
		{"chat", 2, 0}, {"mastery.judge", 0, 1}, {"practice.judge", 1, 0},
	})
}

// A call made outside a conversation has no session (migration 00017), and the
// breakdown gives it one bucket with the empty key rather than a bucket nothing
// can name. Giving up NULL-versus-empty in a *label* is deliberate: the column
// keeps the distinction, and a label is not a count.
func TestDBABreakdownBySessionNamesTheConversationlessCallsWithNothing(t *testing.T) {
	ledger, resource, owner := newDBLedger(t)
	theWindow(t, resource, owner)

	report := read(t, ledger, Query{OwnerID: owner, GroupBy: GroupBySession})

	assertBuckets(t, report.Groups, []labels{
		{"", 1, 0}, {"ses_1", 2, 0}, {"ses_2", 0, 1},
	})
}

// The day is the day a call started, and the label is produced in SQL so that
// the report serves the label the database made rather than formatting it a
// second time here.
func TestDBABreakdownByDay(t *testing.T) {
	ledger, resource, owner := newDBLedger(t)
	theWindow(t, resource, owner)

	report := read(t, ledger, Query{OwnerID: owner, GroupBy: GroupByDay})

	assertBuckets(t, report.Groups, []labels{
		{"2026-10-01", 2, 0}, {"2026-10-02", 1, 1},
	})
}

// §7.2's hand-summed comparison, as a case: the whole is the sum of the parts.
// It is true by construction — the report's totals are the buckets added
// together — and this is what would fail the day somebody made it two queries
// that have to agree.
func TestDBTheWholeIsTheSumOfTheBuckets(t *testing.T) {
	for _, by := range GroupBys {
		t.Run(string(by), func(t *testing.T) {
			ledger, resource, owner := newDBLedger(t)
			theWindow(t, resource, owner)

			report := read(t, ledger, Query{OwnerID: owner, GroupBy: by})

			sum, unfinished := Totals{}, int64(0)
			for _, group := range report.Groups {
				sum = sum.add(group.Finished)
				unfinished += group.Unfinished
			}
			if sum != report.Finished {
				t.Errorf("the buckets add up to %+v and the report says %+v", sum, report.Finished)
			}
			if unfinished != report.Unfinished {
				t.Errorf("the buckets hold %d unfinished and the report says %d", unfinished, report.Unfinished)
			}
			// And the whole is the same answer the unbroken report gives, which
			// is the other half of "adding up": the breakdown is a way of reading
			// the window, not a different window.
			whole := read(t, ledger, Query{OwnerID: owner})
			if report.Finished != whole.Finished || report.Unfinished != whole.Unfinished {
				t.Errorf("the breakdown says %+v/%d and the window says %+v/%d",
					report.Finished, report.Unfinished, whole.Finished, whole.Unfinished)
			}
		})
	}
}

// A bucket whose every call hung is a bucket, and it is the case the second
// query exists for: the first one never mentions it, and dropping it would make
// the breakdown quietly shorter than the window it is over.
func TestDBABucketOfNothingButHungCallsIsStillABucket(t *testing.T) {
	ledger, resource, owner := newDBLedger(t)
	insertRow(t, resource, aFinishedCall("call_done", owner, atNine))
	for _, callID := range []string{"call_hung_1", "call_hung_2"} {
		hung := anOpenCall(callID, owner, atNine)
		hung.Model = "deepseek-reasoner"
		insertRow(t, resource, hung)
	}

	report := read(t, ledger, Query{OwnerID: owner, GroupBy: GroupByModel})

	assertBuckets(t, report.Groups, []labels{
		{"deepseek-chat", 1, 0},
		{"deepseek-reasoner", 0, 2},
	})
	// It is a bucket nothing but the second query knows about, and its count is
	// still part of the window's: a report whose breakdown was missing it would
	// add up to a smaller window than the one it says it is over.
	if report.Unfinished != 2 {
		t.Errorf("Unfinished = %d, want the two calls from the bucket only the second query has",
			report.Unfinished)
	}
}

// The buckets of every breakdown come back in one order, and it is the key's:
// the two queries are merged and the merged list is sorted, because neither
// query's order is the merged order — a bucket only the second one knows about
// is appended after one only the first one knew.
func TestDBTheBucketsComeBackInKeyOrder(t *testing.T) {
	ledger, resource, owner := newDBLedger(t)
	for _, model := range []string{"zebra", "alpha", "middle"} {
		row := aFinishedCall("call_"+model, owner, atNine)
		row.Model = model
		insertRow(t, resource, row)
	}
	// The one that only the second query knows about is the one that has to be
	// sorted into place rather than left where it was appended.
	hung := anOpenCall("call_hung", owner, atNine)
	hung.Model = "beta"
	insertRow(t, resource, hung)

	report := read(t, ledger, Query{OwnerID: owner, GroupBy: GroupByModel})

	var got []string
	for _, group := range report.Groups {
		got = append(got, group.Key)
	}
	if want := []string{"alpha", "beta", "middle", "zebra"}; !equalStrings(got, want) {
		t.Errorf("the buckets are %v, want %v", got, want)
	}
}

// A window with nothing in it is broken down into nothing, and the report says
// so with an empty list rather than with a missing field.
func TestDBAnEmptyWindowBreaksDownIntoNothing(t *testing.T) {
	ledger, _, owner := newDBLedger(t)

	report := read(t, ledger, Query{OwnerID: owner, GroupBy: GroupByModel})

	if len(report.Groups) != 0 {
		t.Errorf("groups = %+v, want none", report.Groups)
	}
	if report.Finished != (Totals{}) || report.Unfinished != 0 {
		t.Errorf("an empty window reported %+v/%d", report.Finished, report.Unfinished)
	}
}

// The narrowings apply to a breakdown exactly as they do to a whole window, and
// the buckets are read over the narrowed set rather than the whole table.
func TestDBABreakdownIsReadOverTheNarrowedWindow(t *testing.T) {
	ledger, resource, owner := newDBLedger(t)
	theWindow(t, resource, owner)

	report := read(t, ledger, Query{
		OwnerID: owner, GroupBy: GroupByModel, Source: "chat", Since: atNine, Until: atTen,
	})

	assertBuckets(t, report.Groups, []labels{{"deepseek-chat", 2, 0}})
}

// The breakdown is built in SQL and there is nothing to fall back on: a caller
// that named one this build does not serve gets a failure rather than a report
// over the whole window wearing the shape of a breakdown. The service refuses
// such a name first (service_test.go); this is the layer below saying the same
// thing in the only way it can.
func TestDBABreakdownNobodyNamedIsAFailureAndNotTheWholeWindow(t *testing.T) {
	ledger, resource, owner := newDBLedger(t)
	insertRow(t, resource, aFinishedCall("call_done", owner, atNine))

	_, err := ledger.Read(context.Background(), Query{OwnerID: owner, GroupBy: GroupBy("owner_id")})

	if err == nil {
		t.Fatal("a breakdown nobody serves was answered")
	}
}

// assertBuckets compares the buckets as keys and numbers, in the order they came
// back, so that a case says what it means rather than what Go printed.
func assertBuckets(t *testing.T, groups []Group, want []labels) {
	t.Helper()
	got := keysOf(groups)
	if len(got) != len(want) {
		t.Fatalf("buckets = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("bucket %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	// A last look at the order, in the failing case where the keys came back
	// shuffled and the comparison above reported every one of them.
	if !sort.SliceIsSorted(groups, func(i, j int) bool { return groups[i].Key < groups[j].Key }) {
		t.Errorf("the buckets are not in key order: %+v", got)
	}
}
