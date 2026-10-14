package reading

// What the reading service decides before storage is asked anything: which
// coordinates it will resolve, whether a position write is a create or a move,
// and what a mark is allowed to be.
//
// The two doubles below are a record of the calls rather than evidence about
// storage — the real database is that, in the tests beside this file. What they
// are here for is the negative: a service that refuses a request before reaching
// storage can only be shown to have done so by a repository that would have
// noticed, and a reading of the client's own text can only be shown not to happen
// by a port that writes down which window it was asked for.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"backend/internal/authz"
)

// testCaller is the account the service tests act as. What these tests are about
// is what the service decides before storage is asked anything, and the account is
// the same one throughout; the two-account cases — a material one account must not
// see — live beside the real database, where the filter is actually applied.
var testCaller = authz.Caller{UserID: 1, Role: authz.RoleUser}

// fakeRepository is the catalogue, the positions and the marks, in memory.
//
// It answers the contract rather than the database: an ErrDuplicate here is what
// the primary key does, and it is reproduced by hand because the service's
// translation of it into a conflict is a decision of the service's, not of
// storage's.
type fakeRepository struct {
	materials []Material
	positions []Position
	// marks is in the order they were written, which is the order storage
	// promises to list them in.
	marks []Mark
	// pairings is every confirmed pairing, which is only ever written by a
	// confirmation: a recommendation computes its answer and stores none of it.
	pairings []Pairing

	// Errors a test asks for, so that the failure paths a service has are
	// reachable without a database that is unwell.
	materialErr      error
	createErr        error
	positionErr      error
	saveErr          error
	markErr          error
	deleteMarkErr    error
	pairingErr       error
	createPairingErr error
	deletePairingErr error

	// created and moved record what reached storage, which is how a create and a
	// move of the same position are told apart.
	created []Position
	moved   []Position
	// deletes records the marks that were taken off.
	deletes []string
	// confirmed and cancelled record the two pairing writes, which is how a
	// confirmation that was answered from the row that was there is told from one
	// that wrote a row.
	confirmed []Pairing
	cancelled []string
	// reads counts the material reads, which is how a refusal that never reached
	// storage is told from one that did.
	reads int
}

var _ Repository = (*fakeRepository)(nil)

func newFakeRepository() *fakeRepository { return &fakeRepository{} }

// withMaterial puts one material in the catalogue.
func (f *fakeRepository) withMaterial(material Material) *fakeRepository {
	f.materials = append(f.materials, material)
	return f
}

// withPosition puts one saved position in place.
func (f *fakeRepository) withPosition(position Position) *fakeRepository {
	f.positions = append(f.positions, position)
	return f
}

func (f *fakeRepository) findPosition(materialID, sessionID string) int {
	for index, position := range f.positions {
		if position.MaterialID == materialID && position.SessionID == sessionID {
			return index
		}
	}
	return -1
}

func (f *fakeRepository) Materials(_ context.Context, _ string, _ authz.Caller) ([]Material, error) {
	return f.materials, f.materialErr
}

func (f *fakeRepository) Material(_ context.Context, materialID string, _ authz.Caller) (Material, error) {
	f.reads++
	if f.materialErr != nil {
		return Material{}, f.materialErr
	}
	for _, material := range f.materials {
		if material.MaterialID == materialID {
			return material, nil
		}
	}
	return Material{}, fmt.Errorf("%w: material %s", ErrNotFound, materialID)
}

func (f *fakeRepository) MaterialOfContent(_ context.Context, contentID string, _ authz.Caller) (Material, error) {
	if f.materialErr != nil {
		return Material{}, f.materialErr
	}
	for _, material := range f.materials {
		if material.ContentID == contentID {
			return material, nil
		}
	}
	return Material{}, fmt.Errorf("%w: no material reads document %s", ErrNotFound, contentID)
}

func (f *fakeRepository) CreateMaterial(_ context.Context, material Material) error {
	if f.createErr != nil {
		return f.createErr
	}
	for _, existing := range f.materials {
		if existing.MaterialID == material.MaterialID {
			return fmt.Errorf("%w: material %s", ErrDuplicate, material.MaterialID)
		}
	}
	f.withMaterial(material)
	return nil
}

