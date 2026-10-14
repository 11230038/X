package practice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"backend/internal/authz"
	"backend/internal/data"
	"backend/internal/data/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// RecordReview writes one event row.
//
// The request id is the primary key, so a duplicate is not a failure of this
// write: it is the answer to "has this submission been recorded already", given
// by the only thing that can give it without a race. The transaction is what
// makes the answer usable — a check made by reading and then inserting has a
// window between the two, and a submission recorded twice is exactly what this
// path exists to prevent.
func (r *GORMRepository) RecordReview(ctx context.Context, review StoredReview, caller authz.Caller) error {
	if err := r.ready(); err != nil {
		return err
	}
	// An insert has no WHERE to narrow, so the question this event is filed under
	// is read through the scope first: without it a caller holding another
	// account's row key could attach a review to that account's question.
	if err := requireOwnedEntry(r.db, ctx, review.NotebookEntriesID, caller); err != nil {
		return err
	}
	record := models.PracticeReviewEvent{
		RequestID:         review.RequestID,
		NotebookEntriesID: review.NotebookEntriesID,
		UserAnswer:        nullable(review.UserAnswer),
		Rating:            review.Rating,
		IsCorrect:         review.IsCorrect,
		Outcome:           datatypes.JSON(review.Outcome),
		ReviewTime:        &review.ReviewTime,
	}
	return duplicateOr(r.db.WithContext(ctx).Create(&record).Error, "record review %s", review.RequestID)
}

// StartState writes the first state row for a question.
func (r *GORMRepository) StartState(ctx context.Context, entryID string, state State, caller authz.Caller) error {
	if err := r.ready(); err != nil {
		return err
	}
	// Same guard as RecordReview, and for the same reason: this is an insert, so
	// the question it is filed under has to be checked before it is written.
	if err := requireOwnedEntry(r.db, ctx, entryID, caller); err != nil {
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
func (r *GORMRepository) AdvanceState(ctx context.Context, entryID string, expected int64, state State, caller authz.Caller) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	columns := stateColumns(state)
	columns["version"] = gorm.Expr("version + 1")
	// The update is narrowed to the caller's own entries, the same subquery the
	// reads use: a row key belonging to another account does not match, so the
	// guard reports false rather than moving somebody else's curve.
	owned := ownedEntries(r.db.WithContext(ctx), caller)
	result := r.db.WithContext(ctx).Table(tableState).
		Where("notebook_entries_id = ? AND version = ?", entryID, expected).
		Where("notebook_entries_id IN (?)", owned).
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
func (r *GORMRepository) RecordAnswer(ctx context.Context, entryID string, face AnswerFace, now time.Time, caller authz.Caller) error {
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
	// This writes the question row itself, which has an owner column, so the
	// scope is applied directly rather than through a subquery: the answer face
	// moves for the caller's question and for no other.
	query := data.OwnerScope(r.db.WithContext(ctx).Table("notebook_entries"),
		caller.UserID, caller.IsAdmin(), "owner_id")
	err := query.Where("notebook_entries_id = ?", entryID).Updates(columns).Error
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

// nullable is the write side of the NULL columns the read side reads through
// stringFrom and its siblings: an empty string is written as no value rather
// than as an empty one, because the two mean different things to a reader.
func nullable(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
