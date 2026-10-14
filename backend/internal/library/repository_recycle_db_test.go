package library

// The storage half of dedup and the recycle bin, checked against a real seekdb.
//
// Everything in this file is a claim about SQL: an ORDER BY that decides which of
// two rows holding one digest is the answer, a unique key that covers binned rows
// as well as live ones, a CHECK that holds the two state columns together, a
// substring search under a byte-wise collation, and a foreign key that cascades a
// file's documents away. A double can restate any of it and agree with itself;
// only the database can say whether it is true (see internal/data/testdb).
//
// One database per package, so nothing here calls t.Parallel.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"backend/internal/data"
	"backend/internal/data/models"
	"backend/internal/data/testdb"
)

// recycleTables are the tables these tests write, children first: a document
// holds a foreign key into a library file, and a material holds none at all but
// is emptied beside them so that a leftover row cannot answer for it.
var recycleTables = []string{"reading_materials", "document_chunks", "documents", "library_files"}

func newRecycleDB(t *testing.T) (*GORMRepository, *data.Resource) {
	t.Helper()
	resource := testdb.Open(t, "library")
	testdb.Reset(t, resource, append(recycleTables, "users")...)
	seedAccounts(t, resource)
	return NewGORMRepository(resource.GORM()), resource
}

// fileNumbered is aFile with an id and a digest a fixture can name, and a path
// the schema's rule accepts: the name inside the category directory *is* the row's
// id.
func fileNumbered(number int, digest string, createdAt time.Time) FileRecord {
	id := fmt.Sprintf("lib_%032x", number)
	return FileRecord{
		ID: id, SHA256: digest, Filename: fmt.Sprintf("第%d课.md", number),
		MIMEType: "text/markdown", SizeBytes: 128,
		LibraryPath: "md/" + id + ".md", OwnerID: testOwnerID,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}
}

// bin is the soft delete as a fixture does it: through the adapter's own write,
// so that a row a test calls binned is binned the way the application bins one.
func bin(t *testing.T, repo *GORMRepository, id string, at time.Time) {
	t.Helper()
	if err := repo.SetDeleted(context.Background(), id, true, at, testCaller()); err != nil {
		t.Fatalf("move %s into the recycle bin: %v", id, err)
	}
}

// TestDBTheDedupLookupPrefersTheLiveRowAndThenTheOldest is §2.14's ordering, and
// it is the whole of the dedup policy: a re-upload of bytes the account already
// has must be answered with the file it already has, and one whose only row is
// binned must be answered with that row — which is what keeps the path's unique
// key satisfiable (§2.14).
func TestDBTheDedupLookupPrefersTheLiveRowAndThenTheOldest(t *testing.T) {
	repo, resource := newRecycleDB(t)
	first := fileNumbered(1, fileDigest, atSecond(1))
	second := fileNumbered(2, fileDigest, atSecond(2))
	storeFile(t, repo, first)
	storeFile(t, repo, second)

	// Both live: the older one is the file this account has had all along.
	found, err := repo.FindByDigest(context.Background(), fileDigest, testCaller())
	if err != nil {
		t.Fatalf("FindByDigest() error = %v", err)
	}
	if found.ID != first.ID {
		t.Errorf("the lookup answered with %s, want the oldest row %s", found.ID, first.ID)
	}

	// The oldest goes into the bin: a live row is a file that is already usable,
	// and restoring a second one would be a change nobody asked for.
	bin(t, repo, first.ID, atSecond(3))
	found, err = repo.FindByDigest(context.Background(), fileDigest, testCaller())
	if err != nil {
		t.Fatalf("FindByDigest() error = %v", err)
	}
	if found.ID != second.ID {
		t.Errorf("the lookup answered with %s, want the live row %s", found.ID, second.ID)
	}

	// Both binned: the oldest of those, so that a second re-upload brings back the
	// same file as the first one did rather than a different one each time.
	bin(t, repo, second.ID, atSecond(4))
	found, err = repo.FindByDigest(context.Background(), fileDigest, testCaller())
	if err != nil {
		t.Fatalf("FindByDigest() error = %v", err)
	}
	if found.ID != first.ID || !found.InRecycleBin() {
		t.Errorf("the lookup answered with %+v, want the oldest binned row %s", found, first.ID)
	}
	if found.DeletedAt == nil {
		t.Error("a binned row read back without the moment it was binned")
	}

	if got := countFiles(t, resource); got != 2 {
		t.Errorf("%d rows are stored, want the two the fixture wrote", got)
	}
}

