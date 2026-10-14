package workspace

// The storage half of a job, checked against a real seekdb.
//
// What is pinned here is what a double cannot be wrong about. A fake map can
// restate "a job round-trips" and agree with itself; only the table can say
// whether a NULL owner comes back as zero, whether the three JSON columns
// survive a column-by-column update, whether `update_time` moves when a state
// change writes it by hand, and whether the CHECK constraints refuse the two
// spellings nobody would notice were missing — a state the table does not have
// and a job kind that is not a direction.
//
// One database per package, so nothing here calls t.Parallel.

import (
	"context"
	"errors"
	"testing"
	"time"

	"backend/internal/data"
	"backend/internal/data/models"
	"backend/internal/data/testdb"
)

// jobTableOrder is child first: a job points at an account, so the rows that
// point at an account go before the account does.
var jobTableOrder = []string{"workspace_jobs", "messages", "sessions", "users"}

func newDBStore(t *testing.T) (*GORMRepository, *data.Resource) {
	t.Helper()
	resource := testdb.Open(t, "workspace")
	testdb.Reset(t, resource, jobTableOrder...)
	return NewGORMRepository(resource.GORM()), resource
}

// account writes one user row for the jobs that have an owner to point at.
func account(t *testing.T, resource *data.Resource, username string) int64 {
	t.Helper()
	testdb.Exec(t, resource,
		"INSERT INTO users (username, password, learner_profile, extra) VALUES (?, 'hash', '{}', '{}')",
		username)
	var row struct {
		UserID int64 `gorm:"column:user_id"`
	}
	if err := resource.GORM().Raw("SELECT user_id FROM users WHERE username = ?", username).
		Scan(&row).Error; err != nil {
		t.Fatalf("read account %s: %v", username, err)
	}
	return row.UserID
}

// atSecond is a whole second on a fixed day. creat_time and update_time are
// DATETIME columns, which keep whole seconds, so a test that let the clock pick
// one would be comparing against a rounded value.
func atSecond(second int) time.Time {
	return time.Date(2026, 10, 14, 12, 0, second, 0, time.UTC)
}

func dbJob(owner int64, at time.Time) Job {
	return Job{
		ID:        "job_db_1",
		Kind:      JobExport,
		State:     StateRunning,
		OwnerID:   owner,
		CreatedAt: at,
		UpdatedAt: at,
	}
}

func createJob(t *testing.T, store *GORMRepository, job Job) {
	t.Helper()
	if err := store.Create(context.Background(), job); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
}

func findJob(t *testing.T, store *GORMRepository, jobID string) Job {
	t.Helper()
	job, err := store.Find(context.Background(), jobID)
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	return job
}

func TestDBTheJobRoundTripsEveryColumn(t *testing.T) {
	store, resource := newDBStore(t)
	owner := account(t, resource, "alice")

	job := dbJob(owner, atSecond(0))
	createJob(t, store, job)
	if err := store.SaveManifest(context.Background(), job.ID, sampleManifest().Entries); err != nil {
		t.Fatalf("SaveManifest() error = %v", err)
	}
	if err := store.SaveCounts(context.Background(), job.ID, map[string]int{"session": 2}); err != nil {
		t.Fatalf("SaveCounts() error = %v", err)
	}
	if err := store.SetArchivePath(context.Background(), job.ID, "job_db_1.zip"); err != nil {
		t.Fatalf("SetArchivePath() error = %v", err)
	}
	if err := store.SaveState(context.Background(), job.ID, StateCommitted, "", atSecond(1)); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}

	stored := findJob(t, store, job.ID)
	if stored.Kind != JobExport || stored.State != StateCommitted {
		t.Errorf("stored %s/%s, want export/committed", stored.Kind, stored.State)
	}
	if stored.OwnerID != owner {
		t.Errorf("OwnerID = %d, want %d", stored.OwnerID, owner)
	}
	if stored.ArchivePath != "job_db_1.zip" {
		t.Errorf("ArchivePath = %q", stored.ArchivePath)
	}
	if stored.FailureCode != "" {
		t.Errorf("FailureCode = %q, want empty on a committed job", stored.FailureCode)
	}
	if len(stored.Manifest) != 3 || stored.Manifest[0].Kind != KindSession {
		t.Errorf("Manifest = %+v, want the three entries back", stored.Manifest)
	}
	if stored.Counts["session"] != 2 {
		t.Errorf("Counts = %v, want the two sessions", stored.Counts)
	}
	if !stored.CreatedAt.Equal(atSecond(0)) {
		t.Errorf("CreatedAt = %s, want %s — the row's moment is the one it was given", stored.CreatedAt, atSecond(0))
	}
	// `update_time` is written by hand on every column update, because a write
	// by column name is what keeps a state change from also rewriting the
	// manifest — and a hand-written timestamp is a timestamp that can be
	// forgotten. This is the case that would notice.
	if !stored.UpdatedAt.Equal(atSecond(1)) {
		t.Errorf("UpdatedAt = %s, want %s — the state change should have moved it", stored.UpdatedAt, atSecond(1))
	}
}

