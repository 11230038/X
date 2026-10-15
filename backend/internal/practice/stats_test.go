package practice

// The statistics against a real database. Every claim here is a claim about SQL
// this package writes and the engine has an opinion about: what a bucket label
// means, whether a review that was never judged counts as a false, whether the
// two halves of the union really line up bucket for bucket, and which index the
// engine reaches for.
//
// The correctness of a review is the one that matters most, because it is the
// one that fails silently: read the wrong field and every review counts as
// neither right nor wrong, so a user who got everything right is shown an
// accuracy of zero and nothing anywhere reports an error. The cases below pin
// the field by constructing the reviews that tell the candidates apart — a skip
// after a wrong answer, whose answer face still says the old verdict — and the
// last of them pins the one thing that separates the field being read now from
// the JSON path it replaced, which is not an answer but a plan.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"backend/internal/authz"
	"backend/internal/data"
	"backend/internal/data/testdb"
	"backend/internal/notebook"
)

// statsDays are the days the cases in this file work on. They are spelled out
// rather than derived from the clock: the series is an answer about time, and a
// case that has to compute which day it is running on is a case testing the
// calendar.
var (
	statsDay       = time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	statsOtherDay  = time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	statsBeforeDay = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
)

// newStatsHarness is the review harness with the filing tables cleared too. The
// breakdown by category reads them, and a category left behind by another case
// would be counted as if this case had filed a question under it.
func newStatsHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	testdb.Reset(t, h.resource, "notebook_entry_categories", "notebook_categories")
	return h
}

// answerAt submits one review as of an instant, which is the only way an event
// lands in the bucket a case is about.
func (h *harness) answerAt(t *testing.T, instant time.Time, review Review) ReviewResult {
	t.Helper()
	h.moveAt(instant)
	return h.submit(t, review)
}

// stats reads one window as the account these tests act as. The caller rides in
// the query rather than in a parameter, so it is set here: a trend is always
// somebody's, and a query a case writes names only the window it is asking about.
func (h *harness) stats(t *testing.T, query StatsQuery) Stats {
	t.Helper()
	query.Caller = testCaller()
	stats, err := h.service.Stats(context.Background(), query)
	if err != nil {
		t.Fatalf("read the statistics: %v", err)
	}
	return stats
}

// labelsOf is the series' shape: the buckets it covers, in order.
func labelsOf(series []BucketStat) []string {
	labels := make([]string, 0, len(series))
	for _, point := range series {
		labels = append(labels, point.Label)
	}
	return labels
}

func bucketAt(t *testing.T, stats Stats, label string) BucketStat {
	t.Helper()
	for _, point := range stats.Series {
		if point.Label == label {
			return point
		}
	}
	t.Fatalf("the series has no bucket %s: %v", label, labelsOf(stats.Series))
	return BucketStat{}
}

