package notebook

import (
	"context"
	"errors"
	"fmt"

	"backend/internal/data/models"
	"gorm.io/gorm"
)

// AddCategories files a question under the given names.
//
// A category is created under a key derived from its name, so two writers
// creating the same category at the same moment collide on the primary key
// instead of making two rows — and a collision is not an error here, because
// the row that is there says exactly what the one being inserted would have
// said. The join rows behave the same way: filing a question under a category
// it is already filed under is a no-op that reports success.
func (r *GORMRepository) AddCategories(ctx context.Context, entryID string, names []string) error {
	if err := r.ready(); err != nil {
		return err
	}
	cleaned := cleanCategoryNames(names)
	if len(cleaned) == 0 {
		return nil
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, name := range cleaned {
			id := categoryID(name)
			if err := requireCategory(tx, id, name); err != nil {
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

// requireCategory makes sure a category row exists under the given key.
func requireCategory(tx *gorm.DB, id, name string) error {
	record := models.NotebookCategory{NotebookCategoriesID: id, Name: name}
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
