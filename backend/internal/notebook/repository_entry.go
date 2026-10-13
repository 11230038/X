package notebook

import (
	"context"
	"errors"
	"fmt"

	"backend/internal/data/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Entry reads one question by its content address.
func (r *GORMRepository) Entry(ctx context.Context, questionID string) (Entry, error) {
	if err := r.ready(); err != nil {
		return Entry{}, err
	}
	var record models.NotebookEntry
	err := r.db.WithContext(ctx).First(&record, "question_id = ?", questionID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Entry{}, fmt.Errorf("%w: %s", ErrNotFound, questionID)
	}
	if err != nil {
		return Entry{}, fmt.Errorf("read question %s: %w", questionID, err)
	}
	entry, err := toEntry(record)
	if err != nil {
		return Entry{}, err
	}
	categories, err := r.categoriesOf(ctx, []string{record.NotebookEntriesID})
	if err != nil {
		return Entry{}, err
	}
	entry.Categories = categories[record.NotebookEntriesID]
	return entry, nil
}

// Mark applies a property change to one question.
func (r *GORMRepository) Mark(ctx context.Context, questionID string, change Change) error {
	if err := r.ready(); err != nil {
		return err
	}
	updates := map[string]any{}
	if change.Bookmarked != nil {
		updates["bookmarked"] = *change.Bookmarked
	}
	if change.Resolved != nil {
		updates["resolved"] = *change.Resolved
	}
	if len(updates) == 0 {
		return fmt.Errorf("%w: no marks were named", ErrEmptyChange)
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireEntry(tx, questionID); err != nil {
			return err
		}
		if err := tx.Table("notebook_entries").
			Where("question_id = ?", questionID).
			Updates(updates).Error; err != nil {
			return fmt.Errorf("mark question %s: %w", questionID, err)
		}
		return nil
	})
}

// requireEntry reports a question that is not stored, checking by reading it
// rather than by what an update of it reports.
//
// The row count an update returns is the number of rows whose values actually
// changed, which is zero for a row written back the values it already holds:
// marking an already-bookmarked question reports none, and read as "no such
// question" it would turn a repeated click into an error about a question that
// is right there. The read is a locking one, so what it reports still holds
// when the write that follows lands.
func requireEntry(tx *gorm.DB, questionID string) error {
	var stored models.NotebookEntry
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&stored, "question_id = ?", questionID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("%w: %s", ErrNotFound, questionID)
	}
	if err != nil {
		return fmt.Errorf("look for question %s: %w", questionID, err)
	}
	return nil
}

// categoriesOf returns the category names of each given question, in one query.
