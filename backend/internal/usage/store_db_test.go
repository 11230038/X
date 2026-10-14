package usage

// The reading side of the usage table, against a real seekdb.
//
// What these cases are here for is the half of the contract that no fake can
// speak for: a SELECT over a nullable column, a SUM that MySQL hands back as a
// DECIMAL, a window on a DATETIME, an aggregate that has to add up. The one
// claim that matters most is the first case below — the numbers come from the
// columns and never from usage_json (§2.8) — and it is checked the only way it
// can be: by writing a row whose two copies of the numbers disagree, which the
// recorder (writing both from one value) can never produce.
//
// The rows are written by hand rather than through GORMStore, for that reason
// and for one more: the columns a report reads have to be set to values the test
// chose, including the ones a recorder's own rules would not let it write.
//
// Nothing here calls t.Parallel: one database per package, emptied at each test.

import (
	"context"
	"testing"
	"time"

	"backend/internal/data"
	"backend/internal/data/testdb"
)

// Two more moments than repository_db_test.go defines, both about the window:
// one past the end of the window the cases use, and one on the next day.
var (
	atEleven = time.Date(2026, 10, 1, 11, 0, 0, 0, time.UTC)
	nextDay  = time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
)

// callRow is one row of the usage table as a case wants it. Every column the
// report reads is here, and so is usage_json, because the first case needs to
// write the two out of step.
type callRow struct {
	CallID     string
	OwnerID    *int64
	SessionID  *string
	Source     string
	Provider   string
	Model      string
	StartedAt  time.Time
	FinishedAt *time.Time
	Outcome    Outcome
	Prompt     int64
	Completion int64
	Total      int64
	Duration   int64
	TTFT       int64
	UsageJSON  string
}