// TestDBTheDedupLookupIsScopedToOneAccount: two accounts uploading one file are
// two files, because a file belongs to an account and an answer that crossed that
// line would hand one account an id it cannot read (§2.3, AUTH-06).
func TestDBTheDedupLookupIsScopedToOneAccount(t *testing.T) {
	repo, _ := newRecycleDB(t)
	theirs := fileNumbered(1, fileDigest, atSecond(1))
	theirs.OwnerID = testOwnerID + 1
	storeFile(t, repo, theirs)

	if _, err := repo.FindByDigest(context.Background(), fileDigest, testCaller()); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindByDigest() error = %v, want %v for another account's row", err, ErrNotFound)
	}

	mine := fileNumbered(2, fileDigest, atSecond(2))
	storeFile(t, repo, mine)
	found, err := repo.FindByDigest(context.Background(), fileDigest, testCaller())
	if err != nil {
		t.Fatalf("FindByDigest() error = %v", err)
	}
	if found.ID != mine.ID {
		t.Errorf("the lookup answered with %s, want this account's own row %s", found.ID, mine.ID)
	}
}

// TestDBAFileInTheBinStillHoldsItsPath is the fact §2.14's restore is built on,
// and it is a fact no double can hold: `library_files_path_unique` is a unique
// key over every row, not over the live ones, because seekdb has no partial
// index. So a binned file has not given its path up — which is why a re-upload of
// its bytes restores the row rather than writing a second one.
func TestDBAFileInTheBinStillHoldsItsPath(t *testing.T) {
	repo, _ := newRecycleDB(t)
	held := fileNumbered(1, fileDigest, atSecond(1))
	storeFile(t, repo, held)
	bin(t, repo, held.ID, atSecond(2))

	// A different id, the same path: the schema refuses it, binned or not.
	second := fileNumbered(2, "aa"+fileDigest[2:], atSecond(3))
	second.LibraryPath = held.LibraryPath
	if err := repo.Create(context.Background(), second); err == nil {
		t.Fatal("the schema accepted two rows holding one path, so a restore could not reuse the row")
	}

	// And when the binned row is restored, the path is still its own — there is
	// nothing to rebuild, which is what §2.14's prose says and its table does not.
	if _, err := repo.Find(context.Background(), held.ID, testCaller()); err != nil {
		t.Fatalf("read the binned row back: %v", err)
	}
	if err := repo.SetDeleted(context.Background(), held.ID, false, atSecond(4), testCaller()); err != nil {
		t.Fatalf("restore the row: %v", err)
	}
	found, err := repo.Find(context.Background(), held.ID, testCaller())
	if err != nil {
		t.Fatalf("read the restored row back: %v", err)
	}
	if found.LibraryPath != held.LibraryPath || found.InRecycleBin() {
		t.Errorf("the restored row is %+v, want the same path and a live state", found)
	}
}

