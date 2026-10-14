package workspace

// The export against a real database, through a real domain read.
//
// §2.13's fourth discipline and the stage's own case: **导出不改源**. Everything
// else in this package's tests says what a package is; this file says that
// making one leaves the rows it was made from where they were. A fake source
// could restate that and agree with itself, because a fake source has no rows —
// so the collector here is the session service over the test database, reading
// real conversations out of real tables.
//
// One database per package, so nothing here calls t.Parallel.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"backend/internal/authz"
	"backend/internal/data"
	"backend/internal/session"
)

// sourceTables are the tables an export of a conversation must not touch. It is
// the account as well as the conversation, because a job names an owner and a
// careless writer could think that meant writing one.
var sourceTables = []string{"users", "sessions", "messages"}

// sessionSource reads a conversation the way a deployment's collector does:
// through the domain's own service, as the account the package is for.
type sessionSource struct {
	service *session.Service
}

func (s sessionSource) Objects(ctx context.Context, caller authz.Caller, refs []Ref) ([]Object, error) {
	objects := make([]Object, 0, len(refs))
	for _, ref := range refs {
		record, err := s.service.Get(ctx, ref.ID, caller)
		if err != nil {
			return nil, err
		}
		messages, err := s.service.Messages(ctx, ref.ID, caller)
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(struct {
			Session  session.Session   `json:"session"`
			Messages []session.Message `json:"messages"`
		}{Session: record, Messages: messages})
		if err != nil {
			return nil, fmt.Errorf("encode a session: %w", err)
		}
		objects = append(objects, Object{Ref: ref, Data: data})
	}
	return objects, nil
}

// refusingSource is what the kinds this file does not read are wired to. A
// source that answered instead of failing would let a case here pass while
// asking for something other than a conversation.
type refusingSource struct{ kind Kind }

func (r refusingSource) Objects(context.Context, authz.Caller, []Ref) ([]Object, error) {
	return nil, fmt.Errorf("this test has no %s source", r.kind)
}

// collectableSessions builds the collector a deployment would have, with the
// three kinds this file does not exercise wired to a refusal.
func collectableSessions(t *testing.T, service *session.Service) *Sources {
	t.Helper()
	sources := map[Kind]Source{KindSession: sessionSource{service: service}}
	for _, kind := range Kinds {
		if kind != KindSession {
			sources[kind] = refusingSource{kind: kind}
		}
	}
	built, err := NewSources(sources)
	if err != nil {
		t.Fatalf("NewSources() error = %v", err)
	}
	return built
}

