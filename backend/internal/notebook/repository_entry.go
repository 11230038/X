package notebook

import (
	"context"
	"errors"
	"fmt"

	"backend/internal/authz"
	"backend/internal/data"
	"backend/internal/data/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Entry reads one question by its content address.
//
// The read is scoped, so a question belonging to another account is not this
// caller's to see: it answers ErrNotFound, and that is the whole of what the
// caller learns (§2.3). The categories read after it is keyed by the row key
// this scoped read produced, so it cannot reach past the question either.
func (r *GORMRepository) Entry(ctx context.Context, questionID string, caller authz.Caller) (Entry, error) {
	if err := r.ready(); err != nil {
		return Entry{}, err
	}
	var record models.NotebookEntry
	query := data.OwnerScope(r.db.WithContext(ctx), caller.UserID, caller.IsAdmin(), "owner_id")
	err := query.First(&record, "question_id = ?", questionID).Error
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
func (r *GORMRepository) Mark(ctx context.Context, questionID string, change Change, caller authz.Caller) error {
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
		if _, err := lockedEntryID(tx, questionID, caller); err != nil {
			return err
		}
		// Scoped as well as guarded: the locking read above already established
		// that this question is the caller's, and this keeps the write itself from
		// being a second, unscoped door onto the same row — the failure mode where
		// the check and the write disagree about what they are protecting.
		scoped := data.OwnerScope(tx.Table("notebook_entries"),
			caller.UserID, caller.IsAdmin(), "owner_id")
		if err := scoped.
			Where("question_id = ?", questionID).
			Updates(updates).Error; err != nil {
			return fmt.Errorf("mark question %s: %w", questionID, err)
		}
		return nil
	})
}

// lockedEntryID reports whether a question is stored, and hands back the row
// key the tables beside it are filed under.
//
// The check is a read rather than what an update of the row reports. The row
// count an update returns is the number of rows whose values actually changed,
// which is zero for a row written back the values it already holds: marking an
// already-bookmarked question reports none, and read as "no such question" it
// would turn a repeated click into an error about a question that is right
// there. The read takes the row's lock, so what it reports still holds when the
// write that follows lands.
//
// It is scoped like every other read, and for the same reason: a question
// belonging to another account is not found here, so the row key it would hand
// back — the key the writes beside this table are filed under — is never handed
// out across accounts.
func lockedEntryID(tx *gorm.DB, questionID string, caller authz.Caller) (string, error) {
	var stored models.NotebookEntry
	query := data.OwnerScope(tx, caller.UserID, caller.IsAdmin(), "owner_id")
	err := query.Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&stored, "question_id = ?", questionID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", fmt.Errorf("%w: %s", ErrNotFound, questionID)
	}
	if err != nil {
		return "", fmt.Errorf("look for question %s: %w", questionID, err)
	}
	return stored.NotebookEntriesID, nil
}

// requireEntryRow reports whether a row key names a question the caller may see.
//
// It is lockedEntryID's twin for the one caller that starts from a row key
// rather than a content address: a join row is written against a
// notebook_entries_id and carries nothing else to check, so the key is resolved
// through the caller before anything is filed under it.
func requireEntryRow(tx *gorm.DB, entryID string, caller authz.Caller) error {
	var stored models.NotebookEntry
	query := data.OwnerScope(tx, caller.UserID, caller.IsAdmin(), "owner_id")
	err := query.First(&stored, "notebook_entries_id = ?", entryID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("%w: %s", ErrNotFound, entryID)
	}
	if err != nil {
		return fmt.Errorf("look for question row %s: %w", entryID, err)
	}
	return nil
}

// categoriesOf returns the category names of each given question, in one query.
