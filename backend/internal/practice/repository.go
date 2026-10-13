package practice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"backend/internal/data/models"
	"backend/internal/notebook"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// tableState is the review state table. The event table is only ever reached
// through its model, because every read of it is by primary key and there is
// nothing to name.
const tableState = "practice_review_state"

// Repository is the storage the review path needs.
//
// Every method here is one statement, and the shape of the interface is what
// keeps it that way: the decision about what one submission *is* — whether it
// has been recorded before, whether the version matches, whether the answer face
// moves — belongs to the service, and the repository answers questions rather
// than asking them. The transaction is the service's too, which is why the only
// way in is Within: a repository that could be used without a transaction would
// be a repository that could write half a submission.
type Repository interface {
	// Within runs work against one transaction. The repository it hands over is
	// bound to that transaction and is the only one the work may use: a write
	// made through the outer repository while a transaction is open would be
	// committed on its own, and would survive the rollback that was supposed to
	// take it back.
	Within(ctx context.Context, work func(Repository) error) error

	// Entry reads a question, its answer face, and the state beside it.
	Entry(ctx context.Context, questionID string) (StoredEntry, error)
	// RecordedReview reads an event row by its request id, ErrNotFound when
	// there is none.
	RecordedReview(ctx context.Context, requestID string) (StoredReview, error)

	// RecordReview writes one event. ErrDuplicate means this request id has
	// already been recorded.
	RecordReview(ctx context.Context, review StoredReview) error
	// StartState writes the first state row for a question, at version 0.
	// ErrDuplicate means somebody else created it first.
	StartState(ctx context.Context, entryID string, state State) error
	// AdvanceState moves a state row from one version to the next, and reports
	// whether it was still at the version it was read at: false is the answer
	// this whole path exists to be able to give.
	AdvanceState(ctx context.Context, entryID string, expected int64, state State) (bool, error)
	// RecordAnswer writes the answer face of a submission. What it leaves out is
	// AnswerFace's business, not this method's.
	RecordAnswer(ctx context.Context, entryID string, face AnswerFace, now time.Time) error
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

// stateOfQuestionSQL reads the review state of a question by the question's
// content address, in the same statement as the row key the state is filed
// under.
//
// The join is here because the state table is keyed by the row key and the
// outside world addresses a question by its content, and because the two have to
// be read together anyway — see Entry. `case` is backquoted because it is a
// MySQL reserved word; the column keeps its name (§2.7).
const stateOfQuestionSQL = "SELECT e.notebook_entries_id AS id, " +
	"s.first_wrong_time, s.due_time, s.last_review_time, s.is_mistake, " +
	"s.`case` AS ease, s.streak, s.review_count, s.lapses, s.version " +
	"FROM notebook_entries e " +
	"LEFT JOIN practice_review_state s ON s.notebook_entries_id = e.notebook_entries_id " +
	"WHERE e.question_id = ?"

// Entry reads the question and the review state waiting beside it.
//
// The state is read first, and that order is the whole reason these are one
// method rather than two. A submission is guarded by the version it read, so a
// successful guard is a promise that nothing advanced the question between the
// state read and the write. The answer face read after the state therefore
// belongs to the generation the guard was about — and the attempt count in it is
// the one this submission is about to increment. Read the other way round, the
// face could be a generation older than the version, and the answer would be
// counted as it was before the review that slipped in between.
//
// The two statements are not one transaction of their own. The caller's
// transaction is what makes the pair consistent, because the guard is what
// turns the state read into a promise about everything after it.
func (r *GORMRepository) Entry(ctx context.Context, questionID string) (StoredEntry, error) {
	if err := r.ready(); err != nil {
		return StoredEntry{}, err
	}
	var state struct {
		ID             string     `gorm:"column:id"`
		FirstWrongTime *time.Time `gorm:"column:first_wrong_time"`
		DueTime        *time.Time `gorm:"column:due_time"`
		LastReviewTime *time.Time `gorm:"column:last_review_time"`
		IsMistake      *bool      `gorm:"column:is_mistake"`
		Ease           *float64   `gorm:"column:ease"`
		Streak         *int32     `gorm:"column:streak"`
		ReviewCount    *int32     `gorm:"column:review_count"`
		Lapses         *int32     `gorm:"column:lapses"`
		Version        *int64     `gorm:"column:version"`
	}
	err := r.db.WithContext(ctx).Raw(stateOfQuestionSQL, questionID).Scan(&state).Error
	if err != nil {
		return StoredEntry{}, fmt.Errorf("read the review state of question %s: %w", questionID, err)
	}
	// The left join answers even when the question is not there, with the row
	// key empty: the state has nothing to join to, and the key comes from the
	// question side.
	if state.ID == "" {
		return StoredEntry{}, fmt.Errorf("%w: %s", ErrNotFound, questionID)
	}

	var record models.NotebookEntry
	err = r.db.WithContext(ctx).First(&record, "notebook_entries_id = ?", state.ID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// It was there for the join and gone for this read, so it was removed in
		// between. Reporting it as absent is the truth about it now.
		return StoredEntry{}, fmt.Errorf("%w: %s", ErrNotFound, questionID)
	}
	if err != nil {
		return StoredEntry{}, fmt.Errorf("read question %s: %w", questionID, err)
	}
	question, err := notebook.QuestionOf(record)
	if err != nil {
		return StoredEntry{}, err
	}

	entry := StoredEntry{ID: state.ID, Question: question, Answer: notebook.AnswerOf(record)}
	if state.Version != nil {
		entry.State = &StoredState{
			Version: *state.Version,
			State: State{
				Streak:         intsFrom(state.Streak),
				Lapses:         intsFrom(state.Lapses),
				ReviewCount:    intsFrom(state.ReviewCount),
				Ease:           floatsFrom(state.Ease),
				IsMistake:      state.IsMistake != nil && *state.IsMistake,
				FirstWrongTime: state.FirstWrongTime,
				LastReviewTime: timesFrom(state.LastReviewTime),
				DueTime:        timesFrom(state.DueTime),
			},
		}
	}
	return entry, nil
}

// RecordedReview reads one event row by its request id.
func (r *GORMRepository) RecordedReview(ctx context.Context, requestID string) (StoredReview, error) {
	if err := r.ready(); err != nil {
		return StoredReview{}, err
	}
	var record models.PracticeReviewEvent
	err := r.db.WithContext(ctx).First(&record, "request_id = ?", requestID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return StoredReview{}, fmt.Errorf("%w: review %s", ErrNotFound, requestID)
	}
	if err != nil {
		return StoredReview{}, fmt.Errorf("read review %s: %w", requestID, err)
	}
	return StoredReview{
		RequestID:         record.RequestID,
		NotebookEntriesID: record.NotebookEntriesID,
		UserAnswer:        stringFrom(record.UserAnswer),
		Rating:            record.Rating,
		Outcome:           record.Outcome,
		ReviewTime:        timesFrom(record.ReviewTime),
	}, nil
}

// RecordReview writes one event row.
//
// The request id is the primary key, so a duplicate is not a failure of this
// write: it is the answer to "has this submission been recorded already", given
// by the only thing that can give it without a race. The transaction is what
// makes the answer usable — a check made by reading and then inserting has a
// window between the two, and a submission recorded twice is exactly what this
// path exists to prevent.
func (r *GORMRepository) RecordReview(ctx context.Context, review StoredReview) error {
	if err := r.ready(); err != nil {
		return err
	}
	record := models.PracticeReviewEvent{
		RequestID:         review.RequestID,
		NotebookEntriesID: review.NotebookEntriesID,
		UserAnswer:        nullable(review.UserAnswer),
		Rating:            review.Rating,
		Outcome:           datatypes.JSON(review.Outcome),
		ReviewTime:        &review.ReviewTime,
	}
	return duplicateOr(r.db.WithContext(ctx).Create(&record).Error, "record review %s", review.RequestID)
}

// StartState writes the first state row for a question.
func (r *GORMRepository) StartState(ctx context.Context, entryID string, state State) error {
	if err := r.ready(); err != nil {
		return err
	}
	columns := stateColumns(state)
	columns["notebook_entries_id"] = entryID
	// The row starts at zero, before its first advance: a client that read no
	// version sends nothing, and a client that sends the zero it was handed
	// means the same thing.
	columns["version"] = 0
	return duplicateOr(r.db.WithContext(ctx).Table(tableState).Create(columns).Error,
		"start the review state of %s", entryID)
}

// AdvanceState moves a state row from the version it was read at to the next
// one, and reports whether it was still there to move.
//
// `version = version + 1` and not a value: the version that lands is the one the
// row had plus one, which is what makes the guard a comparison rather than a
// write, and which stays correct when two writers reach the row in the same
// instant and only the first may count.
func (r *GORMRepository) AdvanceState(ctx context.Context, entryID string, expected int64, state State) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	columns := stateColumns(state)
	columns["version"] = gorm.Expr("version + 1")
	result := r.db.WithContext(ctx).Table(tableState).
		Where("notebook_entries_id = ? AND version = ?", entryID, expected).
		Updates(columns)
	if result.Error != nil {
		return false, fmt.Errorf("advance the review state of %s: %w", entryID, result.Error)
	}
	return result.RowsAffected == 1, nil
}

