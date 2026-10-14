package workspace

// Taking it back, and reading the table back (§2.13).
//
// Recovery is the design's trade made visible. There is no rollback, because a
// transaction cannot span a ZIP, so what a recovery can undo is exactly what the
// job wrote down — which is why `written_json` is written after every batch and
// why these cases are about the record rather than about a transaction. The two
// refusals matter as much as the successes: a committed job's work is kept, and a
// job that cannot be taken back all the way is left failed rather than marked
// recovered, because a row saying "taken back" over rows that are still there is
// the one thing an audit of this feature would be reading for.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"backend/internal/audit"
	"backend/internal/authz"
)

// failedImport is a job that wrote three of its four objects and stopped. Three
// is the number that makes the rest of this file interesting: it is not zero, so
// there is something to take back, and it is not all of them, so a recovery that
// deleted by the package's manifest instead of by the record would be caught.
func failedImport(t *testing.T) (*importHarness, Job) {
	t.Helper()
	app := newImportHarness(t)
	app.writer.failOn, app.writer.keepOn = 2, 1
	preview := app.preview(t)
	job, err := app.service.Import(context.Background(), CommitRequest{Actor: importer(), JobID: preview.JobID})
	if err == nil {
		t.Fatalf("Import() error = nil, want the writer's failure")
	}
	if len(job.Written) != 3 {
		t.Fatalf("the job records %v, want the three that landed", refsOf(job.Written))
	}
	return app, job
}

func TestRecoverTakesBackExactlyWhatWasRecorded(t *testing.T) {
	app, failed := failedImport(t)

	recovered, err := app.service.Recover(context.Background(), RecoverRequest{Actor: importer(), JobID: failed.ID})
	if err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	if recovered.State != StateRecovered || recovered.FailureCode != "" {
		t.Fatalf("the job ended %s (%q), want recovered with no failure code", recovered.State, recovered.FailureCode)
	}
	if len(app.writer.undone) != 1 {
		t.Fatalf("the writer was asked to undo %d times, want once", len(app.writer.undone))
	}
	// What was taken back is what was recorded, ref for ref and in order. Not
	// "everything the package held": the object that never landed was never
	// written down, and deleting by the manifest would delete a row this import
	// did not create.
	if !sameRefs(refsOf(app.writer.undone[0]), refsOf(failed.Written)) {
		t.Errorf("took back %v, want the %v that landed", refsOf(app.writer.undone[0]), refsOf(failed.Written))
	}
	// The action is its own, not the import's with a reason: this is the one
	// action in the audit's list that removes somebody's rows (§2.6), and an
	// auditor reading "imported, ok" beside a failed import would be reading the
	// opposite of what happened.
	last := app.auditor.events[len(app.auditor.events)-1]
	if last.Action != audit.ActionWorkspaceRecovered || last.Result != audit.ResultOK {
		t.Fatalf("the entry is %s/%s, want workspace.recovered/ok", last.Action, last.Result)
	}
	if last.Target.ID != failed.ID {
		t.Errorf("the entry points at %s, want the job", last.Target.ID)
	}
	if want := countsOfRefs(refsOf(failed.Written)); !sameCounts(last.Detail.Counts, want) {
		t.Errorf("the entry's counts are %v, want %v", last.Detail.Counts, want)
	}
	if stored := app.repository.jobs[failed.ID]; stored.State != StateRecovered {
		t.Errorf("the stored job is %s, want recovered", stored.State)
	}
}

func TestRecoverRefusesAJobThatCommitted(t *testing.T) {
	app := newImportHarness(t)
	preview := app.preview(t)
	if _, err := app.service.Import(context.Background(), CommitRequest{Actor: importer(), JobID: preview.JobID}); err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	// The work is kept. A recovery here would be a delete wearing recovery's name,
	// and the refusal is what makes the button safe to put on a screen.
	if _, err := app.service.Recover(context.Background(), RecoverRequest{Actor: importer(), JobID: preview.JobID}); !errors.Is(err, ErrNotRecoverable) {
		t.Fatalf("Recover() error = %v, want ErrNotRecoverable", err)
	}
	if len(app.writer.undone) != 0 {
		t.Errorf("a committed job had %d batches taken back", len(app.writer.undone))
	}
	// Its row is untouched, so the screen still shows a committed import.
	if stored := app.repository.jobs[preview.JobID]; stored.State != StateCommitted {
		t.Errorf("the stored job is %s, want it left committed", stored.State)
	}
}

func TestRecoverRefusesAJobThatNeverRan(t *testing.T) {
	app := newImportHarness(t)
	preview := app.preview(t)
	// A job still in preview has written nothing, so there is nothing to take
	// back: the refusal is the same shape as the committed one and for the same
	// reason.
	if _, err := app.service.Recover(context.Background(), RecoverRequest{Actor: importer(), JobID: preview.JobID}); !errors.Is(err, ErrNotRecoverable) {
		t.Fatalf("Recover() error = %v, want ErrNotRecoverable", err)
	}
}

