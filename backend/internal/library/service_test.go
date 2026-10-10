package library

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recordingRepository struct {
	record FileRecord
	err    error
}

func (r *recordingRepository) Create(_ context.Context, record FileRecord) error {
	r.record = record
	return r.err
}

func TestNewServiceCreatesStorageDirectories(t *testing.T) {
	root := filepath.Join(t.TempDir(), "upload")
	if _, err := NewService(Options{Root: root, MaxFileBytes: 1024}, &recordingRepository{}); err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	for _, directory := range append([]string{".staging"}, categories...) {
		info, err := os.Stat(filepath.Join(root, directory))
		if err != nil || !info.IsDir() {
			t.Fatalf("directory %s missing: %v", directory, err)
		}
	}
}

func TestNewServiceRejectsSymlinkCategory(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "pdf")); err != nil {
		t.Skipf("create symlink: %v", err)
	}
	if _, err := NewService(Options{Root: root, MaxFileBytes: 1024}, &recordingRepository{}); err == nil {
		t.Fatal("NewService() error = nil, want symlink rejection")
	}
}

func TestUploadStoresValidatedPDFAndMetadata(t *testing.T) {
	root := t.TempDir()
	repository := &recordingRepository{}
	service, err := NewService(Options{Root: root, MaxFileBytes: 1024 * 1024}, repository)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	content := []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\n%%EOF\n")

	result, err := service.Upload(context.Background(), UploadInput{
		Filename:     "../../course-notes.pdf",
		DeclaredMIME: "application/pdf",
		Content:      bytes.NewReader(content),
	})
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	if result.Category != "pdf" || result.MIMEType != "application/pdf" {
		t.Errorf("result = %+v", result)
	}
	if result.Filename != "course-notes.pdf" {
		t.Errorf("Filename = %q, want course-notes.pdf", result.Filename)
	}
	wantHash := sha256.Sum256(content)
	if result.SHA256 != hex.EncodeToString(wantHash[:]) {
		t.Errorf("SHA256 = %q", result.SHA256)
	}
	if repository.record.LibraryPath == "" || !strings.HasPrefix(repository.record.LibraryPath, "pdf/") {
		t.Errorf("LibraryPath = %q, want pdf relative path", repository.record.LibraryPath)
	}
	if strings.Contains(repository.record.LibraryPath, "course-notes") || filepath.IsAbs(repository.record.LibraryPath) {
		t.Errorf("LibraryPath exposed original or absolute path: %q", repository.record.LibraryPath)
	}
	stored, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(repository.record.LibraryPath)))
	if err != nil {
		t.Fatalf("read stored file: %v", err)
	}
	if !bytes.Equal(stored, content) {
		t.Fatal("stored content differs from uploaded content")
	}
	assertStagingEmpty(t, root)
}

func TestUploadAcceptsFileAtExactSizeLimit(t *testing.T) {
	root := t.TempDir()
	content := "12345678"
	repository := &recordingRepository{}
	service, err := NewService(Options{Root: root, MaxFileBytes: int64(len(content))}, repository)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Upload(context.Background(), UploadInput{Filename: "notes.md", Content: strings.NewReader(content)}); err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	if repository.record.SizeBytes != int64(len(content)) {
		t.Fatalf("SizeBytes = %d, want %d", repository.record.SizeBytes, len(content))
	}
}

func TestUploadRejectsOversizedFileAndCleansStaging(t *testing.T) {
	root := t.TempDir()
	service, err := NewService(Options{Root: root, MaxFileBytes: 8}, &recordingRepository{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Upload(context.Background(), UploadInput{
		Filename: "notes.md",
		Content:  strings.NewReader("more than eight bytes"),
	})
	if !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("Upload() error = %v, want ErrFileTooLarge", err)
	}
	assertStagingEmpty(t, root)
}

func TestUploadRollsBackFileWhenRepositoryFails(t *testing.T) {
	root := t.TempDir()
	repository := &recordingRepository{err: errors.New("database unavailable")}
	service, err := NewService(Options{Root: root, MaxFileBytes: 1024}, repository)
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Upload(context.Background(), UploadInput{
		Filename: "notes.md",
		Content:  strings.NewReader("plain markdown"),
	})
	if err == nil {
		t.Fatal("Upload() error = nil, want repository failure")
	}
	if repository.record.LibraryPath == "" {
		t.Fatal("repository did not receive metadata")
	}
	if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(repository.record.LibraryPath))); !os.IsNotExist(statErr) {
		t.Fatalf("final file remains after repository error: %v", statErr)
	}
	assertStagingEmpty(t, root)
}

func TestUploadHonorsCanceledContextAndCleansStaging(t *testing.T) {
	root := t.TempDir()
	service, err := NewService(Options{Root: root, MaxFileBytes: 1024}, &recordingRepository{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = service.Upload(ctx, UploadInput{
		Filename: "notes.md",
		Content:  strings.NewReader("plain markdown"),
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Upload() error = %v, want context.Canceled", err)
	}
	assertStagingEmpty(t, root)
}

func TestUploadRejectsInvalidFileAndCleansStaging(t *testing.T) {
	root := t.TempDir()
	service, err := NewService(Options{Root: root, MaxFileBytes: 1024}, &recordingRepository{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Upload(context.Background(), UploadInput{
		Filename: "fake.pdf",
		Content:  strings.NewReader("not a pdf"),
	})
	if err == nil {
		t.Fatal("Upload() error = nil, want invalid file error")
	}
	assertStagingEmpty(t, root)
	assertCategoryEmpty(t, root, "pdf")
}

func TestSafeJoinRejectsEscapingPaths(t *testing.T) {
	root := t.TempDir()
	if _, err := safeJoin(root, "../outside.pdf"); err == nil {
		t.Fatal("safeJoin() error = nil, want escaping path rejection")
	}
}

func assertStagingEmpty(t *testing.T, root string) {
	t.Helper()
	assertCategoryEmpty(t, root, ".staging")
}

func assertCategoryEmpty(t *testing.T, root, category string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, category))
	if err != nil {
		t.Fatalf("read %s directory: %v", category, err)
	}
	if len(entries) != 0 {
		t.Fatalf("%s contains %d files, want empty", category, len(entries))
	}
}
