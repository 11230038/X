package models

import (
	"time"

	"gorm.io/datatypes"
)

// Stable turn event type IDs seeded by migration 00002.
const (
	TurnEventTypeStageStart   int16 = 1
	TurnEventTypeStageEnd     int16 = 2
	TurnEventTypeThinking     int16 = 3
	TurnEventTypeObservation  int16 = 4
	TurnEventTypeContent      int16 = 5
	TurnEventTypeToolCall     int16 = 6
	TurnEventTypeToolResult   int16 = 7
	TurnEventTypeProgress     int16 = 8
	TurnEventTypeSources      int16 = 9
	TurnEventTypeResult       int16 = 10
	TurnEventTypeError        int16 = 11
	TurnEventTypeSession      int16 = 12
	TurnEventTypeSessionMeta  int16 = 13
	TurnEventTypeDone         int16 = 14
	TurnEventTypeWaitForInput int16 = 15
)

// Stable turn event type names seeded by migration 00002.
const (
	TurnEventTypeNameStageStart   = "stage_start"
	TurnEventTypeNameStageEnd     = "stage_end"
	TurnEventTypeNameThinking     = "thinking"
	TurnEventTypeNameObservation  = "observation"
	TurnEventTypeNameContent      = "content"
	TurnEventTypeNameToolCall     = "tool_call"
	TurnEventTypeNameToolResult   = "tool_result"
	TurnEventTypeNameProgress     = "progress"
	TurnEventTypeNameSources      = "sources"
	TurnEventTypeNameResult       = "result"
	TurnEventTypeNameError        = "error"
	TurnEventTypeNameSession      = "session"
	TurnEventTypeNameSessionMeta  = "session_meta"
	TurnEventTypeNameDone         = "done"
	TurnEventTypeNameWaitForInput = "wait_for_input"
)

// TurnEventType is the persistence mapping for the event type lookup table.
type TurnEventType struct {
	TypeID      int16  `gorm:"column:type_id;primaryKey;autoIncrement:false"`
	Name        string `gorm:"column:name;not null"`
	Description string `gorm:"column:description;not null"`
}

// TableName fixes the event type table name.
func (TurnEventType) TableName() string {
	return "turn_event_types"
}

// Session is the persistence mapping for a conversation session.
type Session struct {
	SessionID     string         `gorm:"column:session_id;primaryKey;autoIncrement:false"`
	Title         *string        `gorm:"column:title"`
	CreatedAt     time.Time      `gorm:"column:creat_time;autoCreateTime"`
	UpdatedAt     time.Time      `gorm:"column:update_time;autoCreateTime;autoUpdateTime"`
	Preferences   datatypes.JSON `gorm:"column:preferences_json;type:json"`
	WorkspaceMode *string        `gorm:"column:workspace_mode"`
}

// TableName fixes the sessions table name.
func (Session) TableName() string {
	return "sessions"
}

// Message is the persistence mapping for a conversation message.
type Message struct {
	MessageID   string         `gorm:"column:message_id;primaryKey;autoIncrement:false"`
	SessionID   string         `gorm:"column:session_id;not null"`
	Content     *string        `gorm:"column:content"`
	Role        string         `gorm:"column:role;not null"`
	Extra       datatypes.JSON `gorm:"column:extra;type:json"`
	CreatedAt   time.Time      `gorm:"column:creat_time;autoCreateTime"`
	Metadata    datatypes.JSON `gorm:"column:metadata_json;type:json"`
	Attachments datatypes.JSON `gorm:"column:attachments_json;type:json"`
	Event       datatypes.JSON `gorm:"column:event_json;type:json"`
	Capability  *string        `gorm:"column:capability"`
}

// TableName fixes the messages table name.
func (Message) TableName() string {
	return "messages"
}

// Summary is a historical compressed snapshot for one session.
type Summary struct {
	SummaryID             string         `gorm:"column:summary_id;primaryKey;autoIncrement:false"`
	SessionID             string         `gorm:"column:session_id;not null"`
	CompressedSummary     *string        `gorm:"column:compressed_summary"`
	SummaryUpToMessageIDs datatypes.JSON `gorm:"column:summary_up_to_msg_id;type:json"`
	Revision              int64          `gorm:"column:revision;not null"`
	CreatedAt             time.Time      `gorm:"column:creat_time;autoCreateTime"`
}

// TableName fixes the summaries table name.
func (Summary) TableName() string {
	return "summaries"
}

// Turn is the persistence mapping for one model or tool execution.
type Turn struct {
	TurnID             string         `gorm:"column:turn_id;primaryKey;autoIncrement:false"`
	SessionID          string         `gorm:"column:session_id;not null"`
	Extra              datatypes.JSON `gorm:"column:extra;type:json"`
	CreatedAt          time.Time      `gorm:"column:creat_time;autoCreateTime"`
	UpdatedAt          *time.Time     `gorm:"column:update_time"`
	FinishedAt         *time.Time     `gorm:"column:finish_time"`
	OwnerID            *string        `gorm:"column:own_id"`
	FencingToken       *int64         `gorm:"column:fencing_token"`
	StateVersion       int64          `gorm:"column:state_version;not null"`
	Status             string         `gorm:"column:status;not null"`
	Capability         *string        `gorm:"column:capability"`
	Error              *string        `gorm:"column:error"`
	Retryable          *int16         `gorm:"column:retryable"`
	FailureCode        *string        `gorm:"column:failure_code"`
	AssistantMessageID *string        `gorm:"column:assistant_message_id"`
}

// TableName fixes the turns table name.
func (Turn) TableName() string {
	return "turns"
}

// TurnEvent is one ordered event emitted during a turn.
type TurnEvent struct {
	TurnEventID string         `gorm:"column:turn_events_id;primaryKey;autoIncrement:false"`
	TurnID      string         `gorm:"column:turn_id;not null"`
	Sequence    int64          `gorm:"column:seq;not null"`
	CreatedAt   time.Time      `gorm:"column:creat_time;autoCreateTime"`
	Timestamp   time.Time      `gorm:"column:timestamp;not null"`
	TypeID      int16          `gorm:"column:type;not null"`
	Extra       datatypes.JSON `gorm:"column:extra;type:json"`
	Metadata    datatypes.JSON `gorm:"column:metadata_json;type:json"`
	Content     *string        `gorm:"column:content"`
	Source      *string        `gorm:"column:source"`
	Stage       *string        `gorm:"column:stage"`
}

// TableName fixes the turn events table name.
func (TurnEvent) TableName() string {
	return "turn_events"
}
