package turn

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"backend/internal/data/models"
	"backend/internal/event"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	eventIDPrefix = "evt_"
	turnIDPrefix  = "turn_"
	idRandomBytes = 16
)

// Repository is the storage seam for turns and the events they produce.
type Repository interface {
	// Start stores a queued turn, refusing it when the session already has a
	// turn that has not stopped. The check and the insert are one operation:
	// done separately, two turns started at the same moment would both find
	// nothing and both be stored (RT-02).
	Start(ctx context.Context, record Turn) error
	// Turn returns one turn whatever state it is in.
	Turn(ctx context.Context, turnID string) (Turn, error)
	// ActiveTurn returns the session's unfinished turn, or ErrNotFound.
	ActiveTurn(ctx context.Context, sessionID string) (Turn, error)
	// Save applies a state change, refusing it if the stored turn has moved on
	// since the change was prepared.
	//
	// record must be what Turn.Transition or Turn.Fail returned, and from must
	// be the status it was called on. The guard is the status and the version
	// the stored turn had *before* the change, which are not in record: a turn
	// reports where it is now, and only the caller knows where it was.
	Save(ctx context.Context, record Turn, from Status) error
	// AppendEvent stores one event. It reports ErrSeqTaken when the sequence
	// number is already there, which means another writer holds this turn.
	AppendEvent(ctx context.Context, record event.Event) error
	// Events returns a turn's events after a sequence number, oldest first.
	Events(ctx context.Context, turnID string, afterSeq int64) ([]event.Event, error)
	// LastEvent returns the most recent event of a turn, or ErrNotFound when it
	// has produced none. It is how a subscriber that is already up to date
	// learns there is nothing left to wait for.
	LastEvent(ctx context.Context, turnID string) (event.Event, error)
	// LastSeq returns the highest stored sequence number, or zero.
	LastSeq(ctx context.Context, turnID string) (int64, error)
}

// GORMRepository stores turns through GORM (seekdb over the MySQL protocol).
type GORMRepository struct {
	db *gorm.DB
}

// NewGORMRepository creates a storage adapter over an existing GORM handle.
func NewGORMRepository(db *gorm.DB) *GORMRepository {
	return &GORMRepository{db: db}
}

var _ Repository = (*GORMRepository)(nil)

// Start stores a queued turn unless the session already has an active one.
func (r *GORMRepository) Start(ctx context.Context, record Turn) error {
	if err := r.ready(); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var active models.Turn
		// A locking read, not a plain one. Two turns started for one session at
		// the same instant would both read no active turn and both insert; the
		// lock makes the second wait for the first to commit and then see it.
		// The lock covers the gap after the matched rows too, which is what
		// makes it work when there is nothing to match.
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("session_id = ? AND status IN ?", record.SessionID, activeStatusNames()).
			First(&active).Error
		if err == nil {
			return fmt.Errorf("%w: session %s", ErrActiveTurnExists, record.SessionID)
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("look for an active turn: %w", err)
		}
		model := models.Turn{
			TurnID:       record.ID,
			SessionID:    record.SessionID,
			Status:       string(record.Status),
			StateVersion: record.StateVersion,
			CreatedAt:    record.CreatedAt,
			UpdatedAt:    timePointer(record.UpdatedAt),
			Capability:   stringPointer(record.Capability),
		}
		if err := tx.Create(&model).Error; err != nil {
			return fmt.Errorf("create turn: %w", err)
		}
		return nil
	})
}

// Turn returns one turn whatever state it is in.
func (r *GORMRepository) Turn(ctx context.Context, turnID string) (Turn, error) {
	if err := r.ready(); err != nil {
		return Turn{}, err
	}
	var model models.Turn
	if err := r.db.WithContext(ctx).First(&model, "turn_id = ?", turnID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Turn{}, fmt.Errorf("%w: %s", ErrNotFound, turnID)
		}
		return Turn{}, fmt.Errorf("read turn: %w", err)
	}
	return toTurn(model), nil
}

