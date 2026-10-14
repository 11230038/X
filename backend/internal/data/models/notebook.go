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
	// UpdatedAt is on the answer face: the review path writes it, and nothing else
	// does. GORM would otherwise fill a field of this name on every insert and
	// every update, which would put a value on the answer face of a question
	// nobody has answered — so the automatic behaviour is turned off and the one
	// writer that means it writes the column itself.
	UpdatedAt        *time.Time     `gorm:"column:update_time;autoUpdateTime:false"`
	Difficulty       *string        `gorm:"column:difficulty"`
	UserAnswer       *string        `gorm:"column:user_answer"`
	UserAnswerImage  datatypes.JSON `gorm:"column:user_answer_image;type:json"`
	Options          datatypes.JSON `gorm:"column:options;type:json"`
	CorrectAnswer    *string        `gorm:"column:correct_answer"`
	Explanation      *string        `gorm:"column:explanation"`
	Quality          *string        `gorm:"column:quality"`
	AssessmentType   *string        `gorm:"column:assessment_type"`
	IsCorrect        *bool          `gorm:"column:is_correct"`
	Result           *string        `gorm:"column:result"`
	Resolved         *bool          `gorm:"column:resolved"`
	AttemptCount     *int32         `gorm:"column:attempt_count"`
	Bookmarked       *bool          `gorm:"column:bookmarked"`
	Source           *string        `gorm:"column:source"`
	MaterialID       *string        `gorm:"column:material_id"`
	MasteryPathID    *string        `gorm:"column:mastery_path_id"`
	KnowledgePointID *string        `gorm:"column:knowledge_point_id"`
	SessionID        *string        `gorm:"column:session_id"`
	TurnID           *string        `gorm:"column:turn_id"`
	// OwnerID is carried here rather than derived from the session because
	// session_id is nullable (migration 00014): a question asked with no
	// conversation behind it has no chain to walk.
	OwnerID *int64 `gorm:"column:owner_id"`
}

// TableName fixes the notebook entry table name.
func (NotebookEntry) TableName() string {
	return "notebook_entries"
}

// NotebookCategory is a persisted question category.
type NotebookCategory struct {
	NotebookCategoriesID string    `gorm:"column:notebook_categories_id;primaryKey;autoIncrement:false"`
	Name                 string    `gorm:"column:name;not null"`
	CreatedAt            time.Time `gorm:"column:creat_time;autoCreateTime"`
	// Names are unique per owner, not globally (migration 00014).
	OwnerID *int64 `gorm:"column:owner_id"`
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
