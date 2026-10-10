package models

import "time"

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
type ReadingWorkspaceMaterial struct {
	WorkspaceID string    `gorm:"column:workspace_id;primaryKey;autoIncrement:false"`
	MaterialID  string    `gorm:"column:material_id;primaryKey;autoIncrement:false"`
	TabOrder    *int32    `gorm:"column:tab_order"`
	CreatedAt   time.Time `gorm:"column:creat_time;autoCreateTime"`
}

func (ReadingWorkspaceMaterial) TableName() string { return "reading_workspace_materials" }
