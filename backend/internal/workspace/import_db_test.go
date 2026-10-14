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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"backend/internal/authz"
	"backend/internal/data"
	"backend/internal/data/models"
	"backend/internal/session"
	"backend/internal/turn"
)

// importTables are the tables a case here counts to answer "what did that
// leave". The job table is among them because it is what an import is *meant* to
// change, and the other three are what a preview must not.
var importTables = []string{"sessions", "messages", "users", "workspace_jobs"}

func countsInDB(t *testing.T, resource *data.Resource) map[string]int64 {
	t.Helper()
	counted := make(map[string]int64, len(importTables))
	for _, table := range importTables {
		counted[table] = rowCount(t, resource, table)
	}
	return counted
}

// ── a package's worth of conversations ──────────────────────────────────────

// conversationObject is one conversation as the session domain would have
// encoded it: the row and the messages under it.
//
// The filler is not decoration. The corruption case below has to change one byte
// *inside* an object member while leaving the manifest member alone, and a run of
// one character that appears nowhere else in the archive is what makes that a
// thing this file can do rather than a thing it hopes for.
func conversationObject(id, title string) Object {
	return Object{
		Ref: Ref{Kind: KindSession, ID: id},
		Data: []byte(fmt.Sprintf(
			`{"session":{"ID":%q,"Title":%q,"OwnerID":0},`+
				`"messages":[{"ID":"msg_%s","Role":"user","Content":%q}]}`,
			id, title, id, "why is the sky blue")),
	}
}

func conversationPackage(t *testing.T, ids ...string) []byte {
	t.Helper()
	objects := make([]Object, 0, len(ids))
	for _, id := range ids {
		objects = append(objects, conversationObject(id, "a conversation"))
	}
	return writeSample(t, objects)
}

// ── the writer, over the real tables ────────────────────────────────────────

// dbWriter is an import's writer that leaves real rows: one conversation per
// object, into the account that asked, with the messages the object carried.
//
// The four sinks are the assembly's and are tested where they live (package
// main, against this same database). What is under test *here* is the service's
// half of the contract — the plan, the batches, the record written after every
// batch, and the recovery that follows it — and that half can only be checked
// against rows a real DELETE removes and a real SELECT finds. A writer that
// returned ids without writing anything would make every case in this file a
// case about a map.
type dbWriter struct {
	store session.Repository
	// failOn is the object, counted from one across the whole import, that the
	// write refuses — which is what makes "a batch failed halfway through" a
	// case rather than a claim.
	failOn int
	seen   int
}

// conversationIn is the little of a session payload this file's writer reads.
//
// It is not the payload type the assembly shares, and it is not trying to be:
// that type is package main's, and it is the thing that keeps an export and an
// import in one build from drifting. A writer double only has to understand what
// it is handed, and what it writes here is rows.
type conversationIn struct {
	Session struct {
		Title string `json:"Title"`
	} `json:"session"`
	Messages []struct {
		ID      string `json:"ID"`
		Role    string `json:"Role"`
		Content string `json:"Content"`
	} `json:"messages"`
}

func (w *dbWriter) Plan(ctx context.Context, caller authz.Caller, objects []Object) ([]Item, error) {
	items := make([]Item, 0, len(objects))
	for _, object := range objects {
		_, err := w.store.Session(ctx, object.ID, caller)
		switch {
		case errors.Is(err, session.ErrNotFound):
			items = append(items, Item{Ref: object.Ref, Action: ActionCreate})
		case err != nil:
			return nil, err
		default:
			items = append(items, Item{
				Ref: object.Ref, Action: ActionConflict, Reason: ReasonAlreadyPresent,
			})
		}
	}
	return items, nil
}