func TestTheSeriesHasAPointForEveryBucketIncludingTheQuietOnes(t *testing.T) {
	h := newStatsHarness(t)
	// Two questions from before the window: they are answered inside it, and
	// they must not be counted as questions added by it.
	right := storeQuestionAt(t, h.resource, "nbe_1", fillFor("闭包是什么"), statsBeforeDay)
	wrong := storeQuestionAt(t, h.resource, "nbe_2", choiceFor("下面哪个是闭包"), statsBeforeDay)
	h.answerAt(t, statsDay.Add(9*time.Hour), Review{RequestID: "rev_1",
		QuestionID: right.QuestionID, UserAnswer: "闭包", Marker: MarkerNone})
	h.answerAt(t, statsDay.Add(10*time.Hour), Review{RequestID: "rev_2",
		QuestionID: wrong.QuestionID, UserAnswer: "B", Marker: MarkerNone})
	storeQuestionAt(t, h.resource, "nbe_3", fillFor("什么是作用域"), statsOtherDay.Add(12*time.Hour))

	// The 5th to the 9th, both ends named inside their day: the window is those
	// five buckets and no others.
	stats := h.stats(t, StatsQuery{
		From: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 10, 9, 23, 0, 0, 0, time.UTC),
	})
	if got := labelsOf(stats.Series); len(got) != 5 {
		t.Fatalf("the window covers 5 days and the series has %d points: %v", len(got), got)
	}
	busy := bucketAt(t, stats, "2026-10-06")
	if busy.Reviews != 2 || busy.Correct != 1 || busy.Wrong != 1 || busy.Accuracy != 0.5 {
		t.Errorf("the day with two reviews reads %+v", busy)
	}
	if busy.Added != 0 {
		t.Errorf("the day is credited with %d questions it did not add", busy.Added)
	}
	added := bucketAt(t, stats, "2026-10-08")
	if added.Added != 1 || added.Reviews != 0 {
		t.Errorf("the day a question was written reads %+v", added)
	}
	// A day nobody practised is a point at zero, and it is there so that a chart
	// can draw the gap where it is: a series of only the days with data puts the
	// quiet ones next to each other and says the user practised every day.
	for _, label := range []string{"2026-10-05", "2026-10-07", "2026-10-09"} {
		if quiet := bucketAt(t, stats, label); quiet != (BucketStat{Label: label}) {
			t.Errorf("the quiet day %s reads %+v", label, quiet)
		}
	}
	// The window the answer covers is the one it used, widened outwards, and the
	// caller is told which that was.
	wantFrom := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	wantTo := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	if stats.From != wantFrom || stats.To != wantTo {
		t.Errorf("the answer covers %s to %s, wanted %s to %s", stats.From, stats.To, wantFrom, wantTo)
	}
	if stats.Bucket != BucketDay {
		t.Errorf("a request that named no bucket was answered in %q", stats.Bucket)
	}
}

func TestAReviewIsCountedInTheBucketItHappenedIn(t *testing.T) {
	h := newStatsHarness(t)
	question := storeQuestionAt(t, h.resource, "nbe_1", fillFor("闭包是什么"), statsBeforeDay)
	h.answerAt(t, statsDay.Add(23*time.Hour), Review{RequestID: "rev_1",
		QuestionID: question.QuestionID, UserAnswer: "闭包", Marker: MarkerNone})
	h.answerAt(t, statsDay.Add(25*time.Hour), Review{RequestID: "rev_2",
		QuestionID: question.QuestionID, UserAnswer: "闭包", Marker: MarkerNone})

	// Two hours apart and in two days: the boundary is the one the bucket has,
	// not the one the boundary is near.
	stats := h.stats(t, StatsQuery{From: statsDay, To: statsDay.Add(48 * time.Hour)})
	first := bucketAt(t, stats, "2026-10-06")
	second := bucketAt(t, stats, "2026-10-07")
	if first.Reviews != 1 || second.Reviews != 1 {
		t.Errorf("two reviews either side of midnight read %+v and %+v", first, second)
	}
}

func TestAccuracyCountsTheJudgedReviewsAndNotTheSkips(t *testing.T) {
	h := newStatsHarness(t)
	wrong := storeQuestionAt(t, h.resource, "nbe_1", choiceFor("下面哪个是闭包"), statsBeforeDay)
	right := storeQuestionAt(t, h.resource, "nbe_2", fillFor("闭包是什么"), statsBeforeDay)

	// The wrong answer, then a skip of the same question an hour later, then a
	// right answer to another one. The skip leaves the answer face exactly as the
	// wrong answer left it, which is what makes this the case that tells the two
	// JSON paths apart: read the answer face instead of the judgement and the
	// skip is counted as a second wrong answer, and nothing reports an error.
	h.answerAt(t, statsDay.Add(8*time.Hour), Review{RequestID: "rev_1",
		QuestionID: wrong.QuestionID, UserAnswer: "B", Marker: MarkerNone})
	h.answerAt(t, statsDay.Add(9*time.Hour), Review{RequestID: "rev_2",
		QuestionID: wrong.QuestionID, Marker: MarkerSkipped})
	h.answerAt(t, statsDay.Add(10*time.Hour), Review{RequestID: "rev_3",
		QuestionID: right.QuestionID, UserAnswer: "闭包", Marker: MarkerNone})

	point := bucketAt(t, h.stats(t, StatsQuery{From: statsDay, To: statsDay}), "2026-10-06")
	if point.Reviews != 3 {
		t.Errorf("three reviews were made and the bucket counts %d", point.Reviews)
	}
	if point.Correct != 1 || point.Wrong != 1 {
		t.Errorf("one right and one wrong review read %+v", point)
	}
	// The accuracy is over the reviews that were judged, not over every review:
	// a skip is not a wrong answer, and it is not a right one either.
	if point.Accuracy != 0.5 {
		t.Errorf("the accuracy is %v, wanted 0.5", point.Accuracy)
	}
}

