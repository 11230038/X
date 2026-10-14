package workspace

// The way in: preview, commit, and taking it back (§2.13).
//
// Stage 16 was the way out and this is the way in, and the two share one state
// machine. What makes the read worth making is that the preview is a *job*: it
// is written down, it has an id, and the commit names it. Without that the two
// requests would be two independent uploads of the same bytes, and "preview
// before commit" would be a discipline nobody could enforce — the caller could
// commit a package they never looked at, or a different one.
//
// The three disciplines of §12.4 land here, and one of them lands next door:
//
//   - **Preview before commit** is `StatePreview`. Committing a job that is not
//     in it is refused, which is what makes the preview more than a courtesy.
//   - **Refuse while a turn is running** is checked on the two calls that write:
//     the commit and the recovery. The granularity is the instance's — one
//     query for any active turn, not a query per session the package names —
//     because an import reaches four domains and the sessions it names are only
//     one of them.
//   - **Verify the manifest and do not delete the source** is archive.go's, and
//     the commit leans on it: the package is read again here and re-verified,
//     because between the preview and the commit the file could have been
//     swapped.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"backend/internal/audit"
)

// defaultBatchSize is how many objects one write covers before the job's record
// is updated.
//
// It is the number that makes §2.13's recovery worth anything. The design's
// sentence — `written_json` must be written after *every batch* rather than at
// the end — is a claim about how much work a crash can lose, and this is the
// work. It is small enough that an interrupted import leaves a record very close
// to the truth, and large enough that a package of a few thousand objects does
// not become a few thousand updates.
const defaultBatchSize = 100

// How much of the export list a request may ask for, and how much it gets when
// it does not say. The bound is there because the list is an unbounded table:
// a client that asked for everything would get every package this account has
// ever made, and the answer would grow until the response did not fit.
const (
	defaultListLimit = 50
	maxListLimit     = 200
)

// PreviewRequest is one look at a package.
type PreviewRequest struct {
	// Actor is the account the package would be imported into. It is the owner
	// the job is recorded under and the caller every read and write of the
	// package's contents is scoped to.
	Actor Actor
	// Package is the uploaded bytes. It is read, and — once it has been
	// established that it is a package this build wrote — written down, because
	// the commit that follows names this preview's job and reads the package
	// back from the archive root rather than from the client a second time.
	Package io.ReaderAt
	// Size is how long the package is, which a ReaderAt cannot say for itself.
	Size int64
}

// CommitRequest is one commit of a package that has been previewed.
type CommitRequest struct {
	Actor Actor
	JobID string
}

// RecoverRequest is one taking-back of an import that did not commit.
type RecoverRequest struct {
	Actor Actor
	JobID string
}

// ListRequest is one read of an account's packages, one direction at a time.
type ListRequest struct {
	// OwnerID is whose packages to list. It is the account on the token and
	// never a query parameter: a listing that took an owner from the request
	// would be a listing of anybody's packages.
	OwnerID int64
	// Kind is the direction, and it is required rather than defaulted. "All of
	// my jobs" is a question with two answers of very different shapes in it —
	// what I have made and what I have taken in — and a default would pick one
	// of them silently.
	Kind JobKind
	// Limit is how many to return at most, zero for the default.
	Limit int
}

