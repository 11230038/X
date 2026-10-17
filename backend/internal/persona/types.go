// Package persona is a saved prompt an account can run a turn under.
//
// A persona is content, not authority. Nothing in this package is consulted by
// the authorization layer, and the turn runtime uses the instruction it returns
// only as a system message. The consequence worth stating plainly: naming a
// persona cannot widen what a turn may read, write or call.
package persona

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"backend/internal/authz"
)

// The bounds a persona is written within. They are the column widths in
// migration 00020, restated here where a caller can be told which one it broke.
const (
	MaxNameRunes        = 64
	MaxDescriptionRunes = 500
	MaxContentRunes     = 4000
)

// The failures a caller can act on.
var (
	// ErrNotFound reports a persona that is not stored, or that belongs to
	// somebody else. The two are one answer on purpose: an account is not
	// entitled to learn which ids other accounts hold.
	ErrNotFound = errors.New("persona: not found")
	// ErrNameRequired and ErrNameTooLong are the name's two rules.
	ErrNameRequired = errors.New("persona: the name is empty")
	ErrNameTooLong  = errors.New("persona: the name is too long")
	// ErrDescriptionTooLong is the one rule the description has; an empty one is
	// allowed.
	ErrDescriptionTooLong = errors.New("persona: the description is too long")
	// ErrContentRequired and ErrContentTooLong are the instruction's two rules.
	// An empty instruction would be a persona that changes nothing, which is
	// indistinguishable from selecting none.
	ErrContentRequired = errors.New("persona: the instruction is empty")
	ErrContentTooLong  = errors.New("persona: the instruction is too long")
	// ErrNameTaken reports a second persona with a name the account already uses.
	// Names are what the composer lists, so they have to tell rows apart.
	ErrNameTaken = errors.New("persona: this account already has a persona with that name")
)

// Persona is one saved prompt.
type Persona struct {
	ID          string
	OwnerID     int64
	Name        string
	Description string
	Content     string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Input is what a create or an update carries.
type Input struct {
	Name        string
	Description string
	Content     string
}

// Validate trims and checks one input, returning the cleaned copy to store.
//
// Trimming happens before every check so that a name of spaces is refused as
// empty rather than stored as a name nobody can see or select.
func (i Input) Validate() (Input, error) {
	i.Name = strings.TrimSpace(i.Name)
	i.Description = strings.TrimSpace(i.Description)
	i.Content = strings.TrimSpace(i.Content)

	switch {
	case i.Name == "":
		return Input{}, fmt.Errorf("%w: a persona needs a name", ErrNameRequired)
	case utf8.RuneCountInString(i.Name) > MaxNameRunes:
		return Input{}, fmt.Errorf("%w: the name must be at most %d characters", ErrNameTooLong, MaxNameRunes)
	case utf8.RuneCountInString(i.Description) > MaxDescriptionRunes:
		return Input{}, fmt.Errorf("%w: the description must be at most %d characters", ErrDescriptionTooLong, MaxDescriptionRunes)
	case i.Content == "":
		return Input{}, fmt.Errorf("%w: a persona needs an instruction", ErrContentRequired)
	case utf8.RuneCountInString(i.Content) > MaxContentRunes:
		return Input{}, fmt.Errorf("%w: the instruction must be at most %d characters", ErrContentTooLong, MaxContentRunes)
	}
	return i, nil
}

// Repository is the storage adapter the service is written against.
type Repository interface {
	List(ctx context.Context, caller authz.Caller) ([]Persona, error)
	Get(ctx context.Context, personaID string, caller authz.Caller) (Persona, error)
	Create(ctx context.Context, record Persona, caller authz.Caller) error
	Update(ctx context.Context, record Persona, caller authz.Caller) error
	Delete(ctx context.Context, personaID string, caller authz.Caller) error
}