// The equivalence case for §2.8, and it is written to be green on both sides of
// the change: the reviews are put in the table with the column and the document
// saying the same thing, so it passes whether the statement reads
// `is_correct` or `$.judgment.is_correct`. What it pins is that the two agree
// where it matters — on the review nobody judged.
//
// The rows are written here rather than submitted through the service, because
// what is under test is the SQL: a submitted skip is written by a writer this
// case would then be trusting to have written the column too.
func TestStatsAccuracyIgnoresUnjudgedReviews(t *testing.T) {
	h := newStatsHarness(t)
	storeQuestionAt(t, h.resource, "nbe_1", fillFor("闭包是什么"), statsBeforeDay)

	review := func(requestID string, at time.Time, correct *bool, outcome any) {
		t.Helper()
		var column any
		if correct != nil {
			column = *correct
		}
		testdb.Exec(t, h.resource, "INSERT INTO practice_review_events "+
			"(request_id, notebook_entries_id, is_correct, outcome_json, review_time) VALUES (?, ?, ?, ?, ?)",
			requestID, "nbe_1", column, outcome, at)
	}
	judged := func(value bool) *bool { return &value }

	review("rev_1", statsDay.Add(8*time.Hour), judged(true), `{"judgment":{"is_correct":true}}`)
	review("rev_2", statsDay.Add(9*time.Hour), judged(false), `{"judgment":{"is_correct":false}}`)
	// The skip. Its document carries no judgement at all and its answer face still
	// holds the verdict of the review before it, which is the shape that makes
	// `$.entry.is_correct` the wrong field to read: counted from there, a learner
	// who skipped a question they had just got right is credited with another
	// right answer.
	review("rev_3", statsDay.Add(10*time.Hour), nil, `{"judgment":null,"entry":{"is_correct":true}}`)
	// And a row whose document is not there at all, which is the other way a
	// review arrives unjudged.
	review("rev_4", statsDay.Add(11*time.Hour), nil, nil)

	point := bucketAt(t, h.stats(t, StatsQuery{From: statsDay, To: statsDay}), "2026-10-06")
	if point.Reviews != 4 {
		t.Errorf("four reviews were written and the bucket counts %d", point.Reviews)
	}
	if point.Correct != 1 || point.Wrong != 1 {
		t.Errorf("one right and one wrong review read %+v", point)
	}
	// The accuracy is a division over the reviews that were judged, so an
	// unjudged one moves neither the numerator nor the denominator: it is not a
	// wrong answer and not a right one.
	if point.Accuracy != 0.5 {
		t.Errorf("the accuracy is %v, wanted 0.5", point.Accuracy)
	}
}