func rowCount(t *testing.T, resource *data.Resource, table string) int64 {
	t.Helper()
	var count int64
	if err := resource.GORM().Table(table).Count(&count).Error; err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func countsOf(t *testing.T, resource *data.Resource) map[string]int64 {
	t.Helper()
	counts := make(map[string]int64, len(sourceTables))
	for _, table := range sourceTables {
		counts[table] = rowCount(t, resource, table)
	}
	return counts
}

// seedConversation writes one account with one conversation and two messages,
// through the session domain's own service.
func seedConversation(t *testing.T, resource *data.Resource) (*session.Service, authz.Caller, string) {
	t.Helper()
	owner := account(t, resource, "alice")
	caller := authz.Caller{UserID: owner, Role: authz.RoleUser}
	service, err := session.NewService(session.NewGORMRepository(resource.GORM()))
	if err != nil {
		t.Fatalf("build the session service: %v", err)
	}
	record, err := service.Create(context.Background(), caller)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	for _, content := range []string{"为什么天是蓝的", "因为瑞利散射"} {
		role := session.RoleUser
		if content == "因为瑞利散射" {
			role = session.RoleAssistant
		}
		if _, err := service.AppendMessage(context.Background(), session.NewMessage{
			SessionID: record.ID, Role: role, Content: content,
		}, caller); err != nil {
			t.Fatalf("AppendMessage() error = %v", err)
		}
	}
	return service, caller, record.ID
}

// refusingWriter is the import half of a service built to export. A service
// needs all of its parts (see NewService), and a double that answered instead of
// refusing would hide the one thing this file would need to hear about: an
// export that wrote something.
type refusingWriter struct{}

func (refusingWriter) Plan(context.Context, authz.Caller, []Object) ([]Item, error) {
	return nil, fmt.Errorf("this service only exports")
}

func (refusingWriter) Write(context.Context, authz.Caller, []Object) ([]Written, error) {
	return nil, fmt.Errorf("this service only exports")
}

func (refusingWriter) Undo(context.Context, authz.Caller, []Written) error {
	return fmt.Errorf("this service only exports")
}

// idleTurns is a deployment with nothing running, which is the state an export
// does not even ask about: the two writes are what the port is consulted by.
type idleTurns struct{}

func (idleTurns) Running(context.Context) (int64, error) { return 0, nil }

func newExportService(t *testing.T, resource *data.Resource, collector Collector, auditor Auditor) *Service {
	t.Helper()
	service, err := NewService(Options{
		Root:            t.TempDir(),
		MaxArchiveBytes: 4 << 20,
		JobTimeout:      time.Minute,
		Repository:      NewGORMRepository(resource.GORM()),
		Collector:       collector,
		Writer:          refusingWriter{},
		Busy:            idleTurns{},
		Auditor:         auditor,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

// TestDBAnExportLeavesEverySourceRowWhereItWas is the stage's own case.
//
// It compares row counts per table rather than asserting that nothing failed,
// because "the export reported success" and "the export changed nothing" are
// different claims and only the second one is the discipline. The three tables
// are counted before and after, and the conversation's own text is read back to
// confirm it is the text that was written.
func TestDBAnExportLeavesEverySourceRowWhereItWas(t *testing.T) {
	_, resource := newDBStore(t)
	service, caller, sessionID := seedConversation(t, resource)
	auditor := &recordingAuditor{}
	packages := newExportService(t, resource, collectableSessions(t, service), auditor)

	before := countsOf(t, resource)
	if before["sessions"] != 1 || before["messages"] != 2 {
		t.Fatalf("the fixture is %v, want one conversation with two messages", before)
	}

	job, err := packages.Export(context.Background(), ExportRequest{
		Actor: Actor{Caller: caller, Name: "alice"},
		Refs:  []Ref{{Kind: KindSession, ID: sessionID}},
	})
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if job.State != StateCommitted {
		t.Fatalf("the job ended %s, want committed", job.State)
	}

	after := countsOf(t, resource)
	for _, table := range sourceTables {
		if after[table] != before[table] {
			t.Errorf("%s holds %d rows after the export, want the %d it held before", table, after[table], before[table])
		}
	}
	// An export writes exactly one thing of its own: the job row. Asserting the
	// count rather than "at least one" is what would notice a version that
	// started writing a second row per package.
	if jobs := rowCount(t, resource, "workspace_jobs"); jobs != 1 {
		t.Errorf("workspace_jobs holds %d rows, want exactly the one the export wrote", jobs)
	}

	// And the conversation is still the conversation. A read that took the rows
	// away and wrote them back would pass the counts above and fail this.
	messages, err := service.Messages(context.Background(), sessionID, caller)
	if err != nil {
		t.Fatalf("Messages() after the export: %v", err)
	}
	if len(messages) != 2 || messages[0].Content != "为什么天是蓝的" || messages[1].Content != "因为瑞利散射" {
		t.Fatalf("the conversation reads back as %+v", messages)
	}
}

// TestDBTheRowAndThePackageSayTheSameThing ties the two halves together on real
// storage: the manifest in the job row is the manifest inside the package the
// row points at, entry for entry, and the manifest inside the package is what
// the package's own digest covers.
func TestDBTheRowAndThePackageSayTheSameThing(t *testing.T) {
	store, resource := newDBStore(t)
	service, caller, sessionID := seedConversation(t, resource)
	packages := newExportService(t, resource, collectableSessions(t, service), &recordingAuditor{})

	job, err := packages.Export(context.Background(), ExportRequest{
		Actor: Actor{Caller: caller, Name: "alice"},
		Refs:  []Ref{{Kind: KindSession, ID: sessionID}},
	})
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	stored, err := store.Find(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	archive, err := packages.Open(context.Background(), stored)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if !archive.Report.Clean() {
		t.Fatalf("the package does not match its own manifest: %s", archive.Report.Summary())
	}
	if len(stored.Manifest) != archive.Manifest.Len() {
		t.Fatalf("the row names %d objects and the package %d", len(stored.Manifest), archive.Manifest.Len())
	}
	for index, entry := range stored.Manifest {
		if entry != archive.Manifest.Entries[index] {
			t.Errorf("entry %d: the row says %+v and the package says %+v", index, entry, archive.Manifest.Entries[index])
		}
	}
	// The conversation really is in there, and it really is this one.
	held, found := archive.Objects[Ref{Kind: KindSession, ID: sessionID}]
	if !found {
		t.Fatalf("the package does not hold %s", sessionID)
	}
	if !json.Valid(held) {
		t.Errorf("the object is not JSON: %q", held)
	}
	if len(held) == 0 {
		t.Errorf("the object is empty")
	}
}

// TestDBAnExportReadsAsTheAccountItIsFor: the ref arrives in a request body, so
// the actor is the only thing standing between an id somebody guessed and
// somebody else's conversation. The domain refuses it as "not found" rather than
// as "not yours", which is the answer AUTH-06 asks for.
func TestDBAnExportReadsAsTheAccountItIsFor(t *testing.T) {
	_, resource := newDBStore(t)
	service, _, sessionID := seedConversation(t, resource)
	other := account(t, resource, "bob")
	packages := newExportService(t, resource, collectableSessions(t, service), &recordingAuditor{})

	job, err := packages.Export(context.Background(), ExportRequest{
		Actor: Actor{Caller: authz.Caller{UserID: other, Role: authz.RoleUser}, Name: "bob"},
		Refs:  []Ref{{Kind: KindSession, ID: sessionID}},
	})
	if err == nil {
		t.Fatalf("Export() packed somebody else's conversation")
	}
	// The job is still there and still says it failed, so the refusal is
	// something the caller can come back and read rather than an error with
	// nothing behind it.
	if job.State != StateFailed || job.FailureCode != FailureCollectFailed {
		t.Fatalf("the job ended %s (%s), want failed/collect_failed", job.State, job.FailureCode)
	}
	if !errors.Is(err, session.ErrNotFound) {
		t.Errorf("error = %v, want the domain's own not-found", err)
	}
}

// TestDBAnUnknownRefIsTheSameAnswerAsSomebodyElse's: an id nobody was issued and
// an id that belongs to another account have to read the same, or the answer
// itself says that the row exists.
func TestDBAnUnknownRefIsTheSameAnswerAsSomebodyElse(t *testing.T) {
	_, resource := newDBStore(t)
	service, caller, _ := seedConversation(t, resource)
	packages := newExportService(t, resource, collectableSessions(t, service), &recordingAuditor{})

	_, err := packages.Export(context.Background(), ExportRequest{
		Actor: Actor{Caller: caller, Name: "alice"},
		Refs:  []Ref{{Kind: KindSession, ID: "ses_nobody_ever_issued"}},
	})
	if !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("error = %v, want the same not-found an unrelated id gets", err)
	}
	// Nothing was left on the disk by a job that read nothing.
	if jobs := rowCount(t, resource, "workspace_jobs"); jobs != 1 {
		t.Errorf("workspace_jobs holds %d rows, want the one failed job", jobs)
	}
}
