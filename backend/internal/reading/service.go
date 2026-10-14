package reading

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
	materialIDPrefix = "rm_"
	markIDPrefix     = "rmk_"
	idRandomBytes    = 16
)

// Service owns the rules about the reading layer: which materials the catalogue
// holds, what makes a position write a conflict, and what a mark may be.
type Service struct {
	repository Repository
	// documents is the knowledge base, through the port this package declares.
	// A catalogue is made of coordinates and asks it for coordinates; the one
	// thing that reads words through it is a locator being resolved.
	documents Documents
	// maxPassageChars is the largest a unit of text may be, and one bound serves
	// both jobs: what the outline cuts the text into and what one resolved
	// passage may hold. They are the same size of thing on purpose — the passage
	// a model is handed is a unit a reader could have been looking at.
	maxPassageChars int
}

// Options tune the reading layer.
type Options struct {
	// UnitMaxChars is the largest a unit of a material's text may be (§5).
	UnitMaxChars int
}

// NewService returns the service over a storage adapter and the knowledge base.
func NewService(repository Repository, documents Documents, options Options) (*Service, error) {
	if repository == nil {
		return nil, fmt.Errorf("reading service needs a repository")
	}
	if documents == nil {
		return nil, fmt.Errorf("reading service needs the knowledge base")
	}
	if options.UnitMaxChars <= 0 {
		return nil, fmt.Errorf("reading service needs a positive unit bound")
	}
	return &Service{
		repository:      repository,
		documents:       documents,
		maxPassageChars: options.UnitMaxChars,
	}, nil
}

// Materials lists the catalogue, most recently touched first.
//
// The workspace is optional and it is what decides whether each material comes
// back with the tab state of that workspace or with none. A read outside a
// workspace is not a different kind of read: it is the same catalogue seen by
// somebody who has no tabs open.
func (s *Service) Materials(ctx context.Context, workspaceID string, caller authz.Caller) ([]Material, error) {
	return s.repository.Materials(ctx, strings.TrimSpace(workspaceID), caller)
}

// Material reads one row of the catalogue.
//
// It is the read behind the routes that are about a material without being about
// its text — the extension catalogue is the one that asks — and it exists so
// those can answer "no such material" rather than describing a book that is not
// there.
func (s *Service) Material(ctx context.Context, materialID string, caller authz.Caller) (Material, error) {
	return s.repository.Material(ctx, strings.TrimSpace(materialID), caller)
}

// Outline returns one material and what it is made of.
//
// A material whose row names no document comes back with the material and no
// units rather than as an error. The row exists and the catalogue should list
// it; what it lacks is a text to be a coordinate into, and saying so with an
// empty outline is truer than refusing to describe a row that is there.
func (s *Service) Outline(ctx context.Context, materialID string, caller authz.Caller) (Outline, error) {
	material, err := s.repository.Material(ctx, strings.TrimSpace(materialID), caller)
	if err != nil {
		return Outline{}, err
	}
	if material.ContentID == "" {
		return Outline{Material: material, Units: []Unit{}}, nil
	}
	document, err := s.documents.Document(ctx, material.ContentID, caller)
	if err != nil {
		return Outline{}, err
	}
	spans, err := s.documents.ChunkSpans(ctx, material.ContentID, caller)
	if err != nil {
		return Outline{}, err
	}
	return Outline{
		Material:  material,
		Units:     unitsOf(spans, s.maxPassageChars),
		TextChars: document.TextChars,
	}, nil
}

// Create builds a material from a file that has already been uploaded and
// parsed.
//
// A document that already has a material gets that material back rather than a
// second one, because what this answers is "open this file in the reader" and a
// reader who clicks it twice should not end up with two tabs. The lookup is not
// a lock — content_id carries no unique index (migration 00012) — so this is
// idempotent in the ordinary case and not under a race; that limit is recorded
// in the design's deviation list rather than papered over here.
func (s *Service) Create(ctx context.Context, fileID, sourceType string, caller authz.Caller) (Material, error) {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return Material{}, fmt.Errorf("%w: no file was named", ErrInvalidRequest)
	}
	document, err := s.documents.DocumentOfFile(ctx, fileID, caller)
	if err != nil {
		return Material{}, err
	}
	existing, err := s.repository.MaterialOfContent(ctx, document.ID, caller)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Material{}, err
	}
	materialID, err := newID(materialIDPrefix)
	if err != nil {
		return Material{}, fmt.Errorf("generate material id: %w", err)
	}
	now := time.Now().UTC()
	material := Material{
		MaterialID: materialID,
		ContentID:  document.ID,
		Filename:   document.Filename,
		SourceType: strings.TrimSpace(sourceType),
		// The owner is the caller's account, and it travels in the record because
		// this is the moment a material comes into being: there is no existing row
		// to narrow against, so what the repository needs is the owner value, not a
		// scope. Zero here would mean "belongs to nobody", which is the state the
		// reader is leaving, not the one it is entering.
		OwnerID:   caller.UserID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.repository.CreateMaterial(ctx, material); err != nil {
		return Material{}, err
	}
	return material, nil
}

