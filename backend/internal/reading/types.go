// Package reading owns the immersion layer: which materials a reader has, where
// they stopped in one, and what they wrote in the margin.
//
// A material is not a copy of a document. It is a row pointing at one
// (reading_materials.content_id), and everything that reads the text — the
// parser, the chunker, the vectors, the citation path — is V2's, unchanged
// (§2.8). What this package adds is the part V2 has no opinion about.
//
// Three things are rows here and one is not, and the split is the design:
//
//   - a position is stored per (material, conversation), so two tabs reading
//     different books cannot interfere while two tabs reading the same one share
//     a place (§2.11);
//   - a mark belongs to the material, not to the conversation it was written in,
//     so deleting a conversation leaves what the reader wrote standing
//     (migration 00012);
//   - a confirmed pairing is a row, while the candidates an interface offers are
//     a query that stores nothing (§2.10);
//   - a selection is not stored at all. What the reader has selected travels
//     with the turn that asks about it, because a saved selection would show up
//     in the second tab as somebody else's.
package reading

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"backend/internal/authz"
)

var (
	// ErrNotFound reports a material, position or mark that is not there.
	ErrNotFound = errors.New("reading: not found")
	// ErrInvalidRequest reports a request this version cannot act on: a locator
	// that names no span, a mark of a kind that does not exist, a note with no
	// text. It is about what storage would refuse or what the reader's client got
	// wrong, not about what a model should phrase differently.
	ErrInvalidRequest = errors.New("reading: invalid request")
	// ErrStalePosition reports a position write against a revision that is no
	// longer current. It is a conflict and not a failure: somebody else saved a
	// position first, and the caller's answer is to read the row and decide again
	// (§2.11).
	ErrStalePosition = errors.New("reading: stale position")
	// ErrDuplicate reports a row that is already there — a position somebody else
	// created first, and later a pairing the reader has already confirmed.
	ErrDuplicate = errors.New("reading: already there")
)

// Locator is a coordinate into one material's text, and it is V2's citation
// rescaled rather than a second scheme (§2.8): Ordinal, CharStart and CharEnd are
// the columns document_chunks already stores, and Page rides along when the
// parse knew one.
//
// The material is part of the coordinate and not a field beside it. A span that
// does not say which text it spans cannot be resolved, and a client holding one
// has no way to ask for the wrong text.
//
// A locator of zero width is legal and is what a saved position is: where the
// reader stopped is a place, not a selection, and insisting on a non-empty span
// would make "I am at the top of the document" unsayable.
type Locator struct {
	MaterialID string `json:"material_id"`
	Ordinal    int    `json:"ordinal"`
	CharStart  int64  `json:"char_start"`
	CharEnd    int64  `json:"char_end"`
	Page       *int   `json:"page,omitempty"`
}

// Validate checks that a locator names a span of one material's text.
func (l Locator) Validate() error {
	if strings.TrimSpace(l.MaterialID) == "" {
		return fmt.Errorf("%w: the locator names no material", ErrInvalidRequest)
	}
	if l.Ordinal < 0 {
		return fmt.Errorf("%w: the locator ordinal is negative", ErrInvalidRequest)
	}
	if l.CharStart < 0 {
		return fmt.Errorf("%w: the locator starts before the text does", ErrInvalidRequest)
	}
	if l.CharEnd < l.CharStart {
		return fmt.Errorf("%w: the locator ends before it starts", ErrInvalidRequest)
	}
	if l.Page != nil && *l.Page < 1 {
		return fmt.Errorf("%w: the locator names a page before the first one", ErrInvalidRequest)
	}
	return nil
}

