package practice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"backend/internal/authz"
	"backend/internal/data"
	"backend/internal/data/models"
	"backend/internal/notebook"
	"gorm.io/gorm"
)

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
func (r *GORMRepository) Entry(ctx context.Context, questionID string, caller authz.Caller) (StoredEntry, error) {
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
	// The join is narrowed to the caller's own entries, so the state and the row
	// key both come from a question this account may see: another account's
	// question is not there to join to, and the empty key below reads as absent.
	scope, owner := ownerPredicate("e", caller)
	err := r.db.WithContext(ctx).Raw(stateOfQuestionSQL+" AND "+scope, questionID, owner).Scan(&state).Error
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
	// Scoped as well, even though the key came from the scoped join: a read that
	// trusted the key it was handed would be a second, unscoped door onto the
	// row, and the two doors would only have to disagree once.
	entryQuery := data.OwnerScope(r.db.WithContext(ctx), caller.UserID, caller.IsAdmin(), "owner_id")
	err = entryQuery.First(&record, "notebook_entries_id = ?", state.ID).Error
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

// DueTimes reads the stored due time of the named questions.
//
// It is one statement for the whole page and not one per question, because the
// caller is a listing: a bank page of twenty rows would otherwise be twenty
// reads of a table this one query already answers.
//
// Only the due time is selected. The rest of the state row — the streak, the
// ease, the review count — belongs to the curve and to the queue, and a listing
// that carried it would be handing a client numbers it has no use for.
func (r *GORMRepository) DueTimes(ctx context.Context, entryIDs []string, caller authz.Caller) (map[string]time.Time, error) {
	due := make(map[string]time.Time, len(entryIDs))
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(entryIDs) == 0 {
		return due, nil
	}
	var rows []struct {
		EntryID string     `gorm:"column:notebook_entries_id"`
		DueTime *time.Time `gorm:"column:due_time"`
	}
	// The state table has no owner of its own, so the caller narrows it through
	// the questions the state is filed under: a row key belonging to another
	// account is not in this account's entries and yields no date rather than
	// somebody else's date.
	owned := ownedEntries(r.db.WithContext(ctx), caller)
	err := r.db.WithContext(ctx).Table(tableState).
		Select("notebook_entries_id, due_time").
		Where("notebook_entries_id IN ?", entryIDs).
		Where("notebook_entries_id IN (?)", owned).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read the due times of %d questions: %w", len(entryIDs), err)
	}
	for _, row := range rows {
		// A state row whose due time is NULL is a question that has been reviewed
		// without the curve ever setting a date for it. It stays out of the map
		// for the same reason a question with no row does: what the map holds is
		// dates, and a zero time would be the epoch.
		if row.DueTime == nil {
			continue
		}
		due[row.EntryID] = *row.DueTime
	}
	return due, nil
}

// RecordedReview reads one event row by its request id.
//
// The caller narrows it through the question the event is filed under, which is
// the only owner this table has. A request id that belongs to another account is
// not found, which is what makes an id one account cannot use to read another's
// submission.
func (r *GORMRepository) RecordedReview(ctx context.Context, requestID string, caller authz.Caller) (StoredReview, error) {
	if err := r.ready(); err != nil {
		return StoredReview{}, err
	}
	var record models.PracticeReviewEvent
	owned := ownedEntries(r.db.WithContext(ctx), caller)
	err := r.db.WithContext(ctx).
		Where("request_id = ?", requestID).
		Where("notebook_entries_id IN (?)", owned).
		First(&record).Error
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
		IsCorrect:         record.IsCorrect,
		Outcome:           record.Outcome,
		ReviewTime:        timesFrom(record.ReviewTime),
	}, nil
}

// ── columns that may be NULL, as Go values ──────────────────────────────────
//
// A row written by nothing rather than by a zero: the state table's columns are
// nullable because "never reviewed" is not the same as "reviewed, streak zero".
// These four are the read side of that, and nullable in repository_write.go is
// the write side.

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
