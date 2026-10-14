package workspace

// The fixtures an import case runs against: the job table's two extra columns,
// the four sinks behind one stub, the turn check, and a package already written
// out on the disk.
//
// The cases that use them are import_preview_test.go and import_commit_test.go;
// the recovery cases and the DB-backed ones share them too, which is why this is
// one file rather than something the preview's tests own.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"

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