// TestDBAnOwnerlessJobKeepsNoOwner: owner_id is nullable on purpose. Every row
// written before V5 belongs to nobody, and 00016 gives the column a foreign key
// with SET NULL rather than CASCADE — a package record outlives the account that
// asked for it.
func TestDBAnOwnerlessJobKeepsNoOwner(t *testing.T) {
	store, resource := newDBStore(t)
	createJob(t, store, dbJob(0, atSecond(0)))

	var row struct {
		OwnerID *int64 `gorm:"column:owner_id"`
	}
	if err := resource.GORM().Raw("SELECT owner_id FROM workspace_jobs WHERE job_id = ?", "job_db_1").
		Scan(&row).Error; err != nil {
		t.Fatalf("read the row: %v", err)
	}
	// NULL rather than zero, which is the whole difference: zero is an account
	// id nobody was issued and NULL is no account, and only one of them can be
	// foreign-keyed.
	if row.OwnerID != nil {
		t.Fatalf("owner_id = %d, want NULL", *row.OwnerID)
	}
	if stored := findJob(t, store, "job_db_1"); stored.OwnerID != 0 {
		t.Errorf("OwnerID = %d, want zero for a job with no owner", stored.OwnerID)
	}
}

func TestDBDeletingTheAccountLeavesTheJob(t *testing.T) {
	store, resource := newDBStore(t)
	owner := account(t, resource, "alice")
	createJob(t, store, dbJob(owner, atSecond(0)))

	if err := resource.GORM().Where("user_id = ?", owner).Delete(&models.User{}).Error; err != nil {
		t.Fatalf("delete the account: %v", err)
	}
	stored := findJob(t, store, "job_db_1")
	if stored.OwnerID != 0 {
		t.Errorf("OwnerID = %d after the account was deleted, want zero", stored.OwnerID)
	}
	// The job and its package record are still readable. What was packed is a
	// fact about the instance, not about the account, and a record that went
	// away with the account would answer "was anything taken" with silence.
	if stored.State != StateRunning {
		t.Errorf("State = %s after the account was deleted, want the job untouched", stored.State)
	}
}

func TestDBAnUnknownJobIsAnsweredTheSameWayAsAnEmptyOne(t *testing.T) {
	store, _ := newDBStore(t)
	if _, err := store.Find(context.Background(), "job_nobody"); !errors.Is(err, ErrNoSuchJob) {
		t.Fatalf("Find() error = %v, want ErrNoSuchJob", err)
	}
	// A write to a job id nobody has is the same answer rather than a silent
	// success. A store that reported nothing written as written would turn a
	// lost job id into a job that appears to have moved.
	for name, err := range map[string]error{
		"state":    store.SaveState(context.Background(), "job_nobody", StateCommitted, "", atSecond(0)),
		"manifest": store.SaveManifest(context.Background(), "job_nobody", sampleManifest().Entries),
		"counts":   store.SaveCounts(context.Background(), "job_nobody", map[string]int{"session": 1}),
		"path":     store.SetArchivePath(context.Background(), "job_nobody", "x.zip"),
	} {
		if !errors.Is(err, ErrNoSuchJob) {
			t.Errorf("%s on an unknown job = %v, want ErrNoSuchJob", name, err)
		}
	}
}

