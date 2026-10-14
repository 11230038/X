package library

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"backend/internal/audit"
	"backend/internal/authz"
)

const (
	fileIDPrefix       = "lib_"
	fileIDRandomBytes  = 16
	maxFilenameBytes   = 255
	storagePermissions = 0o700
	filePermissions    = 0o600
)

var categories = []string{"images", "pdf", "ppt", "doc", "md", "mp3"}

// Service owns upload validation, local storage, and metadata persistence.
type Service struct {
	root         string
	maxFileBytes int64
	repository   MetadataRepository
	auditor      Auditor
}

// NewService initializes every local directory needed by the file library.
//
// An auditor is required and not defaulted, because the operations that take a
// file away are the ones the audit exists for (§2.14), and a library built
// without one would be a library where those operations cannot be recorded —
// which is a wiring fault, and so belongs here where it refuses a deployment
// rather than at the moment a user deletes something.
func NewService(options Options, repository MetadataRepository) (*Service, error) {
	if strings.TrimSpace(options.Root) == "" || options.MaxFileBytes <= 0 || repository == nil {
		return nil, fmt.Errorf("invalid library configuration")
	}
	if options.Auditor == nil {
		return nil, fmt.Errorf("invalid library configuration: no auditor")
	}
	root, err := filepath.Abs(filepath.Clean(options.Root))
	if err != nil {
		return nil, fmt.Errorf("resolve upload root: %w", err)
	}
	for _, directory := range append([]string{".staging"}, categories...) {
		directoryPath := filepath.Join(root, directory)
		if err := os.MkdirAll(directoryPath, storagePermissions); err != nil {
			return nil, fmt.Errorf("create upload directory %q: %w", directory, err)
		}
		if err := verifyStorageDirectory(root, directoryPath); err != nil {
			return nil, fmt.Errorf("verify upload directory %q: %w", directory, err)
		}
	}
	return &Service{
		root: root, maxFileBytes: options.MaxFileBytes,
		repository: repository, auditor: options.Auditor,
	}, nil
}