// TestDBSetDeletedWritesBothColumnsAndTheCheckHoldsThem: the two columns are one
// fact, the table refuses each way of writing half of it, and so a writer that
// set one without the other would be stopped by the database rather than stored
// — which is why the adapter writes them in one statement.
//
// Both halves are tried, because they are different mistakes: a flag without a
// moment says a file was binned and leaves the bin unsortable, and a moment
// without a flag says a file is live while the bin still holds a date for it.
func TestDBSetDeletedWritesBothColumnsAndTheCheckHoldsThem(t *testing.T) {
	repo, resource := newRecycleDB(t)
	record := fileNumbered(1, fileDigest, atSecond(1))
	storeFile(t, repo, record)

	// The two half-written pairs, on a row that is live and complete to begin
	// with. Going around the adapter is the only way to write one, which is the
	// point: the adapter cannot.
	if err := testdb.Try(resource,
		"UPDATE library_files SET deleted_at = ? WHERE id = ?", atSecond(5), record.ID); err == nil {
		t.Error("the table stored a moment without the flag, so the bin holds a date for a live file")
	}
	if err := testdb.Try(resource,
		"UPDATE library_files SET is_deleted = TRUE WHERE id = ?", record.ID); err == nil {
		t.Error("the table stored the flag without the moment, so a binned file has no date to sort by")
	}

	// Written as a pair, both arrive, and the moment is the one that was asked
	// for — the recycle bin is sorted by how long a file has been in it.
	bin(t, repo, record.ID, atSecond(2))
	found, err := repo.Find(context.Background(), record.ID, testCaller())
	if err != nil {
		t.Fatalf("read the binned row back: %v", err)
	}
	if !found.InRecycleBin() || found.DeletedAt == nil || !found.DeletedAt.Equal(atSecond(2)) {
		t.Fatalf("the binned row is %+v, want the flag and the moment together", found)
	}

	// And a restore clears both, which is the other half of the same CHECK.
	if err := repo.SetDeleted(context.Background(), record.ID, false, atSecond(6), testCaller()); err != nil {
		t.Fatalf("restore the row: %v", err)
	}
	found, err = repo.Find(context.Background(), record.ID, testCaller())
	if err != nil {
		t.Fatalf("read the restored row back: %v", err)
	}
	if found.InRecycleBin() || found.DeletedAt != nil {
		t.Errorf("the restored row is %+v, want neither column set", found)
	}
}

// TestDBDeleteRemovesTheRowAndSaysWhenThereWasNone. An update reports the same
// zero rows for "nothing matched" and "nothing changed", which is why SetDeleted
// reads back; a delete does not, and this is that it reports the truth.
func TestDBDeleteRemovesTheRowAndSaysWhenThereWasNone(t *testing.T) {
	repo, resource := newRecycleDB(t)
	record := fileNumbered(1, fileDigest, atSecond(1))
	storeFile(t, repo, record)

	if err := repo.Delete(context.Background(), record.ID, testCaller()); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if got := countFiles(t, resource); got != 0 {
		t.Errorf("%d rows are stored, want none", got)
	}
	if err := repo.Delete(context.Background(), record.ID, testCaller()); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete(a row that is gone) error = %v, want %v", err, ErrNotFound)
	}
	// And an account that does not own the row cannot remove it, which is a
	// delete reporting no rows rather than a refusal.
	other := fileNumbered(2, fileDigest, atSecond(2))
	other.OwnerID = testOwnerID + 1
	storeFile(t, repo, other)
	if err := repo.Delete(context.Background(), other.ID, testCaller()); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete(another account's row) error = %v, want %v", err, ErrNotFound)
	}
	if got := countFiles(t, resource); got != 1 {
		t.Errorf("%d rows are stored, want the one that was not the caller's to remove", got)
	}
}

