package workspace

// The way in: the preview and the commit (§2.13).
//
// The archive tests say what a package is and the export tests say what the
// service does to one on the way out. These say what it does on the way in, and
// the claims are the ones the design spends its paragraphs on: a preview writes
// nothing of the account's, what it says will be written is what gets written,
// nothing lands when the package does not match its own manifest, and the record
// of what has landed is written after every batch rather than once at the end.
//
// Two of those are about *ordering*, and an order is not visible in a result. It
// is read from the fakes' own records — the sequence the repository was written
// in, and which objects each write call carried — which is the only place the
// order of two things that both happened is still there.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"backend/internal/audit"
	"backend/internal/authz"
)

// ── the job table, extended for the two columns a commit owns ───────────────

func (r *fakeRepository) SaveWritten(_ context.Context, jobID string, written []Written) error {
	if err := r.record("written"); err != nil {
		return err
	}
	job, held := r.jobs[jobID]
	if !held {
		return fmt.Errorf("%w: %s", ErrNoSuchJob, jobID)
	}
	// The slice is copied because the service keeps appending to its own: a fake
	// that kept the caller's backing array would show a job whose record grew
	// without a write, and what this column is read for is the writes.
	job.Written = append([]Written(nil), written...)
	r.jobs[jobID] = job
	return nil
}

// List mirrors the real query's ordering — newest first, ties by id — rather
// than leaving the answer in map order, so that a case reading a list is reading
// the shape of answer a caller gets.
func (r *fakeRepository) List(_ context.Context, ownerID int64, kind JobKind, limit int) ([]Job, error) {
	if err := r.record("list"); err != nil {
		return nil, err
	}
	held := make([]Job, 0, len(r.jobs))
	for _, job := range r.jobs {
		if job.OwnerID == ownerID && job.Kind == kind {
			held = append(held, job)
		}
	}
	sort.Slice(held, func(first, second int) bool {
		if !held[first].CreatedAt.Equal(held[second].CreatedAt) {
			return held[first].CreatedAt.After(held[second].CreatedAt)
		}
		return held[first].ID > held[second].ID
	})
	if limit > 0 && len(held) > limit {
		held = held[:limit]
	}
	return held, nil
}

// ── the four domains behind one stub ────────────────────────────────────────

// stubWriter stands in for the four sinks at once, because what a case here is
// about is what the *service* does with their answers, and the domains
// themselves are the sinks' business rather than this file's.
type stubWriter struct {
	// conflicts and missing are what Plan calls those refs; every other ref is
	// creatable.
	conflicts map[Ref]struct{}
	missing   map[Ref][]Ref
	// writes counts the Write calls made. failOn is the call that fails, 1-based,
	// and keepOn is how many of that call's objects land before it fails — which
	// is what makes "a batch failed halfway through" a case rather than a claim.
	writes  int
	failOn  int
	keepOn  int
	plans   [][]Ref
	asked   []Ref
	batches [][]Ref
	undone  [][]Written
	undoErr error
}

func (w *stubWriter) Plan(_ context.Context, _ authz.Caller, objects []Object) ([]Item, error) {
	asked := make([]Ref, 0, len(objects))
	items := make([]Item, 0, len(objects))
	for _, object := range objects {
		asked = append(asked, object.Ref)
		switch {
		case len(w.missing[object.Ref]) != 0:
			items = append(items, Item{
				Ref: object.Ref, Action: ActionMissing,
				Reason: ReasonDependsOn, Needs: w.missing[object.Ref],
			})
		case hasRef(w.conflicts, object.Ref):
			items = append(items, Item{Ref: object.Ref, Action: ActionConflict, Reason: ReasonAlreadyPresent})
		default:
			items = append(items, Item{Ref: object.Ref, Action: ActionCreate})
		}
	}
	w.plans = append(w.plans, asked)
	return items, nil
}

func (w *stubWriter) Write(_ context.Context, _ authz.Caller, objects []Object) ([]Written, error) {
	w.writes++
	failing := w.writes == w.failOn
	batch := make([]Ref, 0, len(objects))
	written := make([]Written, 0, len(objects))
	for index, object := range objects {
		batch = append(batch, object.Ref)
		w.asked = append(w.asked, object.Ref)
		if failing && index >= w.keepOn {
			w.batches = append(w.batches, batch)
			// What landed is returned beside the error, because it is the half a
			// recovery has to be able to name and the half a record written at the
			// end of the job would have lost.
			return written, errors.New("the write layer refused the object")
		}
		written = append(written, Written{Kind: object.Kind, ID: object.ID})
	}
	w.batches = append(w.batches, batch)
	return written, nil
}

func (w *stubWriter) Undo(_ context.Context, _ authz.Caller, written []Written) error {
	if w.undoErr != nil {
		return w.undoErr
	}
	w.undone = append(w.undone, append([]Written(nil), written...))
	return nil
}