// Upload validates and stores one file before persisting its metadata.
//
// Dedup happens here and it happens before anything is moved into place (§2.14):
// the bytes are hashed into staging, and then this account's library is asked
// whether it already holds them. A yes is answered with the file that was already
// there and nothing new is written — which is why two uploads of one file share
// an id, and why the second one is told about a file it has seen before by the id
// alone.
//
// A yes on a row in the recycle bin is a restore rather than a hit: the binned
// row comes back instead of a second row being written, and that is not a
// shortcut. `library_files_path_unique` is a unique key over every row, binned
// ones included (seekdb has no partial index), so the binned row still holds the
// path a new row would need — restoring and deduplicating are the same act
// because a second row could not be written at all.
//
// Nothing is validated on a hit, and that is not an omission: these are the same
// bytes, already validated, already on disk under a name this library wrote.
func (s *Service) Upload(ctx context.Context, input UploadInput) (UploadResult, error) {
	filename, err := cleanFilename(input.Filename)
	if err != nil {
		return UploadResult{}, err
	}
	if input.Content == nil {
		return UploadResult{}, ErrInvalidFile
	}
	if !input.Actor.Valid() {
		return UploadResult{}, ErrNoActor
	}
	id, err := newFileID()
	if err != nil {
		return UploadResult{}, fmt.Errorf("generate file id: %w", err)
	}
	stagingPath := filepath.Join(s.root, ".staging", id+".part")
	size, digest, err := s.writeStaging(ctx, stagingPath, input.Content)
	if err != nil {
		return UploadResult{}, err
	}
	stagingExists := true
	defer func() {
		if stagingExists {
			_ = os.Remove(stagingPath)
		}
	}()

	existing, err := s.repository.FindByDigest(ctx, digest, input.Actor.Caller)
	switch {
	case err == nil:
		// The staging file is removed by the defer above: on this path nothing is
		// ever moved out of it.
		return s.reuse(ctx, existing, input.Actor)
	case errors.Is(err, ErrNotFound):
	default:
		return UploadResult{}, err
	}

	classification, err := validateFile(stagingPath, filename, input.DeclaredMIME)
	if err != nil {
		return UploadResult{}, err
	}
	relativePath := path.Join(classification.Category, id+classification.Extension)
	finalPath, err := safeJoin(s.root, relativePath)
	if err != nil {
		return UploadResult{}, fmt.Errorf("resolve final path: %w", err)
	}
	if err := os.Rename(stagingPath, finalPath); err != nil {
		return UploadResult{}, fmt.Errorf("finalize uploaded file: %w", err)
	}
	stagingExists = false

	now := time.Now().UTC()
	record := FileRecord{
		ID: id, SHA256: digest, Filename: filename, MIMEType: classification.MIMEType,
		SizeBytes: size, LibraryPath: relativePath, OwnerID: input.Actor.UserID,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.repository.Create(ctx, record); err != nil {
		if removeErr := os.Remove(finalPath); removeErr != nil {
			return UploadResult{}, fmt.Errorf("persist metadata and compensate file: %w", errors.Join(err, removeErr))
		}
		return UploadResult{}, fmt.Errorf("persist file metadata: %w", err)
	}
	return UploadResult{
		ID: id, SHA256: digest, Filename: filename, MIMEType: classification.MIMEType,
		SizeBytes: size, Category: classification.Category, CreatedAt: now,
	}, nil
}

// reuse answers an upload with the file this account already had.
//
// The name returned is the stored row's name and not the one just uploaded, and
// that is the point of a merge: there is one file here, it has one name, and the
// name it has is the one it has always had. Answering with the new spelling would
// be reporting a rename that did not happen.
func (s *Service) reuse(ctx context.Context, existing FileRecord, actor Actor) (UploadResult, error) {
	if !existing.InRecycleBin() {
		return s.resultOf(existing)
	}
	// The same bytes the account threw away, so this is a restore (§2.14). The
	// record is written before the row changes: a change with no record is
	// unrecoverable, and a record for a change that then failed is an error the
	// caller is told about and can be seen in the log.
	if err := s.record(ctx, actor, audit.ActionFileRestored, existing.ID, audit.ResultOK, audit.Detail{
		Reason: "restored_by_upload",
	}); err != nil {
		return UploadResult{}, err
	}
	now := time.Now().UTC()
	if err := s.repository.SetDeleted(ctx, existing.ID, false, now, actor.Caller); err != nil {
		return UploadResult{}, err
	}
	existing.Deleted = false
	existing.DeletedAt = nil
	return s.resultOf(existing)
}

// Find reads one stored file's metadata, without opening its bytes.
//
// It answers in the shape the upload answered in, because it is the same file
// read at a different time: a client that stored a file and one that comes back
// for it later are reading one resource, and two shapes for it would be two
// things to keep in step.
//
// A file in the recycle bin is not found (§2.14). The bin is not a second
// library with a flag on it — while a file is in there it is not in the library,
// and every read that would have handed it over stops finding it.
func (s *Service) Find(ctx context.Context, id string, caller authz.Caller) (UploadResult, error) {
	record, err := s.live(ctx, id, caller)
	if err != nil {
		return UploadResult{}, err
	}
	return s.resultOf(record)
}

// Open reads one stored file back: its metadata, and a reader over its bytes.
//
// It serves both readers of a file — the download route, which hands the bytes
// to a client, and the parsers, which stream them into a knowledge base — because
// there is one answer to "may this caller have these bytes" and two methods for
// it would be two places to get it right. What differs is what the caller does
// with the reader, and that is the caller's business.
//
// The caller closes the reader. The path is resolved the same way it was when
// the file was stored — the metadata's relative path joined to the root, then
// checked to be inside it — so that a row that says something else cannot be
// used to read outside the library.
func (s *Service) Open(ctx context.Context, id string, caller authz.Caller) (StoredFile, error) {
	record, err := s.live(ctx, id, caller)
	if err != nil {
		return StoredFile{}, err
	}
	category, err := categoryOf(record.LibraryPath)
	if err != nil {
		return StoredFile{}, err
	}
	full, err := safeJoin(s.root, record.LibraryPath)
	if err != nil {
		// A row whose path escapes the root is not a file that is missing; it is
		// one this library would not have written. Saying so is more use than
		// opening whatever the path turns out to point at.
		return StoredFile{}, fmt.Errorf("resolve stored path for %s: %w", id, err)
	}
	content, err := os.Open(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return StoredFile{}, fmt.Errorf("%w: %s has a record but no file", ErrNotFound, id)
		}
		return StoredFile{}, fmt.Errorf("open stored file %s: %w", id, err)
	}
	return StoredFile{
		ID: record.ID, Filename: record.Filename, MIMEType: record.MIMEType,
		Category: category, SizeBytes: record.SizeBytes, Content: content,
	}, nil
}

// resultOf is one record as the upload reports it.
//
// The category is read from the path rather than from a column, for the same
// reason Open does: the directory is the classification, and a second copy of it
// is a second thing that can disagree.
func (s *Service) resultOf(record FileRecord) (UploadResult, error) {
	category, err := categoryOf(record.LibraryPath)
	if err != nil {
		return UploadResult{}, err
	}
	return UploadResult{
		ID: record.ID, SHA256: record.SHA256, Filename: record.Filename,
		MIMEType: record.MIMEType, SizeBytes: record.SizeBytes,
		Category: category, CreatedAt: record.CreatedAt,
	}, nil
}

