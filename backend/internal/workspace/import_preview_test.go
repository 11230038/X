package workspace

// The preview: what a package would do before anything of the account's is
// touched (§2.13).
//
// The claims here are the ones the design spends its paragraphs on — a preview
// writes nothing, it reports a conflict as an entry in a list rather than as a
// refusal, and it is the one import call allowed while something else runs.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestPreviewSaysWhatWouldBeWrittenAndWritesNothingOfTheAccount(t *testing.T) {
	app := newImportHarness(t)
	preview := app.preview(t)

	if preview.Objects != len(importObjects()) {
		t.Fatalf("the preview is of %d objects, want %d", preview.Objects, len(importObjects()))
	}
	if preview.Actions[string(ActionCreate)] != len(importObjects()) {
		t.Errorf("the preview calls %v creatable, want all %d", preview.Actions, len(importObjects()))
	}
	for _, item := range preview.Items {
		if item.Action != ActionCreate || item.Reason != "" || len(item.Needs) != 0 {
			t.Errorf("item %+v, want a bare create", item)
		}
	}
	// The only row a preview leaves behind is its own job, and it is the only one:
	// the four domains are reached through the writer, and all a preview may do to
	// them is ask. Not a single write call, and nothing audited — nothing has
	// happened yet, and an audit line for a preview would say it had.
	if app.writer.writes != 0 {
		t.Fatalf("the preview wrote %d batches", app.writer.writes)
	}
	if len(app.writer.plans) != 1 {
		t.Errorf("the writer was planned %d times, want once", len(app.writer.plans))
	}
	if len(app.auditor.events) != 0 {
		t.Errorf("the preview audited %d events, want none", len(app.auditor.events))
	}
	if len(app.repository.jobs) != 1 {
		t.Fatalf("the preview left %d rows, want its own job", len(app.repository.jobs))
	}
	job := app.repository.jobs[preview.JobID]
	if job.State != StatePreview || job.Kind != JobImport || job.OwnerID != 7 {
		t.Errorf("the job is %s/%s for %d, want a previewed import of the actor's", job.Kind, job.State, job.OwnerID)
	}
	if len(job.Manifest) != len(importObjects()) {
		t.Errorf("the job records %d objects, want the package's %d", len(job.Manifest), len(importObjects()))
	}
	if job.Counts[string(KindSession)] != 2 {
		t.Errorf("the previewed counts are %v, want what the package holds", job.Counts)
	}
	if _, err := os.Stat(app.staged(t, preview.JobID)); err != nil {
		t.Errorf("the preview staged no package: %v", err)
	}
}

// TestPreviewReportsConflictsAndMissesAsItems is §7.3's eighth item: a conflict
// is an entry in a list, not a refusal of the request.
func TestPreviewReportsConflictsAndMissesAsItems(t *testing.T) {
	app := newImportHarness(t)
	app.writer.conflicts = map[Ref]struct{}{{Kind: KindSession, ID: "ses_1"}: {}}
	app.writer.missing = map[Ref][]Ref{
		{Kind: KindQuestion, ID: "q_1"}: {{Kind: KindMaterial, ID: "mat_9"}},
	}

	preview := app.preview(t)
	actions := make(map[Ref]Action, len(preview.Items))
	for _, item := range preview.Items {
		actions[item.Ref] = item.Action
		switch item.Action {
		case ActionMissing:
			if item.Reason != ReasonDependsOn || len(item.Needs) != 1 || item.Needs[0].ID != "mat_9" {
				t.Errorf("the miss is %+v, want it to name the material it needs", item)
			}
		case ActionConflict:
			if item.Reason != ReasonAlreadyPresent {
				t.Errorf("the conflict is %+v, want the conflict's own reason", item)
			}
		}
	}
	if actions[Ref{Kind: KindSession, ID: "ses_1"}] != ActionConflict {
		t.Errorf("an object this instance already has was not called a conflict")
	}
	if actions[Ref{Kind: KindQuestion, ID: "q_1"}] != ActionMissing {
		t.Errorf("an object missing what it depends on was not called a miss")
	}
	if actions[Ref{Kind: KindSession, ID: "ses_2"}] != ActionCreate {
		t.Errorf("an object with nothing against it was not called creatable")
	}
	// The three counts are the package's own totals broken down, so they add up to
	// it: a screen whose headline disagreed with its list would be a screen a user
	// could not check.
	sum := 0
	for _, count := range preview.Actions {
		sum += count
	}
	if sum != preview.Objects {
		t.Errorf("the actions add up to %d and the package holds %d", sum, preview.Objects)
	}
}

func TestPreviewRefusesBytesThatAreNotAPackage(t *testing.T) {
	app := newImportHarness(t)
	note := strings.NewReader("this is a note, not a package")
	if _, err := app.service.Preview(context.Background(), PreviewRequest{
		Actor: importer(), Package: note, Size: int64(note.Len()),
	}); !errors.Is(err, ErrBadPackage) {
		t.Fatalf("Preview() error = %v, want ErrBadPackage", err)
	}
	// Bytes that are not a package are not a failed preview: there was nothing to
	// look at, so there is nothing left to explain. It is the same call an export
	// makes for an empty selection.
	if len(app.repository.jobs) != 0 || len(app.auditor.events) != 0 {
		t.Errorf("a refused preview left a job or an audit entry behind")
	}
	left, err := os.ReadDir(app.root)
	if err != nil {
		t.Fatalf("read the archive root: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("a refused preview left %d files in the archive root", len(left))
	}
}

func TestPreviewRefusesAPackageOverTheBound(t *testing.T) {
	app := newImportHarness(t, func(o *Options) { o.MaxArchiveBytes = 64 })
	if _, err := app.service.Preview(context.Background(), PreviewRequest{
		Actor: importer(), Package: app.upload(t), Size: int64(len(app.bytes)),
	}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Preview() error = %v, want ErrTooLarge", err)
	}
	if len(app.repository.jobs) != 0 {
		t.Errorf("an over-large upload left a job behind")
	}
}

// TestPreviewIsAllowedWhileSomethingIsRunning: the refusal belongs to the two
// calls that write. A preview is what a user does before deciding, which is
// exactly when they may be in the middle of something else.
func TestPreviewIsAllowedWhileSomethingIsRunning(t *testing.T) {
	app := newImportHarness(t)
	app.busy.running = 2
	if preview := app.preview(t); preview.JobID == "" {
		t.Fatalf("the preview answered no job")
	}
	if app.busy.asked != 0 {
		t.Errorf("the preview asked whether anything was running, which it has no business doing")
	}
}