func TestRecoverRefusesWhileSomethingIsRunning(t *testing.T) {
	app, failed := failedImport(t)
	app.busy.running = 1
	if _, err := app.service.Recover(context.Background(), RecoverRequest{Actor: importer(), JobID: failed.ID}); !errors.Is(err, ErrBusy) {
		t.Fatalf("Recover() error = %v, want ErrBusy", err)
	}
	// Recovery deletes, so it is a write, and it waits for the same quiet the
	// commit waits for.
	if len(app.writer.undone) != 0 {
		t.Errorf("a refused recovery took %d batches back", len(app.writer.undone))
	}
	if stored := app.repository.jobs[failed.ID]; stored.State != StateFailed {
		t.Errorf("the stored job is %s after a refusal, want it left failed", stored.State)
	}
}

// TestRecoverLeavesTheJobFailedWhenItCannotTakeEverythingBack: a half-done undo
// must not become a row saying it was undone.
func TestRecoverLeavesTheJobFailedWhenItCannotTakeEverythingBack(t *testing.T) {
	app, failed := failedImport(t)
	app.writer.undoErr = errors.New("the session store would not delete")

	job, err := app.service.Recover(context.Background(), RecoverRequest{Actor: importer(), JobID: failed.ID})
	if err == nil {
		t.Fatalf("Recover() error = nil, want the store's failure")
	}
	if !strings.Contains(err.Error(), "the session store would not delete") {
		t.Errorf("error = %v, want the cause to survive", err)
	}
	// The job stays failed with its record whole, so the button can be pressed
	// again and the second attempt starts from the same list.
	if job.State != StateFailed || job.FailureCode != FailureWriteFailed {
		t.Fatalf("the job is %s (%s), want it left failed", job.State, job.FailureCode)
	}
	if stored := app.repository.jobs[failed.ID]; stored.State != StateFailed || len(stored.Written) != 3 {
		t.Errorf("the stored job is %s with %d recorded rows, want it left failed and whole",
			stored.State, len(stored.Written))
	}
}

func TestRecoverRefusesSomebodyElsesJob(t *testing.T) {
	app, failed := failedImport(t)
	other := Actor{Caller: authz.Caller{UserID: 9, Role: authz.RoleUser}, Name: "bob"}
	if _, err := app.service.Recover(context.Background(), RecoverRequest{Actor: other, JobID: failed.ID}); !errors.Is(err, ErrNoSuchJob) {
		t.Fatalf("Recover() error = %v, want the same not-found a stranger gets", err)
	}
	if len(app.writer.undone) != 0 {
		t.Errorf("a stranger's recovery took %d batches back", len(app.writer.undone))
	}
}

// ── the listing ─────────────────────────────────────────────────────────────

func TestListAnswersOneDirectionAtATimeForOneAccount(t *testing.T) {
	app := newImportHarness(t)
	if _, err := app.service.Export(context.Background(), ExportRequest{Actor: importer(), Refs: sampleRefs()}); err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	preview := app.preview(t)

	exports, err := app.service.List(context.Background(), ListRequest{OwnerID: 7, Kind: JobExport})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(exports) != 1 || exports[0].Kind != JobExport {
		t.Fatalf("the export list is %+v, want the one export", exports)
	}
	imports, err := app.service.List(context.Background(), ListRequest{OwnerID: 7, Kind: JobImport})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(imports) != 1 || imports[0].ID != preview.JobID {
		t.Fatalf("the import list is %+v, want the one preview", imports)
	}
	// The owner is the account and never a parameter, so another account's read
	// finds nothing of this one's.
	if theirs, err := app.service.List(context.Background(), ListRequest{OwnerID: 9, Kind: JobExport}); err != nil || len(theirs) != 0 {
		t.Errorf("another account's list is %+v (%v), want nothing", theirs, err)
	}
}

func TestListRefusesAnAccountlessOrDirectionlessRead(t *testing.T) {
	app := newImportHarness(t)
	if _, err := app.service.List(context.Background(), ListRequest{Kind: JobExport}); !errors.Is(err, ErrNoActor) {
		t.Errorf("List() without an account did not refuse")
	}
	if _, err := app.service.List(context.Background(), ListRequest{OwnerID: 7}); err == nil {
		t.Errorf("List() without a direction did not refuse: the two directions are two lists")
	}
	if _, err := app.service.List(context.Background(), ListRequest{OwnerID: 7, Kind: "transfer"}); err == nil {
		t.Errorf("List() with a direction the table does not have did not refuse")
	}
}

// TestListClampsWhatItIsAskedFor: the list is an unbounded table, so a client
// that asked for everything would get an answer that grew with the account's
// history. The bound is the service's rather than the store's, so that a caller
// cannot ask past it.
func TestListClampsWhatItIsAskedFor(t *testing.T) {
	app := newImportHarness(t)
	for index := 0; index < 3; index++ {
		if _, err := app.service.Export(context.Background(), ExportRequest{Actor: importer(), Refs: sampleRefs()}); err != nil {
			t.Fatalf("Export() error = %v", err)
		}
	}
	limited, err := app.service.List(context.Background(), ListRequest{OwnerID: 7, Kind: JobExport, Limit: 2})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(limited) != 2 {
		t.Errorf("a list limited to two answered %d", len(limited))
	}
	// A negative limit is the default rather than everything, which is the one
	// reading of it that cannot be a client accidentally asking for its whole
	// history.
	byDefault, err := app.service.List(context.Background(), ListRequest{OwnerID: 7, Kind: JobExport, Limit: -1})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(byDefault) != 3 {
		t.Errorf("a negative limit answered %d of the three", len(byDefault))
	}
}
