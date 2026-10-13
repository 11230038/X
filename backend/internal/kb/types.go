package kb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"backend/internal/embed"
	"backend/internal/event"
)

// The two kinds of row this package creates. They are readable ids rather than
// opaque ones because they are what a log line and a citation carry.
const (
	kbIDPrefix     = "kb_"
	docIDPrefix    = "doc_"
	idRandomBytes  = 16
	chunkBatchSize = 200
)

// ProviderNative is the only retrieval provider this build has. KB-02 requires
// the provider to be bound when a base is created and kept afterwards, so it is
// written into the row rather than held in configuration: a base created today
// must still say what it is after the build has moved on.
const ProviderNative = "native"

// The document vector states. They are for the UI and for diagnosis: whether a
// chunk can be found by the vector path depends on the chunk's own embedding
// column, never on this value.
const (
	VectorStatePending = "pending"
	VectorStateRunning = "running"
	VectorStateReady   = "ready"
	VectorStateFailed  = "failed"
)

// minQueryChars is the shortest query worth sending to the full-text path.
//
// It is not a policy choice: the full-text index is built WITH PARSER ngram and
// this seekdb build's ngram_token_size is 2, so a one-character query matches
// nothing at all — it would return an empty branch and read as "the library has
// nothing about this", which is a different and wrong answer.
const minQueryChars = 2

var (
	// ErrNotFound reports a knowledge base, document or file that is not there.
	ErrNotFound = errors.New("kb: not found")
	// ErrInvalidInput reports a request that cannot be acted on as given.
	ErrInvalidInput = errors.New("kb: invalid input")
	// ErrQueryTooShort reports a query the full-text index cannot match.
	ErrQueryTooShort = errors.New("kb: the query is too short to search with")
	// ErrConflict reports a write that lost a race — a concurrent rebuild of the
	// same document, or the same file added twice into one base.
	ErrConflict = errors.New("kb: the record changed while this write was in flight")
)

// KnowledgeBase is one retrievable collection.
type KnowledgeBase struct {
	ID             string
	Name           string
	Description    string
	Provider       string
	EmbeddingModel string
	IsDefault      bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
	// Documents and ReadyDocuments are how much is in the base and how much of
	// it can be found semantically yet. They are read for the list endpoint, not
	// stored on this row.
	Documents      int
	ReadyDocuments int
}

// Counts is one base's contents: how many documents it holds and how many of
// them have every chunk embedded.
type Counts struct {
	Documents int
	Ready     int
}

