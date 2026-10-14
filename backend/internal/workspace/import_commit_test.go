package workspace

// The commit: what actually lands, and under what refusals (§2.13).
//
// Two of the claims here are about *ordering*, and an order is not visible in a
// result. It is read from the fakes' own records — the sequence the repository
// was written in, and which objects each write call carried — which is the only
// place the order of two things that both happened is still there.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"backend/internal/audit"
	"backend/internal/authz"
)

func TestImportWritesExactlyWhatThePreviewCalledCreatable(t *testing.T) {
	app := newImportHarness(t)
	app.writer.conflicts = map[Ref]struct{}{{Kind: KindSession, ID: "ses_1"}: {}}
	preview := app.preview(t)

	job, err := app.service.Import(context.Background(), CommitRequest{Actor: importer(), JobID: preview.JobID})
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if job.State != StateCommitted {
		t.Fatalf("the job ended %s, want committed", job.State)
	}
	// The commit plans again rather than replaying the preview's list, because the
	// world moves between two requests — and what it writes is the second plan's
	// creates. The object called a conflict at preview time is not written, which
	// is §2.13's answer to a collision: leave it alone, never replace it.
	creatable := createsOf(preview.Items)
	if len(app.writer.asked) != len(creatable) {
		t.Fatalf("the commit wrote %v, want the %d the preview called creatable", app.writer.asked, len(creatable))
	}
	for _, ref := range app.writer.asked {
		if !hasRef(creatable, ref) {
			t.Errorf("the commit wrote %s, which the preview did not call creatable", ref)
		}
	}
	for _, item := range preview.Items {
		if item.Action == ActionCreate && !contains(app.writer.asked, item.Ref) {
			t.Errorf("the preview called %s creatable and the commit never wrote it", item.Ref)
		}
	}
	if refs := refsOf(job.Written); !sameRefs(refs, creatableRefs(preview.Items)) {
		t.Errorf("the job records %v, want %v", refs, creatableRefs(preview.Items))
	}
	if want := countsOfRefs(creatableRefs(preview.Items)); !sameCounts(job.Counts, want) {
		t.Errorf("the job's counts are %v, want %v", job.Counts, want)
	}
	stored := app.repository.jobs[preview.JobID]
	if stored.State != StateCommitted {
		t.Errorf("the stored job is %s, want committed", stored.State)
	}
	if want := countsOfRefs(creatableRefs(preview.Items)); !sameCounts(stored.Counts, want) {
		t.Errorf("the stored counts are %v, want %v, the rows really written", stored.Counts, want)
	}
	// One audit entry, and it is the import's action rather than the export's: the
	// two directions are two actions (§2.6).
	if len(app.auditor.events) != 1 {
		t.Fatalf("the audit holds %d entries, want the one the commit wrote", len(app.auditor.events))
	}
	event := app.auditor.events[0]
	if event.Action != audit.ActionWorkspaceImported || event.Result != audit.ResultOK {
		t.Errorf("the entry is %s/%s, want workspace.imported/ok", event.Action, event.Result)
	}
	if event.Target.Kind != audit.TargetWorkspaceJob || event.Target.ID != preview.JobID {
		t.Errorf("the entry points at %+v, want the job", event.Target)
	}
}