func (f *fakeRepository) Position(_ context.Context, materialID, sessionID string, _ authz.Caller) (Position, error) {
	if index := f.findPosition(materialID, sessionID); index >= 0 {
		return f.positions[index], nil
	}
	return Position{}, fmt.Errorf("%w: no position for material %s in conversation %s",
		ErrNotFound, materialID, sessionID)
}

func (f *fakeRepository) CreatePosition(_ context.Context, position Position, _ authz.Caller) error {
	if f.positionErr != nil {
		return f.positionErr
	}
	// The primary key is (material_id, session_id): a second first-write for one
	// pair is a collision and not a move.
	if f.findPosition(position.MaterialID, position.SessionID) >= 0 {
		return fmt.Errorf("%w: a position for material %s in conversation %s",
			ErrDuplicate, position.MaterialID, position.SessionID)
	}
	f.withPosition(position)
	f.created = append(f.created, position)
	return nil
}

func (f *fakeRepository) SavePosition(_ context.Context, materialID, sessionID string,
	expected int64, locator Locator, now time.Time, _ authz.Caller) (bool, error) {
	if f.saveErr != nil {
		return false, f.saveErr
	}
	index := f.findPosition(materialID, sessionID)
	if index < 0 || f.positions[index].Revision != expected {
		return false, nil
	}
	position := f.positions[index]
	position.Locator, position.Revision, position.UpdatedAt = locator, expected+1, now
	f.positions[index] = position
	f.moved = append(f.moved, position)
	return true, nil
}

func (f *fakeRepository) Marks(_ context.Context, materialID string, _ authz.Caller) ([]Mark, error) {
	if f.markErr != nil {
		return nil, f.markErr
	}
	found := make([]Mark, 0, len(f.marks))
	for _, mark := range f.marks {
		if mark.MaterialID == materialID {
			found = append(found, mark)
		}
	}
	return found, nil
}

func (f *fakeRepository) Mark(_ context.Context, markID string, _ authz.Caller) (Mark, error) {
	for _, mark := range f.marks {
		if mark.MarkID == markID {
			return mark, nil
		}
	}
	return Mark{}, fmt.Errorf("%w: mark %s", ErrNotFound, markID)
}

func (f *fakeRepository) CreateMark(_ context.Context, mark Mark, _ authz.Caller) error {
	if f.markErr != nil {
		return f.markErr
	}
	f.marks = append(f.marks, mark)
	return nil
}

func (f *fakeRepository) DeleteMark(_ context.Context, markID string, _ authz.Caller) error {
	if f.deleteMarkErr != nil {
		return f.deleteMarkErr
	}
	for index, mark := range f.marks {
		if mark.MarkID == markID {
			f.marks = append(f.marks[:index], f.marks[index+1:]...)
			f.deletes = append(f.deletes, markID)
			return nil
		}
	}
	return fmt.Errorf("%w: mark %s", ErrNotFound, markID)
}

// The pairings, in memory. A confirm and a cancel are the only two writes there
// are, and both are written down: a test that says a recommendation stored
// nothing needs somewhere that would have noticed a row appearing.
func (f *fakeRepository) PairingOf(_ context.Context, materialID string, _ authz.Caller) (Pairing, error) {
	if f.pairingErr != nil {
		return Pairing{}, f.pairingErr
	}
	for _, pairing := range f.pairings {
		if pairing.PrimaryMaterialID == materialID || pairing.CounterpartMaterialID == materialID {
			return pairing, nil
		}
	}
	return Pairing{}, fmt.Errorf("%w: material %s is in no pairing", ErrNotFound, materialID)
}

func (f *fakeRepository) PairingBetween(_ context.Context, first, second string, _ authz.Caller) (Pairing, error) {
	if f.pairingErr != nil {
		return Pairing{}, f.pairingErr
	}
	for _, pairing := range f.pairings {
		one := pairing.PrimaryMaterialID == first && pairing.CounterpartMaterialID == second
		other := pairing.PrimaryMaterialID == second && pairing.CounterpartMaterialID == first
		if one || other {
			return pairing, nil
		}
	}
	return Pairing{}, fmt.Errorf("%w: materials %s and %s are not paired", ErrNotFound, first, second)
}

func (f *fakeRepository) CreatePairing(_ context.Context, pairing Pairing, _ authz.Caller) error {
	if f.createPairingErr != nil {
		return f.createPairingErr
	}
	f.pairings = append(f.pairings, pairing)
	f.confirmed = append(f.confirmed, pairing)
	return nil
}