// RecordAnswer writes the answer face of one submission.
//
// The columns are named one by one rather than derived, and this is the
// statement where that matters most: it runs after a successful guard and
// nothing else will fix what it writes. What it writes is the face and the
// update time and nothing else — not the question, not the marks a review does
// not speak for, and not the row key.
func (r *GORMRepository) RecordAnswer(ctx context.Context, entryID string, face AnswerFace, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}
	columns := map[string]any{
		"user_answer":   nullable(face.UserAnswer),
		"is_correct":    face.IsCorrect,
		"result":        nullable(face.Result),
		"attempt_count": face.AttemptCount,
		"update_time":   now,
	}
	if face.Resolved != nil {
		columns["resolved"] = *face.Resolved
	}
	err := r.db.WithContext(ctx).Table("notebook_entries").
		Where("notebook_entries_id = ?", entryID).
		Updates(columns).Error
	if err != nil {
		return fmt.Errorf("write the answer face of %s: %w", entryID, err)
	}
	return nil
}

// stateColumns is the eight curve columns of one state, as a write wants them.
//
// They are named here once and read back in Entry, which is the pair a new
// column has to be added to. A zero value is a value — a wrong answer writes a
// streak of zero, a repaired question writes no mistake — so this is a map and
// not a struct: GORM drops the zero fields of a struct, and dropping them is how
// a curve stops moving.
func stateColumns(state State) map[string]any {
	return map[string]any{
		"first_wrong_time": state.FirstWrongTime,
		"due_time":         state.DueTime,
		"last_review_time": state.LastReviewTime,
		"is_mistake":       state.IsMistake,
		"case":             state.Ease,
		"streak":           state.Streak,
		"review_count":     state.ReviewCount,
		"lapses":           state.Lapses,
	}
}

// duplicateOr turns a collision on a primary key into ErrDuplicate.
//
// Which collision it was is the caller's to say, because the two statements this
// is used by mean different things by it — and a repository that answered
// "somebody else got there first" would be answering a question it was not
// asked.
func duplicateOr(err error, what string, args ...any) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return fmt.Errorf("%w: %s", ErrDuplicate, fmt.Sprintf(what, args...))
	}
	return fmt.Errorf("%s: %w", fmt.Sprintf(what, args...), err)
}

func (r *GORMRepository) ready() error {
	if r == nil || r.db == nil {
		return fmt.Errorf("practice repository is not initialized")
	}
	return nil
}

func nullable(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func stringFrom(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func timesFrom(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

func intsFrom(value *int32) int {
	if value == nil {
		return 0
	}
	return int(*value)
}

func floatsFrom(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}
