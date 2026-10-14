package workspace

// The store behind a job: one insert, one select, and four column writes.
//
// There is no Delete. A job row is the record of a package having been made or
// taken in, and a record that can be removed is not a record — the same reason
// the audit has no delete-by-id. A deployment that wants rows gone removes them
// by age, which is a statement somebody runs rather than a method this package
// offers, and which is how the audit's window works too.
//
// Nothing here validates a state against a transition. What a job may become is
// a rule about the *job*, and rules live above this line — the store's job is to
// write what it is told, and 00016's CHECK is what stops it writing a state the
// table does not have.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"backend/internal/data"
	"backend/internal/data/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// GORMRepository persists workspace_jobs through GORM.
type GORMRepository struct {
	db *gorm.DB
}

// NewGORMRepository creates a store over an existing handle.
func NewGORMRepository(db *gorm.DB) *GORMRepository { return &GORMRepository{db: db} }

func (r *GORMRepository) ready() error {
	if r == nil || r.db == nil {
		return fmt.Errorf("workspace store is not initialized")
	}
	return nil
}

// Create writes a new job row.
func (r *GORMRepository) Create(ctx context.Context, job Job) error {
	if err := r.ready(); err != nil {
		return err
	}
	if err := job.validate(); err != nil {
		return err
	}
	manifest, err := encodeEntries(job.Manifest)
	if err != nil {
		return err
	}
	written, err := encodeWritten(job.Written)
	if err != nil {
		return err
	}
	counts, err := encodeCounts(job.Counts)
	if err != nil {
		return err
	}
	row := models.WorkspaceJob{
		JobID:       job.ID,
		Kind:        string(job.Kind),
		State:       string(job.State),
		OwnerID:     data.OwnerPointer(job.OwnerID),
		ArchivePath: job.ArchivePath,
		Manifest:    manifest,
		Written:     written,
		Counts:      counts,
		FailureCode: job.FailureCode,
		// The row's moment is the one the service chose, not the one the insert
		// happened at. GORM fills a timestamp column from the clock only when the
		// value is zero, so a job that carries one keeps it — and a job whose
		// state and whose start time are read together is one a client can show.
		CreatedAt: job.CreatedAt,
		UpdatedAt: job.UpdatedAt,
	}
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("create workspace job: %w", err)
	}
	return nil
}

// Find reads one job back by id.
func (r *GORMRepository) Find(ctx context.Context, jobID string) (Job, error) {
	if err := r.ready(); err != nil {
		return Job{}, err
	}
	var row models.WorkspaceJob
	err := r.db.WithContext(ctx).Where("job_id = ?", jobID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// A job that is not there and a job that is not yours are one answer.
		// Which account a job belongs to is the route's business (§2.16), and a
		// store that distinguished the two would hand the distinction to a
		// caller that must not have it (AUTH-06).
		return Job{}, fmt.Errorf("%w: %s", ErrNoSuchJob, jobID)
	}
	if err != nil {
		return Job{}, fmt.Errorf("read workspace job: %w", err)
	}
	return jobOf(row)
}

// SaveState writes the state and the failure code together.
func (r *GORMRepository) SaveState(ctx context.Context, jobID string, state State, failureCode string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}
	if !KnownState(state) {
		return fmt.Errorf("workspace: %q is not a state this table accepts", state)
	}
	if failureCode != "" && !knownFailure(failureCode) {
		return fmt.Errorf("workspace: %q is not a failure code this build writes", failureCode)
	}
	updated, err := r.touch(ctx, jobID, map[string]any{
		"state":        string(state),
		"failure_code": failureCode,
	}, now)
	if err != nil {
		return err
	}
	if updated == 0 {
		return fmt.Errorf("%w: %s", ErrNoSuchJob, jobID)
	}
	return nil
}

// SaveManifest writes what the package holds.
func (r *GORMRepository) SaveManifest(ctx context.Context, jobID string, entries []Entry) error {
	if err := r.ready(); err != nil {
		return err
	}
	encoded, err := encodeEntries(entries)
	if err != nil {
		return err
	}
	return r.update(ctx, jobID, "manifest_json", encoded)
}