// Position reads where a reader stopped. The second return value is false when
// nothing has been saved, which is an answer and not a missing resource.
func (s *Service) Position(ctx context.Context, materialID, sessionID string, caller authz.Caller) (Position, bool, error) {
	if _, err := s.repository.Material(ctx, strings.TrimSpace(materialID), caller); err != nil {
		return Position{}, false, err
	}
	position, err := s.repository.Position(ctx, strings.TrimSpace(materialID), strings.TrimSpace(sessionID), caller)
	if errors.Is(err, ErrNotFound) {
		return Position{}, false, nil
	}
	if err != nil {
		return Position{}, false, err
	}
	return position, true, nil
}

// SavePosition writes where a reader stopped, under the revision guard (§2.11).
//
// A revision of zero is a request to create the position and anything else is a
// promise about what the row held. Neither is a last-writer-wins write: a client
// that read a revision somebody has since moved gets ErrStalePosition and reads
// the row again, which is the third appearance of the same shape in this
// version — the review state, the path state, and this.
func (s *Service) SavePosition(ctx context.Context, request SavePositionRequest, caller authz.Caller) (Position, error) {
	if err := request.Validate(); err != nil {
		return Position{}, err
	}
	if _, err := s.repository.Material(ctx, request.MaterialID, caller); err != nil {
		return Position{}, err
	}
	now := time.Now().UTC()
	if request.Revision == 0 {
		position := Position{
			MaterialID: request.MaterialID,
			SessionID:  request.SessionID,
			Locator:    request.Locator,
			Revision:   1,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		err := s.repository.CreatePosition(ctx, position, caller)
		if errors.Is(err, ErrDuplicate) {
			return Position{}, fmt.Errorf("%w: another tab saved a position first", ErrStalePosition)
		}
		if err != nil {
			return Position{}, err
		}
		return position, nil
	}
	moved, err := s.repository.SavePosition(ctx, request.MaterialID, request.SessionID, request.Revision, request.Locator, now, caller)
	if err != nil {
		return Position{}, err
	}
	if !moved {
		// Nothing moved, and there are two reasons that look alike from here: the
		// row was removed, or it has moved on. Reading it tells the two apart, and
		// the caller is told which — "your position is gone" and "your position has
		// moved" invite different next steps.
		if _, err := s.repository.Position(ctx, request.MaterialID, request.SessionID, caller); errors.Is(err, ErrNotFound) {
			return Position{}, fmt.Errorf("%w: the position is gone", ErrStalePosition)
		}
		return Position{}, fmt.Errorf("%w: revision %d is no longer current", ErrStalePosition, request.Revision)
	}
	return s.repository.Position(ctx, request.MaterialID, request.SessionID, caller)
}

// Marks lists the marks on one material.
func (s *Service) Marks(ctx context.Context, materialID string, caller authz.Caller) ([]Mark, error) {
	if _, err := s.repository.Material(ctx, strings.TrimSpace(materialID), caller); err != nil {
		return nil, err
	}
	return s.repository.Marks(ctx, strings.TrimSpace(materialID), caller)
}

// AddMark writes one bookmark, highlight or note.
//
// The mark is written at revision 1 and never revised: a mark is a thing the
// reader put on the text, and editing it is a later version's business. What the
// revision column is for is the same thing it is for everywhere else — being
// able to say whether the row moved — and it starts at the first version.
func (s *Service) AddMark(ctx context.Context, request MarkRequest, caller authz.Caller) (Mark, error) {
	if err := request.Validate(); err != nil {
		return Mark{}, err
	}
	if _, err := s.repository.Material(ctx, request.MaterialID, caller); err != nil {
		return Mark{}, err
	}
	markID, err := newID(markIDPrefix)
	if err != nil {
		return Mark{}, fmt.Errorf("generate mark id: %w", err)
	}
	now := time.Now().UTC()
	mark := Mark{
		MarkID:     markID,
		MaterialID: request.MaterialID,
		SessionID:  request.SessionID,
		Kind:       request.Kind,
		Locator:    request.Locator,
		Body:       strings.TrimSpace(request.Body),
		Revision:   1,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := s.repository.CreateMark(ctx, mark, caller); err != nil {
		return Mark{}, err
	}
	return mark, nil
}

// RemoveMark takes one mark off the material.
func (s *Service) RemoveMark(ctx context.Context, markID string, caller authz.Caller) error {
	return s.repository.DeleteMark(ctx, strings.TrimSpace(markID), caller)
}

// newID returns a readable row id with the given prefix.
//
// The row key is random while a question's address is derived, and the two are
// different jobs: an address has to be stable across writers, and a row key only
// has to be unique.
func newID(prefix string) (string, error) {
	value := make([]byte, idRandomBytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(value), nil
}
