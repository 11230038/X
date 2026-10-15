package models

import (
	"time"

	"gorm.io/datatypes"
)

// User is the seekdb persistence mapping for the users table.
type User struct {
	UserID         int64          `gorm:"column:user_id;primaryKey;autoIncrement"`
	Username       string         `gorm:"column:username;not null"`
	PasswordHash   string         `gorm:"column:password;not null"`
	Disable        bool           `gorm:"column:disable;not null"`
	Avatar         string         `gorm:"column:avatar;not null"`
	Role           string         `gorm:"column:role;not null"`
	Preset         string         `gorm:"column:preset;not null"`
	LearnerProfile datatypes.JSON `gorm:"column:learner_profile;type:json;not null"`
	Extra          datatypes.JSON `gorm:"column:extra;type:json;not null"`

	// TokenValidAfter is when the account's tokens stopped counting: every token
	// signed at or before it is refused. Nil is "never revoked", which is not the
	// same as an early moment — the judgement compares the token's iat against
	// this value, and nil takes no part in it.
	TokenValidAfter *time.Time `gorm:"column:token_valid_after"`
	// FailedAttempts counts the run of failed logins that FailedWindowStart
	// opened. Reaching the lockout threshold writes LockedUntil; what clears the
	// run is a sign-in that succeeds, not the lock.
	FailedAttempts int `gorm:"column:failed_attempts;not null"`
	// FailedWindowStart is when that run began; a failure after the window has
	// passed starts a new run at one.
	FailedWindowStart *time.Time `gorm:"column:failed_window_start"`
	// LockedUntil is when a lock ends. Nil and a moment in the past both mean the
	// account is not locked.
	LockedUntil *time.Time `gorm:"column:locked_until"`

	CreatedAt time.Time `gorm:"column:creat_time;autoCreateTime"`
	UpdatedAt time.Time `gorm:"column:update_time;autoCreateTime;autoUpdateTime"`
}

// TableName fixes the persistence table name independently of GORM naming rules.
func (User) TableName() string {
	return "users"
}
