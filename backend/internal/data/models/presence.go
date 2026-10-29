package models

import "time"

// PresenceMinute is one minute an account spent in the app (migration 00025).
//
// The pair is the primary key, which is what makes the write idempotent: two
// beats inside the same minute — two tabs, a retry, a client that fired early —
// are one row rather than a minute counted twice.
type PresenceMinute struct {
	OwnerID  int64     `gorm:"column:owner_id;primaryKey"`
	MinuteAt time.Time `gorm:"column:minute_at;primaryKey"`
}

// TableName is the table migration 00025 creates.
func (PresenceMinute) TableName() string { return "presence_minutes" }
