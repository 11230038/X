package workspace

// The export, its job and its bounds (§2.13).
//
// The archive tests say what a package is; these say what the service *does* to
// one. Four claims, and each is about a different half of the job table being
// right: that a success leaves a committed row pointing at a package that is
// really there, that a failure leaves a failed row with a code, that the row is
// written before anything else happens (so a failure is visible at all), and
// that a package over the deployment's bound is refused rather than written and
// then measured.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"backend/internal/audit"
	"backend/internal/authz"
)

// ── the three things a service is built from, kept small ────────────────────

// fakeRepository is the job table in memory, remembering the order it was
// written in.
type fakeRepository struct {
	jobs    map[string]Job
	written []string
	fail    map[string]error
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{jobs: map[string]Job{}, fail: map[string]error{}}
}

func (r *fakeRepository) record(name string) error {
	r.written = append(r.written, name)
	return r.fail[name]
}

func (r *fakeRepository) Create(_ context.Context, job Job) error {
	if err := r.record("create"); err != nil {
		return err
	}
	r.jobs[job.ID] = job
	return nil
}

func (r *fakeRepository) Find(_ context.Context, jobID string) (Job, error) {
	if err := r.record("find"); err != nil {
		return Job{}, err
	}
	job, held := r.jobs[jobID]
	if !held {
		return Job{}, fmt.Errorf("%w: %s", ErrNoSuchJob, jobID)
	}
	return job, nil
}

func (r *fakeRepository) SaveState(_ context.Context, jobID string, state State, code string, now time.Time) error {
	if err := r.record("state:" + string(state)); err != nil {
		return err
	}
	job, held := r.jobs[jobID]
	if !held {
		return fmt.Errorf("%w: %s", ErrNoSuchJob, jobID)
	}
	job.State = state
	job.FailureCode = code
	job.UpdatedAt = now
	r.jobs[jobID] = job
	return nil
}

func (r *fakeRepository) SaveManifest(_ context.Context, jobID string, entries []Entry) error {
	if err := r.record("manifest"); err != nil {
		return err
	}
	job := r.jobs[jobID]
	job.Manifest = entries
	r.jobs[jobID] = job
	return nil
}

func (r *fakeRepository) SaveCounts(_ context.Context, jobID string, counts map[string]int) error {
	if err := r.record("counts"); err != nil {
		return err
	}
	job := r.jobs[jobID]
	job.Counts = counts
	r.jobs[jobID] = job
	return nil
}

func (r *fakeRepository) SetArchivePath(_ context.Context, jobID, archivePath string) error {
	if err := r.record("path"); err != nil {
		return err
	}
	job := r.jobs[jobID]
	job.ArchivePath = archivePath
	r.jobs[jobID] = job
	return nil
}

// stubCollector answers a fixed set of objects, or a fixed error.
type stubCollector struct {
	objects []Object
	err     error
	asked   [][]Ref
	callers []authz.Caller
	before  func()
}

func (c *stubCollector) Collect(_ context.Context, caller authz.Caller, refs []Ref) ([]Object, error) {
	c.asked = append(c.asked, append([]Ref(nil), refs...))
	c.callers = append(c.callers, caller)
	if c.before != nil {
		c.before()
	}
	if c.err != nil {
		return nil, c.err
	}
	return c.objects, nil
}

// recordingAuditor keeps the entries a service wrote.
type recordingAuditor struct{ events []audit.Event }

func (a *recordingAuditor) Record(_ context.Context, event audit.Event) error {
	a.events = append(a.events, event)
	return nil
}

// ── the service under test ──────────────────────────────────────────────────

type harness struct {
	service    *Service
	repository *fakeRepository
	collector  *stubCollector
	auditor    *recordingAuditor
	root       string
}

func newHarness(t *testing.T, options ...func(*Options)) *harness {
	t.Helper()
	root := t.TempDir()
	repository := newFakeRepository()
	collector := &stubCollector{objects: sampleObjects()}
	auditor := &recordingAuditor{}
	settings := Options{
		Root:            root,
		MaxArchiveBytes: 1 << 20,
		JobTimeout:      time.Minute,
		Repository:      repository,
		Collector:       collector,
		Auditor:         auditor,
		Now:             func() time.Time { return time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC) },
		NewJobID:        func() (string, error) { return "job_test", nil },
	}
	for _, apply := range options {
		apply(&settings)
	}
	service, err := NewService(settings)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return &harness{service: service, repository: repository, collector: collector, auditor: auditor, root: root}
}

