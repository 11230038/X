package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"backend/internal/data/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	//
	// The two optional fields come from the caller so registration keeps writing
	// what it always wrote: no role means the ordinary one, and no preset means
	// the empty string V5 reads as standard.
	role := params.Role
	if role == "" {
		role = defaultRole
	}
	model := models.User{
		Username:       params.Username,
		PasswordHash:   params.PasswordHash,
		Disable:        false,
		Avatar:         "",
		Role:           role,
		Preset:         params.Preset,
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

// RecordFailure counts one failed sign-in, and writes the lock once the run of
// them reaches the policy's threshold.
//
// The read and the write are one transaction with a locking read, because the
// rule compares a number this statement is about to change: two requests that both
// read four would both write five, and half of a paired guessing attack would go
// uncounted. The lock is taken on a row that exists, which under this engine's
// READ-COMMITTED is a record lock — there is no gap to leave unlocked here, since
// the row the count lives in is the one being read.
//
// A run that keeps going while the account is locked keeps the lock: the count is
// compared with "at or over" the threshold, so hammering a locked account does not
// let the lock lapse in the middle of the attempt.
func (r *GORMRepository) RecordFailure(
	ctx context.Context, userID int64, at time.Time, policy FailurePolicy,
) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("auth repository is not initialized")
	}
	moment := at.UTC()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var model models.User
		err := tx.
			Select([]string{"user_id", "failed_attempts", "failed_window_start"}).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ?", userID).
			First(&model).Error
		if err != nil {
			return mapRepositoryError(err)
		}

		count := model.FailedAttempts
		start := model.FailedWindowStart
		// A window that is empty, or that started longer ago than the window lasts,
		// has run out: this failure is the first of a new run rather than one more
		// of the old one. There is no "last failed at" column, so when the run
		// started is the only thing the row remembers about it (§2.3).
		if start == nil || !start.Add(policy.Window).After(moment) {
			start = &moment
			count = 1
		} else {
			count++
		}

		updates := map[string]any{
			"failed_attempts":     count,
			"failed_window_start": *start,
		}
		if count >= policy.Threshold {
			updates["locked_until"] = moment.Add(policy.Duration)
		}
		return tx.Model(&models.User{}).Where("user_id = ?", userID).Updates(updates).Error
	})
}

// ClearFailures forgets the run of failed sign-ins.
//
// It clears the lock as well, even though the successful sign-in that calls it
// could only have got there with the lock already expired: an expired moment reads
// as "not locked" at every use, so leaving it would keep a column that says two
// things — was this account once locked, and is it locked now. What an account
// that just signed in knows for certain is that there is no lock to remember.
func (r *GORMRepository) ClearFailures(ctx context.Context, userID int64) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("auth repository is not initialized")
	}
	err := r.db.WithContext(ctx).
		Model(&models.User{}).
		Where("user_id = ?", userID).
		Updates(map[string]any{
			"failed_attempts":     0,
			"failed_window_start": nil,
			"locked_until":        nil,
		}).Error
	if err != nil {
		return mapRepositoryError(err)
	}
	return nil
}

// ReplacePassword writes the new hash and the revocation moment in one statement.
//
// One statement rather than two, because the two columns are two halves of one
// change: the account's credential was replaced, so everything signed with the
// old one stops counting. Written separately there is a window — as long as a
// network round trip — in which the new password is already in force and a token
// signed with the old one still is, and a caller that saw a failure between the
// two writes would have no way to tell which half had happened.
//
// An account that is not there is not an error, for the reason RevokeTokens
// gives: this is a write about an account its caller has already established,
// and here the establishment is stronger than usual — ChangePassword reads the
// row and verifies the old password against it before calling this.
func (r *GORMRepository) ReplacePassword(
	ctx context.Context, userID int64, passwordHash string, at time.Time,
) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("auth repository is not initialized")
	}
	err := r.db.WithContext(ctx).
		Model(&models.User{}).
		Where("user_id = ?", userID).
		Updates(map[string]any{
			"password":          passwordHash,
			"token_valid_after": at.UTC(),
		}).Error
	if err != nil {
		return mapRepositoryError(err)
	}
	return nil
}

// Delete removes one account.
//
// It is a hard delete and the foreign keys do the rest: every table that names
// the account as its owner was declared with ON DELETE CASCADE, so this one
// statement is the whole of what an administrator means by deleting somebody.
// The audit is the deliberate exception — its columns carry no foreign key, so
// the record of the deletion outlives the account.
func (r *GORMRepository) Delete(ctx context.Context, userID int64) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("auth repository is not initialized")
	}
	result := r.db.WithContext(ctx).Delete(&models.User{}, "user_id = ?", userID)
	if result.Error != nil {
		return mapRepositoryError(result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrUserNotFound
	}
	return nil
}

// SetDisabled writes whether an account may sign in.
//
// Disabling records the revocation moment in the same statement, because the
// two are one change: a token minted before it stops counting on the next
// request, which is what makes "disabled" mean now rather than whenever the
// token would have expired. Enabling writes the flag alone; the tokens that
// were taken back are not brought back, and the account signs in again.
//
// The row count cannot tell "missing" from "already this value": an update that
// changes nothing is zero rows too. The second is not an error, so existence is
// looked up only when the count is zero — the same shape SavePreset uses in the
// authorization store.
func (r *GORMRepository) SetDisabled(
	ctx context.Context, userID int64, disabled bool, at time.Time,
) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("auth repository is not initialized")
	}
	updates := map[string]any{"disable": disabled}
	if disabled {
		updates["token_valid_after"] = at.UTC()
	}
	result := r.db.WithContext(ctx).
		Model(&models.User{}).
		Where("user_id = ?", userID).
		Updates(updates)
	if result.Error != nil {
		return mapRepositoryError(result.Error)
	}
	if result.RowsAffected > 0 {
		return nil
	}
	var count int64
	if err := r.db.WithContext(ctx).Model(&models.User{}).
		Where("user_id = ?", userID).Count(&count).Error; err != nil {
		return mapRepositoryError(err)
	}
	if count == 0 {
		return ErrUserNotFound
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
