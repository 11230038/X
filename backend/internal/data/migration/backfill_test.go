package migration_test

// Migration 00011's backfill, run against a real seekdb with rows already in the
// table it alters.
//
// §7.2 第 2 条 asks for this one by name and says it is not a formality: "NULL
// 与新列的两个语义撞在一起是这条 migration 唯一可能出错的形态". The three-valued
// column is the whole point of 00011 — NULL is *skipped*, TRUE and FALSE are
// *judged* — and the backfill is the one statement that can collapse two of the
// three into each other. What it must not do is turn every historical skip into
// a wrong answer, silently and permanently, for every row that already exists.
//
// It cannot be checked by reading the SQL. `JSON_UNQUOTE(JSON_EXTRACT(...))` is
// being asked to turn a JSON boolean into the text `true`, the comparison is
// against a string because of that, and `->>` — the spelling anyone would reach
// for — does not parse on this engine at all (V3 §0.3 第 11 条). The only way to
// know is to put rows in the table, run the migration over them, and read the
// column back.
//
// The database is walked *down* to version 10 first, for the reason the check
// itself is worth having: a test that ran on an already-migrated database would
// have nothing to backfill, and would pass whether the WHERE clause was there or
// not. Going down also runs three Down statements that nothing else runs.

import (
	"context"
	"database/sql"
	"testing"

	"backend/internal/data"
	"backend/internal/data/migration"
	"backend/internal/data/testdb"
)

const (
	backfillEntryID   = "nbe_backfill_probe"
	backfillJudgedID  = "rev_backfill_judged"
	backfillSkipID    = "rev_backfill_wrong"
	backfillNoKeyID   = "rev_backfill_no_judgement"
	backfillSkippedID = "rev_backfill_skipped"
)

func TestTheCorrectnessBackfillLeavesASkippedReviewUnknown(t *testing.T) {
	resource := testdb.Open(t, "migration")
	testdb.Reset(t, resource, "practice_review_events", "notebook_entries")

	provider, err := migration.Provider(resource.SQLDB())
	if err != nil {
		t.Fatalf("build the migration provider: %v", err)
	}
	ctx := context.Background()
	// Down to the version before the column exists, so that the rows below are
	// rows the migration has to fill in rather than rows it wrote itself.
	if _, err := provider.DownTo(ctx, 10); err != nil {
		t.Fatalf("roll the schema back to version 10: %v", err)
	}

	// The question the reviews are about, and the four shapes a deployment can
	// already hold. The last two are the ones the JSON cannot tell apart, which is
	// the reason this migration exists at all: `judgment` is absent, and `judgment`
	// is present and null — V3 §0.3 第 10 条's spelling of "the user skipped".
	testdb.Exec(t, resource,
		`INSERT INTO notebook_entries
		    (notebook_entries_id, question_id, question, question_type)
		 VALUES (?, 'qn_backfill_probe', '这一章讲了什么？', 'short_answer')`, backfillEntryID)
	testdb.Exec(t, resource,
		`INSERT INTO practice_review_events (request_id, notebook_entries_id, user_answer, outcome_json)
		 VALUES (?, ?, '答对了', JSON_OBJECT('judgment', JSON_OBJECT('is_correct', TRUE)))`,
		backfillJudgedID, backfillEntryID)
	testdb.Exec(t, resource,
		`INSERT INTO practice_review_events (request_id, notebook_entries_id, user_answer, outcome_json)
		 VALUES (?, ?, '答错了', JSON_OBJECT('judgment', JSON_OBJECT('is_correct', FALSE)))`,
		backfillSkipID, backfillEntryID)
	testdb.Exec(t, resource,
		`INSERT INTO practice_review_events (request_id, notebook_entries_id, outcome_json)
		 VALUES (?, ?, JSON_OBJECT('judgment', CAST(NULL AS JSON)))`,
		backfillNoKeyID, backfillEntryID)
	testdb.Exec(t, resource,
		"INSERT INTO practice_review_events (request_id, notebook_entries_id, outcome_json) "+
			"VALUES (?, ?, CAST('{\"judgment\": null}' AS JSON))",
		backfillSkippedID, backfillEntryID)

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("apply the migrations over rows that already exist: %v", err)
	}

	// Judged reviews get a verdict, and the verdict is the one the document held:
	// this is the `JSON_UNQUOTE(JSON_EXTRACT(...)) = 'true'` spelling actually
	// being measured, which is the half of §3 that could only ever have been
	// guessed at.
	if got := correctnessOf(t, resource, backfillJudgedID); got == nil || !*got {
		t.Errorf("a review judged right reads back as %v, want TRUE", got)
	}
	if got := correctnessOf(t, resource, backfillSkipID); got == nil || *got {
		t.Errorf("a review judged wrong reads back as %v, want FALSE", got)
	}
	// And a review nobody judged stays NULL, in both spellings of "skipped". This
	// is the outcome the three-valued column exists for: give it FALSE and
	// "nobody answered" becomes "somebody answered wrong" for every row that
	// predates this migration, permanently and silently.
	//
	// What it does *not* prove is that the WHERE clause is what saves them — the
	// assignment's own NULL propagation does, `NULL = 'true'` being NULL, which
	// was measured separately. The migration's comment says so.
	for _, requestID := range []string{backfillNoKeyID, backfillSkippedID} {
		if got := correctnessOf(t, resource, requestID); got != nil {
			t.Errorf("the skipped review %s reads back as %v, want NULL", requestID, *got)
		}
	}
}

// correctnessOf reads one review's verdict, NULL and all — the whole reason the
// column is a pointer rather than a bool.
func correctnessOf(t *testing.T, resource *data.Resource, requestID string) *bool {
	t.Helper()
	var row struct {
		IsCorrect *bool `gorm:"column:is_correct"`
	}
	err := resource.GORM().Raw(
		"SELECT is_correct FROM practice_review_events WHERE request_id = ?", requestID).Scan(&row).Error
	if err != nil && err != sql.ErrNoRows {
		t.Fatalf("read the review %s back: %v", requestID, err)
	}
	return row.IsCorrect
}
