package workspace

// The vocabulary of a package move (§2.13).
//
// §2.13 inherits three disciplines from §12.4 and none of its three interfaces.
// The reason is in the second half of that sentence: §12.4's interfaces are
// about moving data between two Workspace directories on one machine, and this
// repository has one seekdb and one upload root. There is no "from" and no "to".
// What is left is a package — a ZIP that holds a selection of an account's work
// and a manifest that says exactly what is in it — and the three disciplines
// applied to it: preview before commit, refuse while a turn is running, verify
// the manifest and keep the source.
//
// Two closed sets live here and they are not the same one. Kind is what an
// object *is* (a session, a material, a question, a collection); JobKind is
// which way a job moves packages. They would be one word in English and they are
// two columns in 00016, so they are two types here: `workspace_jobs.kind` holds
// 'export' or 'import' and nothing else, and a single type would let a job row
// claim to be a knowledge base.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"backend/internal/audit"
	"backend/internal/authz"
)

// Kind names what an object in a package is. It is a closed set for the reason
// every closed set in this repository is: a kind nobody can spell the same way
// twice is a kind nobody can narrow a read by, and the mistake is silent.
type Kind string

const (
	// KindSession is a conversation: the row and its messages.
	KindSession Kind = "session"
	// KindMaterial is a reading material. Its text is *not* in the package: a
	// material reads a document, the document was parsed from a library file,
	// and the file's bytes are the library's. What travels is the material, and
	// a package brought to another instance names a file that instance does not
	// have — which is exactly the missing dependency §2.13 asks the import
	// preview to report.
	KindMaterial Kind = "material"
	// KindQuestion is one question in the notebook, with its answer and its
	// categories.
	KindQuestion Kind = "question"
	// KindKnowledgeBase is a collection and its documents' metadata. The chunks
	// and their vectors do not travel: they are derived from a file this
	// instance may not have, and they are rebuilt by the same indexer that built
	// them the first time.
	KindKnowledgeBase Kind = "knowledge_base"
)

// Kinds is every kind a package may hold, in one place so that the writer, the
// reader and the collector cannot drift.
//
// It is also the list of what a package may *not* hold, and the list is the
// point (§12.5, AC-13): account settings, Memory, credentials and llm_calls are
// not absent because somebody filtered them out at the end — there is no kind
// for them, so a collector that tried to put one in would be refused by the
// archive writer rather than silently dropped.
var Kinds = []Kind{KindSession, KindMaterial, KindQuestion, KindKnowledgeBase}

// KnownKind reports whether a name is a kind this build moves.
func KnownKind(kind Kind) bool {
	for _, known := range Kinds {
		if known == kind {
			return true
		}
	}
	return false
}

// JobKind is which way a job moves packages.
type JobKind string

const (
	// JobExport is work being packed out.
	JobExport JobKind = "export"
	// JobImport is work being unpacked in.
	JobImport JobKind = "import"
)

// JobKinds is every job kind, in the order 00016's CHECK lists them.
var JobKinds = []JobKind{JobExport, JobImport}

// KnownJobKind reports whether a name is one the table accepts.
func KnownJobKind(kind JobKind) bool {
	return kind == JobExport || kind == JobImport
}

// State is where a job has got to.
//
// The five are 00016's CHECK and no more. `preview` is a state rather than a dry
// run because a preview is a thing a user comes back to: it is written down with
// the counts it reported, and the import that follows either matches it or does
// not (§2.13).
//
// `recovered` is terminal and is not `failed`. A failed job left rows behind
// that nobody has dealt with; a recovered one had them taken away again, and
// "did this leave anything" is the question a caller asks about a job that did
// not commit.
type State string

const (
	// StatePreview is a job that has looked at a package and written nothing.
	StatePreview State = "preview"
	// StateRunning is work in progress.
	StateRunning State = "running"
	// StateCommitted is work finished and kept.
	StateCommitted State = "committed"
	// StateFailed is work that stopped, with a code saying why.
	StateFailed State = "failed"
	// StateRecovered is what a failed job becomes once its rows have been taken
	// back out.
	StateRecovered State = "recovered"
)