// The reason the statement changed, and the only thing that distinguishes it
// from the one it replaced: the document cannot be indexed and the column can.
//
// The reviews half now reads nothing that is not in the table's own columns —
// the question, the verdict and the time — so the window is served by an index
// instead of a full scan that decodes a document per row. That is what this case
// watches, and it has to watch the plan rather than the answer: both statements
// return the same counters, which is the point of the equivalence case above.
//
// Which index is deliberately not the assertion — see the case's own comment for
// what the engine actually chose and why the column is the signature that moves.
func TestStatsAccuracyUsesTheIndex(t *testing.T) {
	h := newStatsHarness(t)
	storeQuestionAt(t, h.resource, "nbe_1", fillFor("闭包是什么"), statsBeforeDay)
	storeQuestionAt(t, h.resource, "nbe_2", fillFor("逃逸分析是什么"), statsBeforeDay)
	// Rows to have a plan about: a cost-based optimizer choosing between a full
	// scan and an index on a table of four rows is choosing at random, so the case
	// gives it a table worth deciding about.
	seedReviews(t, h.resource, "nbe_1", 600, statsDay.Add(9*time.Hour))
	seedReviews(t, h.resource, "nbe_2", 600, statsDay.Add(10*time.Hour))

	scope, owner := ownerPredicate("notebook_entries", testCaller())
	from, to := statsDay, statsDay.Add(24*time.Hour)
	plan := explain(t, h.resource, "EXPLAIN "+statsSeriesSQL(BucketDay, scope), from, to, owner, from, to, owner)
	// Which column the plan reaches for, and not which index it names. Measured
	// rather than assumed, by explaining both spellings of this aggregation on the
	// same seeded table: the engine picks `idx_practice_review_events_entry_time`
	// either way, because the window's predicate is `review_time` and that index
	// leads with it. What moves is what the scan then does with the row —
	// `is_index_back=true` to fetch `is_correct`, against fetching and parsing
	// `outcome_json` — and the plan says which by naming the column in the node's
	// access list. So this asserts the signature that actually differs between the
	// two versions; an assertion on the outcome index would have been asserting the
	// optimizer's choice, and would have failed on the fixed code.
	if !strings.Contains(plan, "practice_review_events.is_correct") {
		t.Fatalf("the plan never names practice_review_events.is_correct, so the reviews half is not "+
			"reading the column at all:\n%s", plan)
	}
	if strings.Contains(plan, "practice_review_events.outcome_json") {
		t.Fatalf("the plan reaches for practice_review_events.outcome_json, so the reviews half is "+
			"decoding the document per row again:\n%s", plan)
	}
}

// seedReviews writes n judged reviews of one question, straight into the table
// and all inside one day. It exists for the plan case and says nothing about the
// service: what a plan needs from a case is volume, not history.
func seedReviews(t *testing.T, resource *data.Resource, entryID string, n int, at time.Time) {
	t.Helper()
	placeholders := make([]string, 0, n)
	args := make([]any, 0, n*5)
	for index := range n {
		placeholders = append(placeholders, "(?, ?, ?, ?, ?)")
		args = append(args, fmt.Sprintf("rev_%s_%d", entryID, index), entryID,
			index%2 == 0, fmt.Sprintf(`{"judgment":{"is_correct":%t}}`, index%2 == 0), at)
	}
	testdb.Exec(t, resource, "INSERT INTO practice_review_events "+
		"(request_id, notebook_entries_id, is_correct, outcome_json, review_time) VALUES "+
		strings.Join(placeholders, ", "), args...)
}

// explain runs a statement through the engine's planner and returns what it said,
// as text.
//
// Every cell is read as a string and the whole answer is handed back, because the
// columns a plan comes back in are the engine's business and the question a case
// asks of it is which table and which index the plan names — not which column the
// engine chose to name it in.
func explain(t *testing.T, resource *data.Resource, statement string, args ...any) string {
	t.Helper()
	rows, err := resource.GORM().Raw(statement, args...).Rows()
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("read the plan's columns: %v", err)
	}

	var plan strings.Builder
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			t.Fatalf("read the plan: %v", err)
		}
		for _, value := range values {
			plan.WriteString(planCell(value))
			plan.WriteByte(' ')
		}
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read the plan: %v", err)
	}
	return plan.String()
}

