package library

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recordingRepository struct {
	record FileRecord
	err    error
	// stored is every record Create was given, by id, which is what Find reads
	// back. A test that wants a read to answer something Create never wrote sets
	// it directly: a row is not always a row this process put there.
	stored map[string]FileRecord
	// findErr, when set, fails the read the way storage does.
	findErr error
}

func (r *recordingRepository) Create(_ context.Context, record FileRecord) error {
	r.record = record
	if r.err == nil {
		if r.stored == nil {
			r.stored = make(map[string]FileRecord)
		}
		r.stored[record.ID] = record
	}
	return r.err
}

func (r *recordingRepository) Find(_ context.Context, id string) (FileRecord, error) {
	if r.findErr != nil {
		return FileRecord{}, r.findErr
	}
	record, ok := r.stored[id]
	if !ok {
		return FileRecord{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return record, nil
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

func TestOpenReadsAStoredFileBackWithItsCategory(t *testing.T) {
	root := t.TempDir()
	repository := &recordingRepository{}
	service, err := NewService(Options{Root: root, MaxFileBytes: 1024}, repository)
	if err != nil {
		t.Fatal(err)
	}
	content := "# 数学分析\n\n拉格朗日中值定理。\n"
	uploaded, err := service.Upload(context.Background(), UploadInput{
		Filename: "notes.md", DeclaredMIME: "text/markdown", Content: strings.NewReader(content),
	})
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}

	file, err := service.Open(context.Background(), uploaded.ID)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = file.Content.Close() }()

	read, err := io.ReadAll(file.Content)
	if err != nil {
		t.Fatalf("read the stored file: %v", err)
	}
	if string(read) != content {
		t.Errorf("Open() gave %q, want what was uploaded", read)
	}
	// The category is what a parser is chosen by, so it has to come back with the
	// bytes and agree with where they were stored.
	if file.Category != "md" || file.Filename != "notes.md" || file.MIMEType != "text/markdown" {
		t.Errorf("Open() = %+v, want the uploaded file's metadata", file)
	}
}

func TestOpenReportsAFileThatIsNotThere(t *testing.T) {
	service, err := NewService(Options{Root: t.TempDir(), MaxFileBytes: 1024}, &recordingRepository{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Open(context.Background(), "lib_missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Open() error = %v, want ErrNotFound", err)
	}
	if _, err := service.Open(context.Background(), "  "); !errors.Is(err, ErrInvalidFile) {
		t.Errorf("Open(blank) error = %v, want ErrInvalidFile", err)
	}
	// A record with no file behind it is the same answer: the bytes are not
	// here, and which half is missing is not something the caller can act on
	// differently.
	repository := &recordingRepository{stored: map[string]FileRecord{
		"lib_gone": {ID: "lib_gone", Filename: "gone.md", LibraryPath: "md/lib_gone.md"},
	}}
	service, err = NewService(Options{Root: t.TempDir(), MaxFileBytes: 1024}, repository)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Open(context.Background(), "lib_gone"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Open(record without a file) error = %v, want ErrNotFound", err)
	}
}

func TestOpenRefusesAPathItWouldNotHaveWritten(t *testing.T) {
	for _, tt := range []struct {
		name string
		path string
	}{
		{name: "escapes the root", path: "../outside.md"},
		{name: "absolute", path: "/etc/passwd"},
		{name: "no category directory", path: "loose.md"},
		{name: "empty category", path: "/md/file.md"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repository := &recordingRepository{stored: map[string]FileRecord{
				"lib_odd": {ID: "lib_odd", Filename: "odd.md", LibraryPath: tt.path},
			}}
			service, err := NewService(Options{Root: t.TempDir(), MaxFileBytes: 1024}, repository)
			if err != nil {
				t.Fatal(err)
			}
			file, err := service.Open(context.Background(), "lib_odd")
			if err == nil {
				_ = file.Content.Close()
				t.Fatalf("Open(%q) opened a file outside the library", tt.path)
			}
			if errors.Is(err, ErrNotFound) {
				t.Errorf("Open(%q) error = %v, want a refusal rather than a missing file", tt.path, err)
			}
		})
	}
}

// Find answers in the shape Upload answered in, which is the point: the client
// that stored the file and the one that comes back for it a week later are
// reading one resource, and the preview beside it names the file the same way.
func TestFindReturnsTheFileAsUploadDid(t *testing.T) {
	repository := &recordingRepository{}
	service, err := NewService(Options{Root: t.TempDir(), MaxFileBytes: 1024 * 1024}, repository)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	content := []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\n%%EOF\n")
	uploaded, err := service.Upload(context.Background(), UploadInput{
		Filename: "notes.pdf", DeclaredMIME: "application/pdf", Content: bytes.NewReader(content),
	})
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}

	found, err := service.Find(context.Background(), uploaded.ID)
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}

	if found != uploaded {
		t.Errorf("Find() = %+v, want the upload's own answer %+v", found, uploaded)
	}
	if found.Category != "pdf" {
		t.Errorf("category = %q, want it read back from where the file was stored", found.Category)
	}
}

// The category is read from the directory the file is in rather than from a
// column: the directory is the classification, and a second copy of it is a
// second thing that can disagree. A path with no directory at all is therefore
// not one this process wrote, and the read says so rather than answering with a
// category nothing means.
func TestFindRefusesARecordWithNoCategory(t *testing.T) {
	repository := &recordingRepository{stored: map[string]FileRecord{
		"lib_odd": {ID: "lib_odd", Filename: "odd.md", LibraryPath: "loose.md"},
	}}
	service, err := NewService(Options{Root: t.TempDir(), MaxFileBytes: 1024}, repository)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.Find(context.Background(), "lib_odd"); err == nil {
		t.Fatal("Find() error = nil, want a refusal")
	}
}

func TestFindReportsAMissingFileAndAnEmptyId(t *testing.T) {
	service, err := NewService(Options{Root: t.TempDir(), MaxFileBytes: 1024}, &recordingRepository{})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.Find(context.Background(), "lib_missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Find(unknown) error = %v, want ErrNotFound", err)
	}
	// An empty id is not a file that is missing: nothing was asked for, and the
	// empty string is not an id anything was ever stored under.
	if _, err := service.Find(context.Background(), "  "); !errors.Is(err, ErrInvalidFile) {
		t.Errorf("Find(blank) error = %v, want ErrInvalidFile", err)
	}
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
