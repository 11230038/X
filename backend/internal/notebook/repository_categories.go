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
func (r *GORMRepository) SetCategories(ctx context.Context, questionID string, names []string) error {
	if err := r.ready(); err != nil {
		return err
	}
	cleaned := cleanCategoryNames(names)
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		entryID, err := lockedEntryID(tx, questionID)
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
			id := categoryID(name)
			if err := requireCategory(tx, id, name); err != nil {
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
func (r *GORMRepository) Categories(ctx context.Context) ([]Category, error) {
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
	err := r.db.WithContext(ctx).
		Table("notebook_categories AS category").
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