// ActiveTurn returns the session's unfinished turn.
func (r *GORMRepository) ActiveTurn(ctx context.Context, sessionID string) (Turn, error) {
	if err := r.ready(); err != nil {
		return Turn{}, err
	}
	var model models.Turn
	err := r.db.WithContext(ctx).
		Where("session_id = ? AND status IN ?", sessionID, activeStatusNames()).
		Order("update_time DESC").
		First(&model).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Turn{}, fmt.Errorf("%w: session %s has no active turn", ErrNotFound, sessionID)
		}
		return Turn{}, fmt.Errorf("read active turn: %w", err)
	}
	return toTurn(model), nil
}

// Save applies a state change, guarded by the state it was prepared from.
func (r *GORMRepository) Save(ctx context.Context, record Turn, from Status) error {
	if err := r.ready(); err != nil {
		return err
	}
	result := r.db.WithContext(ctx).
		Model(&models.Turn{}).
		Where("turn_id = ? AND status = ? AND state_version = ?",
			record.ID, string(from), record.StateVersion-1).
		Updates(r.updates(record))
	if result.Error != nil {
		return fmt.Errorf("update turn: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		// Nothing matched. Which of the two reasons it was decides whether the
		// caller is looking at the wrong turn or a turn that moved, so it is
		// worth the second read on a path that should not happen.
		if _, err := r.Turn(ctx, record.ID); err != nil {
			return err
		}
		return fmt.Errorf("%w: %s", ErrStateConflict, record.ID)
	}
	return nil
}

// updates is the set of columns a state change may write.
//
// Only the ones that apply are written. A failure code on a completed turn, or
// a retryable flag on one that never failed, would each be a column claiming
// something the turn never said.
func (r *GORMRepository) updates(record Turn) map[string]any {
	updates := map[string]any{
		"status":        string(record.Status),
		"state_version": record.StateVersion,
		"update_time":   record.UpdatedAt,
	}
	if record.Capability != "" {
		updates["capability"] = record.Capability
	}
	if record.AssistantMessageID != "" {
		updates["assistant_message_id"] = record.AssistantMessageID
	}
	if record.Status == StatusFailed {
		updates["error"] = record.Error
		updates["failure_code"] = record.FailureCode
		updates["retryable"] = boolToInt(record.Retryable)
	}
	if record.Status.Terminal() && !record.FinishedAt.IsZero() {
		updates["finish_time"] = record.FinishedAt
	}
	return updates
}

// AppendEvent stores one event of a turn.
func (r *GORMRepository) AppendEvent(ctx context.Context, record event.Event) error {
	if err := r.ready(); err != nil {
		return err
	}
	typeID, ok := record.Type.ID()
	if !ok {
		return fmt.Errorf("append event: %q is not an event type", record.Type)
	}
	metadata, err := metadataJSON(record.Metadata)
	if err != nil {
		return err
	}
	id, err := newID(eventIDPrefix)
	if err != nil {
		return fmt.Errorf("generate event id: %w", err)
	}
	model := models.TurnEvent{
		TurnEventID: id,
		TurnID:      record.TurnID,
		Sequence:    record.Seq,
		Timestamp:   record.Timestamp,
		TypeID:      typeID,
		Metadata:    metadata,
		Content:     stringPointer(record.Content),
		Source:      stringPointer(record.Source),
		Stage:       stringPointer(record.Stage),
	}
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		// A taken sequence number means another writer owns this turn, so it is
		// reported as such rather than as a storage failure to retry.
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return fmt.Errorf("%w: %s seq %d", ErrSeqTaken, record.TurnID, record.Seq)
		}
		return fmt.Errorf("append event: %w", err)
	}
	return nil
}

// Events returns a turn's events after a sequence number.
func (r *GORMRepository) Events(ctx context.Context, turnID string, afterSeq int64) ([]event.Event, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	var records []models.TurnEvent
	err := r.db.WithContext(ctx).
		Where("turn_id = ? AND seq > ?", turnID, afterSeq).
		Order("seq ASC").
		Find(&records).Error
	if err != nil {
		return nil, fmt.Errorf("read events: %w", err)
	}
	events := make([]event.Event, 0, len(records))
	for _, record := range records {
		converted, err := toEvent(record)
		if err != nil {
			return nil, err
		}
		events = append(events, converted)
	}
	return events, nil
}

