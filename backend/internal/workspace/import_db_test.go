package workspace

// The way in, against a real seekdb.
//
// A preview and a commit are two statements about rows, and the second one is
// what makes §2.13's recovery possible at all: the record of what landed has to
// be written while those rows are really there, because a recovery driven by it
// deletes what it names. A fake job table can restate all of that and agree with
// itself; only the table can say whether the record and the rows are the same
// fact, and whether a commit that failed halfway really left the half it
// recorded.
//
// One database per package, so nothing here calls t.Parallel.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"backend/internal/data"
	"backend/internal/data/models"
	"backend/internal/session"
	"backend/internal/turn"
)

// ── the cases ───────────────────────────────────────────────────────────────

// TestDBAPreviewWritesNothingButItsOwnJob is §2.13's first discipline said as a
// counting: every table is counted before and after, and the only one that moved
// is the job table, by exactly one row.
//
// It is a count rather than "no error was returned" because those are different
// claims, and it is per table rather than in total because two tables moving in
// opposite directions would pass a total.
func TestDBAPreviewWritesNothingButItsOwnJob(t *testing.T) {
	app := newDBImportHarness(t)
	pkg := conversationPackage(t, "ses_1", "ses_2")

	before := countsInDB(t, app.resource)
	preview := app.preview(t, pkg)
	after := countsInDB(t, app.resource)

	for table, rows := range before {
		want := rows
		if table == "workspace_jobs" {
			want = rows + 1
		}
		if after[table] != want {
			t.Errorf("%s holds %d rows after the preview, want %d", table, after[table], want)
		}
	}
	// The job is not a side effect to be apologised for: it is what the commit
	// names, so it has to be a real row with the package's own manifest on it.
	job, err := app.store.Find(context.Background(), preview.JobID)
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if job.State != StatePreview || job.Kind != JobImport || job.OwnerID != app.importer.UserID {
		t.Fatalf("the preview ended as %+v", job)
	}
	if len(job.Manifest) != 2 {
		t.Fatalf("the row names %d objects, want the two that were uploaded", len(job.Manifest))
	}
	// And the bytes are on this machine, because the commit reads them back from
	// the archive root rather than from the client.
	if _, err := os.Stat(app.staged(t, preview.JobID)); err != nil {
		t.Errorf("the previewed package is not in the archive root: %v", err)
	}
}

