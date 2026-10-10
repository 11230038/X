package models

import (
	"time"

	"gorm.io/datatypes"
)

// LLMCall stores usage metadata for one language-model invocation.
type LLMCall struct {
	CallID    string         `gorm:"column:call_id;primaryKey;autoIncrement:false"`
	StartedAt time.Time      `gorm:"column:started_at;not null"`
	SessionID string         `gorm:"column:session_id;not null"`
	TurnID    string         `gorm:"column:turn_id;not null"`
	Source    string         `gorm:"column:source;not null"`
	Usage     datatypes.JSON `gorm:"column:usage_json;type:json;not null"`
}

func (LLMCall) TableName() string { return "llm_calls" }