// States is every state, in the order 00016's CHECK lists them.
var States = []State{StatePreview, StateRunning, StateCommitted, StateFailed, StateRecovered}

// KnownState reports whether a name is one the table accepts.
func KnownState(state State) bool {
	for _, known := range States {
		if known == state {
			return true
		}
	}
	return false
}

// Failure codes a job records instead of a message.
//
// They are codes and not sentences for the reason audit details are codes: a
// sentence is free text, and free text in a column that a client renders is a
// column where a filename lands. The set is closed and short, and the code is
// the *category* of the failure — the specific error goes back to the caller
// that asked for the job, not into the row.
const (
	// FailureCollectFailed is a source that could not be read.
	FailureCollectFailed = "collect_failed"
	// FailureArchiveTooLarge is a package that outgrew the deployment's bound.
	FailureArchiveTooLarge = "archive_too_large"
	// FailureArchiveWriteFailed is storage that could not be written.
	FailureArchiveWriteFailed = "archive_write_failed"
	// FailureTimeout is a job that ran out of its wall clock.
	FailureTimeout = "timeout"
	// FailureVerifyFailed is a package that does not match its own manifest.
	FailureVerifyFailed = "verify_failed"
	// FailureWriteFailed is an import that could not write what it was given.
	FailureWriteFailed = "write_failed"
)

// Ref names one object: what kind of thing, and which one.
type Ref struct {
	Kind Kind   `json:"kind"`
	ID   string `json:"id"`
}

// String is the ref as one word, which is how it is logged and how a set of
// them is keyed.
func (r Ref) String() string { return string(r.Kind) + "/" + r.ID }

// Validate refuses a ref this build cannot address.
func (r Ref) Validate() error {
	if !KnownKind(r.Kind) {
		return fmt.Errorf("%w: %q", ErrUnknownKind, r.Kind)
	}
	if !isIdentifier(r.ID) {
		return fmt.Errorf("%w: %q is not one of this product's ids", ErrInvalidRef, r.ID)
	}
	return nil
}

// Object is one thing on its way into a package: where it came from, and the
// bytes it became.
//
// The bytes are the object as the domain that owns it rendered it, and the
// rendering is that domain's own types encoded — not a second shape invented
// here. Two shapes would be two things to keep in step, and the failure would be
// the quiet kind: an export that stopped carrying a field the import still
// expected would produce a package that verifies perfectly and loses the field.
type Object struct {
	Ref
	Data []byte
}

