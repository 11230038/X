package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"backend/internal/data/models"
	"gorm.io/gorm"
)

// GORMRepository reads and writes conversations through GORM (seekdb over the
// MySQL protocol).
type GORMRepository struct {
	db *gorm.DB
}

// NewGORMRepository creates a storage adapter over an existing GORM handle.
func NewGORMRepository(db *gorm.DB) *GORMRepository {
	return &GORMRepository{db: db}
}

var _ Repository = (*GORMRepository)(nil)

// Create inserts one session.
func (r *GORMRepository) Create(ctx context.Context, record Session) error {
	if err := r.ready(); err != nil {
		return err
	}
	model := models.Session{
		SessionID: record.ID,
		Title:     titlePointer(record.Title),
		CreatedAt: record.CreatedAt,
		UpdatedAt: record.UpdatedAt,
	}
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// Session returns one session whatever state it is in.
func (r *GORMRepository) Session(ctx context.Context, sessionID string) (Session, error) {
	if err := r.ready(); err != nil {
		return Session{}, err
	}
	var model models.Session
	if err := r.db.WithContext(ctx).First(&model, "session_id = ?", sessionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Session{}, fmt.Errorf("%w: %s", ErrNotFound, sessionID)
		}
		return Session{}, fmt.Errorf("read session: %w", err)
	}
	return toSession(model), nil
}

// List returns one page of sessions, most recently active first.
//
// The order has a tiebreaker: update_time alone is not unique, and a listing
// whose order changes between identical requests would make paging skip and
// repeat rows.
func (r *GORMRepository) List(ctx context.Context, options ListOptions) ([]Session, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	var records []models.Session
	query := r.db.WithContext(ctx).
		Where("is_deleted = ?", options.Deleted).
		Order("update_time DESC, session_id ASC").
		Limit(clampLimit(options.Limit)).
		Offset(max(options.Offset, 0))
	if err := query.Find(&records).Error; err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	sessions := make([]Session, 0, len(records))
	for _, record := range records {
		sessions = append(sessions, toSession(record))
	}
	return sessions, nil
}

// Rename sets a session's title and marks the change as activity.
func (r *GORMRepository) Rename(ctx context.Context, sessionID, title string, now time.Time) error {
	return r.update(ctx, sessionID, map[string]any{
		"title":       title,
		"update_time": now,
	})
}

// SetDeleted moves a session into or out of the recycle bin.
//
// Both columns are written together because the table's check constraint
// requires them to agree, so a state where one was updated and the other was not
// is rejected by the database rather than stored.
func (r *GORMRepository) SetDeleted(ctx context.Context, sessionID string, deleted bool, now time.Time) error {
	updates := map[string]any{"is_deleted": deleted, "update_time": now}
	if deleted {
		updates["deleted_at"] = now
	} else {
		updates["deleted_at"] = nil
	}
	return r.update(ctx, sessionID, updates)
}

// Delete removes a session for good. Its messages and turns go with it, through
// the cascade the schema already declares.
func (r *GORMRepository) Delete(ctx context.Context, sessionID string) error {
	if err := r.ready(); err != nil {
		return err
	}
	result := r.db.WithContext(ctx).Delete(&models.Session{}, "session_id = ?", sessionID)
	if result.Error != nil {
		return fmt.Errorf("delete session: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, sessionID)
	}
	return nil
}

// AppendMessage stores a message and marks the session active, together.
func (r *GORMRepository) AppendMessage(ctx context.Context, record Message) error {
	if err := r.ready(); err != nil {
		return err
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		model := models.Message{
			MessageID: record.ID,
			SessionID: record.SessionID,
			Role:      string(record.Role),
			Content:   &record.Content,
			CreatedAt: record.CreatedAt,
		}
		if record.Capability != "" {
			model.Capability = &record.Capability
		}
		if err := tx.Create(&model).Error; err != nil {
			return fmt.Errorf("create message: %w", err)
		}
		result := tx.Model(&models.Session{}).
			Where("session_id = ?", record.SessionID).
			Update("update_time", record.CreatedAt)
		if result.Error != nil {
			return fmt.Errorf("mark session active: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("%w: %s", ErrNotFound, record.SessionID)
		}
		return nil
	})
	return err
}

// Messages returns a session's messages, oldest first.
func (r *GORMRepository) Messages(ctx context.Context, sessionID string) ([]Message, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	var records []models.Message
	// message_id breaks ties: two messages can share a timestamp, and the order
	// the model sees has to be the order they were written in.
	err := r.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Order("creat_time ASC, message_id ASC").
		Find(&records).Error
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	messages := make([]Message, 0, len(records))
	for _, record := range records {
		messages = append(messages, toMessage(record))
	}
	return messages, nil
}

// update applies a column change to one session and reports whether it was
// there.
func (r *GORMRepository) update(ctx context.Context, sessionID string, updates map[string]any) error {
	if err := r.ready(); err != nil {
		return err
	}
	result := r.db.WithContext(ctx).
		Model(&models.Session{}).
		Where("session_id = ?", sessionID).
		Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("update session: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, sessionID)
	}
	return nil
}

func (r *GORMRepository) ready() error {
	if r == nil || r.db == nil {
		return fmt.Errorf("session repository is not initialized")
	}
	return nil
}

func toSession(model models.Session) Session {
	converted := Session{
		ID:        model.SessionID,
		CreatedAt: model.CreatedAt,
		UpdatedAt: model.UpdatedAt,
	}
	if model.Title != nil {
		converted.Title = *model.Title
	}
	if model.DeletedAt != nil {
		converted.DeletedAt = *model.DeletedAt
	}
	return converted
}

func toMessage(model models.Message) Message {
	converted := Message{
		ID:        model.MessageID,
		SessionID: model.SessionID,
		Role:      Role(model.Role),
		CreatedAt: model.CreatedAt,
	}
	if model.Content != nil {
		converted.Content = *model.Content
	}
	if model.Capability != nil {
		converted.Capability = *model.Capability
	}
	return converted
}

func titlePointer(title string) *string {
	if title == "" {
		return nil
	}
	return &title
}

func clampLimit(limit int) int {
	if limit <= 0 {
		return DefaultListLimit
	}
	return min(limit, MaxListLimit)
}
