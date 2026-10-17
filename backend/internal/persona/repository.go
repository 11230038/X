package persona

import (
	"context"
	"errors"
	"fmt"

	"backend/internal/authz"
	"backend/internal/data/models"
	"gorm.io/gorm"
)

// GORMRepository reads and writes personas through GORM (seekdb over the MySQL
// protocol).
//
// Unlike the session repository, every read here is narrowed to the caller
// without an administrator's exception. A persona is a prompt somebody wrote
// about how they want to be answered; being an administrator is not a reason to
// read it, and there is no operation in the product for which it would be.
type GORMRepository struct {
	db *gorm.DB
}

// NewGORMRepository creates a storage adapter over an existing GORM handle.
func NewGORMRepository(db *gorm.DB) *GORMRepository {
	return &GORMRepository{db: db}
}

var _ Repository = (*GORMRepository)(nil)

// List returns the caller's personas, ordered by name.
//
// persona_id breaks ties: the unique key makes a duplicate name impossible for
// one account, but the ordering still needs a second column to be stable across
// identical reads.
func (r *GORMRepository) List(ctx context.Context, caller authz.Caller) ([]Persona, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	var records []models.Persona
	err := r.db.WithContext(ctx).
		Where("owner_id = ?", caller.UserID).
		Order("name ASC, persona_id ASC").
		Find(&records).Error
	if err != nil {
		return nil, fmt.Errorf("list personas: %w", err)
	}
	personas := make([]Persona, 0, len(records))
	for _, record := range records {
		personas = append(personas, toPersona(record))
	}
	return personas, nil
}

// Get returns one of the caller's personas.
func (r *GORMRepository) Get(ctx context.Context, personaID string, caller authz.Caller) (Persona, error) {
	if err := r.ready(); err != nil {
		return Persona{}, err
	}
	var record models.Persona
	err := r.db.WithContext(ctx).
		Where("persona_id = ? AND owner_id = ?", personaID, caller.UserID).
		First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Persona{}, fmt.Errorf("%w: %s", ErrNotFound, personaID)
	}
	if err != nil {
		return Persona{}, fmt.Errorf("read persona: %w", err)
	}
	return toPersona(record), nil
}

// Create inserts one persona for the caller.
func (r *GORMRepository) Create(ctx context.Context, record Persona, caller authz.Caller) error {
	if err := r.ready(); err != nil {
		return err
	}
	model := models.Persona{
		PersonaID:   record.ID,
		OwnerID:     caller.UserID,
		Name:        record.Name,
		Description: record.Description,
		Content:     record.Content,
		CreatedAt:   record.CreatedAt,
		UpdatedAt:   record.UpdatedAt,
	}
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return mapWriteError(err, "create persona")
	}
	return nil
}

// Update replaces one persona's editable fields.
//
// A row count of zero is not taken as "not found": MySQL reports zero for a row
// written back the values it already holds, and saving a form without changing
// anything is an ordinary thing to do. The scoped re-read is what tells the two
// apart without a transaction in between.
func (r *GORMRepository) Update(ctx context.Context, record Persona, caller authz.Caller) error {
	if err := r.ready(); err != nil {
		return err
	}
	result := r.db.WithContext(ctx).Model(&models.Persona{}).
		Where("persona_id = ? AND owner_id = ?", record.ID, caller.UserID).
		Updates(map[string]any{
			"name":        record.Name,
			"description": record.Description,
			"content":     record.Content,
			"update_time": record.UpdatedAt,
		})
	if result.Error != nil {
		return mapWriteError(result.Error, "update persona")
	}
	if result.RowsAffected > 0 {
		return nil
	}
	if _, err := r.Get(ctx, record.ID, caller); err != nil {
		return err
	}
	return nil
}

// Delete removes one of the caller's personas.
func (r *GORMRepository) Delete(ctx context.Context, personaID string, caller authz.Caller) error {
	if err := r.ready(); err != nil {
		return err
	}
	result := r.db.WithContext(ctx).
		Where("persona_id = ? AND owner_id = ?", personaID, caller.UserID).
		Delete(&models.Persona{})
	if result.Error != nil {
		return fmt.Errorf("delete persona: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, personaID)
	}
	return nil
}

func (r *GORMRepository) ready() error {
	if r == nil || r.db == nil {
		return fmt.Errorf("persona repository is not initialized")
	}
	return nil
}

// mapWriteError keeps a duplicate name a fact the caller can act on, and leaves
// every other storage failure as what it is.
func mapWriteError(err error, action string) error {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return ErrNameTaken
	}
	return fmt.Errorf("%s: %w", action, err)
}

func toPersona(model models.Persona) Persona {
	return Persona{
		ID:          model.PersonaID,
		OwnerID:     model.OwnerID,
		Name:        model.Name,
		Description: model.Description,
		Content:     model.Content,
		CreatedAt:   model.CreatedAt,
		UpdatedAt:   model.UpdatedAt,
	}
}
