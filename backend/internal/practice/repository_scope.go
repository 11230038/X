package practice

import (
	"context"
	"errors"
	"fmt"

	"backend/internal/authz"
	"backend/internal/data"
	"backend/internal/data/models"
	"gorm.io/gorm"
)

// tableState is the review state table. The event table is only ever reached
// through its model, because every read of it is by primary key and there is
// nothing to name.
const tableState = "practice_review_state"

// ownedEntries is the caller's questions, as the subquery the review tables are
// narrowed through.
//
// Neither review table carries an owner column — migration 00014 gave one to
// notebook_entries and to the seven others, and these two are not among them —
// because ownership is derived: a review state and a review event are filed
// under the question they are about, so their owner is that question's owner.
// The subquery is how that one rule is stated once instead of twice, and it is
// the same shape the session and knowledge-base adapters use for their
// owner-derived children.
func ownedEntries(db *gorm.DB, caller authz.Caller) *gorm.DB {
	return data.OwnerScope(db.Model(&models.NotebookEntry{}).Select("notebook_entries_id"),
		caller.UserID, caller.IsAdmin(), "owner_id")
}

// ownerPredicate is OwnerScope's rule as a raw predicate over the table aliased
// alias.
//
// The two raw statements in this package — the state join and the queue — cannot
// go through data.OwnerScope, which takes a *gorm.DB and builds a WHERE for it,
// because the predicate has to sit inside a statement that is written whole. So
// the one rule is restated here, once, and every raw read in the package takes
// it from here rather than spelling it out again. It returns the predicate and
// the single bound value it wants, so a caller can slot both into its argument
// list.
func ownerPredicate(alias string, caller authz.Caller) (string, int64) {
	column := alias + ".owner_id"
	if caller.IsAdmin() {
		return "(" + column + " = ? OR " + column + " IS NULL)", caller.UserID
	}
	return column + " = ?", caller.UserID
}

// requireOwnedEntry reports a question the caller may not see, so that nothing
// can be written under it.
//
// It is the guard the two inserts share. The review tables have no owner column
// of their own and a statement that only inserts has no WHERE to narrow, so the
// parent question is read through the scope the rest of the package reads
// through before anything is filed under it. A question belonging to another
// account answers ErrNotFound, which is the same answer a question that is
// nowhere gets.
func requireOwnedEntry(db *gorm.DB, ctx context.Context, entryID string, caller authz.Caller) error {
	var entry models.NotebookEntry
	query := data.OwnerScope(db.WithContext(ctx), caller.UserID, caller.IsAdmin(), "owner_id")
	err := query.Select("notebook_entries_id").First(&entry, "notebook_entries_id = ?", entryID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("%w: %s", ErrNotFound, entryID)
	}
	if err != nil {
		return fmt.Errorf("look for question %s: %w", entryID, err)
	}
	return nil
}