func exporter() Actor {
	return Actor{Caller: authz.Caller{UserID: 7, Role: authz.RoleUser}, Name: "alice"}
}

func sampleRefs() []Ref {
	return []Ref{
		{Kind: KindSession, ID: "ses_1"},
		{Kind: KindMaterial, ID: "mat_1"},
		{Kind: KindSession, ID: "ses_2"},
	}
}

func TestExportPacksTheSelectionAndCommitsTheJob(t *testing.T) {
	app := newHarness(t)

	job, err := app.service.Export(context.Background(), ExportRequest{Actor: exporter(), Refs: sampleRefs()})
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if job.State != StateCommitted || job.FailureCode != "" {
		t.Fatalf("the job ended %s (%s), want committed", job.State, job.FailureCode)
	}
	if job.Kind != JobExport {
		t.Errorf("Kind = %s, want export", job.Kind)
	}
	if job.OwnerID != 7 {
		t.Errorf("OwnerID = %d, want the actor's 7", job.OwnerID)
	}
	if job.ArchivePath != "job_test.zip" {
		t.Errorf("ArchivePath = %q, want the job id and .zip", job.ArchivePath)
	}
	// The path is relative to the root. A row that named an absolute path would
	// stop meaning anything the moment the deployment moved its archive root,
	// and it would put this machine's layout into a table that gets served.
	if filepath.IsAbs(job.ArchivePath) || strings.Contains(job.ArchivePath, string(filepath.Separator)) {
		t.Errorf("ArchivePath = %q, want a name under the root and not a path", job.ArchivePath)
	}
	// The selection reached the collector as the account, once, in the order it
	// was asked for. The account is the half that matters: a collector called
	// without one is a way to pack up somebody else's work by naming their ids.
	if len(app.collector.asked) != 1 {
		t.Fatalf("the collector was asked %d times, want once", len(app.collector.asked))
	}
	if app.collector.callers[0] != exporter().Caller {
		t.Errorf("the collector was called as %+v, want the actor", app.collector.callers[0])
	}
	if got := app.collector.asked[0]; len(got) != 3 || got[0] != sampleRefs()[0] {
		t.Errorf("the collector was asked for %v, want the selection", got)
	}

	// The row says what was packed, and the package is where the row says.
	stored := app.repository.jobs["job_test"]
	if stored.State != StateCommitted {
		t.Fatalf("the stored job is %s, want committed", stored.State)
	}
	if len(stored.Manifest) != len(sampleObjects()) {
		t.Errorf("the stored manifest names %d objects, want %d", len(stored.Manifest), len(sampleObjects()))
	}
	if stored.Counts[string(KindSession)] != 2 {
		t.Errorf("the stored counts = %v, want two sessions", stored.Counts)
	}
	if _, err := os.Stat(filepath.Join(app.root, stored.ArchivePath)); err != nil {
		t.Fatalf("the job points at a package that is not there: %v", err)
	}

	// One audit entry, on the actor, naming the job as the target. A package is
	// a copy of somebody's work leaving the instance, which is the thing the
	// audit exists to be able to answer (§2.6).
	if len(app.auditor.events) != 1 {
		t.Fatalf("the audit holds %d entries, want one", len(app.auditor.events))
	}
	event := app.auditor.events[0]
	if event.Action != audit.ActionWorkspaceExported || event.Result != audit.ResultOK {
		t.Errorf("the entry is %s/%s, want workspace.exported/ok", event.Action, event.Result)
	}
	if event.Actor.ID != 7 || event.Actor.Name != "alice" {
		t.Errorf("the entry names %+v, want the actor with their name", event.Actor)
	}
	if event.Target.Kind != audit.TargetWorkspaceJob || event.Target.ID != "job_test" {
		t.Errorf("the entry points at %+v, want the job", event.Target)
	}
	if event.Detail.Counts[string(KindSession)] != 2 {
		t.Errorf("the entry's counts = %v, want the two sessions", event.Detail.Counts)
	}

	// And what it wrote is a package this build can read back.
	reopened, err := app.service.Open(context.Background(), stored)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if !reopened.Report.Clean() {
		t.Errorf("the package the export wrote does not match its own manifest: %s", reopened.Report.Summary())
	}
}

