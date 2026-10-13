package library

// The metadata half of the storage contract, checked against a real seekdb.
//
// This package exists because of one column and its constraint. A file's bytes
// live in the filesystem and its row in `library_files`, and the two are held
// together by `library_path`: the schema checks that the path is shaped like a
// category directory and that the name inside it *is* this row's id. Nothing in
// Go checks that — the service composes the path from the id it just generated
// — so a mistake there produces a record pointing at a file it does not
// describe, and only the database refuses it.
//
// The rest is the round trip: a record is written once and read once, and the
// adapter's column list is the whole of what survives that.
//
// One database per package, so nothing here calls t.Parallel.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"backend/internal/data"
	"backend/internal/data/testdb"
)

func newDBRepository(t *testing.T) (*GORMRepository, *data.Resource) {
	t.Helper()
	resource := testdb.Open(t, "library")
	testdb.Reset(t, resource, "library_files")
	return NewGORMRepository(resource.GORM()), resource
}

// atSecond is a whole second on a fixed day. DATETIME keeps whole seconds, so a
// fixture that lets the clock pick one compares against a rounded value.
func atSecond(second int) time.Time {
	return time.Date(2026, 10, 1, 12, 0, second, 0, time.UTC)
}

const (
	// fileID is one id in the shape newFileID produces: the prefix and sixteen
	// bytes as hex. It is spelled out rather than generated because the path
	// below has to name it.
	fileID = "lib_0123456789abcdef0123456789abcdef"
	// fileDigest is a sha256 as the table spells it: sixty-four characters of
	// lowercase hex.
	fileDigest = "3b1f1c60e2d9a7b4c5f80d16e2938475a6b7c8d9e0f1a2b3c4d5e6f708192a3b"
)

func aFile() FileRecord {
	return FileRecord{
		ID:          fileID,
		SHA256:      fileDigest,
		Filename:    "第一课.pdf",
		MIMEType:    "application/pdf",
		SizeBytes:   4096,
		LibraryPath: "pdf/" + fileID + ".pdf",
		CreatedAt:   atSecond(1),
		UpdatedAt:   atSecond(2),
	}
}

func storeFile(t *testing.T, repo *GORMRepository, record FileRecord) {
	t.Helper()
	if err := repo.Create(context.Background(), record); err != nil {
		t.Fatalf("store the file %s: %v", record.ID, err)
	}
}

func countFiles(t *testing.T, resource *data.Resource) int64 {
	t.Helper()
	var count int64
	if err := resource.GORM().Raw("SELECT COUNT(*) FROM library_files").Scan(&count).Error; err != nil {
		t.Fatalf("count the stored files: %v", err)
	}
	return count
}

func TestDBALibraryFileGoesInAndComesBackWhole(t *testing.T) {
	repo, resource := newDBRepository(t)
	record := aFile()
	storeFile(t, repo, record)

	// Every field the uploader filled in is read back by `Find`, which is the
	// only way a caller ever sees this row again: the record a client gets after
	// storing a file and the one it gets when it comes back for it are assembled
	// from two different places, and this is that they agree.
	found, err := repo.Find(context.Background(), fileID)
	if err != nil {
		t.Fatalf("read the stored file back: %v", err)
	}
	if found.ID != record.ID || found.SHA256 != record.SHA256 ||
		found.Filename != record.Filename || found.MIMEType != record.MIMEType ||
		found.SizeBytes != record.SizeBytes || found.LibraryPath != record.LibraryPath {
		t.Errorf("the file reads back as %+v, want %+v", found, record)
	}
	if !found.CreatedAt.Equal(record.CreatedAt) || !found.UpdatedAt.Equal(record.UpdatedAt) {
		t.Errorf("the file is stamped %v/%v, want %v/%v",
			found.CreatedAt, found.UpdatedAt, record.CreatedAt, record.UpdatedAt)
	}

	// The columns the record does not carry are the table's, and they say the
	// file is live. A new row that needed its caller to say so would be one the
	// adapter could store deleted.
	var row struct {
		IsDeleted bool       `gorm:"column:is_deleted"`
		DeletedAt *time.Time `gorm:"column:deleted_at"`
	}
	err = resource.GORM().Raw(
		"SELECT is_deleted, deleted_at FROM library_files WHERE id = ?", fileID).Scan(&row).Error
	if err != nil {
		t.Fatalf("read the state columns: %v", err)
	}
	if row.IsDeleted || row.DeletedAt != nil {
		t.Errorf("the stored file is flagged deleted %v at %v, want a live row",
			row.IsDeleted, row.DeletedAt)
	}
}

