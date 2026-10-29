package practice

import (
	"context"
	"fmt"
	"time"

	"backend/internal/authz"
	"gorm.io/gorm"
)

// Repository is the storage the review path needs.
//
// Every method here is one statement, and the shape of the interface is what
// keeps it that way: the decision about what one submission *is* — whether it
// has been recorded before, whether the version matches, whether the answer face
// moves — belongs to the service, and the repository answers questions rather
// than asking them. The transaction is the service's too, which is why the only
// way in is Within: a repository that could be used without a transaction would
// be a repository that could write half a submission.
//
// Every method takes the caller last, and none of them decides anything about
// it: §2.3 makes the account a filter rather than a check, so the caller's
// questions are the only ones a review state or a review event can be reached
// through, and a question belonging to another account is answered as absent.
// Stats takes a StatsQuery, which carries the caller inside it.
//
// The reads are in repository_read.go and the writes in repository_write.go;
// the ownership rule both of them are narrowed by is in repository_scope.go.
type Repository interface {
	// Within runs work against one transaction. The repository it hands over is
	// bound to that transaction and is the only one the work may use: a write
	// made through the outer repository while a transaction is open would be
	// committed on its own, and would survive the rollback that was supposed to
	// take it back.
	Within(ctx context.Context, work func(Repository) error) error

	// Entry reads a question, its answer face, and the state beside it.
	Entry(ctx context.Context, questionID string, caller authz.Caller) (StoredEntry, error)
	// Queue reads the questions to practise now, at most size of them. The time
	// is the caller's: "due" is a comparison against a clock, and a repository
	// that read one would make every case about the day it ran on.
	Queue(ctx context.Context, now time.Time, size int, caller authz.Caller) ([]QueueItem, error)
	// Stats reads the practice history of one window, bucket by bucket, with the
	// distribution of that window's questions.
	Stats(ctx context.Context, query StatsQuery) (Stats, error)
	// Mistakes reads the wrong-answer set twice: how many of its questions were
	// reviewed in the window, and how many are in it now.
	Mistakes(ctx context.Context, from, to time.Time, caller authz.Caller) (MistakeProgress, error)
	// DueTimes reads the stored due time of the named questions, keyed by the
	// bank's row id. A question with no state row is absent: it has never been
	// reviewed, and that is not a due time.
	//
	// It is separate from Queue and not a narrower Queue, because the two answer
	// different questions: Queue asks which questions a sitting should ask, and
	// this asks when the questions somebody is already looking at come back.
	DueTimes(ctx context.Context, entryIDs []string, caller authz.Caller) (map[string]time.Time, error)
	// RecordedReview reads an event row by its request id, ErrNotFound when
	// there is none.
	RecordedReview(ctx context.Context, requestID string, caller authz.Caller) (StoredReview, error)

	// RecordReview writes one event. ErrDuplicate means this request id has
	// already been recorded.
	RecordReview(ctx context.Context, review StoredReview, caller authz.Caller) error
	// StartState writes the first state row for a question, at version 0.
	// ErrDuplicate means somebody else created it first.
	StartState(ctx context.Context, entryID string, state State, caller authz.Caller) error
	// AdvanceState moves a state row from one version to the next, and reports
	// whether it was still at the version it was read at: false is the answer
	// this whole path exists to be able to give.
	AdvanceState(ctx context.Context, entryID string, expected int64, state State, caller authz.Caller) (bool, error)
	// RecordAnswer writes the answer face of a submission. What it leaves out is
	// AnswerFace's business, not this method's.
	RecordAnswer(ctx context.Context, entryID string, face AnswerFace, now time.Time, caller authz.Caller) error
}

// GORMRepository reads and writes the review tables through GORM (seekdb over
// the MySQL protocol).
type GORMRepository struct {
	db *gorm.DB
}

// NewGORMRepository creates a storage adapter over an existing GORM handle.
func NewGORMRepository(db *gorm.DB) *GORMRepository {
	return &GORMRepository{db: db}
}

var _ Repository = (*GORMRepository)(nil)

// Within runs work in one transaction.
func (r *GORMRepository) Within(ctx context.Context, work func(Repository) error) error {
	if err := r.ready(); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return work(&GORMRepository{db: tx})
	})
}

// ready is the guard every method opens with: a repository with no handle is a
// wiring fault, and the error names it rather than letting a nil dereference
// decide what the caller is told.
func (r *GORMRepository) ready() error {
	if r == nil || r.db == nil {
		return fmt.Errorf("practice repository is not initialized")
	}
	return nil
}