// SaveCounts writes what the job reports.
func (r *GORMRepository) SaveCounts(ctx context.Context, jobID string, counts map[string]int) error {
	if err := r.ready(); err != nil {
		return err
	}
	encoded, err := encodeCounts(counts)
	if err != nil {
		return err
	}
	return r.update(ctx, jobID, "counts_json", encoded)
}

// SetArchivePath writes where the package landed.
func (r *GORMRepository) SetArchivePath(ctx context.Context, jobID, archivePath string) error {
	if err := r.ready(); err != nil {
		return err
	}
	updated, err := r.touch(ctx, jobID, map[string]any{"archive_path": archivePath}, time.Now().UTC())
	if err != nil {
		return err
	}
	if updated == 0 {
		return fmt.Errorf("%w: %s", ErrNoSuchJob, jobID)
	}
	return nil
}

// SaveWritten writes what an import has committed so far.
func (r *GORMRepository) SaveWritten(ctx context.Context, jobID string, written []Written) error {
	if err := r.ready(); err != nil {
		return err
	}
	for _, row := range written {
		if !KnownKind(row.Kind) || !isIdentifier(row.ID) {
			return fmt.Errorf("workspace: %s is not a row this table may record", row.Ref())
		}
	}
	encoded, err := encodeWritten(written)
	if err != nil {
		return err
	}
	return r.update(ctx, jobID, "written_json", encoded)
}

