package library

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"backend/internal/audit"
	"backend/internal/authz"
)

var (
	ErrInvalidFilename     = errors.New("invalid filename")
	ErrUnsupportedFileType = errors.New("unsupported file type")
	ErrFileTypeMismatch    = errors.New("file type mismatch")
	ErrInvalidFile         = errors.New("invalid file")
	ErrFileTooLarge        = errors.New("file too large")
	ErrEmptyFile           = errors.New("empty file")
	// ErrNotFound reports a file id this library has no record of. It is a
	// distinct answer from a read that failed: the caller has to tell "this file
	// is not here" (which a user can act on) from "the library is not answering".
	//
	// A file in the recycle bin is answered with this too, and the reason is the
	// rule the bin is made of: while a file is in there it is *not in the
	// library*, so every read that would have handed it over stops finding it.
	// The three operations that are about binned rows — restore, purge, and the
	// listing that asks for them by name — go through the repository instead,
	// where the row is still visible.
	ErrNotFound = errors.New("file not found")
	// ErrInUse is what purging a file somebody still uses is refused with
	// (AC-15). The refusal is an *InUseError, which names who.
	ErrInUse = errors.New("file is still in use")
	// ErrInvalidQuery is what a search that cannot be drawn is refused with.
	ErrInvalidQuery = errors.New("invalid search")
	// ErrNoActor is what an operation with nobody behind it is refused with. It
	// is not a denial — a denial is a caller who was told no — it is a caller the
	// wiring never supplied, and a file operation with nobody behind it is one
	// nothing can be recorded about.
	ErrNoActor = errors.New("an actor is required")
)

// UploadInput describes one file stream entering the library module.
type UploadInput struct {
	Filename     string
	DeclaredMIME string
	Content      io.Reader
	// Actor is who is uploading. The file will belong to them, and that is
	// required rather than optional: a file stored with no owner is a file nobody
	// can find again, because every read of one is narrowed to an account. The
	// upload route used to be public, and this field is why it no longer is.
	//
	// It is an Actor and not the owner id it used to be because the upload can
	// turn out to be a restore — the same bytes this account threw away earlier —
	// and a restore is a state change about this account's library, which is
	// recorded under the name they had at the time (§2.6).
	Actor Actor
}

// UploadResult is safe to return to an HTTP caller.
type UploadResult struct {
	ID        string    `json:"id"`
	SHA256    string    `json:"sha256"`
	Filename  string    `json:"filename"`
	MIMEType  string    `json:"mime_type"`
	SizeBytes int64     `json:"size_bytes"`
	Category  string    `json:"category"`
	CreatedAt time.Time `json:"created_at"`
}

// FileSummary is one row of a library listing: a file as a list shows it.
//
// It is a type of its own rather than an UploadResult because a listing is a
// different question asked of many files at once, and one of its answers has no
// place on a single-file read. A binned file has to say when it was thrown away;
// an upload and a live read have no answer for that, and a field that is empty
// everywhere except one route is a field a client will read as "never deleted".
type FileSummary struct {
	ID       string `json:"id"`
	SHA256   string `json:"sha256"`
	Filename string `json:"filename"`
	MIMEType string `json:"mime_type"`
	// SizeBytes and Category are what the upload reported and the listing repeats:
	// a list offering to open a file quotes its size, and the category is the one
	// classification this library has.
	SizeBytes int64     `json:"size_bytes"`
	Category  string    `json:"category"`
	CreatedAt time.Time `json:"created_at"`
	// DeletedAt is when the file went into the recycle bin, and it is null for a
	// file that is live. It is not null for a binned one, because the table's
	// CHECK requires the flag and the moment to agree — so a client sorting a
	// recycle bin by "how long has this been in here" reads this and never the
	// flag.
	DeletedAt *time.Time `json:"deleted_at"`
}

