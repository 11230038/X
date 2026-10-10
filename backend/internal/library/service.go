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
}

// NewService initializes every local directory needed by the file library.
func NewService(options Options, repository MetadataRepository) (*Service, error) {
	if strings.TrimSpace(options.Root) == "" || options.MaxFileBytes <= 0 || repository == nil {
		return nil, fmt.Errorf("invalid library configuration")
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
	return &Service{root: root, maxFileBytes: options.MaxFileBytes, repository: repository}, nil
}

// Upload validates and stores one file before persisting its metadata.
func (s *Service) Upload(ctx context.Context, input UploadInput) (UploadResult, error) {
	filename, err := cleanFilename(input.Filename)
	if err != nil {
		return UploadResult{}, err
	}
	if input.Content == nil {
		return UploadResult{}, ErrInvalidFile
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
		SizeBytes: size, LibraryPath: relativePath, CreatedAt: now, UpdatedAt: now,
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
