// Package book owns generated books: a snapshot of a source, an outline the
// reader confirms, and one Markdown body per chapter.
//
// A book is not a reading material. A material points at a parsed document and
// is read in the reader's unit coordinates; a book is text this package wrote,
// chapter by chapter, from a source the reader chose. The two share a shelf in
// the product and nothing in the schema: the reader's progress is an ordinal,
// not a character span, and the body is Markdown rather than a parsed document.
package book

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"backend/internal/authz"
)

const (
	bookIDPrefix    = "bk_"
	chapterIDPrefix = "bc_"
	idRandomBytes   = 16

	// SourceBudgetRunes is how much of a source is snapshotted. It is a rune
	// count rather than a byte count because the material is mostly Chinese, and
	// it is well inside the default model's context once the outline and a
	// chapter's output are added.
	SourceBudgetRunes = 32_000

	// BookTitleMaxRunes and SourceTitleMaxRunes match the columns' widths.
	BookTitleMaxRunes   = 200
	SourceTitleMaxRunes = 200

	// Outline bounds. The prompt asks for 4–10 chapters; validation accepts 1–12
	// because an edited outline is the reader's, and a short one is still a book.
	MinOutlineChapters     = 1
	MaxOutlineChapters     = 12
	MaxOutlineTitleRunes   = 120
	MaxOutlineSummaryRunes = 500

	// MaxChapterRunes is a safety net around the stream, not the target: the
	// model is asked for a chapter that fits in 2048 tokens.
	MaxChapterRunes = 20_000

	// Model parameters. The HTTP write timeout and the provider timeout are both
	// two minutes, so one generation request is given 100 seconds in total and
	// the response still has room to be written.
	outlineMaxTokens    = 1536
	chapterMaxTokens    = 2048
	outlineTemperature  = 0.2
	chapterTemperature  = 0.5
	generationBudget    = 100 * time.Second
	streamByteHardLimit = 100_000
)

// SourceKind is where a book's material came from. The set is closed by the
// books.source_kind CHECK.
type SourceKind string

const (
	SourceSession       SourceKind = "session"
	SourceKnowledgeBase SourceKind = "knowledge_base"
	SourceFile          SourceKind = "file"
	SourceQuestionBank  SourceKind = "question_bank"
)

// Valid reports whether the kind is one this build reads.
func (k SourceKind) Valid() bool {
	switch k {
	case SourceSession, SourceKnowledgeBase, SourceFile, SourceQuestionBank:
		return true
	default:
		return false
	}
}

// Status is one chapter's generation state. It is the book_chapters.status
// vocabulary; there is deliberately no "generating" value, because generation
// is a request rather than a stored job.
type Status string

const (
	StatusPending Status = "pending"
	StatusReady   Status = "ready"
	StatusFailed  Status = "failed"
)

// Summary is a book as the shelf and the detail header show it. It never
// carries the source snapshot.
type Summary struct {
	BookID           string
	Title            string
	SourceKind       SourceKind
	SourceID         string
	SourceTitle      string
	SourceChars      int
	SourceTruncated  bool
	OutlineConfirmed bool
	OutlineRevision  int
	LastOrdinal      int
	ChapterCount     int
	ReadyChapters    int
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Book is the storage shape: the summary plus the owner and the snapshot.
type Book struct {
	Summary
	OwnerID    int64
	SourceText string
}

// OutlineItem is one chapter before its body is read: what the outline lists.
type OutlineItem struct {
	Ordinal   int    `json:"ordinal"`
	Title     string `json:"title"`
	Summary   string `json:"summary"`
	Status    Status `json:"status"`
	ErrorCode string `json:"error_code"`
}

// Detail is a book and its outline, in order.
type Detail struct {
	Book    Summary
	Outline []OutlineItem
}

// Chapter is one chapter with its body. Content is empty when the chapter has
// not been generated; callers must read Status before rendering it.
type Chapter struct {
	ChapterID string
	BookID    string
	Ordinal   int
	Title     string
	Summary   string
	Content   string
	Status    Status
	ErrorCode string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CreateInput is one request to start a book from a source.
type CreateInput struct {
	Title      string
	SourceKind SourceKind
	SourceID   string
}

// OutlineItemInput is one chapter the reader wrote or edited.
type OutlineItemInput struct {
	Title   string
	Summary string
}

// OutlineInput replaces a book's outline; Confirmed marks it ready for
// generation. The slice's order is the chapter order.
type OutlineInput struct {
	Chapters  []OutlineItemInput
	Confirmed bool
}

// Source is material a book can be generated from, after ownership has been
// checked and before it is snapshotted.
type Source struct {
	Kind      SourceKind
	ID        string
	Title     string
	Text      string
	Truncated bool
}

// Export is a Markdown document ready to be downloaded.
type Export struct {
	Filename string
	Content  string
}

var (
	// ErrNotFound reports a book or chapter that is not there for this caller.
	ErrNotFound = errors.New("book: not found")
	// ErrInvalidRequest reports a request this build cannot act on.
	ErrInvalidRequest = errors.New("book: invalid request")
	// ErrInvalidOutline reports an outline that fails the shape rules.
	ErrInvalidOutline = errors.New("book: invalid outline")
	// ErrInvalidProgress reports an ordinal outside the outline.
	ErrInvalidProgress = errors.New("book: invalid progress")
	// ErrSourceEmpty reports a source with no usable text.
	ErrSourceEmpty = errors.New("book: the source has no text")
	// ErrSourceUnavailable reports a source the caller cannot read, or a file
	// that has not been parsed.
	ErrSourceUnavailable = errors.New("book: the source is unavailable")
	// ErrOutlineNotConfirmed reports chapter generation before confirmation.
	ErrOutlineNotConfirmed = errors.New("book: the outline is not confirmed")
	// ErrOutlineLocked reports a change after a chapter has been generated.
	ErrOutlineLocked = errors.New("book: the outline is locked")
	// ErrOutlineChanged reports a write under an outline that has been replaced.
	ErrOutlineChanged = errors.New("book: the outline changed")
	// ErrGenerationInFlight reports a second generation for the same outline or
	// chapter in this process.
	ErrGenerationInFlight = errors.New("book: generation is already running")
	// ErrNoReadyChapters reports an export with nothing to export.
	ErrNoReadyChapters = errors.New("book: no chapter has been generated")
	// ErrGenerationUnavailable reports a build without a provider or model.
	ErrGenerationUnavailable = errors.New("book: generation is not configured")
	// ErrGenerationFailed reports a provider failure or model output that stays
	// invalid after the one repair attempt.
	ErrGenerationFailed = errors.New("book: generation failed")
)

// SourceLoader reads one source on behalf of a caller. The adapter lives where
// the source domains do; this package only names what it needs.
type SourceLoader interface {
	Load(ctx context.Context, kind SourceKind, sourceID string, caller authz.Caller) (Source, error)
}

// newID returns a prefixed, random identifier in the shape the rest of the
// repository uses.
func newID(prefix string) (string, error) {
	random := make([]byte, idRandomBytes)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(random), nil
}

// clipRunes cuts text to at most limit runes and reports whether anything was
// dropped. It never splits a multi-byte character.
func clipRunes(text string, limit int) (string, bool) {
	if limit < 0 {
		limit = 0
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text, false
	}
	return string(runes[:limit]), true
}

// cleanTitle trims a title and applies the column's rune bound.
func cleanTitle(title string) string {
	cleaned, _ := clipRunes(strings.TrimSpace(title), BookTitleMaxRunes)
	return cleaned
}