// TestDBTheStateAndTheCodeAreOneAnswer: the two columns are written together
// because they are one answer. A failed job without a code is a job nobody can
// explain; a code on a job that is not failed is a code that means nothing, and
// this is the case that would notice a version that cleared one and not the
// other.
func TestDBTheStateAndTheCodeAreOneAnswer(t *testing.T) {
	store, resource := newDBStore(t)
	createJob(t, store, dbJob(account(t, resource, "alice"), atSecond(0)))

	if err := store.SaveState(context.Background(), "job_db_1", StateFailed, FailureArchiveTooLarge, atSecond(1)); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}
	stored := findJob(t, store, "job_db_1")
	if stored.State != StateFailed || stored.FailureCode != FailureArchiveTooLarge {
		t.Fatalf("stored %s/%s, want failed/%s", stored.State, stored.FailureCode, FailureArchiveTooLarge)
	}
	// Recovery clears the code by moving the job to a state that is not a
	// failure, and it does it in the same write.
	if err := store.SaveState(context.Background(), "job_db_1", StateRecovered, "", atSecond(2)); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}
	stored = findJob(t, store, "job_db_1")
	if stored.State != StateRecovered || stored.FailureCode != "" {
		t.Errorf("stored %s/%q, want recovered with no code", stored.State, stored.FailureCode)
	}
}

// TestDBTheTableRefusesWhatTheClosedSetsDoNot is 00016's two CHECK constraints.
// Neither is reachable through this store — both validate first — and that is
// exactly why they are worth pinning here: the check above the door holds as
// long as everybody remembers it, and the constraint behind it does not depend
// on anybody.
func TestDBTheTableRefusesWhatTheClosedSetsDoNot(t *testing.T) {
	_, resource := newDBStore(t)
	tests := []struct {
		name  string
		query string
	}{
		{"a job kind that is not a direction", "INSERT INTO workspace_jobs (job_id, kind, state) VALUES ('job_x', 'transfer', 'running')"},
		{"a state the table does not have", "INSERT INTO workspace_jobs (job_id, kind, state) VALUES ('job_x', 'export', 'finished')"},
		{"a state that is not a state at all", "INSERT INTO workspace_jobs (job_id, kind, state) VALUES ('job_x', 'export', '')"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := testdb.Try(resource, test.query); err == nil {
				t.Fatalf("the table accepted %s", test.name)
			}
		})
	}
}

func TestDBWritingAJobTwiceIsRefused(t *testing.T) {
	store, resource := newDBStore(t)
	job := dbJob(account(t, resource, "alice"), atSecond(0))
	createJob(t, store, job)
	// The job id is the primary key, so a second write of one id is the
	// database's refusal rather than a read-then-write here. Two jobs sharing a
	// package name is the failure that would otherwise be possible.
	if err := store.Create(context.Background(), job); err == nil {
		t.Fatalf("Create() wrote the same job id twice")
	}
}

// TestDBWritingAColumnLeavesTheOthersAlone: every writer writes its own columns
// and nothing else. A job that lost its manifest because its state changed would
// be a job whose package content is unknown at exactly the moment somebody is
// asking what is in it.
func TestDBWritingAColumnLeavesTheOthersAlone(t *testing.T) {
	store, resource := newDBStore(t)
	createJob(t, store, dbJob(account(t, resource, "alice"), atSecond(0)))
	if err := store.SaveCounts(context.Background(), "job_db_1", map[string]int{"session": 2}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveState(context.Background(), "job_db_1", StateCommitted, "", atSecond(1)); err != nil {
		t.Fatal(err)
	}
	stored := findJob(t, store, "job_db_1")
	if stored.Counts["session"] != 2 {
		t.Errorf("Counts = %v after a state change, want them untouched", stored.Counts)
	}
	if stored.Written != nil {
		t.Errorf("Written = %+v, want nil: nothing has been written by an import", stored.Written)
	}
}

func TestDBTheStoreRefusesWhatItMayNotWrite(t *testing.T) {
	store, resource := newDBStore(t)
	createJob(t, store, dbJob(account(t, resource, "alice"), atSecond(0)))
	tests := []struct {
		name string
		call func() error
	}{
		{"an unknown state", func() error {
			return store.SaveState(context.Background(), "job_db_1", State("finished"), "", atSecond(1))
		}},
		{"an unknown failure code", func() error {
			return store.SaveState(context.Background(), "job_db_1", StateFailed, "went_wrong", atSecond(1))
		}},
		{"an unaddressable job id", func() error {
			return store.Create(context.Background(), Job{ID: "job/../x", Kind: JobExport, State: StateRunning})
		}},
		{"a kind of job the table does not have", func() error {
			return store.Create(context.Background(), Job{ID: "job_db_2", Kind: "transfer", State: StateRunning})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); err == nil {
				t.Fatalf("the store accepted %s", test.name)
			}
		})
	}
}
