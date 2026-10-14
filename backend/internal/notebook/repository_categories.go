package notebook

import (
	"context"
	"errors"
	"fmt"

	"backend/internal/authz"
	"backend/internal/data"
	"backend/internal/data/models"
	"gorm.io/gorm"
)

// AddCategories files a question under the given names.
//
// A category is created under a key derived from its name and its owner, so two
// writers creating the same category at the same moment collide on the primary
// key instead of making two rows — and a collision is not an error here, because
// the row that is there says exactly what the one being inserted would have
// said. The join rows behave the same way: filing a question under a category
// it is already filed under is a no-op that reports success.
//
// The owner takes part in the category key, which is a decision the schema
// forced. Migration 00014 made a category name unique per owner rather than
// globally — two accounts may each have a "数学" — so a key derived from the name
// alone would make the second account's insert collide with the first account's
// row, and the collision-tolerant insert below would read that as "the category
// is already there": the second account's question would be filed under the
// first account's category, and its name would be readable by the first. The
// owner is in the hash so the collision means what it should — the same account
// making the same category twice.
func (r *GORMRepository) AddCategories(ctx context.Context, entryID string, names []string, caller authz.Caller) error {
	if err := r.ready(); err != nil {
		return err
	}
	cleaned := cleanCategoryNames(names)
	if len(cleaned) == 0 {
		return nil
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The filing names a row key and nothing else about it, so the row it
		// names is checked against the caller before anything is filed under it.
		// Without this a caller holding another account's row key could file that
		// question under a label of its own.
		if err := requireEntryRow(tx, entryID, caller); err != nil {
			return err
		}
		for _, name := range cleaned {
			id := categoryID(caller.UserID, name)
			if err := requireCategory(tx, id, name, caller); err != nil {
				return err
			}
			link := models.NotebookEntryCategory{
				NotebookEntriesID:    entryID,
				NotebookCategoriesID: id,
			}
			switch err := tx.Create(&link).Error; {
			case err == nil:
			case errors.Is(err, gorm.ErrDuplicatedKey):
				// Already filed under this name, which is the state this call
				// was asking for.
			default:
				return fmt.Errorf("file question %s under %s: %w", entryID, name, err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

// SetCategories replaces the labels a question is filed under.
//
// The filings are removed first and written second, and that order is the whole
// of it: the write that follows is the same insert AddCategories makes, and
// reversed it would delete a name the request had just added.
//
// The category rows themselves stay. A label is shared — the other questions
// filed under it are still filed under it — and a name a question used to carry
// is worth keeping, because the next question that carries it should find the
// same label rather than a second row with the same text.
func (r *GORMRepository) SetCategories(ctx context.Context, questionID string, names []string, caller authz.Caller) error {
	if err := r.ready(); err != nil {
		return err
	}
	cleaned := cleanCategoryNames(names)
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The scoped lookup is what makes this a write to the caller's question
		// and not to a row key that happened to be named: another account's
		// question is not found, so its filings are neither removed nor replaced.
		entryID, err := lockedEntryID(tx, questionID, caller)
		if err != nil {
			return err
		}
		err = tx.Delete(&models.NotebookEntryCategory{}, "notebook_entries_id = ?", entryID).Error
		if err != nil {
			return fmt.Errorf("unfile question %s: %w", questionID, err)
		}
		// cleaned has no repeats in it, so this insert cannot meet its own
		// primary key; what it can meet is a category row another writer made
		// between requireCategory and here, which requireCategory tolerates.
		for _, name := range cleaned {
			id := categoryID(caller.UserID, name)
			if err := requireCategory(tx, id, name, caller); err != nil {
				return err
			}
			link := models.NotebookEntryCategory{
				NotebookEntriesID:    entryID,
				NotebookCategoriesID: id,
			}
			if err := tx.Create(&link).Error; err != nil {
				return fmt.Errorf("file question %s under %s: %w", questionID, name, err)
			}
		}
		return nil
	})
}

// Categories lists the labels the bank is filed under, most used first.
//
// The join is an inner one, and that is the decision this query makes: a label
// is a filing, so a category row whose questions have all been filed under
// something else is not a name the surface can offer anything under. The name
// breaks ties in the order, so two labels with the same count do not swap places
// between two identical requests.
//
// The caller narrows it through the category's own owner column, which is the
// one place this table is filtered on directly. That is enough: a category row
// belongs to the account that made it (§2.3, and the owner-aware key in
// AddCategories), so the filings counted under it are that account's questions
// and nobody else's.
func (r *GORMRepository) Categories(ctx context.Context, caller authz.Caller) ([]Category, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	// The row is read through a struct of its own rather than through Category,
	// because an alias is what the count needs to be called: the domain type has
	// no column names in it, and this is the same pairing the rest of this file
	// uses for rows it reads.
	var rows []struct {
		Name  string `gorm:"column:name"`
		Count int    `gorm:"column:questions"`
	}
	query := data.OwnerScope(r.db.WithContext(ctx).Table("notebook_categories AS category"),
		caller.UserID, caller.IsAdmin(), "category.owner_id")
	err := query.
		Select("category.name AS name, COUNT(filing.notebook_entries_id) AS questions").
		Joins("JOIN notebook_entry_categories AS filing " +
			"ON filing.notebook_categories_id = category.notebook_categories_id").
		Group("category.name").
		Order("COUNT(filing.notebook_entries_id) DESC, category.name ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list question categories: %w", err)
	}
	categories := make([]Category, 0, len(rows))
	for _, row := range rows {
		categories = append(categories, Category{Name: row.Name, Count: row.Count})
	}
	return categories, nil
}

// requireCategory makes sure a category row exists under the given key.
//
// The owner is written on creation and matches the account whose key this is, so
// the row that ends up there is the caller's — and a collision, when it happens,
// is the same account's second write rather than another account's first.
func requireCategory(tx *gorm.DB, id, name string, caller authz.Caller) error {
	record := models.NotebookCategory{
		NotebookCategoriesID: id,
		Name:                 name,
		OwnerID:              data.OwnerPointer(caller.UserID),
	}
	switch err := tx.Create(&record).Error; {
	case err == nil:
		return nil
	case errors.Is(err, gorm.ErrDuplicatedKey):
		return nil
	default:
		return fmt.Errorf("create category %s: %w", name, err)
	}
}

func (r *GORMRepository) categoriesOf(ctx context.Context, entryIDs []string) (map[string][]string, error) {
	names := make(map[string][]string, len(entryIDs))
	if len(entryIDs) == 0 {
		return names, nil
	}
	var rows []struct {
		NotebookEntriesID string `gorm:"column:notebook_entries_id"`
		Name              string `gorm:"column:name"`
	}
	err := r.db.WithContext(ctx).
		Table("notebook_entry_categories AS filing").
		Select("filing.notebook_entries_id, category.name").
		Joins("JOIN notebook_categories AS category ON category.notebook_categories_id = filing.notebook_categories_id").
		Where("filing.notebook_entries_id IN ?", entryIDs).
		Order("category.name ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read question categories: %w", err)
	}
	for _, row := range rows {
		names[row.NotebookEntriesID] = append(names[row.NotebookEntriesID], row.Name)
	}
	return names, nil
}
