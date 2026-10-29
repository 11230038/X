package presence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"backend/internal/data/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Repository is the presence table as this package reads and writes it.
//
// Three questions are asked of it and none of them joins anything: write one
// minute, count the minutes in a window, and list the days something was
// written. The last one is what a streak walks back over, and it is asked as
// dates rather than minutes because a day of six hours is one day, not 360 rows
// the caller has to fold.
type Repository struct {
	db *gorm.DB
}

// NewRepository builds the repository.
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) ready() error {
	if r == nil || r.db == nil {
		return errors.New("presence: no database")
	}
	return nil
}

// Touch writes the minute the caller is in. It is idempotent by primary key:
// a second beat in the same minute is the same row, so time cannot be counted
// twice by a client that retries or opens two tabs.
func (r *Repository) Touch(ctx context.Context, ownerID int64, minute time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}
	if ownerID <= 0 {
		return errors.New("presence: touch without an account")
	}
	row := models.PresenceMinute{OwnerID: ownerID, MinuteAt: minute.Truncate(time.Minute)}
	err := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&row).Error
	if err != nil {
		return fmt.Errorf("record presence at %s: %w", row.MinuteAt.Format(time.RFC3339), err)
	}
	return nil
}

// MinutesBetween counts the minutes written in [from, to).
//
// Half-open on purpose: two adjacent windows — today and tomorrow — then share
// no minute, so a sum over them is the sum over their union rather than one
// minute counted twice.
func (r *Repository) MinutesBetween(ctx context.Context, ownerID int64, from, to time.Time) (int, error) {
	if err := r.ready(); err != nil {
		return 0, err
	}
	var count int64
	err := r.db.WithContext(ctx).Model(&models.PresenceMinute{}).
		Where("owner_id = ? AND minute_at >= ? AND minute_at < ?", ownerID, from, to).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("count presence minutes: %w", err)
	}
	return int(count), nil
}

// DaysSince lists the local dates that have at least one minute, newest first.
//
// The window is the caller's: a streak only has to look back as far as it can
// possibly run, and the caller is the one that knows how far that is.
func (r *Repository) DaysSince(ctx context.Context, ownerID int64, from time.Time) ([]time.Time, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	var rows []struct {
		Day time.Time `gorm:"column:day"`
	}
	err := r.db.WithContext(ctx).Model(&models.PresenceMinute{}).
		Select("DATE(minute_at) AS day").
		Where("owner_id = ? AND minute_at >= ?", ownerID, from).
		Group("day").
		Order("day DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list presence days: %w", err)
	}
	days := make([]time.Time, 0, len(rows))
	for _, row := range rows {
		days = append(days, row.Day)
	}
	return days, nil
}