// TestImportRecordsWhatLandedAfterEveryBatch is §2.13's paragraph read as an
// order of operations.
//
// The case runs two batches of two over four objects and fails the second batch
// after its first object. So the first batch landed whole, the second landed
// half, and the fourth object never reached the writer. A version that wrote the
// record once at the end would record nothing at all here, which is the version
// this case exists to fail.
func TestImportRecordsWhatLandedAfterEveryBatch(t *testing.T) {
	app := newImportHarness(t)
	app.writer.failOn, app.writer.keepOn = 2, 1
	preview := app.preview(t)

	job, err := app.service.Import(context.Background(), CommitRequest{Actor: importer(), JobID: preview.JobID})
	if err == nil {
		t.Fatalf("Import() error = nil, want the writer's failure")
	}
	if job.State != StateFailed || job.FailureCode != FailureWriteFailed {
		t.Fatalf("the job ended %s (%s), want failed/write_failed", job.State, job.FailureCode)
	}
	// The writer is handed the objects in the package's own order, so which object
	// was lost is not a matter of chance: it is the last one.
	lost := Ref{Kind: KindSession, ID: "ses_2"}
	landed := refsOf(job.Written)
	if len(landed) != 3 {
		t.Fatalf("the job records %v, want the three that landed", landed)
	}
	if contains(landed, lost) {
		t.Errorf("the job records %s, which the failing batch never wrote", lost)
	}
	if app.writer.writes != 2 {
		t.Errorf("the writer was called %d times, want the two batches and no third", app.writer.writes)
	}
	// And the record was written more than once. The repository's own log is the
	// order the writes happened in, so what is counted is the empty write the
	// commit clears the column with and one per batch.
	writes := 0
	for _, name := range app.repository.written {
		if name == "written" {
			writes++
		}
	}
	if writes < 3 {
		t.Errorf("written_json was written %d times over two batches, want it after every one", writes)
	}
	if stored := app.repository.jobs[preview.JobID]; len(stored.Written) != 3 {
		t.Errorf("the stored record holds %d rows, want the three that landed", len(stored.Written))
	}
	// The line for the failure carries what the job really wrote rather than the
	// package's totals the preview recorded. "an import of four objects, failed"
	// would be a row whose numbers say the opposite of what happened.
	event := app.auditor.events[len(app.auditor.events)-1]
	if event.Result != audit.ResultFailed || event.Detail.Reason != FailureWriteFailed {
		t.Fatalf("the entry is %s/%s (%s), want failed with the code", event.Result, event.Action, event.Detail.Reason)
	}
	if event.Detail.Counts[string(KindSession)] != 1 {
		t.Errorf("the entry's counts are %v, want the rows really written", event.Detail.Counts)
	}
}

// TestImportLandsNothingWhenThePackageDoesNotMatchItsManifest is the sha256
// claim, and the case is about the emptiness behind the failure: the check runs
// before the first write rather than beside it.
func TestImportLandsNothingWhenThePackageDoesNotMatchItsManifest(t *testing.T) {
	app := newImportHarness(t)
	preview := app.preview(t)

	// One object edited, and the manifest left exactly as it was.
	staged := app.staged(t, preview.JobID)
	tampered := rewriteMember(t, readFile(t, staged), MemberName(Ref{Kind: KindSession, ID: "ses_1"}),
		func([]byte) []byte { return []byte(`{"session":{"title":"tampered"}}`) })
	if err := os.WriteFile(staged, tampered, 0o600); err != nil {
		t.Fatalf("put the tampered package back: %v", err)
	}

	job, err := app.service.Import(context.Background(), CommitRequest{Actor: importer(), JobID: preview.JobID})
	if err == nil {
		t.Fatalf("Import() committed a package that does not match its own manifest")
	}
	if !errors.Is(err, ErrDigestMismatch) {
		t.Errorf("error = %v, want it to name the mismatch", err)
	}
	if job.State != StateFailed || job.FailureCode != FailureVerifyFailed {
		t.Fatalf("the job ended %s (%s), want failed/verify_failed", job.State, job.FailureCode)
	}
	if app.writer.writes != 0 {
		t.Errorf("the commit attempted %d batches of a package it had not verified", app.writer.writes)
	}
	if len(job.Written) != 0 {
		t.Errorf("the job records %v, want nothing", job.Written)
	}
}