func hasRef(set map[Ref]struct{}, ref Ref) bool {
	_, held := set[ref]
	return held
}

// busyStub is "nothing is running" unless a case says otherwise.
type busyStub struct {
	running int64
	err     error
	asked   int
}

func (b *busyStub) Running(context.Context) (int64, error) {
	b.asked++
	return b.running, b.err
}

// ── one package, on the disk ────────────────────────────────────────────────

// importObjects is a package's worth of objects: the three the archive tests use
// and one question, which is the kind §2.13's miss is about. It is four rather
// than three so that a batch size of two leaves the failing batch a second
// object to lose.
func importObjects() []Object {
	return append(sampleObjects(), Object{
		Ref:  Ref{Kind: KindQuestion, ID: "q_1"},
		Data: []byte(`{"question":{"text":"why is the sky blue"}}`),
	})
}

// importHarness is the export harness with a package already written out, and
// the writer and the turn check under the case's hand.
type importHarness struct {
	*harness
	path   string
	bytes  []byte
	writer *stubWriter
	busy   *busyStub
}

func newImportHarness(t *testing.T, options ...func(*Options)) *importHarness {
	t.Helper()
	writer := &stubWriter{}
	busy := &busyStub{}
	counter := 0
	app := newHarness(t, append([]func(*Options){
		func(o *Options) { o.Writer = writer },
		func(o *Options) { o.Busy = busy },
		// The base harness answers one id for every job, which is right for a case
		// about one job and wrong for a table that holds two.
		func(o *Options) {
			o.NewJobID = func() (string, error) {
				counter++
				return fmt.Sprintf("job_%d", counter), nil
			}
		},
	}, options...)...)
	// The package is built by the writer rather than assembled to look like one,
	// so what is previewed is a package this build really produced.
	bytes := writeSample(t, importObjects())
	path := filepath.Join(t.TempDir(), "incoming.zip")
	if err := os.WriteFile(path, bytes, 0o600); err != nil {
		t.Fatalf("write the package out: %v", err)
	}
	return &importHarness{harness: app, path: path, bytes: bytes, writer: writer, busy: busy}
}

// upload is what the handler hands the service: the bytes, and how long they are.
func (h *importHarness) upload(t *testing.T) io.ReaderAt {
	t.Helper()
	file, err := os.Open(h.path)
	if err != nil {
		t.Fatalf("open the package: %v", err)
	}
	t.Cleanup(func() { file.Close() })
	return file
}

func (h *importHarness) preview(t *testing.T) Preview {
	t.Helper()
	preview, err := h.service.Preview(context.Background(), PreviewRequest{
		Actor: importer(), Package: h.upload(t), Size: int64(len(h.bytes)),
	})
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	return preview
}

// staged is where the service put the package it was given, which is the file
// the commit reads back.
func (h *importHarness) staged(t *testing.T, jobID string) string {
	t.Helper()
	job, held := h.repository.jobs[jobID]
	if !held {
		t.Fatalf("no job %s to find a package for", jobID)
	}
	return filepath.Join(h.root, job.ArchivePath)
}

func importer() Actor {
	return Actor{Caller: authz.Caller{UserID: 7, Role: authz.RoleUser}, Name: "alice"}
}

// ── the preview ─────────────────────────────────────────────────────────────

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

// ── the commit ──────────────────────────────────────────────────────────────

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

// ── small helpers over the two sets of refs ─────────────────────────────────

// creatableRefs is what the preview said would be written, in the package's own
// order — which is the order the commit is handed the objects in.
func creatableRefs(items []Item) []Ref {
	refs := make([]Ref, 0, len(items))
	for _, item := range items {
		if item.Action == ActionCreate {
			refs = append(refs, item.Ref)
		}
	}
	return refs
}

// countsOfRefs is what a job's counts column holds for a list of refs: one
// number per kind. It is here rather than in the service because it is the
// answer a case compares against, not something the code does.
func countsOfRefs(refs []Ref) map[string]int {
	counts := make(map[string]int, len(Kinds))
	for _, ref := range refs {
		counts[string(ref.Kind)]++
	}
	return counts
}

func refsOf(written []Written) []Ref {
	refs := make([]Ref, 0, len(written))
	for _, row := range written {
		refs = append(refs, row.Ref())
	}
	return refs
}

func sameRefs(got, want []Ref) bool {
	if len(got) != len(want) {
		return false
	}
	for index, ref := range want {
		if got[index] != ref {
			return false
		}
	}
	return true
}

func sameCounts(got, want map[string]int) bool {
	if len(got) != len(want) {
		return false
	}
	for kind, count := range want {
		if got[kind] != count {
			return false
		}
	}
	return true
}

func contains(refs []Ref, wanted Ref) bool {
	for _, ref := range refs {
		if ref == wanted {
			return true
		}
	}
	return false
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
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