// TestTheJobIsWrittenBeforeAnythingIsCollected: the row comes first, and it is
// what makes a failure visible. A collector that hangs or a package that is too
// large would otherwise leave a caller with an error and nothing to ask about.
func TestTheJobIsWrittenBeforeAnythingIsCollected(t *testing.T) {
	app := newHarness(t)
	order := []string{}
	app.collector.before = func() { order = append(order, "collect") }
	app.repository.written = nil

	if _, err := app.service.Export(context.Background(), ExportRequest{Actor: exporter(), Refs: sampleRefs()}); err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	// The repository's own record is the order the writes happened in; the
	// collector's hook is where reading happened. The create has to be in front
	// of it.
	order = append([]string{firstWrite(app.repository.written)}, order...)
	if order[0] != "create" || order[1] != "collect" {
		t.Fatalf("the order was %v, want the row written before the reading", order)
	}
}

func firstWrite(written []string) string {
	if len(written) == 0 {
		return ""
	}
	return written[0]
}

func TestExportRefusesAnEmptySelection(t *testing.T) {
	app := newHarness(t)
	if _, err := app.service.Export(context.Background(), ExportRequest{Actor: exporter()}); !errors.Is(err, ErrEmptySelection) {
		t.Fatalf("Export() error = %v, want ErrEmptySelection", err)
	}
	// Nothing was even recorded as attempted: an export of nothing is a
	// request that never happened, not a job that failed.
	if len(app.repository.jobs) != 0 || len(app.auditor.events) != 0 {
		t.Errorf("an empty selection left a job or an audit entry behind")
	}
}

func TestExportRefusesAnActorlessRequest(t *testing.T) {
	app := newHarness(t)
	if _, err := app.service.Export(context.Background(), ExportRequest{Refs: sampleRefs()}); !errors.Is(err, ErrNoActor) {
		t.Fatalf("Export() error = %v, want ErrNoActor", err)
	}
}

func TestExportRefusesARefItCannotAddress(t *testing.T) {
	app := newHarness(t)
	for _, ref := range []Ref{
		{Kind: "memory", ID: "mem_1"},
		{Kind: KindSession, ID: "../other"},
		{Kind: KindSession, ID: ""},
	} {
		if _, err := app.service.Export(context.Background(), ExportRequest{Actor: exporter(), Refs: []Ref{ref}}); err == nil {
			t.Errorf("Export() accepted %+v", ref)
		}
	}
}

// TestExportRefusesAPackageOverTheBound: the bound is enforced while writing,
// so the test is not only that the call fails — it is that nothing was left on
// the disk and that the job says why.
func TestExportRefusesAPackageOverTheBound(t *testing.T) {
	app := newHarness(t, func(options *Options) { options.MaxArchiveBytes = 64 })

	job, err := app.service.Export(context.Background(), ExportRequest{Actor: exporter(), Refs: sampleRefs()})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Export() error = %v, want ErrTooLarge", err)
	}
	if job.State != StateFailed || job.FailureCode != FailureArchiveTooLarge {
		t.Fatalf("the job ended %s (%s), want failed/archive_too_large", job.State, job.FailureCode)
	}
	left, err := os.ReadDir(app.root)
	if err != nil {
		t.Fatalf("read the archive root: %v", err)
	}
	// Not even the staging file. A package too large to be kept is not kept, and
	// a half-written one left behind is the thing the rename exists to prevent.
	if len(left) != 0 {
		t.Errorf("the refused export left %d entries in the archive root", len(left))
	}
	if len(app.auditor.events) != 1 || app.auditor.events[0].Result != audit.ResultFailed {
		t.Errorf("the audit = %+v, want one failed entry", app.auditor.events)
	}
	if app.auditor.events[0].Detail.Reason != FailureArchiveTooLarge {
		t.Errorf("the entry's reason = %q, want the failure code", app.auditor.events[0].Detail.Reason)
	}
}