// planCell renders one cell of a plan. A text column arrives as its bytes, and
// printing those with %v would print a list of numbers instead of the table name
// the case is looking for.
func planCell(value any) string {
	if raw, ok := value.([]byte); ok {
		return string(raw)
	}
	return fmt.Sprintf("%v", value)
}

func TestAWeekIsLabelledByItsMonday(t *testing.T) {
	h := newStatsHarness(t)
	question := storeQuestionAt(t, h.resource, "nbe_1", fillFor("闭包是什么"), statsBeforeDay)

	// Wednesday and Friday of one week, then Wednesday of the next.
	for index, at := range []time.Time{
		time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC),
	} {
		h.answerAt(t, at, Review{RequestID: fmt.Sprintf("rev_%d", index),
			QuestionID: question.QuestionID, UserAnswer: "闭包", Marker: MarkerNone})
	}

	// Monday the 5th to Sunday the 18th is two weeks, and both labels are the
	// Monday that starts them — which is also what makes the labels sort as
	// dates when they are compared as strings.
	stats := h.stats(t, StatsQuery{
		From:   time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
		To:     time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC),
		Bucket: BucketWeek,
	})
	labels := labelsOf(stats.Series)
	if len(labels) != 2 || labels[0] != "2026-10-05" || labels[1] != "2026-10-12" {
		t.Fatalf("two weeks are labelled %v", labels)
	}
	if first := bucketAt(t, stats, "2026-10-05"); first.Reviews != 2 {
		t.Errorf("the first week counts %d reviews, wanted 2", first.Reviews)
	}
	if second := bucketAt(t, stats, "2026-10-12"); second.Reviews != 1 {
		t.Errorf("the second week counts %d reviews, wanted 1", second.Reviews)
	}
}

func TestTheWindowIsWidenedToWholeBuckets(t *testing.T) {
	h := newStatsHarness(t)
	question := storeQuestionAt(t, h.resource, "nbe_1", fillFor("闭包是什么"), statsBeforeDay)
	// Answered in the evening of a day the request asks about only its morning.
	h.answerAt(t, statsDay.Add(20*time.Hour), Review{RequestID: "rev_1",
		QuestionID: question.QuestionID, UserAnswer: "闭包", Marker: MarkerNone})

	stats := h.stats(t, StatsQuery{From: statsDay.Add(8 * time.Hour), To: statsDay.Add(10 * time.Hour)})
	if stats.From != statsDay || stats.To != statsDay.Add(24*time.Hour) {
		t.Fatalf("the answer covers %s to %s", stats.From, stats.To)
	}
	// A bucket is a bucket: the point for the 6th is the whole of the 6th, so a
	// review made after the hours the request named is in it.
	if point := bucketAt(t, stats, "2026-10-06"); point.Reviews != 1 {
		t.Errorf("the widened day reads %+v", point)
	}
}

func TestAWindowOrBucketThisVersionCannotDrawIsRefused(t *testing.T) {
	h := newStatsHarness(t)
	wide := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		query StatsQuery
	}{
		{"a bucket that is not a width", StatsQuery{Bucket: "month"}},
		{"a window that ends before it starts", StatsQuery{
			From: statsDay.Add(48 * time.Hour), To: statsDay}},
		{"a window too wide to be a chart", StatsQuery{From: wide, To: statsDay}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			test.query.Caller = testCaller()
			_, err := h.service.Stats(context.Background(), test.query)
			if !errors.Is(err, ErrInvalidStats) {
				t.Fatalf("the request was answered with %v", err)
			}
		})
	}
}

func TestAWindowNobodyNamedIsTheLastThirtyDays(t *testing.T) {
	h := newStatsHarness(t)
	stats := h.stats(t, StatsQuery{})

	if len(stats.Series) != 30 {
		t.Fatalf("the default window has %d buckets, wanted 30", len(stats.Series))
	}
	// It ends with today, which is the bucket a user opening the page is
	// looking at.
	labels := labelsOf(stats.Series)
	if labels[len(labels)-1] != "2026-10-14" || labels[0] != "2026-09-15" {
		t.Errorf("the default window runs %s to %s", labels[0], labels[len(labels)-1])
	}
	if stats.To != time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC) {
		t.Errorf("the default window ends at %s", stats.To)
	}
}