// insertRow writes one row, leaving out nothing: a case that wants a column
// unset says so, rather than relying on a default that a migration could change.
func insertRow(t *testing.T, resource *data.Resource, row callRow) {
	t.Helper()
	if row.UsageJSON == "" {
		row.UsageJSON = "{}"
	}
	testdb.Exec(t, resource,
		`INSERT INTO llm_calls (call_id, started_at, session_id, turn_id, source, usage_json,
			owner_id, provider, model, prompt_tokens, completion_tokens, total_tokens,
			duration_ms, ttft_ms, finished_at, outcome)
		 VALUES (?, ?, ?, NULL, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.CallID, row.StartedAt, row.SessionID, row.Source, row.UsageJSON,
		row.OwnerID, row.Provider, row.Model, row.Prompt, row.Completion, row.Total,
		row.Duration, row.TTFT, row.FinishedAt, string(row.Outcome))
}

// aFinishedCall is the ordinary row: a call that came back, with numbers on it.
func aFinishedCall(callID string, owner int64, at time.Time) callRow {
	return callRow{
		CallID: callID, OwnerID: &owner, Source: "chat",
		Provider: "deepseek", Model: "deepseek-chat", StartedAt: at,
		FinishedAt: finishedAt(at), Outcome: OutcomeOK,
		Prompt: 100, Completion: 40, Total: 140, Duration: 20000, TTFT: 300,
	}
}

// anOpenCall is a call that has not come back: no finish, no outcome, and none
// of the numbers a call only has once it produced something (§2.7).
func anOpenCall(callID string, owner int64, at time.Time) callRow {
	row := aFinishedCall(callID, owner, at)
	row.FinishedAt, row.Outcome = nil, ""
	row.Prompt, row.Completion, row.Total, row.Duration, row.TTFT = 0, 0, 0, 0, 0
	return row
}

func finishedAt(at time.Time) *time.Time {
	done := at.Add(30 * time.Second)
	return &done
}

func newDBLedger(t *testing.T) (*GORMLedger, *data.Resource, int64) {
	t.Helper()
	_, resource, owner := newDBStore(t)
	return NewGORMLedger(resource.GORM()), resource, owner
}

// read is one report, failing rather than answering zero if it cannot be read:
// a case below asserts on the numbers, and a read that did not happen would
// leave them at zero in a way that reads like an empty window.
func read(t *testing.T, ledger *GORMLedger, query Query) Report {
	t.Helper()
	report, err := ledger.Read(context.Background(), query)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	return report
}

// §2.8: the same numbers are in two places, and only the columns are read back.
// This is the case that says so, and the only way to write it is to plant a row
// whose two copies disagree: a reader that took usage_json would answer 999 and
// look perfectly healthy.
func TestDBTheReportReadsTheColumnsAndNotTheJSON(t *testing.T) {
	ledger, resource, owner := newDBLedger(t)
	row := aFinishedCall("call_columns", owner, atNine)
	row.UsageJSON = `{"prompt_tokens": 999, "completion_tokens": 999, "total_tokens": 999, ` +
		`"duration_ms": 999, "ttft_ms": 999, "finish_reason": "stop"}`
	insertRow(t, resource, row)

	report := read(t, ledger, Query{OwnerID: owner})

	want := Totals{
		Calls: 1, Ok: 1, PromptTokens: 100, CompletionTokens: 40, TotalTokens: 140,
		DurationMs: 20000, TTFTMs: 300,
	}
	if report.Finished != want {
		t.Errorf("Finished = %+v, want the columns, %+v", report.Finished, want)
	}
	if report.Unfinished != 0 {
		t.Errorf("Unfinished = %d, want none", report.Unfinished)
	}
}

// The whole window adds up, and it adds up by hand: the numbers below are the
// ones §7.2 asks somebody to check with a pencil, and a case is a pencil that
// keeps working.
func TestDBTheReportAddsUpTheWindow(t *testing.T) {
	ledger, resource, owner := newDBLedger(t)

	ok := aFinishedCall("call_ok", owner, atNine)
	failed := aFinishedCall("call_error", owner, atNine30)
	failed.Outcome, failed.Prompt, failed.Completion, failed.Total, failed.TTFT = OutcomeError, 20, 0, 20, 120
	cancelled := aFinishedCall("call_cancelled", owner, atTen)
	cancelled.Outcome, cancelled.Prompt, cancelled.Completion, cancelled.Total = OutcomeCancelled, 10, 5, 15
	cancelled.Duration, cancelled.TTFT = 5000, 100
	for _, row := range []callRow{ok, failed, cancelled} {
		insertRow(t, resource, row)
	}

	report := read(t, ledger, Query{OwnerID: owner})

	want := Totals{
		Calls: 3, Ok: 1, Error: 1, Cancelled: 1,
		// 100+20+10, 40+0+5, 140+20+15.
		PromptTokens: 130, CompletionTokens: 45, TotalTokens: 175,
		// 20000+20000+5000, 300+120+100.
		DurationMs: 45000, TTFTMs: 520,
	}
	if report.Finished != want {
		t.Errorf("Finished = %+v, want %+v", report.Finished, want)
	}
}

// An empty window is a row of zeros rather than a row of NULLs: a total nobody
// has spent yet is zero, and NULL is a value a client would have to special-case.
// It is also the case that proves the COALESCE and the CAST do something, since
// an aggregate over no rows is where MySQL hands back a DECIMAL.
func TestDBAnEmptyWindowIsZeroAndNotNothing(t *testing.T) {
	ledger, _, owner := newDBLedger(t)

	report := read(t, ledger, Query{OwnerID: owner})

	if report.Finished != (Totals{}) {
		t.Errorf("Finished = %+v, want zeros", report.Finished)
	}
	if report.Unfinished != 0 {
		t.Errorf("Unfinished = %d, want none", report.Unfinished)
	}
}

// A call that did not come back is counted and not summed (§2.7). It is the
// state the two-part write exists to produce, and the report has a place for it
// rather than treating it as a row that is not there.
func TestDBACallThatDidNotComeBackIsCountedAndNotSummed(t *testing.T) {
	ledger, resource, owner := newDBLedger(t)
	insertRow(t, resource, aFinishedCall("call_done", owner, atNine))
	insertRow(t, resource, anOpenCall("call_open", owner, atTen))

	report := read(t, ledger, Query{OwnerID: owner})

	if report.Finished.Calls != 1 || report.Finished.TotalTokens != 140 {
		t.Errorf("Finished = %+v, want only the call that came back", report.Finished)
	}
	if report.Unfinished != 1 {
		t.Errorf("Unfinished = %d, want the call that has not come back", report.Unfinished)
	}
}

// The window is on started_at rather than on finished_at, and the open call is
// the reason: it has no finish to filter on, and it is the call a report most
// needs to be able to count. A window that read finished_at would silently drop
// every call that hung.
func TestDBTheWindowSeesACallThatNeverFinished(t *testing.T) {
	ledger, resource, owner := newDBLedger(t)
	insertRow(t, resource, anOpenCall("call_open", owner, atNine))

	report := read(t, ledger, Query{
		OwnerID: owner,
		Since:   atNine.Add(-time.Hour),
		Until:   atNine.Add(time.Hour),
	})

	if report.Unfinished != 1 {
		t.Errorf("Unfinished = %d, want the call that hung inside the window", report.Unfinished)
	}
}

// The window's two ends: since is inclusive and until is exclusive, so two
// windows meeting at a moment do not both count the call made in it. The three
// rows below sit on the two ends and in the middle, which is the whole of what
// the boundaries mean.
func TestDBTheWindowStartsAtSinceAndStopsBeforeUntil(t *testing.T) {
	ledger, resource, owner := newDBLedger(t)
	insertRow(t, resource, aFinishedCall("call_before", owner, atNine))
	insertRow(t, resource, aFinishedCall("call_until", owner, atTen))
	insertRow(t, resource, aFinishedCall("call_after", owner, atEleven))

	report := read(t, ledger, Query{OwnerID: owner, Since: atNine, Until: atTen})

	if report.Finished.Calls != 1 {
		t.Errorf("Calls = %d, want only the call at since", report.Finished.Calls)
	}
	// And the window that starts where the first one stopped holds the other
	// call, which is what "exclusive" is for.
	second := read(t, ledger, Query{OwnerID: owner, Since: atTen, Until: atEleven})
	if second.Finished.Calls != 1 {
		t.Errorf("the next window has %d calls, want the one at its since", second.Finished.Calls)
	}
}

// A report is one account's (§2.9), and the two rows below are the whole of the
// claim: B's calls are not in A's report, and there is no parameter anywhere
// that would put them there.
func TestDBTheReportIsTheCallersAndNobodyElses(t *testing.T) {
	ledger, resource, alice := newDBLedger(t)
	bob := createAccount(t, resource, "bob")
	insertRow(t, resource, aFinishedCall("call_alice_1", alice, atNine))
	insertRow(t, resource, aFinishedCall("call_alice_2", alice, atNine))
	insertRow(t, resource, aFinishedCall("call_bob", bob, atNine))

	hers := read(t, ledger, Query{OwnerID: alice})
	his := read(t, ledger, Query{OwnerID: bob})

	if hers.Finished.Calls != 2 {
		t.Errorf("A's report has %d calls, want her own two", hers.Finished.Calls)
	}
	if his.Finished.Calls != 1 {
		t.Errorf("B's report has %d calls, want his own one", his.Finished.Calls)
	}
}

// The administrator is the account that also sees the rows belonging to nobody:
// the ones written before V5 (migration 00014), which somebody has to be able to
// find. That is the whole of the widening — it is not another account's calls,
// and the row belonging to B is the case that says so.
func TestDBTheAdministratorAlsoSeesTheRowsThatBelongToNobody(t *testing.T) {
	ledger, resource, alice := newDBLedger(t)
	bob := createAccount(t, resource, "bob")
	insertRow(t, resource, aFinishedCall("call_owned", alice, atNine))
	legacy := aFinishedCall("call_legacy", alice, atNine)
	legacy.OwnerID = nil
	insertRow(t, resource, legacy)
	insertRow(t, resource, aFinishedCall("call_bob", bob, atNine))

	plain := read(t, ledger, Query{OwnerID: alice})
	wide := read(t, ledger, Query{OwnerID: alice, Admin: true})

	if plain.Finished.Calls != 1 {
		t.Errorf("an account's own report has %d calls, want only the row it owns", plain.Finished.Calls)
	}
	if wide.Finished.Calls != 2 {
		t.Errorf("the administrator's report has %d calls, want its own and the unowned one",
			wide.Finished.Calls)
	}
}

// The four narrowings choose rows and nothing else: they do not change what a
// call is worth, and a call outside the narrowing is left out rather than
// counted as zero.
func TestDBTheNarrowingsChooseTheRows(t *testing.T) {
	ledger, resource, owner := newDBLedger(t)
	chat := aFinishedCall("call_chat", owner, atNine)
	judged := aFinishedCall("call_judged", owner, atNine)
	judged.Source, judged.Model, judged.Provider = "practice.judge", "deepseek-reasoner", "other"
	session := "ses_1"
	chat.SessionID = &session
	for _, row := range []callRow{chat, judged} {
		insertRow(t, resource, row)
	}

	for _, tt := range []struct {
		name  string
		query Query
		want  int64
	}{
		{name: "by source", query: Query{OwnerID: owner, Source: "practice.judge"}, want: 1},
		{name: "by model", query: Query{OwnerID: owner, Model: "deepseek-chat"}, want: 1},
		{name: "by provider", query: Query{OwnerID: owner, Provider: "other"}, want: 1},
		{name: "by session", query: Query{OwnerID: owner, SessionID: "ses_1"}, want: 1},
		{name: "by a value nothing has", query: Query{OwnerID: owner, Source: "mastery.judge"}, want: 0},
		{name: "by two at once", query: Query{OwnerID: owner, Source: "chat", Model: "deepseek-reasoner"}, want: 0},
		{name: "by nothing", query: Query{OwnerID: owner}, want: 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := read(t, ledger, tt.query).Finished.Calls; got != tt.want {
				t.Errorf("Calls = %d, want %d", got, tt.want)
			}
		})
	}
}

// The status route's number: how many calls in the whole table did not come
// back. It is the one read here that is not narrowed to an account, and the case
// says so by writing two accounts' calls and expecting both — the number is a
// property of the process, and a caller cannot name whose calls it covers.
func TestDBTheCountOfCallsThatDidNotComeBackIsOverTheWholeTable(t *testing.T) {
	ledger, resource, alice := newDBLedger(t)
	bob := createAccount(t, resource, "bob")
	insertRow(t, resource, anOpenCall("call_open_alice", alice, atNine))
	insertRow(t, resource, anOpenCall("call_open_bob", bob, atNine))
	insertRow(t, resource, aFinishedCall("call_done", alice, atNine))

	count, err := ledger.Unfinished(context.Background())
	if err != nil {
		t.Fatalf("Unfinished() error = %v", err)
	}

	if count != 2 {
		t.Errorf("Unfinished() = %d, want both accounts' calls that have not come back", count)
	}
}

// Prune is a window and not an editing tool: it takes a moment and removes
// everything that started before it, and there is no way to name the rows it
// removes. A window that removed nothing is not an error.
func TestDBPruneRemovesTheCallsBeforeTheMomentAndOnlyThose(t *testing.T) {
	ledger, resource, owner := newDBLedger(t)
	insertRow(t, resource, aFinishedCall("call_old", owner, atNine))
	insertRow(t, resource, anOpenCall("call_open_old", owner, atNine))
	insertRow(t, resource, aFinishedCall("call_new", owner, nextDay))

	removed, err := ledger.Prune(context.Background(), atTen)
	if err != nil {
		t.Fatalf("Prune() error = %v", err)
	}

	if removed != 2 {
		t.Errorf("Prune() = %d, want the two calls that started before the moment", removed)
	}
	left := read(t, ledger, Query{OwnerID: owner})
	if left.Finished.Calls != 1 || left.Unfinished != 0 {
		t.Errorf("after the prune the report is %+v, want only the call after the moment", left)
	}
	// And pruning again removes nothing, which is an answer rather than a failure.
	again, err := ledger.Prune(context.Background(), atTen)
	if err != nil {
		t.Fatalf("a second Prune() error = %v", err)
	}
	if again != 0 {
		t.Errorf("a second Prune() = %d, want nothing left to remove", again)
	}
}

// A ledger with no handle is a wiring mistake, and the one thing it must not do
// is answer that nobody has spent anything.
func TestAnUnwiredLedgerRefusesRatherThanAnsweringZero(t *testing.T) {
	var ledger *GORMLedger
	ctx := context.Background()

	if _, err := ledger.Read(ctx, Query{OwnerID: 1}); err == nil {
		t.Error("an unwired ledger answered a report")
	}
	if _, err := ledger.Prune(ctx, time.Now()); err == nil {
		t.Error("an unwired ledger reported that it pruned")
	}
}
