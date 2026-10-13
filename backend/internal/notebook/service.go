package notebook

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

const (
	entryIDPrefix = "nbe_"
	idRandomBytes = 16
)

// Service owns the rules about the question bank: which questions exist, what
// is written when one is saved again, and what a filter may ask for.
type Service struct {
	repository Repository
}

// NewService returns the service over a storage adapter.
func NewService(repository Repository) (*Service, error) {
	if repository == nil {
		return nil, fmt.Errorf("notebook service needs a repository")
	}
	return &Service{repository: repository}, nil
}

// Save writes a question into the bank and reports whether it was new.
//
// A question that is already there is rewritten, not duplicated: the identity
// is the content address of the text and type, so saving the same question
// twice is one row and one question. What a rewrite covers is the question face
// and nothing else — the answer face, the conversation this question was first
// asked in, and the categories it is filed under are not this call's to change.
// The tool uses Created to tell the model which happened, which is why it is
// reported rather than worked out from the row afterwards.
func (s *Service) Save(ctx context.Context, question Question) (Upserted, error) {
	if err := question.Validate(); err != nil {
		return Upserted{}, err
	}
	entryID, err := newID(entryIDPrefix)
	if err != nil {
		return Upserted{}, fmt.Errorf("generate question row id: %w", err)
	}
	// The address is computed here rather than taken from the caller: it is the
	// rule that makes "the same question" mean something, and a caller that
	// computed it differently would be saving to a different question.
	question.QuestionID = QuestionID(question.Text, question.Type)

	result, err := s.repository.UpsertQuestion(ctx, entryID, question, time.Now().UTC())
	if err != nil {
		return Upserted{}, err
	}
	// Filing happens after the upsert and outside its statement pair, so a
	// question is never in the bank without the categories it was filed under.
	// The two are not one transaction because a category is not part of a
	// question's identity: failing to file it is worth an error, but it is not
	// worth throwing the question away over.
	if err := s.repository.AddCategories(ctx, result.ID, question.Categories); err != nil {
		return Upserted{}, err
	}
	return result, nil
}

// Get returns one question by its content address.
func (s *Service) Get(ctx context.Context, questionID string) (Entry, error) {
	return s.repository.Entry(ctx, questionID)
}

// List returns one page of the bank.
func (s *Service) List(ctx context.Context, filter Filter) ([]Entry, error) {
	if err := filter.Validate(); err != nil {
		return nil, err
	}
	return s.repository.List(ctx, filter)
}

// Mark applies the marks that are set on a question directly. It is idempotent,
// so asking twice for the state that is already stored succeeds.
func (s *Service) Mark(ctx context.Context, questionID string, change Change) error {
	if change.Empty() {
		return fmt.Errorf("%w: no marks were named", ErrEmptyChange)
	}
	return s.repository.Mark(ctx, questionID, change)
}

// SetCategories replaces the labels one question is filed under. An empty list
// is a request to unfile it, and it is answered rather than refused: the labels
// a question carries are the user's to set, including to none.
func (s *Service) SetCategories(ctx context.Context, questionID string, names []string) error {
	return s.repository.SetCategories(ctx, questionID, names)
}

// Categories lists the labels the bank is filed under, most used first.
func (s *Service) Categories(ctx context.Context) ([]Category, error) {
	return s.repository.Categories(ctx)
}

// newID returns a readable row id with the given prefix.
//
// The row key is random while the question's address is derived, and the two are
// different jobs: the address has to be stable across writers, and the row key
// only has to be unique. Deriving the row key too would make the insert of a
// question that is already there collide on the primary key as well as on the
// question id, and then there would be no way to tell the two collisions apart.
func newID(prefix string) (string, error) {
	value := make([]byte, idRandomBytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(value), nil
}
