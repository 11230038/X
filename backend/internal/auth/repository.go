package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"backend/internal/data/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// accountColumns lists the only columns this adapter reads. The four a
// revocation or a lock is written into are read with the rest of the row because
// Authenticate has to see them on every request and it is already reading this
// row — a second query per request to ask "and is the token still good" would be
// the same answer bought twice.
var accountColumns = []string{
	"user_id", "username", "role", "password", "disable",
	"token_valid_after", "failed_attempts", "failed_window_start", "locked_until",
}

// GORMRepository persists accounts through GORM (seekdb via the MySQL protocol).
type GORMRepository struct {
	db *gorm.DB
}

// NewGORMRepository creates an account adapter over an existing GORM handle.
func NewGORMRepository(db *gorm.DB) *GORMRepository {
	return &GORMRepository{db: db}
}

// Create inserts one account without modifying the schema.
func (r *GORMRepository) Create(ctx context.Context, params CreateParams) (Record, error) {
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
func (r *GORMRepository) FindByUsername(ctx context.Context, username string) (Record, error) {
	return r.find(ctx, "username = ?", username)
}

// FindByID loads one account by primary key.
func (r *GORMRepository) FindByID(ctx context.Context, userID int64) (Record, error) {
	return r.find(ctx, "user_id = ?", userID)
}

func (r *GORMRepository) find(ctx context.Context, query string, value any) (Record, error) {
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

// RevokeTokens writes the moment every token signed at or before it stops
// counting. The moment arrives from the caller rather than from the database's
// clock: what it is compared against is the token's own iat, and two clocks a
// second apart would make the comparison answer the wrong way.
//
// An account that is not there is not an error here. Nothing was going to be
// read back, and this is a write a caller makes about an account it has already
// established — a missing row means the work is already done.
func (r *GORMRepository) RevokeTokens(ctx context.Context, userID int64, at time.Time) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("auth repository is not initialized")
	}
	err := r.db.WithContext(ctx).
		Model(&models.User{}).
		Where("user_id = ?", userID).
		Update("token_valid_after", at.UTC()).Error
	if err != nil {
		return mapRepositoryError(err)
	}
	return nil
}

func recordFromModel(model models.User) Record {
	return Record{
		User: User{
			UserID:   model.UserID,
			Username: model.Username,
			Role:     model.Role,
		},
		PasswordHash:      model.PasswordHash,
		Disabled:          model.Disable,
		TokenValidAfter:   model.TokenValidAfter,
		FailedAttempts:    model.FailedAttempts,
		FailedWindowStart: model.FailedWindowStart,
		LockedUntil:       model.LockedUntil,
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

var _ Repository = (*GORMRepository)(nil)
