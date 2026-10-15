package migration_test

// The migrations, run the way a deployment runs them and the way no test in this
// repository had run them before: over a database that already holds rows, and
// back down again.
//
// V5's closing note recorded both as owed (§7.2 第 2、3 条) because
// testdb.prepare always does the same thing — create, migrate to the latest,
// check that it is the latest — so every schema test in the repository had only
// ever met an empty database being brought forwards. The two states that leaves
// untested are the two that a running deployment is actually in: a database with
// a version table that already has rows behind it, and a rollback.
//
// The new entry point is testdb.OpenAt, which creates the database, empties it,
// and stops there. These tests drive the schema themselves, from the version they
// name.
//
// Each test asks for a suffix of its own. OpenAt empties the database each time
// it is called, so a suffix cannot be shared with a test that has already built
// something on it — and for these tests "already built" is the schema itself.

import (
	"context"
	"database/sql"
	"testing"

	"backend/internal/data"
	"backend/internal/data/migration"
	"backend/internal/data/testdb"
)

const (
	upgradeAccountID  = int64(91001)
	upgradeUsername   = "upgrade_probe"
	upgradeSessionID  = "ses_upgrade_probe"
	upgradeTurnID     = "trn_upgrade_probe"
	upgradePathID     = "msp_upgrade_probe"
	upgradeEntryID    = "nbe_upgrade_probe"
	upgradeReviewID   = "rev_upgrade_probe"
	upgradeAnsweredID = "rev_upgrade_probe_answered"
)

