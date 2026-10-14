package usage

// The rules a report is checked against before it reaches SQL, and the one
// number configuration gives this half.
//
// These run over a ledger that records the queries it was handed instead of
// answering them, and that is what they are for: everything below is about what
// the service refused, what it passed on unchanged, and what it added to the
// answer on its way back. Whether the SQL then adds up is a question for a real
// database (store_db_test.go), and a fake ledger could not answer it.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeLedger is a ledger that answers what it is told to and remembers what it
// was asked. The two lists are kept apart from the answer on purpose: a case
// about a refusal is a case about the second list being empty.
type fakeLedger struct {
	report Report
	err    error
	asked  []Query
	pruned []time.Time
	count  int64
	// open is what the whole table's count of calls that did not come back
	// answers. It is a field of its own rather than the prune count, because the
	// two numbers are about different things.
	open int64
}

func (l *fakeLedger) Read(_ context.Context, query Query) (Report, error) {
	l.asked = append(l.asked, query)
	if l.err != nil {
		return Report{}, l.err
	}
	return l.report, nil
}

func (l *fakeLedger) Prune(_ context.Context, before time.Time) (int64, error) {
	l.pruned = append(l.pruned, before)
	return l.count, l.err
}

func (l *fakeLedger) Unfinished(context.Context) (int64, error) {
	return l.open, l.err
}

func newTestService(t *testing.T, ledger Ledger, retentionDays int) *Service {
	t.Helper()
	service, err := NewService(ledger, retentionDays)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

func anAccount() Query { return Query{OwnerID: 7} }

// A report is about one account's calls (§2.9), and that is the check that has
// to happen before the ledger sees anything: a query with no account behind it
// is not a narrower report, it is the whole table.
func TestServiceRefusesAReportAboutNobody(t *testing.T) {
	ledger := &fakeLedger{}
	service := newTestService(t, ledger, 365)

	_, err := service.Read(context.Background(), Query{})

	if !errors.Is(err, ErrNoAccount) {
		t.Fatalf("error = %v, want ErrNoAccount", err)
	}
	if len(ledger.asked) != 0 {
		t.Errorf("the ledger was asked %d times about nobody's calls", len(ledger.asked))
	}
}

// The breakdown is a closed set, and a name outside it is refused rather than
// dropped: a report that quietly broke the window down by nothing would be an
// answer to a question nobody asked, wearing the shape of the one they did.
func TestServiceRefusesABreakdownItDoesNotServe(t *testing.T) {
	for _, name := range []GroupBy{"owner_id", "call_id", "Model", "model ", "total_tokens"} {
		t.Run(string(name), func(t *testing.T) {
			ledger := &fakeLedger{}
			service := newTestService(t, ledger, 365)
			query := anAccount()
			query.GroupBy = name

			_, err := service.Read(context.Background(), query)

			if !errors.Is(err, ErrUnknownGroupBy) {
				t.Fatalf("error = %v, want ErrUnknownGroupBy", err)
			}
			if !strings.Contains(err.Error(), string(name)) {
				t.Errorf("error = %v, want it to name the breakdown it refused", err)
			}
			if len(ledger.asked) != 0 {
				t.Errorf("the ledger was asked to group by %q anyway", name)
			}
		})
	}
}

// Every breakdown this build claims to serve is one the ledger can group by, and
// the empty string is not among them: it is the report's own "no breakdown",
// which is a different thing from a breakdown whose name nobody wrote.
func TestServiceKnowsExactlyTheBreakdownsItServes(t *testing.T) {
	want := map[GroupBy]bool{
		GroupByModel: true, GroupByProvider: true, GroupBySource: true,
		GroupBySession: true, GroupByDay: true,
	}
	if len(GroupBys) != len(want) {
		t.Fatalf("GroupBys = %v, want the five the report serves", GroupBys)
	}
	for _, known := range GroupBys {
		if !want[known] {
			t.Errorf("GroupBys lists %q, which is not a breakdown this report makes", known)
		}
		if !KnownGroupBy(known) {
			t.Errorf("KnownGroupBy(%q) = false, want true", known)
		}
	}
	if KnownGroupBy(GroupNone) {
		t.Error("GroupNone is a breakdown: the empty string is the report's own no-breakdown")
	}
}

// A window that ends before it starts is refused rather than served as a report
// of zero, because a report of nothing reads as "you spent nothing".
func TestServiceRefusesAWindowThatEndsBeforeItStarts(t *testing.T) {
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name  string
		since time.Time
		until time.Time
	}{
		{name: "until is before since", since: at, until: at.Add(-time.Hour)},
		{name: "until is since", since: at, until: at},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ledger := &fakeLedger{}
			service := newTestService(t, ledger, 365)
			query := anAccount()
			query.Since, query.Until = tt.since, tt.until

			_, err := service.Read(context.Background(), query)

			if !errors.Is(err, ErrEmptyWindow) {
				t.Fatalf("error = %v, want ErrEmptyWindow", err)
			}
			if len(ledger.asked) != 0 {
				t.Error("the ledger was asked over a window that cannot hold anything")
			}
		})
	}
}

