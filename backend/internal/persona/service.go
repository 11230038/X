package persona

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"backend/internal/authz"
)

const (
	personaIDPrefix = "pers_"
	idRandomBytes   = 16
)

// Service owns the rules about personas: what an instruction may be, how a name
// is made unique, and whose rows a caller can see.
type Service struct {
	repository Repository
	now        func() time.Time
}

// NewService returns the service over a storage adapter.
func NewService(repository Repository) (*Service, error) {
	if repository == nil {
		return nil, fmt.Errorf("persona service needs a repository")
	}
	return &Service{repository: repository, now: func() time.Time { return time.Now().UTC() }}, nil
}

// List returns the caller's personas, ordered by name.
//
// There is no page. A persona is a prompt somebody wrote, not a body of work
// that grows with use, and a selector that hid half of them would be a selector
// that lies about what the account has.
func (s *Service) List(ctx context.Context, caller authz.Caller) ([]Persona, error) {
	return s.repository.List(ctx, caller)
}

// Get returns one of the caller's personas.
func (s *Service) Get(ctx context.Context, personaID string, caller authz.Caller) (Persona, error) {
	id := strings.TrimSpace(personaID)
	if id == "" {
		return Persona{}, fmt.Errorf("%w: no persona was named", ErrNotFound)
	}
	return s.repository.Get(ctx, id, caller)
}

// Create stores a new persona for the caller.
func (s *Service) Create(ctx context.Context, input Input, caller authz.Caller) (Persona, error) {
	cleaned, err := input.Validate()
	if err != nil {
		return Persona{}, err
	}
	id, err := newID(personaIDPrefix)
	if err != nil {
		return Persona{}, fmt.Errorf("generate persona id: %w", err)
	}
	now := s.now()
	record := Persona{
		ID:          id,
		OwnerID:     caller.UserID,
		Name:        cleaned.Name,
		Description: cleaned.Description,
		Content:     cleaned.Content,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.repository.Create(ctx, record, caller); err != nil {
		return Persona{}, err
	}
	return record, nil
}

// Update replaces a persona's name, description and instruction.
//
// The whole of it is replaced rather than patched: a persona is a small object
// somebody edits in a form, and "leave this field alone" would be a third state
// every field had to carry for no benefit.
func (s *Service) Update(ctx context.Context, personaID string, input Input, caller authz.Caller) (Persona, error) {
	id := strings.TrimSpace(personaID)
	if id == "" {
		return Persona{}, fmt.Errorf("%w: no persona was named", ErrNotFound)
	}
	current, err := s.repository.Get(ctx, id, caller)
	if err != nil {
		return Persona{}, err
	}
	cleaned, err := input.Validate()
	if err != nil {
		return Persona{}, err
	}
	current.Name = cleaned.Name
	current.Description = cleaned.Description
	current.Content = cleaned.Content
	current.UpdatedAt = s.now()
	if err := s.repository.Update(ctx, current, caller); err != nil {
		return Persona{}, err
	}
	return current, nil
}

// Delete removes one of the caller's personas.
func (s *Service) Delete(ctx context.Context, personaID string, caller authz.Caller) error {
	id := strings.TrimSpace(personaID)
	if id == "" {
		return fmt.Errorf("%w: no persona was named", ErrNotFound)
	}
	return s.repository.Delete(ctx, id, caller)
}

// Instruction resolves a persona id into the prompt a turn should carry.
//
// found is false when the id names no persona the caller can see, and the error
// is reserved for storage that could not answer. The distinction matters to the
// runtime: the first is a request it refuses before the turn exists, and the
// second is a turn it must not start for a reason it cannot fix.
func (s *Service) Instruction(ctx context.Context, personaID string, caller authz.Caller) (string, bool, error) {
	id := strings.TrimSpace(personaID)
	if id == "" {
		return "", false, nil
	}
	record, err := s.repository.Get(ctx, id, caller)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	return record.Content, true, nil
}

func newID(prefix string) (string, error) {
	random := make([]byte, idRandomBytes)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(random), nil
}