// summaryOf is one record as a listing shows it.
func (s *Service) summaryOf(record FileRecord) (FileSummary, error) {
	category, err := categoryOf(record.LibraryPath)
	if err != nil {
		return FileSummary{}, err
	}
	return FileSummary{
		ID: record.ID, SHA256: record.SHA256, Filename: record.Filename,
		MIMEType: record.MIMEType, SizeBytes: record.SizeBytes, Category: category,
		CreatedAt: record.CreatedAt, DeletedAt: record.DeletedAt,
	}, nil
}

// record writes one entry about a file, and it is the only place this package
// writes to the audit.
//
// The actor's name comes from the request and not from storage, for the reason
// the audit stores a snapshot at all (§2.6): the entry has to say who the account
// was when they did it, and a lookup here would be reading a name a rename may
// already have changed.
func (s *Service) record(ctx context.Context, actor Actor, action audit.Action, id string, result audit.Result, detail audit.Detail) error {
	if s == nil || s.auditor == nil {
		return audit.ErrNotConfigured
	}
	event := audit.Event{
		Actor:  actor.auditActor(),
		Action: action,
		Target: audit.Ref{Kind: audit.TargetLibraryFile, ID: id},
		// The subject is the account the file belongs to, and it is the same as
		// the actor on every route this package serves: a library file is only
		// ever touched by the account that uploaded it. It is written anyway,
		// because "what has happened to this account's files" is one of the three
		// questions an audit is read with (§2.6), and the column that answers it
		// is this one.
		SubjectID: actor.UserID,
		Result:    result,
		Detail:    detail,
		// The request the operation was served for, when the caller marked the
		// context (handler.WithRequestID). It is empty for work that came from
		// somewhere other than a request, which is a real answer and not a
		// missing one.
		RequestID: requestIDOf(ctx),
	}
	if err := s.auditor.Record(ctx, event); err != nil {
		return fmt.Errorf("record %s for file %s: %w", action, id, err)
	}
	return nil
}

// categoryOf reads the category back out of where the file was stored.
//
// The category is not a column: it is the directory the bytes are in, written
// from the same classification that produced the name. Reading it from the path
// is what keeps the two from disagreeing — a parser chosen by one answer and a
// file opened at another is a file read by the wrong engine, which fails
// silently with text nobody can use.
func categoryOf(libraryPath string) (string, error) {
	category, _, found := strings.Cut(libraryPath, "/")
	if !found || category == "" {
		return "", fmt.Errorf("stored path %q is not one this library writes", libraryPath)
	}
	return category, nil
}

func (s *Service) writeStaging(ctx context.Context, stagingPath string, source io.Reader) (int64, string, error) {
	file, err := os.OpenFile(stagingPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePermissions)
	if err != nil {
		return 0, "", fmt.Errorf("create staging file: %w", err)
	}
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(stagingPath)
		}
	}()

	hash := sha256.New()
	buffer := make([]byte, 32*1024)
	var size int64
	for {
		if err := ctx.Err(); err != nil {
			return 0, "", err
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			size += int64(read)
			if size > s.maxFileBytes {
				return 0, "", ErrFileTooLarge
			}
			if _, err := file.Write(buffer[:read]); err != nil {
				return 0, "", fmt.Errorf("write staging file: %w", err)
			}
			_, _ = hash.Write(buffer[:read])
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return 0, "", fmt.Errorf("read upload content: %w", readErr)
		}
		if read == 0 {
			return 0, "", fmt.Errorf("read upload content: no progress")
		}
	}
	if size == 0 {
		return 0, "", ErrEmptyFile
	}
	if err := file.Sync(); err != nil {
		return 0, "", fmt.Errorf("sync staging file: %w", err)
	}
	if err := file.Close(); err != nil {
		return 0, "", fmt.Errorf("close staging file: %w", err)
	}
	remove = false
	return size, hex.EncodeToString(hash.Sum(nil)), nil
}

func cleanFilename(value string) (string, error) {
	value = strings.ReplaceAll(value, "\\", "/")
	name := path.Base(value)
	if name == "" || name == "." || name == ".." || len([]byte(name)) > maxFilenameBytes {
		return "", ErrInvalidFilename
	}
	for _, r := range name {
		if r == 0 || unicode.IsControl(r) {
			return "", ErrInvalidFilename
		}
	}
	return name, nil
}

func newFileID() (string, error) {
	value := make([]byte, fileIDRandomBytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return fileIDPrefix + hex.EncodeToString(value), nil
}

func verifyStorageDirectory(root, directory string) error {
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return fmt.Errorf("upload root is not a real directory")
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("storage path is not a real directory")
	}
	probe, err := os.CreateTemp(directory, ".write-test-*")
	if err != nil {
		return err
	}
	probePath := probe.Name()
	if err := probe.Close(); err != nil {
		_ = os.Remove(probePath)
		return err
	}
	if err := os.Remove(probePath); err != nil {
		return err
	}
	return nil
}

func safeJoin(root, relative string) (string, error) {
	full := filepath.Join(root, filepath.FromSlash(relative))
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path escapes upload root")
	}
	return full, nil
}
