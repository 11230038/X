package workspace

import (
	"context"
	"errors"
	"testing"
)

// TestDBAPartialWriteIsRecordedAndTakenBack is §2.13's trade in one case: the
// batch fails after its first object has landed, the record says so, and a
// recovery driven by that record leaves the tables as it found them.
//
// The batch size is two and the package holds two, so the failure happens inside
// one batch — which is the shape a record written at the end of the job would
// have lost entirely.
func TestDBAPartialWriteIsRecordedAndTakenBack(t *testing.T) {
	app := newDBImportHarness(t)
	app.writer.failOn = 2
	preview := app.preview(t, conversationPackage(t, "ses_1", "ses_2"))

	job, err := app.commit(t, preview.JobID)
	if err == nil {
		t.Fatal("the commit was allowed to succeed, and the writer refused its second object")
	}
	if job.State != StateFailed || job.FailureCode != FailureWriteFailed {
		t.Fatalf("the job ended %s (%s), want failed/write_failed", job.State, job.FailureCode)
	}
	// The half that landed really is in the table, with its message beside it,
	// and the half that did not really is not.
	if rows := rowCount(t, app.resource, "sessions"); rows != 1 {
		t.Errorf("sessions holds %d rows, want the one the failed batch landed", rows)
	}
	if rows := rowCount(t, app.resource, "messages"); rows != 1 {
		t.Errorf("messages holds %d rows, want the one that came with it", rows)
	}
	stored, err := app.store.Find(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if len(stored.Written) != 1 || stored.Written[0].ID != "ses_1" {
		t.Fatalf("the record names %v, want the one row that landed", stored.Written)
	}
	if stored.Counts[string(KindSession)] != 1 {
		t.Errorf("the record counts %v, want the one row that landed", stored.Counts)
	}

	// The recovery is whatever the record says, and what it says is checked
	// against the tables rather than trusted: after it, nothing of the import is
	// left, and the job says it was taken back.
	recovered, err := app.service.Recover(context.Background(),
		RecoverRequest{Actor: app.importer, JobID: job.ID})
	if err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	if recovered.State != StateRecovered {
		t.Fatalf("the job ended %s, want recovered", recovered.State)
	}
	for _, table := range []string{"sessions", "messages"} {
		if rows := rowCount(t, app.resource, table); rows != 0 {
			t.Errorf("%s holds %d rows after the recovery", table, rows)
		}
	}
}

// TestDBTheRecoveryRefusesACommittedJobAndLeavesItsRows is the other half of the
// refusal: a commit that finished is not a failure, and "take it back" would be
// a delete wearing recovery's name.
func TestDBTheRecoveryRefusesACommittedJobAndLeavesItsRows(t *testing.T) {
	app := newDBImportHarness(t)
	preview := app.preview(t, conversationPackage(t, "ses_1"))
	if _, err := app.commit(t, preview.JobID); err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	_, err := app.service.Recover(context.Background(),
		RecoverRequest{Actor: app.importer, JobID: preview.JobID})
	if !errors.Is(err, ErrNotRecoverable) {
		t.Fatalf("Recover() error = %v, want a refusal", err)
	}
	if rows := rowCount(t, app.resource, "sessions"); rows != 1 {
		t.Errorf("sessions holds %d rows, want the committed one still there", rows)
	}
}