func (f *fakeRepository) DeletePairing(_ context.Context, pairingID string, _ authz.Caller) error {
	if f.deletePairingErr != nil {
		return f.deletePairingErr
	}
	for index, pairing := range f.pairings {
		if pairing.PairingID == pairingID {
			f.pairings = append(f.pairings[:index], f.pairings[index+1:]...)
			f.cancelled = append(f.cancelled, pairingID)
			return nil
		}
	}
	return fmt.Errorf("%w: pairing %s", ErrNotFound, pairingID)
}

// withPairing puts a confirmed pairing in place.
func (f *fakeRepository) withPairing(pairing Pairing) *fakeRepository {
	f.pairings = append(f.pairings, pairing)
	return f
}

// fakeDocuments is the knowledge base, as the reading layer reaches it.
//
// Every read of a window is written down: a test that says "the passage is what
// the coordinate names" is only saying something if it can also see which window
// was read, and a test that says the client's own text was ignored needs a port
// that would have been asked for the other text had it not been.
type fakeDocuments struct {
	documents map[string]Document
	spans     map[string][]ChunkSpan
	texts     map[string]string

	documentErr error
	spansErr    error
	textErr     error

	// reads is every window that was read, as "document:start:end".
	reads []string
}

var _ Documents = (*fakeDocuments)(nil)

func newFakeDocuments() *fakeDocuments {
	return &fakeDocuments{
		documents: map[string]Document{},
		spans:     map[string][]ChunkSpan{},
		texts:     map[string]string{},
	}
}

// withDocument puts one parsed document in place, with the text every window of
// it is cut out of.
func (f *fakeDocuments) withDocument(document Document, text string, spans ...ChunkSpan) *fakeDocuments {
	f.documents[document.ID] = document
	f.texts[document.ID] = text
	if spans != nil {
		f.spans[document.ID] = spans
	}
	return f
}

func (f *fakeDocuments) DocumentOfFile(_ context.Context, fileID string, _ authz.Caller) (Document, error) {
	if f.documentErr != nil {
		return Document{}, f.documentErr
	}
	for _, document := range f.documents {
		if document.FileID == fileID {
			return document, nil
		}
	}
	return Document{}, fmt.Errorf("%w: no document was parsed from file %s", ErrNotFound, fileID)
}

func (f *fakeDocuments) Document(_ context.Context, documentID string, _ authz.Caller) (Document, error) {
	if f.documentErr != nil {
		return Document{}, f.documentErr
	}
	document, ok := f.documents[documentID]
	if !ok {
		return Document{}, fmt.Errorf("%w: document %s", ErrNotFound, documentID)
	}
	return document, nil
}

func (f *fakeDocuments) ChunkSpans(_ context.Context, documentID string, _ authz.Caller) ([]ChunkSpan, error) {
	if f.spansErr != nil {
		return nil, f.spansErr
	}
	return f.spans[documentID], nil
}

// Text cuts the window out of the document's text by characters rather than
// bytes, the same unit the real read takes.
func (f *fakeDocuments) Text(_ context.Context, documentID string, charStart, charEnd int64, _ authz.Caller) (string, error) {
	if f.textErr != nil {
		return "", f.textErr
	}
	f.reads = append(f.reads, strings.Join([]string{
		documentID, strconv.FormatInt(charStart, 10), strconv.FormatInt(charEnd, 10),
	}, ":"))
	runes := []rune(f.texts[documentID])
	if charStart < 0 || charEnd > int64(len(runes)) {
		return "", fmt.Errorf("the fake was asked for characters the document does not have")
	}
	return string(runes[charStart:charEnd]), nil
}

// newFakeService builds the service over both doubles.
func newFakeService(t *testing.T, unitMaxChars int) (*Service, *fakeRepository, *fakeDocuments) {
	t.Helper()
	repository, documents := newFakeRepository(), newFakeDocuments()
	service, err := NewService(repository, documents, Options{UnitMaxChars: unitMaxChars})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service, repository, documents
}

// sampleMaterial is one material over one document.
func sampleMaterial(materialID, documentID string) Material {
	return Material{
		MaterialID: materialID, ContentID: documentID, Filename: "算法笔记.md",
		SourceType: "library", CreatedAt: sampleTime,
	}
}

// sampleTime is a fixed instant, so that a test asserting on a recorded write is
// not comparing against a clock.
var sampleTime = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