// TestDBSearchListsOneHalfOfTheLibraryAtATime and narrows it: the state filter,
// the name, the category, the window and the page, all against the real
// predicates. The name match is the interesting one — the column is utf8mb4_bin,
// so a LIKE would be a byte comparison and its `%` would be a wildcard, which is
// why the adapter asks INSTR(LOWER(...), LOWER(...)) instead.
func TestDBSearchListsOneHalfOfTheLibraryAtATime(t *testing.T) {
	repo, _ := newRecycleDB(t)
	linear := fileNumbered(1, strings.Repeat("1", 64), atSecond(1))
	linear.Filename = "Linear-Algebra.md"
	exercise := fileNumbered(2, strings.Repeat("2", 64), atSecond(2))
	exercise.Filename = "线性代数习题.md"
	notes := fileNumbered(3, strings.Repeat("3", 64), atSecond(3))
	notes.Filename = "notes.pdf"
	notes.LibraryPath = "pdf/" + notes.ID + ".pdf"
	for _, record := range []FileRecord{linear, exercise, notes} {
		storeFile(t, repo, record)
	}
	bin(t, repo, notes.ID, atSecond(4))

	tests := []struct {
		name  string
		query SearchQuery
		want  []string
	}{
		{"the live files, newest first", SearchQuery{},
			[]string{exercise.ID, linear.ID}},
		{"the recycle bin", SearchQuery{Deleted: true}, []string{notes.ID}},
		{"a name in a case nobody typed", SearchQuery{Filename: "LINEAR-ALGEBRA"},
			[]string{linear.ID}},
		{"a name in the middle of a filename", SearchQuery{Filename: "代数"},
			[]string{exercise.ID}},
		{"a name that is spelled like a pattern", SearchQuery{Filename: "Linear*"}, nil},
		{"a category", SearchQuery{Category: "md"}, []string{exercise.ID, linear.ID}},
		{"a category nothing is stored under", SearchQuery{Category: "mp3"}, nil},
		{"a window that starts after everything", SearchQuery{Since: atSecond(9)}, nil},
		{"a window that ends before everything", SearchQuery{Until: atSecond(1)}, nil},
		{"a window over the first two seconds", SearchQuery{Since: atSecond(1), Until: atSecond(3)},
			[]string{exercise.ID, linear.ID}},
		{"a second page of one", SearchQuery{Limit: 1, Offset: 1}, []string{linear.ID}},
		{"a page past the end", SearchQuery{Offset: 9}, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.query.Caller = testCaller()
			found, err := repo.Search(context.Background(), test.query)
			if err != nil {
				t.Fatalf("Search() error = %v", err)
			}
			if len(found) != len(test.want) {
				t.Fatalf("Search() found %d files, want %d: %v", len(found), len(test.want), idsOf(found))
			}
			for index, id := range test.want {
				if found[index].ID != id {
					t.Errorf("found[%d] = %s, want %s", index, found[index].ID, id)
				}
			}
		})
	}

	// And the same query as a second account sees nothing, because every read is
	// narrowed by a WHERE clause rather than filtered afterwards.
	found, err := repo.Search(context.Background(), SearchQuery{Caller: otherCaller()})
	if err != nil {
		t.Fatalf("Search() as another account: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("a second account's listing holds %d files, want none: %v", len(found), idsOf(found))
	}
}

