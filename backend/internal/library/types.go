package library

import (
	"context"
	"errors"
	"io"
	"time"
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
	ErrNotFound = errors.New("file not found")
)

// UploadInput describes one file stream entering the library module.
type UploadInput struct {
	Filename     string
	DeclaredMIME string
	Content      io.Reader
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

// FileRecord contains metadata persisted for one uploaded file.
type FileRecord struct {
	ID          string
	SHA256      string
	Filename    string
	MIMEType    string
	SizeBytes   int64
	LibraryPath string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// MetadataRepository persists file metadata after content reaches final storage.
type MetadataRepository interface {
	Create(context.Context, FileRecord) error
	// Find reads one record back by file id. The two together are the whole
	// interface: a file is written once and read afterwards, and nothing in this
	// module ever changes one.
	Find(ctx context.Context, id string) (FileRecord, error)
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
}

type fileClassification struct {
	Category  string
	Extension string
	MIMEType  string
}