// TestAMigrationLeavesTheRowsThatWereAlreadyThere is the upgrade a deployment
// performs: rows in the tables the new migrations alter, then the migrations.
func TestAMigrationLeavesTheRowsThatWereAlreadyThere(t *testing.T) {
	resource := testdb.OpenAt(t, "upgrade")
	db := resource.SQLDB()
	ctx := context.Background()

	provider, err := migration.Provider(db)
	if err != nil {
		t.Fatalf("build the migration provider: %v", err)
	}
	// The version this repository shipped before V6. Writing the rows below at
	// 17 is what makes them rows the migrations have to walk past rather than
	// rows the migrations wrote.
	if _, err := provider.UpTo(ctx, 17); err != nil {
		t.Fatalf("bring the schema to version 17: %v", err)
	}
	writeV5Rows(t, resource)

	if err := migration.Up(ctx, db); err != nil {
		t.Fatalf("upgrade a database that already holds rows: %v", err)
	}

	// Every row written before the upgrade is still there and still readable.
	// "The migration ran" and "the migration ran without taking anything with it"
	// are different claims, and only a count tells them apart.
	for _, want := range []struct {
		table string
		rows  int64
	}{
		{"users", 1},
		{"sessions", 1},
		{"turns", 1},
		{"mastery_paths", 1},
		{"mastery_interactions", 1},
		{"notebook_entries", 1},
		{"practice_review_events", 2},
	} {
		if got := countRows(t, db, want.table); got != want.rows {
			t.Errorf("%s holds %d rows after the upgrade, want %d", want.table, got, want.rows)
		}
	}
	if got := correctnessOfReview(t, db, upgradeReviewID); !got.Valid || !got.Bool {
		t.Errorf("the review judged right reads back as %+v, want TRUE: an upgrade must not "+
			"rewrite or drop a verdict that was already written", got)
	}
	if got := correctnessOfReview(t, db, upgradeAnsweredID); !got.Valid || got.Bool {
		t.Errorf("the review judged wrong reads back as %+v, want FALSE", got)
	}

	// The four account columns are there, and an account that existed before them
	// means "never revoked, never failed, never locked" — NULL and 0, not the
	// zero time.
	for _, column := range []string{
		"token_valid_after", "failed_attempts", "failed_window_start", "locked_until",
	} {
		if !columnExists(t, db, "users", column) {
			t.Errorf("the upgrade did not add users.%s", column)
		}
	}
	if got := accountLockState(t, db); got != (lockState{}) {
		t.Errorf("an account that predates the columns reads back as %+v, want all zero", got)
	}

	// The interaction rule is in the schema, which is the whole point of 00019:
	// a column to hang it on and a unique key that hangs it.
	if !columnExists(t, db, "mastery_interactions", "open_scope") {
		t.Error("the upgrade did not add mastery_interactions.open_scope")
	}
	if !indexExists(t, db, "mastery_interactions", "mastery_interactions_one_open") {
		t.Error("the upgrade did not add mastery_interactions_one_open")
	}

	// Running it again is not running it twice. A deployment's migration step may
	// be re-run — by an operator, by a restart, by a retry — and the answer has to
	// be "there was nothing to do" rather than a second attempt at the same work.
	results, err := provider.Up(ctx)
	if err != nil {
		t.Fatalf("run the migrations a second time: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("a second upgrade applied %d migrations, want 0", len(results))
	}
}

// TestDownAndUpAgainTakeTheColumnsWithThem is the rollback, on a database that
// holds rows rather than an empty one.
func TestDownAndUpAgainTakeTheColumnsWithThem(t *testing.T) {
	resource := testdb.OpenAt(t, "rollback")
	db := resource.SQLDB()
	ctx := context.Background()

	provider, err := migration.Provider(db)
	if err != nil {
		t.Fatalf("build the migration provider: %v", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("bring the schema to the latest version: %v", err)
	}
	writeV5Rows(t, resource)

	if _, err := provider.DownTo(ctx, 17); err != nil {
		t.Fatalf("roll the V6 migrations back: %v", err)
	}

	// Down means gone, not "ignored from here on". The columns and the index are
	// asked about rather than assumed, because a Down that silently does nothing
	// leaves a database that is neither at 17 nor at 19 — and the version table
	// would say 17.
	for _, column := range []string{
		"token_valid_after", "failed_attempts", "failed_window_start", "locked_until",
	} {
		if columnExists(t, db, "users", column) {
			t.Errorf("rolling back to 17 left users.%s behind", column)
		}
	}
	if columnExists(t, db, "mastery_interactions", "open_scope") {
		t.Error("rolling back to 17 left mastery_interactions.open_scope behind")
	}
	if indexExists(t, db, "mastery_interactions", "mastery_interactions_one_open") {
		t.Error("rolling back to 17 left mastery_interactions_one_open behind")
	}
	if got := countRows(t, db, "mastery_interactions"); got != 1 {
		t.Errorf("rolling back left %d interactions, want the 1 that was written", got)
	}

	// And back up again, this time over a table that is not freshly built: the
	// open interaction written before the rollback is still open, so the index
	// going back on has a row to consider.
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("bring the schema forward again after a rollback: %v", err)
	}
	for _, column := range []string{
		"token_valid_after", "failed_attempts", "failed_window_start", "locked_until",
	} {
		if !columnExists(t, db, "users", column) {
			t.Errorf("going forward again did not restore users.%s", column)
		}
	}
	if !indexExists(t, db, "mastery_interactions", "mastery_interactions_one_open") {
		t.Error("going forward again did not restore mastery_interactions_one_open")
	}
	// The cost of the rollback, measured rather than described: the columns come
	// back empty, so whatever they held is gone. Nothing else in the row is.
	if got := accountLockState(t, db); got != (lockState{}) {
		t.Errorf("the restored account columns read back as %+v, want all zero", got)
	}
	if got := countRows(t, db, "users"); got != 1 {
		t.Errorf("the second upgrade left %d accounts, want 1", got)
	}
}

// TestTheOpenInteractionRepairKeepsTheEarliest is 00019's repair, on the only
// data it exists for.
//
// The repair statement is the one part of these migrations that a deployment
// cannot discover the meaning of by looking at the schema afterwards, because on
// a correct database it deletes nothing: every deployment that never raced
// exercises only "the statement is syntactically acceptable". This walks the
// schema to 18, writes what the old code could produce, and then asks.
func TestTheOpenInteractionRepairKeepsTheEarliest(t *testing.T) {
	resource := testdb.OpenAt(t, "duplicates")
	db := resource.SQLDB()
	ctx := context.Background()

	provider, err := migration.Provider(db)
	if err != nil {
		t.Fatalf("build the migration provider: %v", err)
	}
	if _, err := provider.UpTo(ctx, 18); err != nil {
		t.Fatalf("bring the schema to version 18: %v", err)
	}
	writeV5RowsWithoutTheInteraction(t, resource)

	// Two open interactions on one path, which is what two Ask calls racing each
	// other leave behind — the read inside each transaction matched no row and
	// took no lock. Plus two answered ones, because the rule must not touch them
	// however many there are: that is what the generated column being NULL for
	// them is for.
	const (
		early    = "mci_upgrade_early"
		late     = "mci_upgrade_late"
		answered = "mci_upgrade_answered"
		second   = "mci_upgrade_answered_two"
	)
	for _, interaction := range []struct {
		id     string
		result string
	}{
		{early, ""},
		{late, ""},
		{answered, `JSON_OBJECT('judgment', JSON_OBJECT('is_correct', TRUE))`},
		{second, `JSON_OBJECT('judgment', JSON_OBJECT('is_correct', FALSE))`},
	} {
		result := sql.NullString{}
		if interaction.result != "" {
			result = sql.NullString{String: interaction.result, Valid: true}
		}
		writeInteraction(t, resource, interaction.id, result)
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("upgrade to the version that enforces the rule: %v", err)
	}

	// Exactly the earliest open interaction on the path is left.
	for _, id := range []string{early, answered, second} {
		if !interactionExists(t, db, id) {
			t.Errorf("the repair removed %s, which the rule does not speak about", id)
		}
	}
	if interactionExists(t, db, late) {
		t.Errorf("the repair kept %s: two open interactions on one path is the state "+
			"the unique key cannot be built over", late)
	}

	// And the rule is now the schema's, so the state cannot come back. This is
	// the assertion the migration exists for: without the unique key the insert
	// below goes through, and with it the database refuses a second open
	// interaction on the path.
	writeInteractionExpectingRefusal(t, resource, upgradePathID)
}

func writeV5Rows(t *testing.T, resource *data.Resource) {
	t.Helper()
	writeV5RowsWithoutTheInteraction(t, resource)
	writeInteraction(t, resource, "mci_upgrade_probe", sql.NullString{})
}

// writeV5RowsWithoutTheInteraction writes one account and one of each row under
// it, in the order the foreign keys require. The account is written through
// testdb.Account because migration 00014 hung eight tables off that row and the
// rest of these inserts name its id.
func writeV5RowsWithoutTheInteraction(t *testing.T, resource *data.Resource) {
	t.Helper()
	testdb.Account(t, resource, upgradeAccountID, upgradeUsername)
	testdb.Exec(t, resource,
		"INSERT INTO sessions (session_id, owner_id) VALUES (?, ?)",
		upgradeSessionID, upgradeAccountID)
	testdb.Exec(t, resource,
		"INSERT INTO turns (turn_id, session_id, status) VALUES (?, ?, 'completed')",
		upgradeTurnID, upgradeSessionID)
	testdb.Exec(t, resource,
		"INSERT INTO mastery_paths (mastery_path_id, owner_session_id, owner_id) VALUES (?, ?, ?)",
		upgradePathID, upgradeSessionID, upgradeAccountID)
	testdb.Exec(t, resource,
		"INSERT INTO notebook_entries (notebook_entries_id, question_id, question, question_type, owner_id) "+
			"VALUES (?, ?, '这一章讲了什么？', 'short_answer', ?)",
		upgradeEntryID, upgradeEntryID+"_q", upgradeAccountID)
	testdb.Exec(t, resource,
		"INSERT INTO practice_review_events (request_id, notebook_entries_id, user_answer, is_correct) "+
			"VALUES (?, ?, '答对了', TRUE)",
		upgradeReviewID, upgradeEntryID)
	testdb.Exec(t, resource,
		"INSERT INTO practice_review_events (request_id, notebook_entries_id, user_answer, is_correct) "+
			"VALUES (?, ?, '答错了', FALSE)",
		upgradeAnsweredID, upgradeEntryID)
}

// writeInteraction writes one interaction on the probe path. An empty result is
// an open one — that is the whole of what "open" means here.
func writeInteraction(t *testing.T, resource *data.Resource, id string, result sql.NullString) {
	t.Helper()
	if result.Valid {
		testdb.Exec(t, resource,
			"INSERT INTO mastery_interactions "+
				"(interaction_id, path_id, turn_id, session_id, result_json) VALUES (?, ?, ?, ?, "+result.String+")",
			id, upgradePathID, upgradeTurnID, upgradeSessionID)
		return
	}
	testdb.Exec(t, resource,
		"INSERT INTO mastery_interactions (interaction_id, path_id, turn_id, session_id) VALUES (?, ?, ?, ?)",
		id, upgradePathID, upgradeTurnID, upgradeSessionID)
}

// writeInteractionExpectingRefusal asks the database for a state the rule
// forbids and reports what it said, so that a unique key nobody notices going
// missing is a failure here rather than a duplicate row in a deployment.
func writeInteractionExpectingRefusal(t *testing.T, resource *data.Resource, pathID string) {
	t.Helper()
	err := testdb.Try(resource,
		"INSERT INTO mastery_interactions (interaction_id, path_id, turn_id, session_id) VALUES (?, ?, ?, ?)",
		"mci_upgrade_should_be_refused", pathID, upgradeTurnID, upgradeSessionID)
	if err == nil {
		t.Fatal("a second open interaction on the same path was accepted, so the rule " +
			"is still the application's and not the schema's")
	}
}

// lockState is the four account columns as one comparable value, so that "an
// account that predates them" is one assertion rather than four.
type lockState struct {
	Revoked  sql.NullTime
	Attempts int
	Window   sql.NullTime
	Locked   sql.NullTime
}

func accountLockState(t *testing.T, db *sql.DB) lockState {
	t.Helper()
	var state lockState
	err := db.QueryRowContext(context.Background(),
		`SELECT token_valid_after, failed_attempts, failed_window_start, locked_until
		 FROM users WHERE user_id = ?`, upgradeAccountID).
		Scan(&state.Revoked, &state.Attempts, &state.Window, &state.Locked)
	if err != nil {
		t.Fatalf("read the account's lock state: %v", err)
	}
	return state
}

// correctnessOfReview reads a verdict back as the three-valued thing it is: a
// NULL here is "nobody judged it", which is not FALSE.
func correctnessOfReview(t *testing.T, db *sql.DB, requestID string) sql.NullBool {
	t.Helper()
	var verdict sql.NullBool
	err := db.QueryRowContext(context.Background(),
		"SELECT is_correct FROM practice_review_events WHERE request_id = ?", requestID).
		Scan(&verdict)
	if err != nil {
		t.Fatalf("read the review %s: %v", requestID, err)
	}
	return verdict
}

func interactionExists(t *testing.T, db *sql.DB, id string) bool {
	t.Helper()
	return countRowsWhere(t, db, "mastery_interactions", "interaction_id", id) > 0
}

func countRows(t *testing.T, db *sql.DB, table string) int64 {
	t.Helper()
	return countRowsWhere(t, db, table, "", "")
}

// countRowsWhere counts a table's rows, or the rows matching one column, over an
// identifier written in this file rather than handed in by a caller.
func countRowsWhere(t *testing.T, db *sql.DB, table, column, value string) int64 {
	t.Helper()
	query := "SELECT COUNT(*) FROM `" + table + "`"
	args := []any{}
	if column != "" {
		query += " WHERE `" + column + "` = ?"
		args = append(args, value)
	}
	var count int64
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func columnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	return informationSchemaHas(t, db, "columns", "column_name", table, column)
}

func indexExists(t *testing.T, db *sql.DB, table, index string) bool {
	t.Helper()
	return informationSchemaHas(t, db, "statistics", "index_name", table, index)
}

// informationSchemaHas asks information_schema rather than trusting the
// migration's exit status: a Down or an Up that silently did nothing is exactly
// the failure these tests are here for, and it is invisible from the version
// table, which is updated either way.
func informationSchemaHas(t *testing.T, db *sql.DB, view, field, table, name string) bool {
	t.Helper()
	var count int
	query := "SELECT COUNT(*) FROM information_schema." + view +
		" WHERE table_schema = DATABASE() AND table_name = ? AND " + field + " = ?"
	if err := db.QueryRowContext(context.Background(), query, table, name).Scan(&count); err != nil {
		t.Fatalf("ask information_schema.%s about %s.%s: %v", view, table, name, err)
	}
	return count > 0
}
