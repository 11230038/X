package models

import (
	"time"

	"gorm.io/datatypes"
)

// NotebookEntry is a persisted question and its answer record.
type NotebookEntry struct {
	NotebookEntriesID    string         `gorm:"column:notebook_entries_id;primaryKey;autoIncrement:false"`
	QuestionID           string         `gorm:"column:question_id;not null"`
	Question             string         `gorm:"column:question;not null"`
	QuestionType         string         `gorm:"column:question_type;not null"`
	QuestionIllustration *string        `gorm:"column:question_illustration"`
	Extra                datatypes.JSON `gorm:"column:extra;type:json"`
	CreatedAt            time.Time      `gorm:"column:creat_time;autoCreateTime"`
	UpdatedAt            *time.Time     `gorm:"column:update_time"`
	Difficulty           *string        `gorm:"column:difficulty"`
	UserAnswer           *string        `gorm:"column:user_answer"`
	UserAnswerImage      datatypes.JSON `gorm:"column:user_answer_image;type:json"`
	Options              datatypes.JSON `gorm:"column:options;type:json"`
	CorrectAnswer        *string        `gorm:"column:correct_answer"`
	Explanation          *string        `gorm:"column:explanation"`
	Quality              *string        `gorm:"column:quality"`
	AssessmentType       *string        `gorm:"column:assessment_type"`
	IsCorrect            *bool          `gorm:"column:is_correct"`
	Result               *string        `gorm:"column:result"`
	Resolved             *bool          `gorm:"column:resolved"`
	AttemptCount         *int32         `gorm:"column:attempt_count"`
	Bookmarked           *bool          `gorm:"column:bookmarked"`
	Source               *string        `gorm:"column:source"`
	MasteryPathID        *string        `gorm:"column:mastery_path_id"`
	KnowledgePointID     *string        `gorm:"column:knowledge_point_id"`
	SessionID            *string        `gorm:"column:session_id"`
	TurnID               *string        `gorm:"column:turn_id"`
}

// TableName fixes the notebook entry table name.
func (NotebookEntry) TableName() string {
	return "notebook_entries"
}

// ReadingQuizPending is a persisted question awaiting submission.
type ReadingQuizPending struct {
	QuestionID string    `gorm:"column:question_id;primaryKey;autoIncrement:false"`
	Question   string    `gorm:"column:question;not null"`
	CreatedAt  time.Time `gorm:"column:creat_time;autoCreateTime"`
}

// TableName fixes the pending quiz table name.
func (ReadingQuizPending) TableName() string {
	return "reading_quiz_pending"
}

// NotebookCategory is a persisted question category.
type NotebookCategory struct {
	NotebookCategoriesID string    `gorm:"column:notebook_categories_id;primaryKey;autoIncrement:false"`
	Name                 string    `gorm:"column:name;not null"`
	CreatedAt            time.Time `gorm:"column:creat_time;autoCreateTime"`
}

// TableName fixes the notebook category table name.
func (NotebookCategory) TableName() string {
	return "notebook_categories"
}

// NotebookEntryCategory connects an entry to a category.
type NotebookEntryCategory struct {
	NotebookEntriesID    string `gorm:"column:notebook_entries_id;primaryKey;autoIncrement:false"`
	NotebookCategoriesID string `gorm:"column:notebook_categories_id;primaryKey;autoIncrement:false"`
}

// TableName fixes the notebook entry category table name.
func (NotebookEntryCategory) TableName() string {
	return "notebook_entry_categories"
}
