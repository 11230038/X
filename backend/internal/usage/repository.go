package usage

// The store behind the recorder: a row opened, and the same row completed.
//
// There is no third method, and the missing one is the interesting part: a call
// cannot be written whole. A store that could insert a finished call would make
// "finished_at IS NULL" mean "somebody used the other method", and the state
// §2.7 asks for — a call that was made and never came back — would stop being
// answerable.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"backend/internal/data"
	"backend/internal/data/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// GORMStore persists llm_calls through GORM.
type GORMStore struct {
	db *gorm.DB
}

// NewGORMStore creates a store over an existing handle.
func NewGORMStore(db *gorm.DB) *GORMStore { return &GORMStore{db: db} }

func (s *GORMStore) ready() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("usage store is not initialized")
	}
	return nil
}

// usageJSON is the row's own copy of its numbers, kept beside the columns.
//
// It holds the same counts the columns hold and the one thing they do not: the
// reason the provider stopped (§2.8). Both are written from this one value, so
// the two spellings of the same number cannot disagree — and a reader still
// reads the columns, because a field inside a JSON document is not something an
// index can answer.
type usageJSON struct {
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	DurationMs       int64  `json:"duration_ms"`
	TTFTMs           int64  `json:"ttft_ms"`
	FinishReason     string `json:"finish_reason,omitempty"`
}

// Open writes the first half of a call's record.
//
// usage_json is written as an empty object here rather than left NULL, because
// the column is NOT NULL and holds an object by check constraint (00005): a row
// that says a call is happening has to say it in the shape the table allows.
func (s *GORMStore) Open(ctx context.Context, open OpenCall) error {
	if err := s.ready(); err != nil {
		return err
	}
	row := models.LLMCall{
		CallID:    open.CallID,
		StartedAt: open.StartedAt,
		SessionID: optionalName(open.Call.SessionID),
		TurnID:    optionalName(open.Call.TurnID),
		Source:    strings.TrimSpace(open.Call.Source),
		Usage:     datatypes.JSON([]byte("{}")),
		OwnerID:   data.OwnerPointer(open.Call.OwnerID),
		Provider:  open.Provider,
		Model:     open.Model,
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("open the record of call %s: %w", open.CallID, err)
	}
	return nil
}

// Complete writes the second half.
//
// An update that matched no row is reported as a failure. On this table the two
// cases MySQL folds together — nothing matched and nothing changed — cannot
// arise together: the row was written open a moment ago by the call that is now
// completing it, so finished_at and outcome are always changing from nothing to
// something, and a zero count means the row is gone.
func (s *GORMStore) Complete(ctx context.Context, callID string, done Completion) error {
	if err := s.ready(); err != nil {
		return err
	}
	numbers := usageJSON{
		PromptTokens:     int64(done.Usage.PromptTokens),
		CompletionTokens: int64(done.Usage.CompletionTokens),
		TotalTokens:      int64(done.Usage.TotalTokens),
		DurationMs:       done.DurationMs,
		TTFTMs:           done.TTFTMs,
		FinishReason:     done.FinishReason,
	}
	encoded, err := json.Marshal(numbers)
	if err != nil {
		return fmt.Errorf("encode the usage of call %s: %w", callID, err)
	}
	result := s.db.WithContext(ctx).Model(&models.LLMCall{}).
		Where("call_id = ?", callID).
		Updates(map[string]any{
			"finished_at":       done.FinishedAt,
			"outcome":           string(done.Outcome),
			"usage_json":        datatypes.JSON(encoded),
			"prompt_tokens":     numbers.PromptTokens,
			"completion_tokens": numbers.CompletionTokens,
			"total_tokens":      numbers.TotalTokens,
			"duration_ms":       numbers.DurationMs,
			"ttft_ms":           numbers.TTFTMs,
		})
	if result.Error != nil {
		return fmt.Errorf("complete the record of call %s: %w", callID, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("complete the record of call %s: there is no such row", callID)
	}
	return nil
}

// optionalName stores a name that may not be there as NULL.
//
// The empty string is not a name, and it is not what this is: a call made
// outside a conversation has no session, and the column says so rather than
// holding a blank that a reader would have to know to read as "none".
func optionalName(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

var _ Store = (*GORMStore)(nil)