// List reads one account's jobs of one direction, newest first.
//
// The order is `creat_time DESC, job_id DESC` and the id is the tiebreak for the
// reason every ordering in this repository has one: DATETIME keeps whole
// seconds, so two packages written in the same second would otherwise come back
// in whatever order the storage chose, and a list that reshuffles between two
// identical requests is a list nobody can page through.
//
// The owner is part of the query and not a filter applied afterwards. A read
// that fetched the table and narrowed it in Go would be a read that once, in one
// version, forgot to narrow it (§2.3).
func (r *GORMRepository) List(ctx context.Context, ownerID int64, kind JobKind, limit int) ([]Job, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if ownerID <= 0 {
		return nil, fmt.Errorf("workspace: a listing belongs to an account")
	}
	if !KnownJobKind(kind) {
		return nil, fmt.Errorf("workspace: %q is not a kind of job this table has", kind)
	}
	query := r.db.WithContext(ctx).
		Where("owner_id = ? AND kind = ?", ownerID, string(kind)).
		Order("creat_time DESC, job_id DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	var rows []models.WorkspaceJob
	if err := query.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list workspace jobs: %w", err)
	}
	jobs := make([]Job, 0, len(rows))
	for _, row := range rows {
		job, err := jobOf(row)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func (r *GORMRepository) update(ctx context.Context, jobID, column string, value any) error {
	updated, err := r.touch(ctx, jobID, map[string]any{column: value}, time.Now().UTC())
	if err != nil {
		return err
	}
	if updated == 0 {
		return fmt.Errorf("%w: %s", ErrNoSuchJob, jobID)
	}
	return nil
}

// touch writes columns on one row and reports how many rows answered.
//
// The count is returned rather than treated as success, because a write that
// matched nothing is the difference between a job that moved and a job id nobody
// has — the same distinction `session` makes when it re-reads to tell an absent
// row from an unchanged one.
//
// "Matched" is the word the re-read below is for. seekdb reports the rows a
// statement *changed*, not the rows it found, so a write that sets a column to
// the value it already holds answers zero — and so does the commit's own
// clearing write when it lands in the same second as the write before it, since
// `update_time` is part of every statement this makes. Zero would otherwise be
// read as a job that is not there, which is why the answer is looked up rather
// than inferred.
//
// `update_time` is set here rather than left to GORM's autoUpdateTime, because
// the write is by column name: a column list is what keeps a state change from
// also rewriting the manifest, and the timestamp is one of the columns it has to
// write by hand when it does.
func (r *GORMRepository) touch(ctx context.Context, jobID string, columns map[string]any, now time.Time) (int64, error) {
	if err := r.ready(); err != nil {
		return 0, err
	}
	columns["update_time"] = now
	result := r.db.WithContext(ctx).
		Model(&models.WorkspaceJob{}).
		Where("job_id = ?", jobID).
		Updates(columns)
	if result.Error != nil {
		return 0, fmt.Errorf("update workspace job: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		var present int64
		if err := r.db.WithContext(ctx).Model(&models.WorkspaceJob{}).
			Where("job_id = ?", jobID).Count(&present).Error; err != nil {
			return 0, fmt.Errorf("look for workspace job: %w", err)
		}
		return present, nil
	}
	return result.RowsAffected, nil
}

// jobOf turns a row into a job.
//
// The three JSON columns each decode into a typed value here, so that a caller
// never sees the storage's shape. A column holding something this build cannot
// read is an error rather than an empty result: a job whose manifest cannot be
// parsed is a job whose package content is unknown, and answering "it holds
// nothing" would be a lie with consequences.
func jobOf(row models.WorkspaceJob) (Job, error) {
	journal := Job{
		ID:          row.JobID,
		Kind:        JobKind(row.Kind),
		State:       State(row.State),
		ArchivePath: row.ArchivePath,
		FailureCode: row.FailureCode,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
	if row.OwnerID != nil {
		journal.OwnerID = *row.OwnerID
	}
	if !KnownJobKind(journal.Kind) {
		return Job{}, fmt.Errorf("workspace: job %s has kind %q", row.JobID, row.Kind)
	}
	if !KnownState(journal.State) {
		return Job{}, fmt.Errorf("workspace: job %s has state %q", row.JobID, row.State)
	}
	manifest, err := decodeEntries(row.Manifest)
	if err != nil {
		return Job{}, err
	}
	journal.Manifest = manifest
	written, err := decodeWritten(row.Written)
	if err != nil {
		return Job{}, err
	}
	journal.Written = written
	counts, err := decodeCounts(row.Counts)
	if err != nil {
		return Job{}, err
	}
	journal.Counts = counts
	return journal, nil
}

// knownFailure reports whether a code is one this build writes.
func knownFailure(code string) bool {
	switch code {
	case FailureCollectFailed, FailureArchiveTooLarge, FailureArchiveWriteFailed,
		FailureTimeout, FailureVerifyFailed, FailureWriteFailed:
		return true
	}
	return false
}

// validate refuses a job this table may not hold.
func (j Job) validate() error {
	if !isIdentifier(j.ID) {
		return fmt.Errorf("workspace: %q is not a job id", j.ID)
	}
	if !KnownJobKind(j.Kind) {
		return fmt.Errorf("workspace: %q is not a kind of job this table accepts", j.Kind)
	}
	if !KnownState(j.State) {
		return fmt.Errorf("workspace: %q is not a state this table accepts", j.State)
	}
	if j.FailureCode != "" && !knownFailure(j.FailureCode) {
		return fmt.Errorf("workspace: %q is not a failure code this build writes", j.FailureCode)
	}
	return nil
}

func encodeEntries(entries []Entry) (datatypes.JSON, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		return nil, fmt.Errorf("encode the manifest column: %w", err)
	}
	return datatypes.JSON(encoded), nil
}

func decodeEntries(raw datatypes.JSON) ([]Entry, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var entries []Entry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("decode the manifest column: %w", err)
	}
	return entries, nil
}

func encodeWritten(written []Written) (datatypes.JSON, error) {
	if len(written) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(written)
	if err != nil {
		return nil, fmt.Errorf("encode the written column: %w", err)
	}
	return datatypes.JSON(encoded), nil
}

func decodeWritten(raw datatypes.JSON) ([]Written, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var written []Written
	if err := json.Unmarshal(raw, &written); err != nil {
		return nil, fmt.Errorf("decode the written column: %w", err)
	}
	return written, nil
}

func encodeCounts(counts map[string]int) (datatypes.JSON, error) {
	if len(counts) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(counts)
	if err != nil {
		return nil, fmt.Errorf("encode the counts column: %w", err)
	}
	return datatypes.JSON(encoded), nil
}

func decodeCounts(raw datatypes.JSON) (map[string]int, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var counts map[string]int
	if err := json.Unmarshal(raw, &counts); err != nil {
		return nil, fmt.Errorf("decode the counts column: %w", err)
	}
	return counts, nil
}

var _ Repository = (*GORMRepository)(nil)