// Entry is one line of the manifest: `{kind, id, sha256, bytes}`, and no more.
//
// The field set is the format (§2.13), so it is pinned rather than described —
// a reader of this format is entitled to a line it can parse without knowing
// which build wrote it, and a field added here would be a field somebody else's
// reader refuses.
type Entry struct {
	Kind   Kind   `json:"kind"`
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

// Ref is the entry's object as a ref.
func (e Entry) Ref() Ref { return Ref{Kind: e.Kind, ID: e.ID} }

// Written is one row an import has already committed.
//
// It is recorded after every batch rather than at the end of the job, and that
// ordering is the whole of §2.13's recovery design: a rollback would need a
// transaction that spans a ZIP, which is not a thing, so recovery is driven by
// this list instead. The cost is stated in the design rather than hidden here —
// recovery is exactly as correct as this list is.
type Written struct {
	Kind Kind   `json:"kind"`
	ID   string `json:"id"`
}

// Actor is who asked for a job, with the name as it was.
//
// The name is carried because an audit entry is a snapshot (§2.6) and because
// this is the last place before the write that still has it.
type Actor struct {
	authz.Caller
	Name string
}

// auditActor is the actor as the audit wants them.
func (a Actor) auditActor() audit.Actor {
	return audit.Actor{ID: a.UserID, Name: a.Name, Role: a.Role}
}

// Job is one row of workspace_jobs.
type Job struct {
	ID   string
	Kind JobKind
	// State is where the job has got to.
	State State
	// OwnerID is the account the job belongs to, or zero for a row that has
	// none. It is a pointer in storage because a deleted account leaves the job
	// behind (§00016's SET NULL) — the work outlives the worker.
	OwnerID int64
	// ArchivePath is where the package is, relative to the configured archive
	// root. It is relative so that a row keeps meaning if the deployment's root
	// moves, and so that a deployment's absolute paths are not copied into a
	// table that gets served.
	ArchivePath string
	// Manifest is what the package holds, as the export wrote it or the import
	// read it.
	Manifest []Entry
	// Written is what an import has committed so far.
	Written []Written
	// Counts is what the job reports: how many of each kind went in or out.
	Counts map[string]int
	// FailureCode is why a failed job failed, from the closed set above.
	FailureCode string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Repository is the persistence the service needs, declared on the consumer's
// side as every store in this repository is.
type Repository interface {
	// Create writes a new job row. A job id that already exists is refused by
	// the primary key rather than by a read-then-write here.
	Create(ctx context.Context, job Job) error
	// Find reads one job back by id.
	Find(ctx context.Context, jobID string) (Job, error)
	// SaveState writes the state and the failure code together, because they
	// are one answer: a failed job without a code is a job nobody can explain,
	// and a code on a job that is not failed is a code that means nothing.
	SaveState(ctx context.Context, jobID string, state State, failureCode string, now time.Time) error
	// SaveManifest writes what the package holds.
	SaveManifest(ctx context.Context, jobID string, entries []Entry) error
	// SaveCounts writes what the job reports.
	SaveCounts(ctx context.Context, jobID string, counts map[string]int) error
	// SetArchivePath writes where the package landed.
	SetArchivePath(ctx context.Context, jobID, archivePath string) error
	// SaveWritten writes what an import has committed so far. It is called after
	// every batch rather than once at the end (see Sink), because it is the whole
	// of §2.13's recovery: what is not written down is what a recovery cannot
	// take back.
	SaveWritten(ctx context.Context, jobID string, written []Written) error
	// List reads one account's jobs of one direction, newest first.
	List(ctx context.Context, ownerID int64, kind JobKind, limit int) ([]Job, error)
}

// Collector is how the service gets hold of the objects a selection names.
//
// It is a port so that the archive writer and the job state machine are
// testable without four domains behind them; the implementation that knows about
// sessions and materials is in collect.go, which is where the design puts it
// (`workspace → audit, library, notebook, reading, session, kb`).
//
// It returns every object it could read and an error for the first it could not.
// There is no partial success: a package missing one of the things it was asked
// for is a package that lies about itself, and the manifest is only worth
// anything if the two agree.
type Collector interface {
	Collect(ctx context.Context, caller authz.Caller, refs []Ref) ([]Object, error)
}

// Busy reports whether anything is running that a write would collide with.
//
// It is one number for the whole instance and not a question about the
// conversations a package names, which is §12.4's second discipline as this
// repository can keep it: an import reaches four domains, a session is one of
// them, and "is anything in flight" is the question that can be answered once.
// The count rather than a bool because the refusal says how many — a user told
// "something is running" has been told nothing they can act on.
type Busy interface {
	Running(ctx context.Context) (int64, error)
}

// Auditor records what happened to a package (§2.6).
type Auditor interface {
	Record(ctx context.Context, event audit.Event) error
}

// Errors the package refuses with.
var (
	// ErrNotConfigured is what a service built without its parts answers with.
	ErrNotConfigured = errors.New("workspace is not configured")
	// ErrUnknownKind is what a kind outside the closed set is refused with.
	ErrUnknownKind = errors.New("workspace: not a kind this build moves")
	// ErrInvalidRef is what an id that is not shaped like one of this product's
	// is refused with. It is a check on *shape* and not on existence: the id's
	// job here is to become a path inside a ZIP, and a value that could escape
	// one is refused before it is anywhere near one.
	ErrInvalidRef = errors.New("workspace: not one of this product's ids")
	// ErrEmptySelection is what an export of nothing is refused with.
	ErrEmptySelection = errors.New("workspace: nothing was selected")
	// ErrNoActor is what a job with no account behind it is refused with. It is
	// a wiring fault rather than a denial: the route is behind a guard, so a
	// job that reached here without an account was reached by a caller the
	// middleware did not see.
	ErrNoActor = errors.New("workspace: a job belongs to an account")
	// ErrTooLarge is what a package over the deployment's bound is refused
	// with, in either direction.
	ErrTooLarge = errors.New("workspace: the package exceeds this deployment's limit")
	// ErrBadPackage is what bytes that are not a package this build wrote are
	// refused with. It names the part that could not be read, so that a caller
	// can tell "this is not a ZIP" from "this is a ZIP with no manifest in it".
	ErrBadPackage = errors.New("workspace: not a package this build wrote")
	// ErrDigestMismatch is what a package whose manifest does not hash to the
	// digest written at its end is refused with. It is the check that a package
	// arrived whole, and it is deliberately separate from the per-entry checks:
	// this one says the manifest was not the manifest that was written, and the
	// others say an object was not the object it names.
	ErrDigestMismatch = errors.New("workspace: the package does not match its manifest")
	// ErrNoSuchJob is what an unknown job id is answered with.
	ErrNoSuchJob = errors.New("workspace: no such job")
	// ErrNotFound is what a ref one of the sources could not read is refused
	// with. It is the same answer an id that was never issued gets, and it has
	// to be: a caller that could tell "this is not yours" from "there is no
	// such thing" has been told that a row exists (AUTH-06).
	ErrNotFound = errors.New("workspace: nothing to export for")
	// ErrNotPreview is what committing something that never previewed is refused
	// with (§2.13's first discipline).
	ErrNotPreview = errors.New("workspace: this job has not previewed")
	// ErrNotRecoverable is what recovering a job that has nothing to take back is
	// refused with. A committed job is the case that matters: its work is kept,
	// and a recovery that ran on it would be a delete wearing recovery's name.
	ErrNotRecoverable = errors.New("workspace: this job has nothing to recover")
	// ErrBusy is what any of it is refused with while a turn is running
	// (AC-09's first sentence). The refusal is about the *instance* rather than
	// about the conversations a job names, because one import touches several
	// domains and "is anything running" is the question that can be answered.
	ErrBusy = errors.New("workspace: a turn is running")
)

// idMax is how long an object id may be: the width of workspace_jobs.job_id and
// of every id column a ref can name.
const idMax = 128

// isIdentifier reports whether a value is shaped like one of this product's ids.
//
// It is the same rule the audit applies to its targets, and it is here because
// of what an id becomes in this package: a path segment inside a ZIP. A value
// carrying `/` or `..` is refused — not because the reader would follow it, but
// because a format whose entry names are not safe to join onto a directory is a
// format every implementation of it has to defend against separately.
func isIdentifier(value string) bool {
	if value == "" || len(value) > idMax {
		return false
	}
	// A run of dots is a directory, not an id. `path.Join("session", "..")` is
	// the parent of the kind's directory rather than a member of it, and while
	// the reader would refuse the name it produced, an id whose meaning depends
	// on a reader's second look is an id that should not have been accepted.
	if value == "." || value == ".." {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9':
			continue
		}
		switch character {
		case '_', '.', '-':
			continue
		}
		return false
	}
	return true
}

// isDigest reports whether a value is a lowercase hex sha256.
func isDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') {
			continue
		}
		return false
	}
	return true
}
