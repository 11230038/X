package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode"

	"backend/internal/authz"
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
//
// The new row is owned by the caller. A session written without an owner is not
// a session the creator can find again — the read that follows would filter it
// out — so this is the one place where forgetting the owner breaks the write
// rather than the read.
func (s *Service) Create(ctx context.Context, caller authz.Caller) (Session, error) {
	id, err := newID(sessionIDPrefix)
	if err != nil {
		return Session{}, fmt.Errorf("generate session id: %w", err)
	}
	now := time.Now().UTC()
	record := Session{ID: id, OwnerID: caller.UserID, CreatedAt: now, UpdatedAt: now}
	if err := s.repository.Create(ctx, record); err != nil {
		return Session{}, err
	}
	return record, nil
}

// Get returns a live session. A session in the recycle bin is not found: it is
// not something a turn may be started in or a history may be read from, and
// saying so here is what keeps that rule from being restated at every call site.
func (s *Service) Get(ctx context.Context, sessionID string, caller authz.Caller) (Session, error) {
	record, err := s.repository.Session(ctx, sessionID, caller)
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
func (s *Service) Rename(ctx context.Context, sessionID, title string, caller authz.Caller) (Session, error) {
	cleaned, err := cleanTitle(title)
	if err != nil {
		return Session{}, err
	}
	record, err := s.Get(ctx, sessionID, caller)
	if err != nil {
		return Session{}, err
	}
	now := time.Now().UTC()
	if err := s.repository.Rename(ctx, sessionID, cleaned, now, caller); err != nil {
		return Session{}, err
	}
	record.Title = cleaned
	record.UpdatedAt = now
	return record, nil
}

// Claim gives a session that belongs to nobody to the caller.
//
// It is the one write in this package that does not begin by reading the session
// it acts on, and it is the exception that proves the rule: every other write
// has a decision to make about the row's state — is it in the bin, is it live —
// and reading is how the decision gets made. Claim's decision is made by the
// update itself, which is a fact about the row rather than about the state the
// service found it in. Reading first would only widen the window in which the
// row it read stops being the row it writes.
//
// There is no rule here about who may claim, and that absence is the design: the
// scoped read inside the repository means a session belonging to another account
// is not there to be claimed (AUTH-06), and a session belonging to nobody is
// visible to the administrator alone (§2.5). What is left is a row the caller is
// entitled to and nobody holds.
func (s *Service) Claim(ctx context.Context, sessionID string, caller authz.Caller) error {
	return s.repository.Claim(ctx, sessionID, caller)
}

// SoftDelete moves a live session into the recycle bin.
func (s *Service) SoftDelete(ctx context.Context, sessionID string, caller authz.Caller) error {
	record, err := s.Get(ctx, sessionID, caller)
	if err != nil {
		return err
	}
	return s.repository.SetDeleted(ctx, record.ID, true, time.Now().UTC(), caller)
}

// Restore takes a session back out of the recycle bin.
func (s *Service) Restore(ctx context.Context, sessionID string, caller authz.Caller) error {
	if _, err := s.binned(ctx, sessionID, caller); err != nil {
		return err
	}
	return s.repository.SetDeleted(ctx, sessionID, false, time.Now().UTC(), caller)
}

// DeletePermanently removes a session and everything in it.
//
// It is only reachable from the recycle bin (CHAT-06). Deleting straight from
// the live list would be one click away from losing a conversation with no step
// to notice it in.
func (s *Service) DeletePermanently(ctx context.Context, sessionID string, caller authz.Caller) error {
	if _, err := s.binned(ctx, sessionID, caller); err != nil {
		return err
	}
	return s.repository.Delete(ctx, sessionID, caller)
}

// AppendMessage adds a message to a live session.
func (s *Service) AppendMessage(ctx context.Context, input NewMessage, caller authz.Caller) (Message, error) {
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
	if err := s.repository.AppendMessage(ctx, record, caller); err != nil {
		return Message{}, err
	}
	return record, nil
}

// Messages returns a live session's history, oldest first.
func (s *Service) Messages(ctx context.Context, sessionID string, caller authz.Caller) ([]Message, error) {
	if _, err := s.Get(ctx, sessionID, caller); err != nil {
		return nil, err
	}
	return s.repository.Messages(ctx, sessionID, caller)
}

// binned returns a session only if it is in the recycle bin.
func (s *Service) binned(ctx context.Context, sessionID string, caller authz.Caller) (Session, error) {
	record, err := s.repository.Session(ctx, sessionID, caller)
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
