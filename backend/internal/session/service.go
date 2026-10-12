package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode"
)

const (
	sessionIDPrefix = "sess_"
	messageIDPrefix = "msg_"
	idRandomBytes   = 16
)

// Service owns the rules about conversations: which state a session is in, what
// each state allows, and what a message may be.
type Service struct {
	repository Repository
}

// NewService returns the service over a storage adapter.
func NewService(repository Repository) (*Service, error) {
	if repository == nil {
		return nil, fmt.Errorf("session service needs a repository")
	}
	return &Service{repository: repository}, nil
}

// Create starts a conversation with no messages and no title. The title is left
// to be generated from what the conversation turns out to be about, so a session
// that no one ever writes in stays untitled rather than misnamed.
func (s *Service) Create(ctx context.Context) (Session, error) {
	id, err := newID(sessionIDPrefix)
	if err != nil {
		return Session{}, fmt.Errorf("generate session id: %w", err)
	}
	now := time.Now().UTC()
	record := Session{ID: id, CreatedAt: now, UpdatedAt: now}
	if err := s.repository.Create(ctx, record); err != nil {
		return Session{}, err
	}
	return record, nil
}

// Get returns a live session. A session in the recycle bin is not found: it is
// not something a turn may be started in or a history may be read from, and
// saying so here is what keeps that rule from being restated at every call site.
func (s *Service) Get(ctx context.Context, sessionID string) (Session, error) {
	record, err := s.repository.Session(ctx, sessionID)
	if err != nil {
		return Session{}, err
	}
	if record.Deleted() {
		return Session{}, fmt.Errorf("%w: %s is in the recycle bin", ErrNotFound, sessionID)
	}
	return record, nil
}

// List returns one page of sessions, the recycle bin or the live ones.
func (s *Service) List(ctx context.Context, options ListOptions) ([]Session, error) {
	return s.repository.List(ctx, options)
}

// Rename titles a live session and returns it as it now stands.
func (s *Service) Rename(ctx context.Context, sessionID, title string) (Session, error) {
	cleaned, err := cleanTitle(title)
	if err != nil {
		return Session{}, err
	}
	record, err := s.Get(ctx, sessionID)
	if err != nil {
		return Session{}, err
	}
	now := time.Now().UTC()
	if err := s.repository.Rename(ctx, sessionID, cleaned, now); err != nil {
		return Session{}, err
	}
	record.Title = cleaned
	record.UpdatedAt = now
	return record, nil
}

// SoftDelete moves a live session into the recycle bin.
func (s *Service) SoftDelete(ctx context.Context, sessionID string) error {
	record, err := s.Get(ctx, sessionID)
	if err != nil {
		return err
	}
	return s.repository.SetDeleted(ctx, record.ID, true, time.Now().UTC())
}

// Restore takes a session back out of the recycle bin.
func (s *Service) Restore(ctx context.Context, sessionID string) error {
	if _, err := s.binned(ctx, sessionID); err != nil {
		return err
	}
	return s.repository.SetDeleted(ctx, sessionID, false, time.Now().UTC())
}

// DeletePermanently removes a session and everything in it.
//
// It is only reachable from the recycle bin (CHAT-06). Deleting straight from
// the live list would be one click away from losing a conversation with no step
// to notice it in.
func (s *Service) DeletePermanently(ctx context.Context, sessionID string) error {
	if _, err := s.binned(ctx, sessionID); err != nil {
		return err
	}
	return s.repository.Delete(ctx, sessionID)
}

// AppendMessage adds a message to a live session.
func (s *Service) AppendMessage(ctx context.Context, input NewMessage) (Message, error) {
	if !input.Role.Valid() {
		return Message{}, fmt.Errorf("%w: %q is not a role this version writes", ErrInvalidMessage, input.Role)
	}
	id, err := newID(messageIDPrefix)
	if err != nil {
		return Message{}, fmt.Errorf("generate message id: %w", err)
	}
	record := Message{
		ID:         id,
		SessionID:  input.SessionID,
		Role:       input.Role,
		Content:    input.Content,
		Capability: input.Capability,
		CreatedAt:  time.Now().UTC(),
	}
	if err := s.repository.AppendMessage(ctx, record); err != nil {
		return Message{}, err
	}
	return record, nil
}

// Messages returns a live session's history, oldest first.
func (s *Service) Messages(ctx context.Context, sessionID string) ([]Message, error) {
	if _, err := s.Get(ctx, sessionID); err != nil {
		return nil, err
	}
	return s.repository.Messages(ctx, sessionID)
}

// binned returns a session only if it is in the recycle bin.
func (s *Service) binned(ctx context.Context, sessionID string) (Session, error) {
	record, err := s.repository.Session(ctx, sessionID)
	if err != nil {
		return Session{}, err
	}
	if !record.Deleted() {
		return Session{}, fmt.Errorf("%w: %s is not in the recycle bin", ErrNotFound, sessionID)
	}
	return record, nil
}

// cleanTitle trims and bounds a title.
//
// Control characters are rejected rather than stripped: a title with a newline
// in it renders as two list rows, and silently editing what the user typed would
// leave them looking at something they did not write.
func cleanTitle(title string) (string, error) {
	cleaned := strings.TrimSpace(title)
	if cleaned == "" {
		return "", fmt.Errorf("%w: a title must not be blank", ErrInvalidTitle)
	}
	if len([]rune(cleaned)) > TitleMaxRunes {
		return "", fmt.Errorf("%w: a title must be at most %d characters", ErrInvalidTitle, TitleMaxRunes)
	}
	for _, r := range cleaned {
		if r == 0 || unicode.IsControl(r) {
			return "", fmt.Errorf("%w: a title must not contain control characters", ErrInvalidTitle)
		}
	}
	return cleaned, nil
}

func newID(prefix string) (string, error) {
	value := make([]byte, idRandomBytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(value), nil
}