// A window with one end is a window. The absence of a bound is not a moment in
// the year one, and a case that read a zero time as one would report every call
// ever made as happening in 1 AD.
func TestServiceAcceptsAWindowWithOneEnd(t *testing.T) {
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name  string
		since time.Time
		until time.Time
	}{
		{name: "from a moment onwards", since: at},
		{name: "up to a moment", until: at},
		{name: "with no bounds at all"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ledger := &fakeLedger{}
			service := newTestService(t, ledger, 365)
			query := anAccount()
			query.Since, query.Until = tt.since, tt.until

			if _, err := service.Read(context.Background(), query); err != nil {
				t.Fatalf("Read() error = %v, want the window accepted", err)
			}
			if len(ledger.asked) != 1 {
				t.Fatalf("the ledger was asked %d times, want once", len(ledger.asked))
			}
		})
	}
}

// The ledger is handed the query as it was built: the window and the four
// narrowings are not re-derived here, because two places deciding what a report
// is over is one place too many.
func TestServiceHandsTheWholeQueryToTheLedger(t *testing.T) {
	ledger := &fakeLedger{}
	service := newTestService(t, ledger, 365)
	query := Query{
		OwnerID: 7, Admin: true,
		Since:   time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		Until:   time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC),
		GroupBy: GroupBySource, Model: "deepseek-chat", Provider: "deepseek",
		Source: "practice.judge", SessionID: "ses_1",
	}

	if _, err := service.Read(context.Background(), query); err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	if len(ledger.asked) != 1 || ledger.asked[0] != query {
		t.Errorf("the ledger was handed %+v, want %+v", ledger.asked, query)
	}
}

// The window and the breakdown come back with the numbers. A total without the
// window it is over is a total nobody can check, and the ledger is not asked to
// remember either: what it read is the numbers.
func TestServiceCarriesTheWindowAndBreakdownWithTheNumbers(t *testing.T) {
	since := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	// The ledger answers with a window of its own and a breakdown of its own,
	// which the service must not pass on: a store that invented a window would
	// otherwise be reporting one the caller never asked over.
	wrong := time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC)
	ledger := &fakeLedger{report: Report{
		Since: &wrong, GroupBy: GroupByDay, Finished: Totals{Calls: 3},
	}}
	service := newTestService(t, ledger, 365)
	query := anAccount()
	query.Since, query.Until, query.GroupBy = since, until, GroupByModel

	report, err := service.Read(context.Background(), query)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	if report.Since == nil || !report.Since.Equal(since) {
		t.Errorf("Since = %v, want %s", report.Since, since)
	}
	if report.Until == nil || !report.Until.Equal(until) {
		t.Errorf("Until = %v, want %s", report.Until, until)
	}
	if report.GroupBy != GroupByModel {
		t.Errorf("GroupBy = %q, want the breakdown that was asked for", report.GroupBy)
	}
	if report.Finished.Calls != 3 {
		t.Errorf("the numbers were not carried through: %+v", report.Finished)
	}
}

// A bound the caller did not give stays absent in the report rather than
// becoming a moment. nil is the absence of a window end; the year one is a claim
// about when.
func TestServiceLeavesAnUnaskedBoundWithoutAMoment(t *testing.T) {
	ledger := &fakeLedger{}
	service := newTestService(t, ledger, 365)

	report, err := service.Read(context.Background(), anAccount())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	if report.Since != nil || report.Until != nil {
		t.Errorf("window = [%v, %v], want no bounds at all", report.Since, report.Until)
	}
}

// A window with nothing in it is an answer, and a broken-down one is an empty
// list rather than a missing field: a client drawing the breakdown reads one
// shape, and `null` is a second one to handle.
func TestServiceServesAGroupedReportWithNoRowsAsAnEmptyList(t *testing.T) {
	ledger := &fakeLedger{}
	service := newTestService(t, ledger, 365)
	query := anAccount()
	query.GroupBy = GroupByModel

	report, err := service.Read(context.Background(), query)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	if report.Groups == nil {
		t.Error("Groups is nil: a window with nothing in it is still an answer")
	}
	if len(report.Groups) != 0 {
		t.Errorf("Groups = %+v, want none", report.Groups)
	}
}

