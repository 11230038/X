package models

import (
	"time"

	"gorm.io/datatypes"
)

// PracticeReviewState stores the current review state for an entry.
type PracticeReviewState struct {
	NotebookEntriesID string     `gorm:"column:notebook_entries_id;primaryKey;autoIncrement:false"`
	FirstWrongTime    *time.Time `gorm:"column:first_wrong_time"`
	DueTime           *time.Time `gorm:"column:due_time"`
	LastReviewTime    *time.Time `gorm:"column:last_review_time"`
	IsMistake         *bool      `gorm:"column:is_mistake"`
	Case              *float64   `gorm:"column:case"`
	Streak            *int32     `gorm:"column:streak"`
	ReviewCount       *int32     `gorm:"column:review_count"`
	Lapses            *int32     `gorm:"column:lapses"`
	// Version is what a writer compares against to find out whether it is
	// updating the row it read. It is not nullable in the table; it is a pointer
	// here because every other column on this row is, and a struct of pointers
	// with one value in it invites reading a zero as "no version".
	Version *int64         `gorm:"column:version"`
	Extra   datatypes.JSON `gorm:"column:extra;type:json"`
}

// TableName fixes the practice review state table name.
func (PracticeReviewState) TableName() string {
	return "practice_review_state"
}

// PracticeReviewEvent records one idempotent review submission.
type PracticeReviewEvent struct {
	RequestID         string   `gorm:"column:request_id;primaryKey;autoIncrement:false"`
	NotebookEntriesID string   `gorm:"column:notebook_entries_id;not null"`
	UserAnswer        *string  `gorm:"column:user_answer"`
	Rating            *float64 `gorm:"column:rating"`
	// IsCorrect is the judgement lifted out of Outcome, which is the same value
	// in a second place on purpose: a JSON document can be read but not indexed,
	// and "the reviews of this question that were right" is a question this table
	// is asked often enough to need an index for.
	//
	// It is a pointer, and the nil is a third value rather than a missing one:
	// nil is a skipped review — nothing was judged — while false is a review that
	// was judged wrong. The table allows the column to be NULL for exactly that
	// distinction (migration 00011).
	IsCorrect  *bool          `gorm:"column:is_correct"`
	Outcome    datatypes.JSON `gorm:"column:outcome_json;type:json"`
	ReviewTime *time.Time     `gorm:"column:review_time"`
}

// TableName fixes the practice review event table name.
func (PracticeReviewEvent) TableName() string {
	return "practice_review_events"
}