// Preview looks at a package and answers what committing it would do.
//
// Nothing of the account's is written: the four domains are read — to see what
// is already there and what is missing — and the only row this leaves behind is
// the job that records the preview itself. That row is not a side effect to be
// apologised for. §2.13's preview is a thing the user comes back to, and a
// preview with no record of itself would be a screen whose numbers nothing can
// be held to.
func (s *Service) Preview(ctx context.Context, request PreviewRequest) (Preview, error) {
	if err := s.ready(); err != nil {
		return Preview{}, err
	}
	if !request.Actor.Valid() {
		return Preview{}, ErrNoActor
	}
	if request.Package == nil || request.Size <= 0 {
		return Preview{}, fmt.Errorf("%w: nothing was uploaded", ErrBadPackage)
	}
	if request.Size > s.maxArchiveBytes {
		return Preview{}, ErrTooLarge
	}
	// The bytes are read before a job row is written, because bytes that are not
	// a package are not a failed preview — there is nothing that was looked at.
	// It is the same call §2.13's export makes for an empty selection: a request
	// that never happened leaves no record, and a record of it would be a row
	// somebody has to explain.
	archive, err := OpenArchive(request.Package, request.Size)
	if err != nil {
		return Preview{}, err
	}
	if !archive.Report.Clean() {
		return Preview{}, fmt.Errorf("%w: %s", ErrBadPackage, archive.Report.Summary())
	}

	jobID, err := s.newJobID()
	if err != nil {
		return Preview{}, err
	}
	now := s.now().UTC()
	job := Job{
		ID:        jobID,
		Kind:      JobImport,
		State:     StateRunning,
		OwnerID:   request.Actor.UserID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.repository.Create(ctx, job); err != nil {
		return Preview{}, err
	}

	objects := archive.ObjectsInOrder()
	items, err := s.writer.Plan(ctx, request.Actor.Caller, objects)
	if err != nil {
		_, _ = s.reject(ctx, request.Actor, job, failureCode(err, FailureCollectFailed), err)
		return Preview{}, err
	}
	location, err := s.stage(jobID, request.Package, request.Size)
	if err != nil {
		_, _ = s.reject(ctx, request.Actor, job, FailureArchiveWriteFailed, err)
		return Preview{}, err
	}
	if err := s.repository.SetArchivePath(ctx, jobID, location); err != nil {
		return s.rejectPreview(ctx, request.Actor, job, err)
	}
	if err := s.repository.SaveManifest(ctx, jobID, archive.Manifest.Entries); err != nil {
		return s.rejectPreview(ctx, request.Actor, job, err)
	}
	// The counts a preview records are what the *package* holds, which is the
	// same fact an export records and the number the user is about to agree to.
	// The counts of what an import really wrote are a different number and are
	// written over this one when the commit runs (see Import).
	if err := s.repository.SaveCounts(ctx, jobID, archive.Manifest.Counts()); err != nil {
		return s.rejectPreview(ctx, request.Actor, job, err)
	}
	if err := s.repository.SaveState(ctx, jobID, StatePreview, "", s.now().UTC()); err != nil {
		return Preview{}, err
	}
	return newPreview(jobID, items)
}

// Import commits a package that has been previewed.
//
// The order of the two checks that come first is the whole of the design: the
// package is read again and verified before a single row is written, so a
// package that does not match its own manifest lands *nothing* — not the part of
// it that happened to be intact. Then the plan is remade, because the world may
// have moved since the preview, and only the objects it calls `create` are
// written.
func (s *Service) Import(ctx context.Context, request CommitRequest) (Job, error) {
	if err := s.ready(); err != nil {
		return Job{}, err
	}
	if !request.Actor.Valid() {
		return Job{}, ErrNoActor
	}
	job, err := s.importJob(ctx, request.Actor, request.JobID)
	if err != nil {
		return Job{}, err
	}
	if job.State != StatePreview {
		return Job{}, fmt.Errorf("%w: job %s is %s", ErrNotPreview, job.ID, job.State)
	}
	if err := s.refuseIfBusy(ctx); err != nil {
		return Job{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, s.jobTimeout)
	defer cancel()

	archive, err := s.Open(ctx, job)
	if err != nil {
		return s.reject(ctx, request.Actor, job, FailureVerifyFailed, err)
	}
	if !archive.Report.Clean() {
		// One byte of one object changed between the preview and the commit, or
		// the package was swapped for another. Either way what was previewed is
		// not what is here, and writing the part that still matches would be
		// writing a package nobody looked at.
		return s.reject(ctx, request.Actor, job, FailureVerifyFailed,
			fmt.Errorf("%w: %s", ErrDigestMismatch, archive.Report.Summary()))
	}
	// The plan is made again rather than replayed from the preview, because the
	// rows it read are the rows it is about to write beside. Replanning is what
	// keeps "what the preview said" and "what the commit does" the same
	// statement rather than two statements made at two moments.
	items, err := s.writer.Plan(ctx, request.Actor.Caller, archive.ObjectsInOrder())
	if err != nil {
		return s.reject(ctx, request.Actor, job, failureCode(err, FailureWriteFailed), err)
	}
	objects := createsOf(items).filter(archive.ObjectsInOrder())

	// The commit starts by clearing what the preview put in the counts column.
	// From here the counts are rows really written, and a job that commits
	// nothing because everything collided must say nothing rather than repeat the
	// package's own totals back as if it had written them.
	if err := s.repository.SaveCounts(ctx, job.ID, nil); err != nil {
		return s.reject(ctx, request.Actor, job, FailureWriteFailed, err)
	}
	if err := s.repository.SaveWritten(ctx, job.ID, nil); err != nil {
		return s.reject(ctx, request.Actor, job, FailureWriteFailed, err)
	}
	if err := s.repository.SaveState(ctx, job.ID, StateRunning, "", s.now().UTC()); err != nil {
		return Job{}, err
	}
	job.State = StateRunning
	written := make([]Written, 0, len(objects))
	counts := make(map[string]int, len(Kinds))
	// The job in hand starts empty in step with the two columns just cleared, so
	// that a commit which writes nothing because every object collided reports
	// nothing rather than repeating the preview's totals back.
	job.Written = written
	job.Counts = counts

	for start := 0; start < len(objects); start += s.batchSize {
		end := min(start+s.batchSize, len(objects))
		done, writeErr := s.writer.Write(ctx, request.Actor.Caller, objects[start:end])
		written = append(written, done...)
		for _, row := range done {
			counts[string(row.Kind)]++
		}
		// The job in hand keeps step with the record, because reject reports on
		// *this* job: a failure whose audit line carried the counts the preview
		// wrote would say the package had been written when a batch of it had, and
		// the caller that asked for the job would be told the same.
		job.Written = written
		job.Counts = counts
		job.UpdatedAt = s.now().UTC()
		// After *every* batch, and before the write's own error is looked at: the
		// half of a failed batch that landed is exactly what a recovery has to
		// know about, and it is the half that a record written at the end of the
		// job would have lost. This is the sentence §2.13 spends its paragraph
		// on, and the ordering of these five lines is that sentence.
		if err := s.repository.SaveWritten(ctx, job.ID, written); err != nil {
			// The record could not be written, and that failure is worse than the
			// one it is recording: what is not written down is what a recovery
			// cannot take back. Both are reported.
			return s.reject(ctx, request.Actor, job, FailureWriteFailed, errors.Join(err, writeErr))
		}
		if err := s.repository.SaveCounts(ctx, job.ID, counts); err != nil {
			return s.reject(ctx, request.Actor, job, FailureWriteFailed, err)
		}
		if writeErr != nil {
			return s.reject(ctx, request.Actor, job, failureCode(writeErr, FailureWriteFailed), writeErr)
		}
	}
	if err := s.repository.SaveState(ctx, job.ID, StateCommitted, "", s.now().UTC()); err != nil {
		return Job{}, err
	}
	job.State = StateCommitted
	job.Written = written
	job.Counts = counts
	job.UpdatedAt = s.now().UTC()
	s.record(ctx, request.Actor, job, audit.ResultOK, audit.Detail{Counts: counts})
	return job, nil
}

// Recover takes back what a job that did not commit had written.
//
// This is §2.13's trade made visible: there is no rollback, so recovery is
// whatever the record says was written, deleted. It follows that the record has
// to be right, which is why Import writes it after every batch, and that a
// recovery can only be as complete as the job it is recovering.
func (s *Service) Recover(ctx context.Context, request RecoverRequest) (Job, error) {
	if err := s.ready(); err != nil {
		return Job{}, err
	}
	if !request.Actor.Valid() {
		return Job{}, ErrNoActor
	}
	job, err := s.importJob(ctx, request.Actor, request.JobID)
	if err != nil {
		return Job{}, err
	}
	// A committed job is not recoverable, and the refusal is the point: the work
	// is kept, and "take it back" would be a delete wearing recovery's name. A
	// job still in preview has written nothing to take back, and one that has
	// already been recovered has nothing left.
	if job.State != StateFailed && job.State != StateRunning {
		return Job{}, fmt.Errorf("%w: job %s is %s", ErrNotRecoverable, job.ID, job.State)
	}
	if err := s.refuseIfBusy(ctx); err != nil {
		return Job{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, s.jobTimeout)
	defer cancel()

	if len(job.Written) != 0 {
		if err := s.writer.Undo(ctx, request.Actor.Caller, job.Written); err != nil {
			return s.reject(ctx, request.Actor, job, FailureWriteFailed, err)
		}
	}
	taken := make(map[string]int, len(Kinds))
	for _, row := range job.Written {
		taken[string(row.Kind)]++
	}
	if err := s.repository.SaveState(ctx, job.ID, StateRecovered, "", s.now().UTC()); err != nil {
		return Job{}, err
	}
	job.State = StateRecovered
	job.UpdatedAt = s.now().UTC()
	s.recordAs(ctx, request.Actor, job, audit.ActionWorkspaceRecovered, audit.ResultOK, audit.Detail{Counts: taken})
	return job, nil
}

// List reads one account's packages, newest first.
func (s *Service) List(ctx context.Context, request ListRequest) ([]Job, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if request.OwnerID <= 0 {
		return nil, ErrNoActor
	}
	if !KnownJobKind(request.Kind) {
		return nil, fmt.Errorf("workspace: %q is not a kind of job this table has", request.Kind)
	}
	limit := request.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	return s.repository.List(ctx, request.OwnerID, request.Kind, limit)
}

// importJob reads one job and refuses anything that is not this account's
// import.
//
// The three refusals are one answer. A job that is not there, a job that belongs
// to somebody else and a job that is an export all come back as ErrNoSuchJob,
// because a caller that could tell them apart has been told that a row exists
// (AUTH-06) — and the route takes the job id from the path, so the caller is
// whoever guessed it.
func (s *Service) importJob(ctx context.Context, actor Actor, jobID string) (Job, error) {
	job, err := s.repository.Find(ctx, jobID)
	if err != nil {
		return Job{}, err
	}
	if job.Kind != JobImport || job.OwnerID != actor.UserID {
		return Job{}, fmt.Errorf("%w: %s", ErrNoSuchJob, jobID)
	}
	return job, nil
}

// refuseIfBusy refuses while anything is running.
//
// One query for the instance rather than one per session the package names,
// which is §12.4's second discipline as this repository can keep it: `turns`
// already holds at most one active turn per conversation, so the question "is
// anything in flight" is a single count, and the question "is anything in flight
// that this import would touch" is a count that would still be wrong — an import
// reaches four domains and a session is one of them.
func (s *Service) refuseIfBusy(ctx context.Context) error {
	running, err := s.busy.Running(ctx)
	if err != nil {
		return err
	}
	if running > 0 {
		return fmt.Errorf("%w: %d in flight", ErrBusy, running)
	}
	return nil
}

// stage writes the uploaded package into the archive root under the job's own
// name, so that the commit reads the bytes that were previewed.
//
// It is the same temporary-file-and-rename as an export's package, and for the
// same reason: the job must never point at a half-written file. A preview that
// failed halfway through leaves the root as it found it.
func (s *Service) stage(jobID string, packageBytes io.ReaderAt, size int64) (string, error) {
	if err := os.MkdirAll(s.root, jobDirectoryPerm); err != nil {
		return "", fmt.Errorf("create the archive root")
	}
	staging, err := os.CreateTemp(s.root, ".staging-*")
	if err != nil {
		return "", fmt.Errorf("create a package in the archive root")
	}
	stagingPath := staging.Name()
	defer func() {
		staging.Close()
		os.Remove(stagingPath)
	}()

	bounded := &countingWriter{inner: staging, maxBytes: s.maxArchiveBytes}
	if _, err := io.Copy(bounded, io.NewSectionReader(packageBytes, 0, size)); err != nil {
		return "", err
	}
	if err := staging.Sync(); err != nil {
		return "", fmt.Errorf("flush the package")
	}
	if err := staging.Close(); err != nil {
		return "", fmt.Errorf("close the package")
	}
	name := jobID + ".zip"
	if err := os.Rename(stagingPath, filepath.Join(s.root, name)); err != nil {
		return "", fmt.Errorf("put the package in the archive root")
	}
	return name, nil
}

// rejectPreview is reject for the preview's three column writes, which all fail
// the same way and all want the same answer: a preview whose record could not be
// written is a preview nobody can come back to.
func (s *Service) rejectPreview(ctx context.Context, actor Actor, job Job, cause error) (Preview, error) {
	_, err := s.reject(ctx, actor, job, FailureArchiveWriteFailed, cause)
	return Preview{}, err
}