// FileRecord contains metadata persisted for one uploaded file.
type FileRecord struct {
	ID          string
	SHA256      string
	Filename    string
	MIMEType    string
	SizeBytes   int64
	LibraryPath string
	// OwnerID is the account that uploaded the file. A file belongs to no
	// conversation, so there is no chain to derive this from — it is on the row.
	OwnerID int64
	// Deleted and DeletedAt are the recycle bin, and they are two fields because
	// the table has two columns under a CHECK that requires them to agree
	// (§2.14). They were dead from migration 00005 until V5; every read here
	// fills them in, so that a state the database enforces is a state this
	// package can reason about rather than one it discovers by failing.
	Deleted   bool
	DeletedAt *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// InRecycleBin reports whether this file is in the recycle bin.
//
// It is a method rather than a bare column read because the state is the thing
// the rules are written in, and a caller reaching for `Deleted` to mean "may I
// hand this over" is a caller deciding a policy one field at a time. The flag
// and the moment cannot disagree on a row this package wrote: they are always
// written together, and the table's CHECK refuses the pair that does not.
func (r FileRecord) InRecycleBin() bool { return r.Deleted }

// SearchQuery narrows a listing of one account's files.
//
// It is a struct rather than a list of arguments because every field is optional
// and the zero value has to mean "everything this account has", which a
// positional list cannot say.
type SearchQuery struct {
	// Caller narrows the listing to one account. A library list with no caller is
	// not a list of everything: before V5 the upload was public and every stored
	// file was everybody's.
	Caller authz.Caller
	// Filename keeps the files whose name contains this text. The match is
	// case-insensitive and literal — no wildcards — because a user typing a name
	// into a box means the name and not a pattern (see the repository for why
	// that is not a LIKE).
	Filename string
	// Category keeps one kind of file: the directory the bytes are in, which is
	// the only classification this library has.
	Category string
	// Since and Until bound created_at. Since is inclusive and Until exclusive,
	// the same convention the usage windows use, so that two adjacent ranges
	// neither overlap nor leave a gap.
	Since time.Time
	Until time.Time
	// Deleted lists the recycle bin instead of the live files. The two are never
	// listed together for the reason the sessions list gives: a client showing
	// both would have to filter every row to know which actions it may offer.
	Deleted bool
	// Limit and Offset page the listing. A zero Limit takes the default; a Limit
	// above the maximum is clamped rather than rejected, since an oversized page
	// is a client asking for more than it should get rather than an error it
	// could correct.
	Limit  int
	Offset int
}

// Reference is one thing that still uses a file: the answer to "who is using
// this" that a refused purge has to be able to give (AC-15).
//
// It carries a name as well as an id because the two are read by different
// people. The id is what a client acts on; the name is what a person who has
// just been told "no" looks for in their own library.
type Reference struct {
	// Kind is what points at the file: a document parsed from it, or a reading
	// material built on one of those documents.
	Kind string
	// ID is that row's own identifier.
	ID string
	// Name is what the row is called where a person would see it: the knowledge
	// base a document is in ("attachment" when it belongs to none), or the
	// material's filename.
	Name string
}

// The two kinds of thing that can hold a file in use.
const (
	// ReferenceDocument is a parsed document. Its file_id points at the file.
	ReferenceDocument = "document"
	// ReferenceReadingMaterial is somebody's place in a book. It points at a
	// document, which is why a file can be in use without a single row naming
	// it: the chain is file → document → material.
	ReferenceReadingMaterial = "reading_material"
)

// InUseError is a refused purge, with everything that still points at the file.
//
// It is a type rather than a sentence for the same reason the quota's refusal is
// (§2.9): "somebody is using this" has a reader that is not a person — a client
// that offers to show the user what is in the way — and a name is what it needs.
type InUseError struct {
	References []Reference
}

func (e *InUseError) Error() string {
	if e == nil || len(e.References) == 0 {
		return ErrInUse.Error()
	}
	named := make([]string, 0, len(e.References))
	for _, reference := range e.References {
		named = append(named, fmt.Sprintf("%s %s", reference.Kind, reference.ID))
	}
	return fmt.Sprintf("%s: %s", ErrInUse, strings.Join(named, ", "))
}

// Unwrap makes errors.Is(err, ErrInUse) true, so a caller checking "may this be
// destroyed" does not have to name the type.
func (e *InUseError) Unwrap() error { return ErrInUse }

// Actor is who is doing something, and it is authz.Caller plus a name.
//
// The name is here because the audit records the name an account had *at the
// time* (§2.6), and that name is not on the row: it is on the request. A service
// that looked it up would be reading a name a rename may already have changed,
// which is the one thing the snapshot exists to prevent — so the caller, which
// already has it, hands it over.
type Actor struct {
	authz.Caller
	Name string
}

// auditActor is the actor as the audit wants them.
func (a Actor) auditActor() audit.Actor {
	return audit.Actor{ID: a.UserID, Name: a.Name, Role: a.Role}
}

// MetadataRepository persists file metadata after content reaches final storage.
//
// It is one interface over one table, and every read narrows itself to the
// caller's rows. Another account's file is not refused here, it is not found —
// the same answer as an id that was never issued, which is what keeps a client
// from telling "this file exists and is not yours" from "there is no such file"
// (AUTH-06).
//
// The state a row is in — live or binned — is deliberately *not* a filter in the
// reads that the service's own rules are about. Find returns a binned row and
// says so; whether that is an answer or a refusal is a rule, and rules live
// above this line.
type MetadataRepository interface {
	Create(context.Context, FileRecord) error
	// Find reads one record back by file id, in the recycle bin or not.
	Find(ctx context.Context, id string, caller authz.Caller) (FileRecord, error)
	// FindByDigest reads the caller's row holding this content, live or binned.
	//
	// It is the dedup lookup (§2.14): the same bytes uploaded twice are one file,
	// and the second upload is answered with the row the first one wrote. A live
	// row is preferred over a binned one when both exist, because a live row is a
	// file that is already usable and restoring a second one would be a change
	// nobody asked for.
	FindByDigest(ctx context.Context, sha256 string, caller authz.Caller) (FileRecord, error)
	// Search lists one account's files, the live ones or the binned ones.
	Search(ctx context.Context, query SearchQuery) ([]FileRecord, error)
	// SetDeleted moves a file into or out of the recycle bin. Both columns are
	// written together, because the table's CHECK requires them to agree.
	SetDeleted(ctx context.Context, id string, deleted bool, now time.Time, caller authz.Caller) error
	// Delete removes one row for good, and the bytes with it are the caller's
	// business rather than this one's: what a row means and what a file is are
	// two different things, and the service is the only place that has both.
	Delete(ctx context.Context, id string, caller authz.Caller) error
	// References reports everything that still points at a file, following the
	// chain through the documents that were parsed from it. It is the question a
	// purge has to ask first (AC-15), and it is deliberately not scoped to an
	// account: whether somebody else's material holds this file in use is exactly
	// the fact that must not be missed.
	References(ctx context.Context, id string) ([]Reference, error)
}

// Auditor records what happened to a file for the questions the audit exists to
// answer (§2.6, §2.14).
//
// It is declared here, in the package that writes through it, and the event it
// takes is the audit's own — a second shape for the same entry would be a second
// place for the two to disagree.
type Auditor interface {
	Record(ctx context.Context, event audit.Event) error
}

// requestContextKey is the type the request id is stored under.
//
// A named empty struct rather than a string, which is the one rule context
// values exist under: a key that is a string can collide with a key some other
// package chose, and the collision is silent — the value is simply somebody
// else's.
type requestContextKey struct{}

// WithRequestID marks ctx as work done for one request, so that anything the
// library records under it can be found beside that request in the access log.
//
// It belongs at the boundary where the request is known — the handler — and not
// next to the write: a service deep enough to change a file's state is too deep
// to know that HTTP exists, which is why this travels on the context rather than
// as a parameter of every operation.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestContextKey{}, requestID)
}

// requestIDOf reads the id a request was marked with, or the empty string.
func requestIDOf(ctx context.Context) string {
	requestID, _ := ctx.Value(requestContextKey{}).(string)
	return requestID
}

// StoredFile is one stored file: its metadata, and its bytes.
//
// The bytes are a reader rather than a buffer because the caller is a parser
// that streams what it is given, and the file may be as large as the upload
// limit. Closing it is the caller's, and it is the reason this type exists at
// all rather than the service returning the path: a path would put the decision
// of whether it is inside the root on whoever opens it next.
type StoredFile struct {
	ID       string
	Filename string
	MIMEType string
	// Category is the classification the file was uploaded under, read back
	// from where it was stored.
	Category  string
	SizeBytes int64
	Content   io.ReadCloser
}

// Options configure local file storage.
type Options struct {
	Root         string
	MaxFileBytes int64
	// Auditor is required, and a library built without one is a library whose
	// files nobody can say was taken (§2.14). The requirement is at construction
	// rather than at the first download because the mistake is a wiring mistake,
	// and a wiring mistake belongs at startup where it refuses a deployment
	// instead of at the moment a user asks for their file.
	Auditor Auditor
}

type fileClassification struct {
	Category  string
	Extension string
	MIMEType  string
}
