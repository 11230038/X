package practice

// The one case that watches the plan instead of the answer, and the three helpers
// only it needs.
//
// Both spellings of the reviews half return the same counters — that is what the
// equivalence case in stats_test.go is for — so the thing that changed between
// them cannot be seen in a result at all. It is visible in what the engine does
// with the rows: a document fetched and decoded per row, against a column the
// scan already has. This file is therefore the whole of the evidence that the
// change did anything, which is why it is worth its own file and its own volume
// of seeded rows: a plan for four rows is a plan the optimizer picked at random.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"backend/internal/data"
	"backend/internal/data/testdb"
)

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