// Document is one parsed file. A document with an empty KBID is an attachment:
// it exists for the turns that carried it and belongs to no collection.
type Document struct {
	ID             string
	KBID           string
	FileID         string
	Filename       string
	Engine         string
	TextChars      int64
	Truncated      bool
	ChunkSize      int
	ChunkOverlap   int
	ActiveRevision int
	VectorState    string
	VectorError    string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ChunkRecord is one chunk as it is stored.
type ChunkRecord struct {
	ChunkID    string
	DocumentID string
	KBID       string
	Revision   int
	Ordinal    int
	Content    string
	CharStart  int64
	CharEnd    int64
	Page       *int
	// Embedding is the vector in the text form the column takes, and is empty
	// until the index worker fills it.
	Embedding string
}

// ChunkSource is a stored chunk joined to the document it belongs to: every
// field a Source needs, in one read.
type ChunkSource struct {
	ChunkID    string
	DocumentID string
	KBID       string
	FileID     string
	Filename   string
	Engine     string
	Revision   int
	Ordinal    int
	Content    string
	CharStart  int64
	CharEnd    int64
	Page       *int
}

// DocumentWrite is one document as it is written: the row, its extracted text,
// and the chunks of one revision.
//
// The text is passed alongside the row rather than as a field of Document,
// because every read path would then carry a copy of a two-hundred-thousand
// character string it has no use for. Write paths need it; read paths never
// select it.
type DocumentWrite struct {
	// Document.TextChars must be the character count of Text: it is what a
	// locator is clamped against, so a row that disagreed with its own text
	// would put every window read past the end.
	Document Document
	Text     string
	Chunks   []ChunkRecord
}

// SourceFile is one stored file's bytes and the metadata that decides how they
// are read.
type SourceFile struct {
	FileID   string
	Filename string
	MIMEType string
	// Category is what the file library classified the file as, which is what
	// the parser registry chooses an engine by.
	Category string
	Content  io.ReadCloser
}

// FileSource is where this package gets a stored file by id.
//
// It is a port declared here rather than an import of internal/library, and the
// reason is direction of dependency, not taste: kb must not know how bytes are
// stored, and library must not know what a document is. The composition root
// adapts one to the other. (Go interface satisfaction means the adapter is a
// small wrapper: two interfaces with the same shape but different return types
// are not interchangeable even when the fields match.)
type FileSource interface {
	Open(ctx context.Context, fileID string) (SourceFile, error)
}

// RetrieveInput is one retrieval over a turn's knowledge base scope.
type RetrieveInput struct {
	// KBIDs is the scope, and it is the whole of it: retrieval never leaves the
	// bases named here, so a base the user had and no longer has is reported,
	// not replaced by the default one or by any other (KB-07).
	KBIDs []string
	Query string
	// TopK caps the returned sources. Zero means the configured default.
	TopK int
	// Vector is the query's embedding, when the caller already has one. Empty
	// means embed the query here.
	Vector embed.Vector
	// SnippetChars caps each source's snippet, at a character boundary. Zero
	// means the whole chunk.
	SnippetChars int
}

// Retrieval is what one retrieval found.
type Retrieval struct {
	Sources []event.Source
	// Unavailable names the bases in the scope that are gone. It is not an
	// error and must not be dropped: it is the only thing that tells the model
	// and the user that material they had expected is missing, and KB-07's
	// "report it, do not substitute" lands exactly here.
	Unavailable []string
}

// Window is a range of one document's extracted text.
type Window struct {
	Document Document
	Text     string
	// CharStart and CharEnd are the span actually returned, which is the
	// requested span clamped to the text that exists.
	CharStart int64
	CharEnd   int64
	// TotalChars is the document's whole length, so a caller can tell a window
	// that ends because it was asked to from one that ends because the document
	// does.
	TotalChars int64
}

// Options configure a Service.
type Options struct {
	// Chunking is the window every document is split with. It is recorded on
	// each document as it is chunked, so a rebuild after a change is
	// reproducible after the fact.
	Chunking Chunking
	// RetrievalTopK is how many chunks reach the caller, Candidates is how many
	// each retrieval path returns before fusion.
	RetrievalTopK int
	Candidates    int
	RRFK          int
	// MaxCharsPerDocument is the per-document limit applied while parsing.
	MaxCharsPerDocument int
	// EmbeddingModel is the model whose vectors this service's bases store. It
	// is written onto a base when it is created, because KB-02 binds the
	// provider to the base; mixing two models' vectors in one column would
	// compare distances that mean nothing.
	EmbeddingModel string
	// Logger records the degradations that are not failures — a query that
	// could not be embedded, so only the full-text path answered. A nil logger
	// means those go unrecorded.
	Logger *slog.Logger
}

// Validate checks the options a service cannot work without.
func (o Options) Validate() error {
	if err := o.Chunking.Validate(); err != nil {
		return err
	}
	if o.RetrievalTopK <= 0 {
		return fmt.Errorf("kb: retrieval top k must be a positive integer")
	}
	if o.Candidates < o.RetrievalTopK {
		return fmt.Errorf("kb: candidates must not be smaller than retrieval top k")
	}
	if o.RRFK < 0 {
		return fmt.Errorf("kb: the reciprocal rank constant must not be negative")
	}
	if o.MaxCharsPerDocument <= 0 {
		return fmt.Errorf("kb: the per-document character limit must be a positive integer")
	}
	if strings.TrimSpace(o.EmbeddingModel) == "" {
		return fmt.Errorf("kb: the embedding model must not be empty")
	}
	return nil
}

// Repository is the storage seam for the three knowledge base tables.
//
// The operations that must not be interleaved are single calls for that reason.
// Switching a document's revision is one of them: the caller cannot get it
// wrong by forgetting a transaction, because there is no sequence to forget. A
// test double implements this interface, so the invariants the service leans on
// — one default base, one visible revision — are testable without a database.
type Repository interface {
	// Knowledge bases.
	CreateKnowledgeBase(ctx context.Context, record KnowledgeBase) error
	KnowledgeBase(ctx context.Context, kbID string) (KnowledgeBase, error)
	KnowledgeBases(ctx context.Context) ([]KnowledgeBase, error)
	KnowledgeBaseCounts(ctx context.Context) (map[string]Counts, error)
	SetDefaultKnowledgeBase(ctx context.Context, kbID string) error
	DeleteKnowledgeBase(ctx context.Context, kbID string) error

	// Documents. For DocumentForFile an empty kbID means "the attachment of
	// this file", which is a different row from the same file inside a base.
	Document(ctx context.Context, documentID string) (Document, error)
	DocumentForFile(ctx context.Context, fileID, kbID string) (Document, error)
	Documents(ctx context.Context, kbID string) ([]Document, error)
	CreateDocument(ctx context.Context, write DocumentWrite) error
	// ReplaceRevision writes a document's new revision and moves the document's
	// active pointer onto it in one transaction. fromRevision is checked before
	// anything is written, so two concurrent rebuilds cannot interleave: the one
	// that loses gets ErrConflict.
	ReplaceRevision(ctx context.Context, write DocumentWrite, fromRevision int) error
	DeleteDocument(ctx context.Context, documentID string) error

	// Chunks. Chunks reads one revision of one document through the chunk id
	// prefix, because nothing indexes document_id.
	Chunks(ctx context.Context, documentID string, revision int) ([]ChunkRecord, error)
	ChunkSources(ctx context.Context, chunkIDs []string) ([]ChunkSource, error)
	// DocumentText reads a window of a document's text in characters, so that a
	// citation can be opened without loading the document.
	DocumentText(ctx context.Context, documentID string, offset, length int64) (string, error)

	// Retrieval. Both return active chunks of the given bases, best first.
	SearchVector(ctx context.Context, kbIDs []string, vector embed.Vector, limit int) ([]string, error)
	SearchFullText(ctx context.Context, kbIDs []string, query string, limit int) ([]string, error)
}

// newID returns a readable id with the given prefix.
func newID(prefix string) (string, error) {
	value := make([]byte, idRandomBytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(value), nil
}

// chunksOf turns the chunks Split produced into the records that get stored.
func chunksOf(documentID, kbID string, revision int, chunks []Chunk) []ChunkRecord {
	records := make([]ChunkRecord, len(chunks))
	for index, chunk := range chunks {
		records[index] = ChunkRecord{
			ChunkID:    ChunkID(documentID, revision, chunk.Ordinal),
			DocumentID: documentID,
			KBID:       kbID,
			Revision:   revision,
			Ordinal:    chunk.Ordinal,
			Content:    chunk.Content,
			CharStart:  chunk.CharStart,
			CharEnd:    chunk.CharEnd,
		}
	}
	return records
}