func TestTheBreakdownsCountTheQuestionsOfTheWindow(t *testing.T) {
	h := newStatsHarness(t)
	bank := notebook.NewGORMRepository(h.resource.GORM())

	// Two filed questions from one source, one with no source at all, and one
	// from before the window that must not be counted.
	filed := func(entryID, text string, source string, category string, at time.Time) {
		question := fillFor(text)
		question.Source = source
		storeQuestionAt(t, h.resource, entryID, question, at)
		if err := bank.AddCategories(context.Background(), entryID, []string{category}, testCaller()); err != nil {
			t.Fatalf("file %s under %s: %v", text, category, err)
		}
	}
	filed("nbe_1", "闭包是什么", "knowledge_base", "数学", statsDay.Add(9*time.Hour))
	filed("nbe_2", "什么是作用域", "knowledge_base", "数学", statsDay.Add(10*time.Hour))
	filed("nbe_3", "什么是递归", "", "编程", statsDay.Add(11*time.Hour))
	filed("nbe_4", "什么是多态", "attachment", "数学", statsBeforeDay)

	stats := h.stats(t, StatsQuery{From: statsDay, To: statsDay})
	wantSources := []Distribution{{Name: "knowledge_base", Count: 2}, {Name: "", Count: 1}}
	if len(stats.Sources) != len(wantSources) {
		t.Fatalf("the window's questions came from %+v", stats.Sources)
	}
	for index, want := range wantSources {
		if stats.Sources[index] != want {
			t.Errorf("source breakdown %d is %+v, wanted %+v", index, stats.Sources[index], want)
		}
	}
	// A question with no source is counted under the empty name rather than
	// dropped: it is an answer — this question was not written by a conversation
	// — and a breakdown that leaves a group out makes the others look whole.
	wantCategories := []Distribution{{Name: "数学", Count: 2}, {Name: "编程", Count: 1}}
	if len(stats.Categories) != len(wantCategories) {
		t.Fatalf("the window's questions are filed as %+v", stats.Categories)
	}
	for index, want := range wantCategories {
		if stats.Categories[index] != want {
			t.Errorf("category breakdown %d is %+v, wanted %+v", index, stats.Categories[index], want)
		}
	}
}

// TestDBTheTrendIsNarrowedToOneAccount is §7.2's acceptance for the statistics:
// the window counts one account's questions and no others, so two accounts
// reading the same window are told about their own work rather than a shared
// total.
func TestDBTheTrendIsNarrowedToOneAccount(t *testing.T) {
	h := newStatsHarness(t)
	storeQuestionAtAs(t, h.resource, "nbe_1", fillFor("我的第一题"), statsDay.Add(9*time.Hour), testCaller())
	storeQuestionAtAs(t, h.resource, "nbe_2", fillFor("我的第二题"), statsDay.Add(10*time.Hour), testCaller())
	storeQuestionAtAs(t, h.resource, "nbe_3", fillFor("别人的题"), statsDay.Add(11*time.Hour), otherCaller())
	window := func(caller authz.Caller) Stats {
		t.Helper()
		stats, err := h.service.Stats(context.Background(), StatsQuery{From: statsDay, To: statsDay, Caller: caller})
		if err != nil {
			t.Fatalf("read the statistics: %v", err)
		}
		return stats
	}

	if added := bucketAt(t, window(testCaller()), "2026-10-06").Added; added != 2 {
		t.Errorf("my trend credits me with %d questions, want my own two", added)
	}
	if added := bucketAt(t, window(otherCaller()), "2026-10-06").Added; added != 1 {
		t.Errorf("the other account's trend credits it with %d questions, want its own one", added)
	}
}