// Documents is the part of the knowledge base the reading layer reads.
//
// It is a port rather than an import (§1.2): the knowledge base is a peer
// domain, not a lower layer, and what this package needs from it is where a
// document's text begins and ends, and — for one span at a time — the text
// itself.
//
// The read is deliberately a window and not a document. A catalogue lists units
// by their coordinates, and the one place the words are needed is a locator
// being resolved; asking for the whole text and cutting it here would make this
// layer a second reader of the document, and asking for a span keeps the
// extractor, the offsets and the unit of "a character" V2's (§2.8).
//
// Every read carries the caller, because a document belongs to nobody of its own
// (migration 00014 gave it no owner column): its ownership is the collection or
// the file it came from, and the knowledge base is the one that can walk that
// chain. A material whose document is another account's must not render its
// text, and the read that would have rendered it is the place to say so — this
// layer has no other view of who a document belongs to.
type Documents interface {
	// DocumentOfFile resolves an uploaded file to the document it was parsed
	// into. A file no parse has reached yet, or one belonging to another account,
	// reports ErrNotFound.
	DocumentOfFile(ctx context.Context, fileID string, caller authz.Caller) (Document, error)
	// Document reads one document's metadata by its own id.
	Document(ctx context.Context, documentID string, caller authz.Caller) (Document, error)
	// ChunkSpans lists the coordinates of a document's chunks, in reading order.
	ChunkSpans(ctx context.Context, documentID string, caller authz.Caller) ([]ChunkSpan, error)
	// Text reads the characters of one document in [charStart, charEnd). The
	// span is the caller's to have checked against the document's length, which
	// is what lets storage clamp and this layer refuse.
	Text(ctx context.Context, documentID string, charStart, charEnd int64, caller authz.Caller) (string, error)
}

// Document is one parsed file, as the reading layer needs it.
type Document struct {
	ID       string
	FileID   string
	Filename string
	// TextChars is the length of the parsed text, so a catalogue can say how long
	// a material is without reading a character of it.
	TextChars int64
	// Revision is which parse of the file this text came from. It rides along
	// because a passage is handed to a turn as a citation, and a citation that
	// cannot say which revision it was read at cannot tell a reader whether the
	// document has moved underneath it since.
	Revision int
}

// ChunkSpan is where one chunk of a document's text begins and ends. It carries
// no content: the text of a span is read through Text, one span at a time, and
// nothing here has a use for a chunk's own number — what this layer numbers is
// its units.
type ChunkSpan struct {
	CharStart int64
	CharEnd   int64
	Page      *int
}

// Material is one row of the catalogue: a document, plus what the reading layer
// knows about reading it.
type Material struct {
	MaterialID string
	// ContentID is the document this material reads. It is a document id and not
	// a foreign key, because a material id points into whichever store its source
	// names — the same reason notebook_entries.material_id has none (§2.8).
	ContentID  string
	Filename   string
	SourceType string
	// OwnerID is the account the material was built for, or zero for a row
	// written before V5 (migration 00014 left every earlier row NULL). Zero is a
	// state rather than a missing value, exactly as it is on a session: such a
	// material belongs to nobody, is visible to the administrator and to nobody
	// else, and no read here ever treats it as the caller's.
	OwnerID   int64
	CreatedAt time.Time
	UpdatedAt time.Time
	// Pinned and Opened are the workspace tab's state about this material, and
	// they are nil when the catalogue is read outside a workspace. Nil says
	// nobody has said, which is not the same as false saying nobody has.
	Pinned *bool
	Opened *bool
}

// Outline is what a material is made of: the units of its text, in reading
// order, and how long the whole text is.
type Outline struct {
	Material  Material
	Units     []Unit
	TextChars int64
}

// Unit is one piece of a material's text as a catalogue lists it, and it is a
// run of the document's chunks rather than one chunk: chunks are sized for
// retrieval and units for reading (§5). The Ordinal numbers the unit — what a
// client holds and sends back — while the character span is what the text is
// read by.
type Unit struct {
	Ordinal   int   `json:"ordinal"`
	CharStart int64 `json:"char_start"`
	CharEnd   int64 `json:"char_end"`
	Page      *int  `json:"page,omitempty"`
}

// Locator turns a unit into a coordinate a client can send back.
func (u Unit) Locator(materialID string) Locator {
	return Locator{
		MaterialID: materialID,
		Ordinal:    u.Ordinal,
		CharStart:  u.CharStart,
		CharEnd:    u.CharEnd,
		Page:       u.Page,
	}
}