// TestImportRefusesWhileSomethingIsRunning is AC-09's first sentence.
func TestImportRefusesWhileSomethingIsRunning(t *testing.T) {
	app := newImportHarness(t)
	preview := app.preview(t)
	app.busy.running = 3

	_, err := app.service.Import(context.Background(), CommitRequest{Actor: importer(), JobID: preview.JobID})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("Import() error = %v, want ErrBusy", err)
	}
	if !strings.Contains(err.Error(), "3") {
		t.Errorf("error = %v, want it to say how many are running", err)
	}
	if app.writer.writes != 0 {
		t.Errorf("a refused commit wrote %d batches", app.writer.writes)
	}
	// A refusal is not a failure: nothing was attempted, so there is nothing to
	// recover and the job is exactly where it was — a user told to wait can press
	// the button again on the same preview.
	if stored := app.repository.jobs[preview.JobID]; stored.State != StatePreview {
		t.Errorf("the stored job is %s after a refusal, want it left in preview", stored.State)
	}
	if len(app.auditor.events) != 0 {
		t.Errorf("a refusal was audited %d times, want none: no job failed", len(app.auditor.events))
	}
}

func TestImportRefusesAJobThatIsNotTheCallersPreview(t *testing.T) {
	app := newImportHarness(t)
	// An export's id is a real id in the caller's own table, which is what makes it
	// the case a guessed path id runs into.
	exported, err := app.service.Export(context.Background(), ExportRequest{Actor: importer(), Refs: sampleRefs()})
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if _, err := app.service.Import(context.Background(), CommitRequest{Actor: importer(), JobID: exported.ID}); !errors.Is(err, ErrNoSuchJob) {
		t.Errorf("Import() error = %v, want ErrNoSuchJob for the other direction's job", err)
	}

	preview := app.preview(t)
	other := Actor{Caller: authz.Caller{UserID: 9, Role: authz.RoleUser}, Name: "bob"}
	if _, err := app.service.Import(context.Background(), CommitRequest{Actor: other, JobID: preview.JobID}); !errors.Is(err, ErrNoSuchJob) {
		t.Errorf("Import() error = %v, want the same not-found a stranger gets", err)
	}
}

// TestImportRefusesAJobThatHasAlreadyRun is §7.3's ninth item from the other
// side: there is no "carry on from where it stopped", so a job that has run is
// not a job to run again.
func TestImportRefusesAJobThatHasAlreadyRun(t *testing.T) {
	app := newImportHarness(t)
	preview := app.preview(t)
	if _, err := app.service.Import(context.Background(), CommitRequest{Actor: importer(), JobID: preview.JobID}); err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	// What the first commit cost is the harness's own business — four objects at a
	// batch size of two is two calls — and the question this case asks is whether
	// the second commit cost anything at all. Counting from the first commit's
	// total rather than from zero is what keeps the two questions apart.
	afterFirst := app.writer.writes
	if _, err := app.service.Import(context.Background(), CommitRequest{Actor: importer(), JobID: preview.JobID}); !errors.Is(err, ErrNotPreview) {
		t.Fatalf("Import() error = %v, want ErrNotPreview", err)
	}
	if app.writer.writes != afterFirst {
		t.Errorf("the refused commit called the writer %d times, want none past the %d the first one cost",
			app.writer.writes-afterFirst, afterFirst)
	}
}

func TestPreviewAndImportRefuseAnActorlessRequest(t *testing.T) {
	app := newImportHarness(t)
	preview := app.preview(t)
	if _, err := app.service.Import(context.Background(), CommitRequest{JobID: preview.JobID}); !errors.Is(err, ErrNoActor) {
		t.Errorf("Import() error = %v, want ErrNoActor", err)
	}
	if _, err := app.service.Preview(context.Background(), PreviewRequest{
		Package: app.upload(t), Size: int64(len(app.bytes)),
	}); !errors.Is(err, ErrNoActor) {
		t.Errorf("Preview() error = %v, want ErrNoActor", err)
	}
}

// The clock is read here rather than beside the export's cases because it is the
// preview that first dates an import, and a job stamped from anywhere but the
// service's clock would order a list wrongly.
func TestTheJobTimesComeFromTheClockTheServiceWasGiven(t *testing.T) {
	app := newImportHarness(t)
	preview := app.preview(t)
	fixed := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	job := app.repository.jobs[preview.JobID]
	if !job.CreatedAt.Equal(fixed) || !job.UpdatedAt.Equal(fixed) {
		t.Errorf("the job is dated %s/%s, want the service's clock at %s", job.CreatedAt, job.UpdatedAt, fixed)
	}
}
