package models

import (
	"time"

	"gorm.io/datatypes"
)

// User is the PostgreSQL persistence mapping for the users table.
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
	CreatedAt      time.Time      `gorm:"column:creat_time;autoCreateTime"`
	UpdatedAt      time.Time      `gorm:"column:update_time;autoCreateTime;autoUpdateTime"`
}

// TableName fixes the persistence table name independently of GORM naming rules.
func (User) TableName() string {
	return "users"
}