// Position is where one reader stopped in one material.
type Position struct {
	MaterialID string
	SessionID  string
	Locator    Locator
	// Revision is what the row held when it was read. It is the guard a write is
	// made under, and it is what makes two tabs saving a position a conflict
	// rather than a silent overwrite.
	Revision  int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// SavePositionRequest is one client's write of a position.
//
// Revision is the revision the client read, and zero means it read none: that is
// a request to create the position rather than to move one, which is why it is
// not simply "revision 0 is a conflict". Anything else is a promise about what
// the row held, and a promise the row no longer bears is refused rather than
// taken as a fresh save.
type SavePositionRequest struct {
	MaterialID string
	SessionID  string
	Locator    Locator
	Revision   int64
}

// Validate checks a position write before anything is read or written.
func (r SavePositionRequest) Validate() error {
	if strings.TrimSpace(r.MaterialID) == "" {
		return fmt.Errorf("%w: no material was named", ErrInvalidRequest)
	}
	if strings.TrimSpace(r.SessionID) == "" {
		return fmt.Errorf("%w: no conversation was named", ErrInvalidRequest)
	}
	if r.Revision < 0 {
		return fmt.Errorf("%w: the revision is negative", ErrInvalidRequest)
	}
	if err := r.Locator.Validate(); err != nil {
		return err
	}
	// The locator's material and the request's are the same field said twice, and
	// a request that says them differently has no reading: it is one position
	// write about two materials.
	if r.Locator.MaterialID != r.MaterialID {
		return fmt.Errorf("%w: the locator belongs to another material", ErrInvalidRequest)
	}
	return nil
}

// MarkKind is what a mark is. The three kinds are a closed set, and the column's
// CHECK says the same thing — a fourth kind would reach the reader as something
// it has no shape to draw.
type MarkKind string

// The three kinds of mark this version knows.
const (
	MarkBookmark  MarkKind = "bookmark"
	MarkHighlight MarkKind = "highlight"
	MarkNote      MarkKind = "note"
)

// Valid reports whether the kind is one of the three.
func (k MarkKind) Valid() bool {
	switch k {
	case MarkBookmark, MarkHighlight, MarkNote:
		return true
	default:
		return false
	}
}

// Mark is one thing a reader wrote in the margin.
type Mark struct {
	MarkID     string
	MaterialID string
	// SessionID is the conversation the mark was written in, and it is empty when
	// that conversation has been deleted. The material owns the mark: deleting a
	// conversation leaves what was written in it standing, with no session
	// recorded (migration 00012).
	SessionID string
	Kind      MarkKind
	Locator   Locator
	Body      string
	Revision  int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Bookmark is one saved place in a material, with the material it was saved in.
//
// The mark carries the coordinate and the material carries the name a
// favourites card shows; Text is the words at that coordinate, filled by the
// service when the list is drawn. It is read from the document rather than
// copied into the row: the text at a coordinate belongs to the document, and a
// bookmark is the reader's pointer at it, not a second copy of it.
type Bookmark struct {
	Mark     Mark
	Material Material
	// Text is the beginning of the saved passage, and empty when its words
	// cannot be read — a material with no document behind it, or one whose text
	// has since moved. The card then shows the paragraph the coordinate names.
	Text string
}

// MarkRequest is one mark to write.
type MarkRequest struct {
	MaterialID string
	SessionID  string
	Kind       MarkKind
	Locator    Locator
	Body       string
}

// Validate checks a mark before it is written.
func (r MarkRequest) Validate() error {
	if strings.TrimSpace(r.MaterialID) == "" {
		return fmt.Errorf("%w: no material was named", ErrInvalidRequest)
	}
	if strings.TrimSpace(r.SessionID) == "" {
		return fmt.Errorf("%w: no conversation was named", ErrInvalidRequest)
	}
	if !r.Kind.Valid() {
		return fmt.Errorf("%w: %q is not a kind of mark", ErrInvalidRequest, r.Kind)
	}
	if err := r.Locator.Validate(); err != nil {
		return err
	}
	if r.Locator.MaterialID != r.MaterialID {
		return fmt.Errorf("%w: the locator belongs to another material", ErrInvalidRequest)
	}
	// A bookmark and a highlight are places in the text and can stand without a
	// word; a note is the word. Refusing an empty one is what keeps the three
	// kinds from collapsing into "a mark with some optional text".
	if r.Kind == MarkNote && strings.TrimSpace(r.Body) == "" {
		return fmt.Errorf("%w: a note with no text", ErrInvalidRequest)
	}
	return nil
}