// LastEvent returns the most recent event of a turn.
func (r *GORMRepository) LastEvent(ctx context.Context, turnID string) (event.Event, error) {
	if err := r.ready(); err != nil {
		return event.Event{}, err
	}
	var model models.TurnEvent
	err := r.db.WithContext(ctx).
		Where("turn_id = ?", turnID).
		Order("seq DESC").
		First(&model).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return event.Event{}, fmt.Errorf("%w: %s has no events", ErrNotFound, turnID)
		}
		return event.Event{}, fmt.Errorf("read last event: %w", err)
	}
	return toEvent(model)
}

// LastSeq returns the highest stored sequence number.
func (r *GORMRepository) LastSeq(ctx context.Context, turnID string) (int64, error) {
	if err := r.ready(); err != nil {
		return 0, err
	}
	var highest sql.NullInt64
	err := r.db.WithContext(ctx).
		Model(&models.TurnEvent{}).
		Where("turn_id = ?", turnID).
		Select("MAX(seq)").
		Scan(&highest).Error
	if err != nil {
		return 0, fmt.Errorf("read last sequence: %w", err)
	}
	// A turn with no events yet aggregates to NULL, which is seq 0 rather than
	// a missing answer: the next event of every turn is 1.
	if !highest.Valid {
		return 0, nil
	}
	return highest.Int64, nil
}

func (r *GORMRepository) ready() error {
	if r == nil || r.db == nil {
		return fmt.Errorf("turn repository is not initialized")
	}
	return nil
}

func toTurn(model models.Turn) Turn {
	converted := Turn{
		ID:           model.TurnID,
		SessionID:    model.SessionID,
		Status:       Status(model.Status),
		StateVersion: model.StateVersion,
		CreatedAt:    model.CreatedAt,
	}
	if model.Capability != nil {
		converted.Capability = *model.Capability
	}
	if model.UpdatedAt != nil {
		converted.UpdatedAt = *model.UpdatedAt
	}
	if model.FinishedAt != nil {
		converted.FinishedAt = *model.FinishedAt
	}
	if model.Error != nil {
		converted.Error = *model.Error
	}
	if model.FailureCode != nil {
		converted.FailureCode = *model.FailureCode
	}
	if model.Retryable != nil {
		converted.Retryable = *model.Retryable != 0
	}
	if model.AssistantMessageID != nil {
		converted.AssistantMessageID = *model.AssistantMessageID
	}
	return converted
}

func toEvent(model models.TurnEvent) (event.Event, error) {
	eventType, ok := event.TypeByID(model.TypeID)
	if !ok {
		return event.Event{}, fmt.Errorf("read event %s: type id %d is not one of ours",
			model.TurnEventID, model.TypeID)
	}
	converted := event.Event{
		TurnID:    model.TurnID,
		Seq:       model.Sequence,
		Type:      eventType,
		Timestamp: model.Timestamp,
	}
	if model.Content != nil {
		converted.Content = *model.Content
	}
	if model.Source != nil {
		converted.Source = *model.Source
	}
	if model.Stage != nil {
		converted.Stage = *model.Stage
	}
	if len(model.Metadata) > 0 {
		if err := json.Unmarshal(model.Metadata, &converted.Metadata); err != nil {
			return event.Event{}, fmt.Errorf("read event %s metadata: %w", model.TurnEventID, err)
		}
	}
	return converted, nil
}

// metadataJSON renders an event's metadata for storage. An empty map is stored
// as nothing rather than as "{}", so a column that holds no payload is absent
// from the row rather than looking like one that does.
func metadataJSON(metadata map[string]any) ([]byte, error) {
	if len(metadata) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("encode event metadata: %w", err)
	}
	return encoded, nil
}

// activeStatusNames is the storage spelling of the statuses that hold a
// session's turn slot.
func activeStatusNames() []string {
	names := make([]string, 0, len(statuses))
	for _, status := range statuses {
		if status.Active() {
			names = append(names, string(status))
		}
	}
	return names
}

func newID(prefix string) (string, error) {
	value := make([]byte, idRandomBytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(value), nil
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func timePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func boolToInt(value bool) int16 {
	if value {
		return 1
	}
	return 0
}