// TestDBTheCommitWritesTheRowsAndRecordsThem is the round trip: what the
// package held is in the tables, owned by the account that asked, and the job
// row says the same thing the returned job does.
func TestDBTheCommitWritesTheRowsAndRecordsThem(t *testing.T) {
	app := newDBImportHarness(t)
	preview := app.preview(t, conversationPackage(t, "ses_1", "ses_2"))

	job, err := app.commit(t, preview.JobID)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if job.State != StateCommitted || job.FailureCode != "" {
		t.Fatalf("the job ended %s (%s), want committed", job.State, job.FailureCode)
	}
	if rows := rowCount(t, app.resource, "sessions"); rows != 2 {
		t.Errorf("sessions holds %d rows, want the two the package carried", rows)
	}
	if rows := rowCount(t, app.resource, "messages"); rows != 2 {
		t.Errorf("messages holds %d rows, want the two the package carried", rows)
	}
	// The owner is the caller's and not the package's: the payload above says
	// owner zero, and a row that landed ownerless would be a row nobody's.
	var owner *int64
	if err := app.resource.GORM().Raw("SELECT owner_id FROM sessions WHERE session_id = ?", "ses_1").
		Scan(&owner).Error; err != nil {
		t.Fatalf("read the session's owner: %v", err)
	}
	if owner == nil || *owner != app.importer.UserID {
		t.Fatalf("the session landed owned by %v, want account %d", owner, app.importer.UserID)
	}
	// The counts a commit records are rows really written, per kind — and the
	// number the preview recorded (the package's own totals) is the number it
	// replaced, so an import that wrote nothing has to say nothing.
	if job.Counts[string(KindSession)] != 2 || len(job.Counts) != 1 {
		t.Errorf("the job counts %v, want the two conversations and nothing else", job.Counts)
	}
	if len(job.Written) != 2 {
		t.Fatalf("the job names %d written rows, want the two that landed", len(job.Written))
	}

	// The row and the returned job are the same fact, read twice.
	stored, err := app.store.Find(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if stored.State != job.State || !sameRefs(refsOf(stored.Written), refsOf(job.Written)) ||
		!sameCounts(stored.Counts, job.Counts) {
		t.Errorf("the row says %s/%v/%v and the answer said %s/%v/%v",
			stored.State, stored.Written, stored.Counts, job.State, job.Written, job.Counts)
	}
}

// TestDBTheCommitRefusesAPackageSwappedAfterThePreview is what the second
// verification buys. The staged bytes are edited on the disk the way a swap
// would edit them, and the commit has to write nothing at all — not the part of
// the package that still matches its manifest.
func TestDBTheCommitRefusesAPackageSwappedAfterThePreview(t *testing.T) {
	app := newDBImportHarness(t)
	preview := app.preview(t, conversationPackage(t, "ses_1", "ses_2"))

	staged := app.staged(t, preview.JobID)
	edited, err := os.ReadFile(staged)
	if err != nil {
		t.Fatalf("read the staged package: %v", err)
	}
	at := bytes.Index(edited, []byte("why is the sky blue"))
	if at < 0 {
		t.Fatalf("the staged package does not hold the text to edit")
	}
	// One byte of one object. The manifest member is untouched, so the package
	// still claims the digest it was written with — which is exactly what a
	// package that was swapped for a different one would look like.
	//
	// The byte has to be one the text does not already hold: overwriting the 'w'
	// of "why" with a 'w' leaves the digest it was written with still true, and
	// the commit that follows would be the one the test expects to be refused.
	edited[at] = 'x'
	if err := os.WriteFile(staged, edited, 0o600); err != nil {
		t.Fatalf("write the staged package back: %v", err)
	}

	job, err := app.commit(t, preview.JobID)
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("Import() error = %v, want a digest mismatch", err)
	}
	if job.State != StateFailed || job.FailureCode != FailureVerifyFailed {
		t.Fatalf("the job ended %s (%s), want failed/verify_failed", job.State, job.FailureCode)
	}
	for _, table := range []string{"sessions", "messages"} {
		if rows := rowCount(t, app.resource, table); rows != 0 {
			t.Errorf("%s holds %d rows after a commit that verified nothing", table, rows)
		}
	}
	// A job that wrote nothing names nothing — which is what tells a client
	// there is nothing to recover rather than a recovery that would be a no-op.
	stored, err := app.store.Find(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if len(stored.Written) != 0 {
		t.Errorf("the record names %v, and nothing was written", stored.Written)
	}
}

// TestDBTheCommitIsRefusedWhileATurnIsRunning is §2.13's second discipline
// end-to-end: a real turn in the active state, a real count of it, and an import
// that leaves the account exactly as it was — still previewed, so the caller can
// come back and commit.
func TestDBTheCommitIsRefusedWhileATurnIsRunning(t *testing.T) {
	app := newDBImportHarness(t)
	// The deployment's busy check answers with a count of the turns table; this
	// is that query over the same rows.
	app.service.busy = countingTurns{resource: app.resource}

	sessions := session.NewGORMRepository(app.resource.GORM())
	if err := sessions.Create(context.Background(), session.Session{
		ID: "ses_busy", Title: "something is happening", OwnerID: app.importer.UserID,
	}); err != nil {
		t.Fatalf("write the conversation the turn hangs off: %v", err)
	}
	turns := turn.NewGORMRepository(app.resource.GORM())
	if err := turns.Start(context.Background(), turn.Turn{
		ID: "turn_running", SessionID: "ses_busy", Status: turn.StatusRunning,
		StateVersion: 1, CreatedAt: atSecond(1),
	}); err != nil {
		t.Fatalf("start a turn: %v", err)
	}

	preview := app.preview(t, conversationPackage(t, "ses_1"))
	// The preview is allowed while something runs — it reads and writes no rows
	// of the account's, and asking whether an import would fit is exactly what a
	// client does before deciding to make one.
	if _, err := app.commit(t, preview.JobID); !errors.Is(err, ErrBusy) {
		t.Fatalf("Import() error = %v, want a busy refusal", err)
	}
	if rows := rowCount(t, app.resource, "sessions"); rows != 1 {
		t.Errorf("sessions holds %d rows, want only the one the turn hangs off", rows)
	}
	job, err := app.store.Find(context.Background(), preview.JobID)
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if job.State != StatePreview {
		t.Errorf("the refused commit left the job %s, want it still previewed", job.State)
	}
}

// countingTurns counts the rows in the three statuses that mean a turn has not
// finished. It is the deployment's query, written out here because the port is
// what the service talks to and the deployment's own lives next door in package
// main (where it is pinned).
type countingTurns struct{ resource *data.Resource }

func (c countingTurns) Running(ctx context.Context) (int64, error) {
	active := []string{
		string(turn.StatusQueued), string(turn.StatusRunning), string(turn.StatusWaitingInput),
	}
	var count int64
	err := c.resource.GORM().WithContext(ctx).Model(&models.Turn{}).
		Where("status IN ?", active).Count(&count).Error
	if err != nil {
		return 0, err
	}
	return count, nil
}
