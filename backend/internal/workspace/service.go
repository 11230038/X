package workspace

// The service: one export, over a collector and a store.
//
// Stage 16 is the way *out* — the package, the manifest and the job state
// machine. The way in is stage 17, and the two are in one file because they are
// one thing seen twice: a package is written by an export and read by an import,
// and the manifest that the first one produces is the second one's contract.
//
// The three disciplines of §12.4 live here and not in the handler. "Preview
// before commit" is a state a job is in, so the store is what makes it true;
// "refuse while a turn is running" is checked in stage 17's import, because an
// export reads and a read does not collide with a running turn; and "verify the
// manifest and keep the source" is the reader's rule, in archive.go.
//
// What is *not* here is a second place that decides what a package may hold. The
// collector says what an object is and the writer refuses anything that is not
// one of the four kinds, so the §12.5 list — no account settings, no Memory, no
// credentials, no llm_calls — is enforced by there being nowhere for them to go.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"backend/internal/audit"
)

// jobIDRandomBytes is how much randomness an id gets. It is the width the file
// library uses for a file id, and for the same reason: an id that collides is
// two jobs sharing one package.
const jobIDRandomBytes = 16

// jobDirectoryPerm and jobFilePerm are how a package is created. The directory
// is owner-only because a package holds an account's work; the file is the same
// mode the archive writer marks its members with.
const (
	jobDirectoryPerm = 0o700
	jobFilePerm      = 0o600
)

// Options are what the service is built from.
type Options struct {
	// Root is the directory packages are written to and read from
	// (WORKSPACE_ARCHIVE_ROOT).
	Root string
	// MaxArchiveBytes bounds one package in either direction
	// (WORKSPACE_MAX_ARCHIVE_BYTES).
	MaxArchiveBytes int64
	// JobTimeout is the wall clock one job gets (WORKSPACE_JOB_TIMEOUT).
	JobTimeout time.Duration
	// Repository is the job table.
	Repository Repository
	// Collector is how the objects a selection names are read.
	Collector Collector
	// Writer is how the objects of a package are written back (stage 17). It is
	// required even by a deployment that only exports, because the routes are
	// built from one service and a service with half a feature is a wiring
	// mistake that would otherwise be found by a user.
	Writer Writer
	// Busy reports whether anything is running that a write would collide with
	// (§12.4's second discipline). It is a port and not a query here because the
	// answer is about turns, which this package has no business knowing about.
	Busy Busy
	// Auditor is required. A package is a copy of an account's work leaving the
	// instance, and "who took it" is a question the audit exists to answer
	// (§2.6). The requirement is at construction rather than at the first
	// export, because this is a wiring mistake and a wiring mistake belongs at
	// startup.
	Auditor Auditor
	// BatchSize is how many objects one write covers before the job's record of
	// what has landed is updated. Zero means defaultBatchSize. It is here as a
	// seam for tests, which is the only reason a deployment would not want the
	// default: the size of a batch is what an interrupted import can lose.
	BatchSize int
	// Now is the clock, for tests.
	Now func() time.Time
	// NewJobID makes a job id, for tests.
	NewJobID func() (string, error)
}

// Service packs a selection out, writes a package back in, and reads back what
// it did.
type Service struct {
	root            string
	maxArchiveBytes int64
	jobTimeout      time.Duration
	batchSize       int
	repository      Repository
	collector       Collector
	writer          Writer
	busy            Busy
	auditor         Auditor
	now             func() time.Time
	newJobID        func() (string, error)
}

// NewService builds the service.
func NewService(options Options) (*Service, error) {
	if options.Repository == nil || options.Collector == nil || options.Writer == nil ||
		options.Busy == nil || options.Auditor == nil {
		return nil, ErrNotConfigured
	}
	if options.Root == "" {
		return nil, fmt.Errorf("invalid workspace configuration: the archive root is not named")
	}
	if options.MaxArchiveBytes <= 0 {
		return nil, fmt.Errorf("invalid workspace configuration: the package bound must be positive")
	}
	if options.JobTimeout <= 0 {
		return nil, fmt.Errorf("invalid workspace configuration: the job timeout must be positive")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	newJobID := options.NewJobID
	if newJobID == nil {
		newJobID = newJobIDValue
	}
	batchSize := options.BatchSize
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}
	return &Service{
		root:            options.Root,
		maxArchiveBytes: options.MaxArchiveBytes,
		jobTimeout:      options.JobTimeout,
		batchSize:       batchSize,
		repository:      options.Repository,
		collector:       options.Collector,
		writer:          options.Writer,
		busy:            options.Busy,
		auditor:         options.Auditor,
		now:             now,
		newJobID:        newJobID,
	}, nil
}