// TestDBReferencesFollowsTheChainThroughDocuments is AC-15's question, and it is
// two queries because a file is used in two ways. A document names the file; a
// reading material names a *document* and never a file, so its claim is invisible
// to anybody who only reads the file's own column — and that is exactly the claim
// a purge must not miss.
func TestDBReferencesFollowsTheChainThroughDocuments(t *testing.T) {
	repo, resource := newRecycleDB(t)
	record := fileNumbered(1, fileDigest, atSecond(1))
	storeFile(t, repo, record)
	// A second file, whose document will hold a material that must not be counted
	// against the first one. Two files rather than two documents, because the
	// schema allows one document per file per place (00008).
	other := fileNumbered(2, strings.Repeat("9", 64), atSecond(2))
	storeFile(t, repo, other)

	addDocument(t, resource, "doc_attachment", record.ID, atSecond(3))
	addDocument(t, resource, "doc_unrelated", other.ID, atSecond(3))
	addMaterial(t, resource, "mat_first", "doc_attachment", "第一课")
	addMaterial(t, resource, "mat_second", "doc_unrelated", "第二课")

	references, err := repo.References(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("References() error = %v", err)
	}
	// The document parsed from the file, and the material built on that document
	// — which names the document and never the file. The other file's document
	// and the material on it are not here.
	if len(references) != 2 {
		t.Fatalf("References() = %+v, want the document and the material on it", references)
	}
	kinds := map[string]int{}
	for _, reference := range references {
		kinds[reference.Kind]++
	}
	if kinds[ReferenceDocument] != 1 || kinds[ReferenceReadingMaterial] != 1 {
		t.Errorf("References() = %+v, want one document and one material", references)
	}
	// The names are what a person is told to go and deal with, so a document with
	// no collection has to be called something they can find.
	names := referenceNames(references)
	for _, want := range []string{"attachment", "第一课"} {
		if !strings.Contains(names, want) {
			t.Errorf("References() names %q, want it to name %q", names, want)
		}
	}

	// A file nothing points at has no references at all — the empty answer is what
	// lets a purge go ahead.
	empty, err := repo.References(context.Background(), other.ID+"x")
	if err != nil {
		t.Fatalf("References(an unknown file) error = %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("References(an unknown file) = %+v, want none", empty)
	}
}

// TestDBDeletingAFileTakesItsDocumentsWithIt is why the check above has to happen
// before the delete rather than after it. The foreign key cascades: removing a
// library file removes the documents parsed from it, and it does *not* remove the
// reading materials pointing at those documents, because `reading_materials.
// content_id` has no foreign key at all. So a purge that skipped the check would
// leave materials naming documents that are gone.
func TestDBDeletingAFileTakesItsDocumentsWithIt(t *testing.T) {
	repo, resource := newRecycleDB(t)
	record := fileNumbered(1, fileDigest, atSecond(1))
	storeFile(t, repo, record)
	addDocument(t, resource, "doc_attachment", record.ID, atSecond(2))
	addMaterial(t, resource, "mat_first", "doc_attachment", "第一课")

	bin(t, repo, record.ID, atSecond(3))
	if err := repo.Delete(context.Background(), record.ID, testCaller()); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if got := countFiles(t, resource); got != 0 {
		t.Errorf("%d library rows are stored, want none", got)
	}
	if got := countRows(t, resource, "documents"); got != 0 {
		t.Errorf("%d documents are left, want the cascade to have taken them", got)
	}
	// And the material is still here, naming a document that is gone. That is the
	// state AC-15's check is written to prevent, and this is the proof that the
	// foreign key would not prevent it.
	if got := countRows(t, resource, "reading_materials"); got != 1 {
		t.Errorf("%d materials are left, want the one that dangles", got)
	}
	references, err := repo.References(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("References() error = %v", err)
	}
	if len(references) != 0 {
		t.Errorf("References(a destroyed file) = %+v, want none: there is nothing left to point at it", references)
	}
}

func idsOf(records []FileRecord) []string {
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.ID)
	}
	return ids
}

func referenceNames(references []Reference) string {
	names := make([]string, 0, len(references))
	for _, reference := range references {
		names = append(names, reference.Name)
	}
	return strings.Join(names, " ")
}

// addDocument writes one document the way the schema writes one, with no
// collection: an attachment is the same row type and needs no knowledge base
// behind it, which keeps this fixture to one table.
func addDocument(t *testing.T, resource *data.Resource, id, fileID string, createdAt time.Time) {
	t.Helper()
	document := models.Document{
		DocumentID: id, FileID: fileID, Filename: "第一课.md", Engine: "text",
		TextChars: 0, ChunkSize: 800, ChunkOverlap: 100, ActiveRevision: 1,
		VectorState: "pending", CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	if err := resource.GORM().Create(&document).Error; err != nil {
		t.Fatalf("write the document %s: %v", id, err)
	}
}

// addMaterial writes one reading material, which points at a document and never
// at a file — the whole reason References is two queries.
func addMaterial(t *testing.T, resource *data.Resource, id, documentID, filename string) {
	t.Helper()
	material := models.ReadingMaterial{
		MaterialID: id, ContentID: &documentID, Filename: &filename,
		CreatedAt: atSecond(5), OwnerID: data.OwnerPointer(testOwnerID),
	}
	if err := resource.GORM().Create(&material).Error; err != nil {
		t.Fatalf("write the reading material %s: %v", id, err)
	}
}

func countRows(t *testing.T, resource *data.Resource, table string) int64 {
	t.Helper()
	var count int64
	err := resource.GORM().Raw("SELECT COUNT(*) FROM `" + table + "`").Scan(&count).Error
	if err != nil {
		t.Fatalf("count the rows of %s: %v", table, err)
	}
	return count
}
