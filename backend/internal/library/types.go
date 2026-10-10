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
