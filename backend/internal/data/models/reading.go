package models

import (
	"time"

	"gorm.io/datatypes"
)

// ReadingMaterial stores one material available to reading workspaces.
type ReadingMaterial struct {
	MaterialID string     `gorm:"column:material_id;primaryKey;autoIncrement:false"`
	ContentID  *string    `gorm:"column:content_id"`
	Filename   *string    `gorm:"column:filename"`
	SourceType *string    `gorm:"column:source_type"`
	CreatedAt  time.Time  `gorm:"column:creat_time;autoCreateTime"`
	UpdatedAt  *time.Time `gorm:"column:update_time;autoUpdateTime:false"`
}

func (ReadingMaterial) TableName() string { return "reading_materials" }

// ReadingWorkspace stores one reading workspace.
type ReadingWorkspace struct {
	WorkspaceID      string     `gorm:"column:workspace_id;primaryKey;autoIncrement:false"`
	ActiveMaterialID *string    `gorm:"column:active_material_id"`
	Title            *string    `gorm:"column:title"`
	Description      *string    `gorm:"column:description"`
	CreatedAt        time.Time  `gorm:"column:creat_time;autoCreateTime"`
	UpdatedAt        *time.Time `gorm:"column:update_time;autoUpdateTime:false"`
}

func (ReadingWorkspace) TableName() string { return "reading_workspaces" }

// ReadingWorkspaceSession connects a workspace to a conversation session.
type ReadingWorkspaceSession struct {
	WorkspaceID      string     `gorm:"column:workspace_id;primaryKey;autoIncrement:false"`
	SessionID        string     `gorm:"column:session_id;primaryKey;autoIncrement:false"`
	ActiveMaterialID *string    `gorm:"column:active_material_id"`
	CreatedAt        time.Time  `gorm:"column:creat_time;autoCreateTime"`
	UpdatedAt        *time.Time `gorm:"column:update_time;autoUpdateTime:false"`
}

func (ReadingWorkspaceSession) TableName() string { return "reading_workspace_sessions" }

// ReadingWorkspaceMaterial connects a material to a workspace tab.
//
// Pinned and Opened are nullable and say three things rather than two: true is
// yes, false is no, and NULL is that nobody has said. A tab that was never
// pinned is not a tab that was unpinned, and a boolean that collapsed the two
// would file every untouched material under "explicitly not pinned".
type ReadingWorkspaceMaterial struct {
	WorkspaceID string    `gorm:"column:workspace_id;primaryKey;autoIncrement:false"`
	MaterialID  string    `gorm:"column:material_id;primaryKey;autoIncrement:false"`
	TabOrder    *int32    `gorm:"column:tab_order"`
	Pinned      *bool     `gorm:"column:pinned"`
	Opened      *bool     `gorm:"column:opened"`
	CreatedAt   time.Time `gorm:"column:creat_time;autoCreateTime"`
}

func (ReadingWorkspaceMaterial) TableName() string { return "reading_workspace_materials" }

// ReadingPosition is where one reader stopped in one material (migration 00012).
//
// The key is the material and the conversation rather than the material alone:
// two tabs reading the same book in one conversation are in the same place,
// which is what a person expects, while two tabs reading different books cannot
// reach each other's row at all.
type ReadingPosition struct {
	MaterialID  string         `gorm:"column:material_id;primaryKey;autoIncrement:false"`
	SessionID   string         `gorm:"column:session_id;primaryKey;autoIncrement:false"`
	LocatorJSON datatypes.JSON `gorm:"column:locator_json"`
	Revision    int64          `gorm:"column:revision"`
	CreatedAt   time.Time      `gorm:"column:creat_time;autoCreateTime"`
	UpdatedAt   *time.Time     `gorm:"column:update_time;autoUpdateTime:false"`
}

func (ReadingPosition) TableName() string { return "reading_positions" }

// ReadingMark is a bookmark, a highlight or a note (READ-01).
//
// SessionID is nullable because the material owns the mark and the conversation
// is only where it was written: a deleted conversation leaves the marginalia
// standing with nothing recorded about which conversation it came from.
type ReadingMark struct {
	MarkID      string         `gorm:"column:mark_id;primaryKey;autoIncrement:false"`
	MaterialID  string         `gorm:"column:material_id"`
	SessionID   *string        `gorm:"column:session_id"`
	Kind        string         `gorm:"column:kind"`
	LocatorJSON datatypes.JSON `gorm:"column:locator_json"`
	Body        *string        `gorm:"column:body"`
	Revision    int64          `gorm:"column:revision"`
	CreatedAt   time.Time      `gorm:"column:creat_time;autoCreateTime"`
	UpdatedAt   *time.Time     `gorm:"column:update_time;autoUpdateTime:false"`
}

func (ReadingMark) TableName() string { return "reading_marks" }

// ReadingPairing is a bilingual pair the reader confirmed (READ-06).
//
// Only confirmed pairs are rows here; the candidates an interface offers are
// computed by a query and never stored, so a row's existence is the
// confirmation.
type ReadingPairing struct {
	PairingID             string    `gorm:"column:pairing_id;primaryKey;autoIncrement:false"`
	PrimaryMaterialID     string    `gorm:"column:primary_material_id"`
	CounterpartMaterialID string    `gorm:"column:counterpart_material_id"`
	PrimaryLanguage       string    `gorm:"column:primary_language"`
	CounterpartLanguage   string    `gorm:"column:counterpart_language"`
	CreatedAt             time.Time `gorm:"column:creat_time;autoCreateTime"`
}

func (ReadingPairing) TableName() string { return "reading_pairings" }

// ReadingQuizPending is a question generated about a passage and not yet
// answered (migration 00012).
//
// The key names the passage as well as the question, because a question about
// paragraph three of one book cannot be told from a question about paragraph
// nine of another by its id alone.
type ReadingQuizPending struct {
	MaterialID   string         `gorm:"column:material_id;primaryKey;autoIncrement:false"`
	Locator      string         `gorm:"column:locator;primaryKey;autoIncrement:false"`
	QuestionID   string         `gorm:"column:question_id;primaryKey;autoIncrement:false"`
	QuestionJSON datatypes.JSON `gorm:"column:question_json"`
	CreatedAt    time.Time      `gorm:"column:creat_time;autoCreateTime"`
}

func (ReadingQuizPending) TableName() string { return "reading_quiz_pending" }