// MaxArchiveBytes is the bound one package is held to, for a caller that
// reports it.
func (s *Service) MaxArchiveBytes() int64 { return s.maxArchiveBytes }

// Root is the directory packages are kept in.
func (s *Service) Root() string { return s.root }

// ExportRequest is one pack-out: what to take, and who is taking it.
type ExportRequest struct {
	// Actor is the account the package is made for. It is both the owner the job
	// is recorded under and the caller every source read is scoped to, and the
	// second is the one that matters: an export that read without a caller would
	// be a way to pack up somebody else's work by naming their ids.
	Actor Actor
	// Refs is the selection. It is ids and not a query, because §2.13's export
	// takes "选定的" objects and a rule that decided what to take would be a
	// second answer to what the user asked for.
	Refs []Ref
}

// Export packs the selection and answers with the job that did it.
//
// The job row is written before anything is collected, so that a failure is
// visible as a job that failed rather than as an error with nothing behind it.
// That ordering is the point of the table: a caller that saw the error can come
// back for the job and be told what happened.
func (s *Service) Export(ctx context.Context, request ExportRequest) (Job, error) {
	if err := s.ready(); err != nil {
		return Job{}, err
	}
	if !request.Actor.Valid() {
		return Job{}, ErrNoActor
	}
	refs, err := selection(request.Refs)
	if err != nil {
		return Job{}, err
	}
	jobID, err := s.newJobID()
	if err != nil {
		return Job{}, err
	}
	now := s.now().UTC()
	job := Job{
		ID:        jobID,
		Kind:      JobExport,
		State:     StateRunning,
		OwnerID:   request.Actor.UserID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.repository.Create(ctx, job); err != nil {
		return Job{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, s.jobTimeout)
	defer cancel()

	objects, err := s.collector.Collect(ctx, request.Actor.Caller, refs)
	if err != nil {
		return s.reject(ctx, request.Actor, job, failureCode(err, FailureCollectFailed), err)
	}
	location, manifest, err := s.write(ctx, jobID, objects)
	if err != nil {
		return s.reject(ctx, request.Actor, job, failureCode(err, FailureArchiveWriteFailed), err)
	}
	counts := manifest.Counts()
	if err := s.repository.SetArchivePath(ctx, jobID, location); err != nil {
		return s.reject(ctx, request.Actor, job, FailureArchiveWriteFailed, err)
	}
	if err := s.repository.SaveManifest(ctx, jobID, manifest.Entries); err != nil {
		return s.reject(ctx, request.Actor, job, FailureArchiveWriteFailed, err)
	}
	if err := s.repository.SaveCounts(ctx, jobID, counts); err != nil {
		return s.reject(ctx, request.Actor, job, FailureArchiveWriteFailed, err)
	}
	if err := s.repository.SaveState(ctx, jobID, StateCommitted, "", s.now().UTC()); err != nil {
		return Job{}, err
	}
	job.State = StateCommitted
	job.ArchivePath = location
	job.Manifest = manifest.Entries
	job.Counts = counts
	job.UpdatedAt = s.now().UTC()
	s.record(ctx, request.Actor, job, audit.ResultOK, audit.Detail{Counts: counts})
	return job, nil
}

// Job reads one job back, for the status route.
func (s *Service) Job(ctx context.Context, jobID string) (Job, error) {
	if err := s.ready(); err != nil {
		return Job{}, err
	}
	return s.repository.Find(ctx, jobID)
}

// Open reads back a package this service wrote, for a caller that already has
// the job.
//
// It answers a job whose package is not where the row says with a refusal rather
// than with an empty archive: an export that reported itself committed and left
// nothing behind is a fact somebody has to be told about, and "here is a package
// with no objects in it" would be the wrong story.
//
// This is the *parsing* way in, and the only thing that uses it is the import
// preview — a preview has to look at every member before it writes anything, and
// that means the whole package is in memory while it does. A caller that means
// to hand the bytes on unread wants Download instead; see the note there.
func (s *Service) Open(ctx context.Context, job Job) (*Archive, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	file, size, err := s.openPackage(job)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return OpenArchive(file, size)
}

// StoredArchive is a package on its way out: the bytes, how many of them there
// are, and nothing parsed.
//
// It carries no type, because a package has none to carry — the row is a ZIP and
// the only honest name for it is application/zip, which the route supplies. It
// is a different shape from library.StoredFile for the reason every domain has
// its own: a package is this service's subject, and a file the library's.
type StoredArchive struct {
	// Content is the package itself, opened for reading. The caller closes it.
	Content io.ReadCloser
	// SizeBytes is the whole package, manifest and all.
	SizeBytes int64
}

// Download opens the package of a job for a caller that intends to stream it.
//
// It exists beside Open because the two are different questions, and the
// difference is the whole point of §2.5: Open parses a package — every member's
// bytes end up in an Archive's Objects map — which is what a preview needs and
// what a download must never do, because one package may be
// WORKSPACE_MAX_ARCHIVE_BYTES (512 MiB by default) and streaming that to a
// socket through a parsed copy of itself is the entire file resident for no
// reason at all.
//
// So this answers a stream and a length and nothing else: no OpenArchive, no
// manifest, no digest, no objects. What makes that a rule rather than an
// intention is that both ways in resolve the path through openPackage below, so
// a job's package means one thing in both; the parsing stays on Open's side of
// the line, where its only caller is the import preview.
func (s *Service) Download(ctx context.Context, job Job) (*StoredArchive, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	file, size, err := s.openPackage(job)
	if err != nil {
		return nil, err
	}
	return &StoredArchive{Content: file, SizeBytes: size}, nil
}

// openPackage resolves the package a job names under the archive root and opens
// it, answering how many bytes it holds. The caller closes what it gets back.
//
// It is the one place that decides what "this job's package" means, and it is
// shared rather than written twice because the two callers above must not drift:
// a download that resolved a path differently from the preview would be a way to
// hand out something the preview never looked at.
//
// A job whose row names no package is refused here rather than answered with an
// empty file: "this job has no package" and "here is a package with nothing in
// it" are different stories about what the job did, and only the first is true.
// The path is joined onto the root rather than used as given, because
// archive_path is relative by construction (`write` below) and a row that had
// become absolute must not be able to name a file outside the root.
func (s *Service) openPackage(job Job) (*os.File, int64, error) {
	if job.ArchivePath == "" {
		return nil, 0, fmt.Errorf("%w: job %s has no package", ErrBadPackage, job.ID)
	}
	file, err := os.Open(filepath.Join(s.root, job.ArchivePath))
	if err != nil {
		return nil, 0, fmt.Errorf("open the package of job %s", job.ID)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, fmt.Errorf("read the package of job %s", job.ID)
	}
	return file, info.Size(), nil
}

// write collects nothing and writes everything: the objects it is given become
// one package under the root, and the path it returns is relative to the root.
//
// The package is written to a temporary file in the same directory and renamed
// into place, so that a job is never pointed at a half-written package. A rename
// within one directory is the one filesystem operation that is atomic everywhere
// this runs, and it is what makes "the row names a path" and "the path holds a
// package" the same statement.
func (s *Service) write(ctx context.Context, jobID string, objects []Object) (string, Manifest, error) {
	if err := ctx.Err(); err != nil {
		return "", Manifest{}, err
	}
	if err := os.MkdirAll(s.root, jobDirectoryPerm); err != nil {
		return "", Manifest{}, fmt.Errorf("create the archive root")
	}
	staging, err := os.CreateTemp(s.root, ".writing-*")
	if err != nil {
		return "", Manifest{}, fmt.Errorf("create a package in the archive root")
	}
	stagingPath := staging.Name()
	defer func() {
		staging.Close()
		os.Remove(stagingPath)
	}()

	// The bound is enforced while writing rather than by measuring afterwards: a
	// check that ran once the file was closed would have put the bytes on the
	// disk and then told the caller about a limit. It bounds the whole file and
	// not the sum of the objects, which is what WORKSPACE_MAX_ARCHIVE_BYTES
	// means — how big a package may be, manifest and all.
	bounded := &countingWriter{inner: staging, maxBytes: s.maxArchiveBytes}
	manifest, err := WriteArchive(bounded, objects)
	if err != nil {
		return "", Manifest{}, err
	}
	if err := staging.Sync(); err != nil {
		return "", Manifest{}, fmt.Errorf("flush the package")
	}
	if err := staging.Close(); err != nil {
		return "", Manifest{}, fmt.Errorf("close the package")
	}
	name := jobID + ".zip"
	if err := os.Rename(stagingPath, filepath.Join(s.root, name)); err != nil {
		return "", Manifest{}, fmt.Errorf("put the package in the archive root")
	}
	return name, manifest, nil
}

// reject marks a job failed, records the failure and returns the error.
//
// The job is marked before the error is returned, and the ordering is the whole
// reason the caller gets a job id back for a failure: what went wrong is in the
// row, and the error that travels up is the account's answer rather than the
// record of it.
func (s *Service) reject(ctx context.Context, actor Actor, job Job, code string, cause error) (Job, error) {
	// The context may be the one that just ran out, so the row is written on a
	// context that has not: marking the job failed is the one write that must
	// happen even when the reason it is happening was a deadline.
	recordCtx := context.WithoutCancel(ctx)
	now := s.now().UTC()
	if err := s.repository.SaveState(recordCtx, job.ID, StateFailed, code, now); err != nil {
		return Job{}, errors.Join(cause, err)
	}
	job.State = StateFailed
	job.FailureCode = code
	job.UpdatedAt = now
	detail := audit.Detail{Counts: job.Counts}
	detail.Reason = code
	s.record(recordCtx, actor, job, audit.ResultFailed, detail)
	return job, cause
}

// record writes one audit entry about a job.
//
// A failure to record is not returned. The work has already happened, and an
// export that succeeded and then refused to say so would be a worse answer than
// one whose audit line is missing — which is the same call the library's
// download makes for the same reason.
//
// The action comes from the job rather than from the caller, because the two
// directions are two actions (§2.6) and a call site that had to remember which
// one it was is a call site that will one day record an import as an export.
func (s *Service) record(ctx context.Context, actor Actor, job Job, result audit.Result, detail audit.Detail) {
	s.recordAs(ctx, actor, job, actionOf(job), result, detail)
}

// recordAs is record under an action the job's direction does not name, which is
// the recovery and nothing else.
func (s *Service) recordAs(ctx context.Context, actor Actor, job Job, action audit.Action, result audit.Result, detail audit.Detail) {
	event := audit.Event{
		Actor:  actor.auditActor(),
		Action: action,
		Target: audit.Ref{Kind: audit.TargetWorkspaceJob, ID: job.ID},
		Result: result,
		Detail: detail,
	}
	_ = s.auditor.Record(ctx, event)
}

// actionOf is the audit action one job's direction is recorded under.
func actionOf(job Job) audit.Action {
	if job.Kind == JobImport {
		return audit.ActionWorkspaceImported
	}
	return audit.ActionWorkspaceExported
}

func (s *Service) ready() error {
	if s == nil || s.repository == nil || s.collector == nil || s.writer == nil ||
		s.busy == nil || s.auditor == nil {
		return ErrNotConfigured
	}
	return nil
}

// selection validates and de-duplicates a selection.
//
// A ref named twice is folded once rather than refused, because "give me these"
// said twice by a client that concatenated two lists is one request; a ref named
// twice that reached the collector would come back as two objects with one name,
// which the writer refuses — and that refusal would arrive as a bug report about
// the writer.
func selection(refs []Ref) ([]Ref, error) {
	if len(refs) == 0 {
		return nil, ErrEmptySelection
	}
	unique := make([]Ref, 0, len(refs))
	seen := make(map[Ref]struct{}, len(refs))
	for _, ref := range refs {
		if err := ref.Validate(); err != nil {
			return nil, err
		}
		if _, repeated := seen[ref]; repeated {
			continue
		}
		seen[ref] = struct{}{}
		unique = append(unique, ref)
	}
	return unique, nil
}

// failureCode names why a failure failed.
//
// Two of the codes are recognised from the error itself, because both can be
// raised by either half of a job: a package can outgrow its bound while it is
// being collected or while it is being written, and either half can run out of
// the job's wall clock. What is left is the step's own code, passed in, because
// "the sources could not be read" and "the package could not be written" are the
// same shape of failure in different places and only the caller knows which.
func failureCode(err error, fallback string) string {
	switch {
	case errors.Is(err, ErrTooLarge):
		return FailureArchiveTooLarge
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return FailureTimeout
	default:
		return fallback
	}
}

func newJobIDValue() (string, error) {
	value := make([]byte, jobIDRandomBytes)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("create a job id")
	}
	return "job_" + hex.EncodeToString(value), nil
}
