package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"backend/internal/authz"
	"backend/internal/data"
	"backend/internal/session"
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