func (w *dbWriter) Write(ctx context.Context, caller authz.Caller, objects []Object) ([]Written, error) {
	landed := make([]Written, 0, len(objects))
	for _, object := range objects {
		w.seen++
		if w.failOn > 0 && w.seen >= w.failOn {
			// What landed is handed back beside the error, because it is the half
			// a recovery has to be able to name.
			return landed, fmt.Errorf("the write layer refused object %d", w.seen)
		}
		var payload conversationIn
		if err := json.Unmarshal(object.Data, &payload); err != nil {
			return landed, err
		}
		if err := w.store.Create(ctx, session.Session{
			ID: object.ID, Title: payload.Session.Title, OwnerID: caller.UserID,
		}); err != nil {
			return landed, err
		}
		landed = append(landed, Written{Kind: object.Kind, ID: object.ID})
		for index, message := range payload.Messages {
			if err := w.store.AppendMessage(ctx, session.Message{
				ID: fmt.Sprintf("%s_msg_%d", object.ID, index), SessionID: object.ID,
				Role: session.Role(message.Role), Content: message.Content,
			}, caller); err != nil {
				return landed, err
			}
		}
	}
	return landed, nil
}

func (w *dbWriter) Undo(ctx context.Context, caller authz.Caller, written []Written) error {
	for _, row := range written {
		if err := w.store.Delete(ctx, row.ID, caller); err != nil &&
			!errors.Is(err, session.ErrNotFound) {
			return err
		}
	}
	return nil
}

// ── the service under test ──────────────────────────────────────────────────

type dbHarness struct {
	service  *Service
	store    *GORMRepository
	resource *data.Resource
	root     string
	writer   *dbWriter
	importer Actor
}

// newDBImportHarness builds an import service over the real job table and a
// package root of its own.
//
// The collector refuses, because this service only imports: a case here that
// collected something would be reading a conversation when it meant to write
// one, and a refusal is louder than a source that quietly answers.
func newDBImportHarness(t *testing.T, options ...func(*Options)) *dbHarness {
	t.Helper()
	store, resource := newDBStore(t)
	owner := account(t, resource, "bob")
	writer := &dbWriter{store: session.NewGORMRepository(resource.GORM())}
	root := t.TempDir()
	counter := 0
	settings := Options{
		Root:            root,
		MaxArchiveBytes: 1 << 20,
		JobTimeout:      time.Minute,
		BatchSize:       2,
		Repository:      store,
		Collector:       refusingCollector(t),
		Writer:          writer,
		Busy:            &busyStub{},
		Auditor:         &recordingAuditor{},
		Now:             func() time.Time { return atSecond(0) },
		NewJobID: func() (string, error) {
			counter++
			return fmt.Sprintf("job_in_%d", counter), nil
		},
	}
	for _, apply := range options {
		apply(&settings)
	}
	service, err := NewService(settings)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return &dbHarness{
		service: service, store: store, resource: resource, root: root, writer: writer,
		importer: Actor{Caller: authz.Caller{UserID: owner, Role: authz.RoleUser}, Name: "bob"},
	}
}

// refusingCollector is every kind wired to a refusal.
func refusingCollector(t *testing.T) Collector {
	t.Helper()
	sources := make(map[Kind]Source, len(Kinds))
	for _, kind := range Kinds {
		if kind != KindSession {
			sources[kind] = refusingSource{kind: kind}
		}
	}
	// A conversation too: this service writes conversations, it does not read
	// them, and a case that exported one would be a case about the other file.
	sources[KindSession] = refusingSource{kind: KindSession}
	built, err := NewSources(sources)
	if err != nil {
		t.Fatalf("NewSources() error = %v", err)
	}
	return built
}

// preview uploads a package and answers the preview, the way the route does.
func (h *dbHarness) preview(t *testing.T, pkg []byte) Preview {
	t.Helper()
	preview, err := h.service.Preview(context.Background(), PreviewRequest{
		Actor: h.importer, Package: bytes.NewReader(pkg), Size: int64(len(pkg)),
	})
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	return preview
}

// staged is where the service put the bytes it was given, which is the file the
// commit reads back rather than the client's copy.
func (h *dbHarness) staged(t *testing.T, jobID string) string {
	t.Helper()
	job, err := h.store.Find(context.Background(), jobID)
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	return filepath.Join(h.root, job.ArchivePath)
}

func (h *dbHarness) commit(t *testing.T, jobID string) (Job, error) {
	t.Helper()
	return h.service.Import(context.Background(), CommitRequest{Actor: h.importer, JobID: jobID})
}

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
