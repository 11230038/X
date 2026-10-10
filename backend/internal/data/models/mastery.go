package models

import (
	"time"

	"gorm.io/datatypes"
)

// MasteryPath stores one mastery path state snapshot.
type MasteryPath struct {
	MasteryPathID  string         `gorm:"column:mastery_path_id;primaryKey;autoIncrement:false"`
	OwnerSessionID string         `gorm:"column:owner_session_id;not null"`
	State          datatypes.JSON `gorm:"column:state_json;type:json"`
	Version        *int32         `gorm:"column:version"`
	CreatedAt      time.Time      `gorm:"column:creat_time;autoCreateTime"`
	UpdatedAt      *time.Time     `gorm:"column:update_time;autoUpdateTime:false"`
}

func (MasteryPath) TableName() string { return "mastery_paths" }

// MasteryLearningEvidence stores one learning result attached to a path.
type MasteryLearningEvidence struct {
	MLEID          string         `gorm:"column:mle_id;primaryKey;autoIncrement:false"`
	SessionID      string         `gorm:"column:session_id;not null"`
	TurnID         string         `gorm:"column:turn_id;not null"`
	PathID         string         `gorm:"column:path_id;not null"`
	Extra          datatypes.JSON `gorm:"column:extra;type:json"`
	Result         *string        `gorm:"column:result"`
	CreatedAt      time.Time      `gorm:"column:creat_time;autoCreateTime"`
	Source         *string        `gorm:"column:source"`
	Quality        *string        `gorm:"column:quality"`
	AssessmentType *string        `gorm:"column:assessment_type"`
}

func (MasteryLearningEvidence) TableName() string { return "mastery_learning_evidences" }

// MasteryPathLease records the current path writer.
type MasteryPathLease struct {
	PathID    string    `gorm:"column:path_id;primaryKey;autoIncrement:false"`
	SessionID string    `gorm:"column:session_id;not null"`
	TurnID    string    `gorm:"column:turn_id;not null"`
	CreatedAt time.Time `gorm:"column:creat_time;autoCreateTime"`
}

func (MasteryPathLease) TableName() string { return "mastery_path_leases" }

// MasteryInteraction stores one mastery question interaction.
type MasteryInteraction struct {
	InteractionID string         `gorm:"column:interaction_id;primaryKey;autoIncrement:false"`
	PathID        string         `gorm:"column:path_id;not null"`
	TurnID        string         `gorm:"column:turn_id;not null"`
	SessionID     string         `gorm:"column:session_id;not null"`
	UserAnswer    *string        `gorm:"column:user_answer"`
	Result        datatypes.JSON `gorm:"column:result_json;type:json"`
	Question      datatypes.JSON `gorm:"column:question_json;type:json"`
	CreatedAt     time.Time      `gorm:"column:creat_time;autoCreateTime"`
	UpdatedAt     *time.Time     `gorm:"column:update_time;autoUpdateTime:false"`
}

func (MasteryInteraction) TableName() string { return "mastery_interactions" }

// MasteryEvent records one path event.
type MasteryEvent struct {
	MasteryEventsID string         `gorm:"column:mastery_events_id;primaryKey;autoIncrement:false"`
	PathID          string         `gorm:"column:path_id;not null"`
	TurnID          string         `gorm:"column:turn_id;not null"`
	SessionID       string         `gorm:"column:session_id;not null"`
	Version         *int32         `gorm:"column:version"`
	Payload         datatypes.JSON `gorm:"column:payload_json;type:json"`
	EventType       *string        `gorm:"column:event_type"`
	CreatedAt       time.Time      `gorm:"column:creat_time;autoCreateTime"`
}

func (MasteryEvent) TableName() string { return "mastery_events" }

// MasteryPathSession connects a path to a participating session.
type MasteryPathSession struct {
	PathID       string     `gorm:"column:path_id;primaryKey;autoIncrement:false"`
	SessionID    string     `gorm:"column:session_id;primaryKey;autoIncrement:false"`
	CreatedAt    time.Time  `gorm:"column:creat_time;autoCreateTime"`
	LastSeenTime *time.Time `gorm:"column:last_seen_time"`
}

func (MasteryPathSession) TableName() string { return "mastery_path_sessions" }

// MasteryTopicMeta stores display metadata for one path.
type MasteryTopicMeta struct {
	PathID      string     `gorm:"column:path_id;primaryKey;autoIncrement:false"`
	Goal        *string    `gorm:"column:goal"`
	Description *string    `gorm:"column:description"`
	Emoji       *string    `gorm:"column:emoji"`
	Status      *string    `gorm:"column:status"`
	CreatedAt   time.Time  `gorm:"column:creat_time;autoCreateTime"`
	UpdatedAt   *time.Time `gorm:"column:update_time;autoUpdateTime:false"`
}

func (MasteryTopicMeta) TableName() string { return "mastery_topic_meta" }
