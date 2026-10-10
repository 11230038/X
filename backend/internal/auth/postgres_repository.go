package auth

import (
	"context"
	"errors"
	"fmt"

	"backend/internal/data/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// accountColumns lists the only columns this adapter reads.
var accountColumns = []string{"user_id", "username", "role", "password", "disable"}

// PostgresRepository persists accounts through GORM.
type PostgresRepository struct {
	db *gorm.DB
}

// NewPostgresRepository creates an account adapter over an existing GORM handle.
func NewPostgresRepository(db *gorm.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

// Create inserts one account without modifying the schema.
func (r *PostgresRepository) Create(ctx context.Context, params CreateParams) (Record, error) {
	if r == nil || r.db == nil {
		return Record{}, fmt.Errorf("auth repository is not initialized")
	}
	// The users table declares every column NOT NULL, so GORM must be handed
	// explicit values: without a default tag it writes zero values, which would
	// store an empty role and NULL JSON.
	model := models.User{
		Username:       params.Username,
		PasswordHash:   params.PasswordHash,
		Disable:        false,
		Avatar:         "",
		Role:           defaultRole,
		Preset:         "",
		LearnerProfile: datatypes.JSON(`{}`),
		Extra:          datatypes.JSON(`{}`),
	}
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return Record{}, mapRepositoryError(err)
	}
	return recordFromModel(model), nil
}

// FindByUsername loads one account by its exact username.
func (r *PostgresRepository) FindByUsername(ctx context.Context, username string) (Record, error) {
	return r.find(ctx, "username = ?", username)
}

// FindByID loads one account by primary key.
func (r *PostgresRepository) FindByID(ctx context.Context, userID int64) (Record, error) {
	return r.find(ctx, "user_id = ?", userID)
}

func (r *PostgresRepository) find(ctx context.Context, query string, value any) (Record, error) {
	if r == nil || r.db == nil {
		return Record{}, fmt.Errorf("auth repository is not initialized")
	}
	var model models.User
	err := r.db.WithContext(ctx).
		Select(accountColumns).
		Where(query, value).
		First(&model).Error
	if err != nil {
		return Record{}, mapRepositoryError(err)
	}
	return recordFromModel(model), nil
}

func recordFromModel(model models.User) Record {
	return Record{
		User: User{
			UserID:   model.UserID,
			Username: model.Username,
			Role:     model.Role,
		},
		PasswordHash: model.PasswordHash,
		Disabled:     model.Disable,
	}
}

// mapRepositoryError translates storage failures into domain errors. Missing
// context is preserved, but never the hash or any column value.
func mapRepositoryError(err error) error {
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return ErrUserNotFound
	case errors.Is(err, gorm.ErrDuplicatedKey):
		return ErrUsernameTaken
	default:
		return fmt.Errorf("access users table: %w", err)
	}
}

var _ Repository = (*PostgresRepository)(nil)