// The path and the id are one fact written twice, and the schema is what holds
// them together: a row whose path names a different file, or runs outside the
// categories this module writes, is refused rather than stored.
func TestDBThePathMustNameTheFileItBelongsTo(t *testing.T) {
	repo, resource := newDBRepository(t)

	somebodyElse := strings.Replace(fileID, "0123", "abcd", 1)
	refused := []struct {
		name   string
		mutate func(*FileRecord)
	}{
		{"a path naming another file", func(r *FileRecord) {
			r.LibraryPath = "pdf/" + somebodyElse + ".pdf"
		}},
		{"a path with no id in it", func(r *FileRecord) {
			r.LibraryPath = "pdf/lesson.pdf"
		}},
		{"a path outside the categories", func(r *FileRecord) {
			r.LibraryPath = "../" + fileID + ".pdf"
		}},
		{"a category the module does not write", func(r *FileRecord) {
			r.LibraryPath = "exe/" + fileID + ".exe"
		}},
		// The path is relative to the storage root, and a leading slash is how a
		// record would escape it: `Open` joins the two, and a join of an absolute
		// second path discards the first.
		{"an absolute path", func(r *FileRecord) {
			r.LibraryPath = "/pdf/" + fileID + ".pdf"
		}},
		{"a digest that is not lowercase hex", func(r *FileRecord) {
			r.SHA256 = strings.ToUpper(fileDigest)
		}},
		{"a filename that is blank", func(r *FileRecord) {
			r.Filename = "   "
		}},
	}
	for _, scenario := range refused {
		record := aFile()
		scenario.mutate(&record)
		if err := repo.Create(context.Background(), record); err == nil {
			t.Errorf("%s was stored, want the schema to refuse it", scenario.name)
		}
	}
	if got := countFiles(t, resource); got != 0 {
		t.Errorf("%d files were stored, want none of the refused ones", got)
	}

	// And the shape the service does write goes in, so the list above is a
	// boundary rather than a wall.
	storeFile(t, repo, aFile())
	if got := countFiles(t, resource); got != 1 {
		t.Errorf("%d files are stored, want the one that was asked for", got)
	}
}

// A file is looked up by the id an HTTP path parameter carries, and the lookup
// is exact: under this column's collation a spelling that differs is a
// different id, not the same file.
func TestDBAFileIsFoundByItsExactIdOrNotAtAll(t *testing.T) {
	repo, resource := newDBRepository(t)
	storeFile(t, repo, aFile())

	missing := []struct {
		name string
		id   string
	}{
		{"an id nobody holds", "lib_ffffffffffffffffffffffffffffffff"},
		{"a prefix of a stored id", fileID[:20]},
		{"a stored id with one character changed", strings.Replace(fileID, "f", "e", 1)},
		{"a stored id in another case", strings.ToUpper(fileID)},
		{"no id at all", ""},
	}
	for _, scenario := range missing {
		_, err := repo.Find(context.Background(), scenario.id)
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v, want %v", scenario.name, err, ErrNotFound)
		}
	}

	// The report is this package's own sentinel and nothing more, so a caller
	// writing a 404 for it cannot leak what the storage layer said.
	_, err := repo.Find(context.Background(), "nobody")
	if text := err.Error(); !strings.Contains(text, ErrNotFound.Error()) {
		t.Errorf("the missing-file report reads %q, want it to name %q", text, ErrNotFound)
	}
	if got := countFiles(t, resource); got != 1 {
		t.Errorf("%d files are stored, want the one that was uploaded", got)
	}
}
