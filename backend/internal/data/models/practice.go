package models

import (
	"time"

	"gorm.io/datatypes"
)

// PracticeReviewState stores the current review state for an entry.
type PracticeReviewState struct {
	NotebookEntriesID string         `gorm:"column:notebook_entries_id;primaryKey;autoIncrement:false"`
	FirstWrongTime    *time.Time     `gorm:"column:first_wrong_time"`
	DueTime           *time.Time     `gorm:"column:due_time"`
	LastReviewTime    *time.Time     `gorm:"column:last_review_time"`
	IsMistake         *bool          `gorm:"column:is_mistake"`
	Case              *float64       `gorm:"column:case"`
	Streak            *int32         `gorm:"column:streak"`
	ReviewCount       *int32         `gorm:"column:review_count"`
	Lapses            *int32         `gorm:"column:lapses"`
	Extra             datatypes.JSON `gorm:"column:extra;type:json"`
}

// TableName fixes the practice review state table name.
func (PracticeReviewState) TableName() string {
	return "practice_review_state"
}

// PracticeReviewEvent records one idempotent review submission.
type PracticeReviewEvent struct {
	RequestID         string         `gorm:"column:request_id;primaryKey;autoIncrement:false"`
	NotebookEntriesID string         `gorm:"column:notebook_entries_id;not null"`
	UserAnswer        *string        `gorm:"column:user_answer"`
	Rating            *float64       `gorm:"column:rating"`
	Outcome           datatypes.JSON `gorm:"column:outcome_json;type:json"`
	ReviewTime        *time.Time     `gorm:"column:review_time"`
}

// TableName fixes the practice review event table name.
func (PracticeReviewEvent) TableName() string {
	return "practice_review_events"
}
