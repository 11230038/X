package models

import (
	"time"

	"gorm.io/datatypes"
)

// LLMCall stores usage metadata for one language-model invocation.
//
// It is written in two parts (§2.7): the row goes in before the request is sent,
// with the identifying columns filled and FinishedAt nil, and it is completed
// when the answer arrives. That is why StartedAt and FinishedAt are two columns
// rather than a duration: a call that never came back is a row with no
// FinishedAt, which is a state this table can be asked about — and it is the
// only way a call that is still in flight is visible at all.
//
// The counts are columns as well as fields inside Usage. That is deliberate
// duplication (migration 00015): the aggregation §17.1 asks for is by model, by
// provider and by source, and a field inside a JSON document cannot be indexed.
// The two are written from one source and only the columns are read back.
type LLMCall struct {
	CallID    string         `gorm:"column:call_id;primaryKey;autoIncrement:false"`
	StartedAt time.Time      `gorm:"column:started_at;not null"`
	SessionID string         `gorm:"column:session_id;not null"`
	TurnID    string         `gorm:"column:turn_id;not null"`
	Source    string         `gorm:"column:source;not null"`
	Usage     datatypes.JSON `gorm:"column:usage_json;type:json;not null"`
	// OwnerID is the account whose quota this call counts against (migration
	// 00014). It is on the row rather than derived through session_id because
	// the calls that name a session have a session and the short ones need not.
	OwnerID *int64 `gorm:"column:owner_id"`
	// Provider, Model and the four counts are the same numbers usage_json
	// carries, promoted to columns so that a window can be summed over without
	// reading and parsing every row.
	Provider         string `gorm:"column:provider;not null;default:''"`
	Model            string `gorm:"column:model;not null;default:''"`
	PromptTokens     int64  `gorm:"column:prompt_tokens;not null;default:0"`
	CompletionTokens int64  `gorm:"column:completion_tokens;not null;default:0"`
	TotalTokens      int64  `gorm:"column:total_tokens;not null;default:0"`
	DurationMs       int64  `gorm:"column:duration_ms;not null;default:0"`
	TTFTMs           int64  `gorm:"column:ttft_ms;not null;default:0"`
	// FinishedAt is nil while the call is open. A row that stays nil is a call
	// that did not come back, which is a thing to be able to see rather than a
	// row to be guessed at from its absence.
	FinishedAt *time.Time `gorm:"column:finished_at"`
	// Outcome is the empty string while the call is open and one of ok, error or
	// cancelled once it is not. The vocabulary is closed by a check constraint,
	// because an outcome that cannot be enumerated is one that cannot be counted.
	Outcome string `gorm:"column:outcome;not null;default:''"`
}

func (LLMCall) TableName() string { return "llm_calls" }