func TestExportMarksTheJobFailedWhenASourceCannotBeRead(t *testing.T) {
	app := newHarness(t)
	app.collector.err = errors.New("the session does not answer")

	job, err := app.service.Export(context.Background(), ExportRequest{Actor: exporter(), Refs: sampleRefs()})
	if err == nil {
		t.Fatalf("Export() error = nil, want the source's failure")
	}
	if job.State != StateFailed || job.FailureCode != FailureCollectFailed {
		t.Fatalf("the job ended %s (%s), want failed/collect_failed", job.State, job.FailureCode)
	}
	stored := app.repository.jobs["job_test"]
	if stored.State != StateFailed {
		t.Errorf("the stored job is %s, want failed", stored.State)
	}
	// The test's own error is what the caller sees. The row's code is what the
	// next person to look at the job sees, and the two do different jobs.
	if !strings.Contains(err.Error(), "the session does not answer") {
		t.Errorf("error = %v, want the cause to survive", err)
	}
}

// TestExportStillMarksTheJobFailedWhenTheClockRanOut: a job that ran out of its
// wall clock is exactly the case where the record matters, so the write has to
// happen on a context that has not also expired.
func TestExportStillMarksTheJobFailedWhenTheClockRanOut(t *testing.T) {
	app := newHarness(t, func(options *Options) { options.JobTimeout = time.Nanosecond })
	app.collector.before = func() { time.Sleep(2 * time.Millisecond) }
	app.collector.err = context.DeadlineExceeded

	job, err := app.service.Export(context.Background(), ExportRequest{Actor: exporter(), Refs: sampleRefs()})
	if err == nil {
		t.Fatalf("Export() error = nil, want the deadline")
	}
	if job.FailureCode != FailureTimeout {
		t.Fatalf("the job failed with %q, want %q", job.FailureCode, FailureTimeout)
	}
}

func TestTheServiceRefusesToBeBuiltWithoutItsParts(t *testing.T) {
	full := func() Options {
		return Options{
			Root: "/tmp", MaxArchiveBytes: 1, JobTimeout: time.Second,
			Repository: newFakeRepository(), Collector: &stubCollector{}, Auditor: &recordingAuditor{},
		}
	}
	tests := []struct {
		name    string
		without func(*Options)
	}{
		{"no store", func(o *Options) { o.Repository = nil }},
		{"no collector", func(o *Options) { o.Collector = nil }},
		{"no auditor", func(o *Options) { o.Auditor = nil }},
		{"no root", func(o *Options) { o.Root = "" }},
		{"no bound", func(o *Options) { o.MaxArchiveBytes = 0 }},
		{"no timeout", func(o *Options) { o.JobTimeout = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := full()
			test.without(&options)
			if _, err := NewService(options); err == nil {
				t.Fatalf("NewService() error = nil, want a refusal")
			}
		})
	}
}

func TestAnUnbuiltServiceRefusesEverything(t *testing.T) {
	var service *Service
	if _, err := service.Export(context.Background(), ExportRequest{Actor: exporter(), Refs: sampleRefs()}); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("Export() error = %v, want ErrNotConfigured", err)
	}
	if _, err := service.Job(context.Background(), "job_1"); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("Job() error = %v, want ErrNotConfigured", err)
	}
}

func TestTheSelectionIsFoldedRatherThanRefused(t *testing.T) {
	app := newHarness(t)
	// The same ref twice is one request, not two failures: a client that
	// concatenated two lists is a client asking for what it asked for. The
	// writer would refuse two objects of one name, and turning a client's
	// bookkeeping into that refusal would be a bug report about the writer.
	refs := []Ref{sampleRefs()[0], sampleRefs()[1], sampleRefs()[0]}
	if _, err := app.service.Export(context.Background(), ExportRequest{Actor: exporter(), Refs: refs}); err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if asked := app.collector.asked[0]; len(asked) != 2 {
		t.Errorf("the collector was asked for %v, want the two distinct refs", asked)
	}
}