// A whole-window report has no buckets to have, and that is the other half of
// the rule above: an empty list here would say the window was broken down into
// nothing, which is not what the caller asked for.
func TestServiceLeavesAnUnbrokenReportWithoutBuckets(t *testing.T) {
	ledger := &fakeLedger{}
	service := newTestService(t, ledger, 365)

	report, err := service.Read(context.Background(), anAccount())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	if report.Groups != nil {
		t.Errorf("Groups = %+v, want none: the caller asked for no breakdown", report.Groups)
	}
}

// A ledger's failure is passed through rather than turned into a report of zero.
// The two look the same on the wire and mean opposite things, and only the
// handler above can tell a caller which happened.
func TestServicePassesALedgerFailureThrough(t *testing.T) {
	trouble := errors.New("the table is missing")
	ledger := &fakeLedger{err: trouble}
	service := newTestService(t, ledger, 365)

	report, err := service.Read(context.Background(), anAccount())

	if !errors.Is(err, trouble) {
		t.Fatalf("error = %v, want the ledger's own", err)
	}
	if report.Finished.Calls != 0 || report.Groups != nil {
		t.Errorf("a report was returned alongside the failure: %+v", report)
	}
}

// The retention window is the deployment's (§5), and it reaches the ledger as a
// moment computed from it. The window is not a rule about the report's shape —
// nothing a deployment writes can widen what a call records — so it lives here,
// beside the one operation that uses it.
func TestServicePrunesFromTheConfiguredWindow(t *testing.T) {
	ledger := &fakeLedger{count: 4}
	service := newTestService(t, ledger, 30)
	before := time.Now().UTC()

	removed, err := service.Prune(context.Background())
	if err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	after := time.Now().UTC()

	if removed != 4 {
		t.Errorf("Prune() = %d, want the count the ledger reported", removed)
	}
	if len(ledger.pruned) != 1 {
		t.Fatalf("the ledger was asked to prune %d times, want once", len(ledger.pruned))
	}
	// A minute of slack rather than an exact moment: the assertion is about which
	// window the service computed, not about how fast the test ran.
	earliest := before.AddDate(0, 0, -30).Add(-time.Minute)
	latest := after.AddDate(0, 0, -30).Add(time.Minute)
	if ledger.pruned[0].Before(earliest) || ledger.pruned[0].After(latest) {
		t.Errorf("Prune(before = %s), want thirty days before now, between %s and %s",
			ledger.pruned[0], earliest, latest)
	}
	if service.RetentionDays() != 30 {
		t.Errorf("RetentionDays() = %d, want the configured window", service.RetentionDays())
	}
}

// The status route's one question of this package: how many calls did not come
// back. It is answered over the whole table and it is not a report — no account,
// no window, no breakdown — which is why it is a method of its own rather than a
// Query with every field left empty, and why nothing here checks an owner.
func TestServiceCountsTheCallsThatDidNotComeBack(t *testing.T) {
	ledger := &fakeLedger{open: 3}
	service := newTestService(t, ledger, 365)

	count, err := service.Unfinished(context.Background())
	if err != nil {
		t.Fatalf("Unfinished() error = %v", err)
	}
	if count != 3 {
		t.Errorf("Unfinished() = %d, want the count the ledger reported", count)
	}
}

// A window nobody could serve is refused where it is set rather than where it is
// used: the same rule as every other configured number, and the reason the
// service is built at startup.
func TestServiceRefusesAWindowNobodyCouldServe(t *testing.T) {
	for _, days := range []int{0, -1, -365} {
		_, err := NewService(&fakeLedger{}, days)
		if err == nil {
			t.Fatalf("NewService(_, %d) was accepted", days)
		}
		if !strings.Contains(err.Error(), "retention") {
			t.Errorf("NewService(_, %d) error = %v, want it to name the window it refused", days, err)
		}
	}
}

// A ledger that was never built is a wiring mistake, and the two things it must
// not do are answer a report and report that it pruned nothing.
func TestServiceRefusesToBeBuiltWithoutALedger(t *testing.T) {
	if _, err := NewService(nil, 365); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("NewService(nil, 365) error = %v, want ErrNotConfigured", err)
	}
}

func TestAnUnwiredServiceRefusesRatherThanAnsweringZero(t *testing.T) {
	var service *Service
	ctx := context.Background()

	if _, err := service.Read(ctx, anAccount()); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("Read() error = %v, want ErrNotConfigured", err)
	}
	if _, err := service.Prune(ctx); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("Prune() error = %v, want ErrNotConfigured", err)
	}
	if _, err := service.Unfinished(ctx); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("Unfinished() error = %v, want ErrNotConfigured", err)
	}
}
