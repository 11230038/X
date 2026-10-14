package kb

// The tests in this file make claims about storage: the order a listing
// promises, the lock a concurrent write depends on, the foreign key that has to
// fire. A test double cannot attest to any of it — it can only restate the
// contract and then agree with itself — so these run against a real seekdb, the
// same one a deployment runs against, in a database of this package's own (see
// internal/data/testdb). fakeRepository is still here for the tests whose
// subject is the service's own logic or a storage failure that has to be
// injected, but it is no longer evidence about SQL.
//
// Two rules follow from one database per package: nothing in these files calls
// t.Parallel, and every test starts on an empty table.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"backend/internal/data"
	"backend/internal/data/testdb"
)

// dbTables are the tables these tests write, children first: a document holds a
// foreign key into both a base and a library file, so it goes before either.
var dbTables = []string{"document_chunks", "documents", "knowledge_bases", "library_files"}

// newDBRepository returns the storage adapter under test, over this package's
// own migrated database, with the tables above emptied.
func newDBRepository(t *testing.T) (*GORMRepository, *data.Resource) {
	t.Helper()
	resource := testdb.Open(t, "kb")
	testdb.Reset(t, resource, dbTables...)
	return NewGORMRepository(resource.GORM()), resource
}

// atSecond is a whole second on a fixed day.
//
// Timestamps are spelled out rather than taken from the clock because DATETIME
// keeps whole seconds: rows written in the same second tie, and an ordering
// asserted over tied rows asserts whatever the plan happened to do that time.
func atSecond(second int) time.Time {
	return time.Date(2026, 10, 1, 12, 0, second, 0, time.UTC)
}

// libraryFileID is an id the library's own path rule accepts, derived from a
// small number so that a fixture can name one.
func libraryFileID(number int) string {
	return fmt.Sprintf("lib_%032x", number)
}

// addLibraryFile writes the row a document's file foreign key needs.
//
// The path is not decoration: migration 00005 checks that it is
// md/lib_<32 lowercase hex digits>.md and that the name before the extension is
// the id, so a document fixture cannot invent a file id — it has to be one the
// library would have stored at that path.
//
// The owner is not decoration either. A document attached to no collection is
// read through the file it came from, so an ownerless file row here would make
// the attachment invisible to the account these tests act as, and the failure
// would look like a broken query rather than a fixture that forgot a column.
func addLibraryFile(t *testing.T, resource *data.Resource, id string) {
	t.Helper()
	sum := sha256.Sum256([]byte(id))
	testdb.Exec(t, resource, "INSERT INTO library_files "+
		"(id, sha256, filename, mime_type, size_bytes, library_path, is_deleted, owner_id) "+
		"VALUES (?, ?, ?, 'text/markdown', 0, ?, FALSE, ?)",
		id, hex.EncodeToString(sum[:]), id+".md", "md/"+id+".md", testOwnerID)
}

// createBase stores one base as of a time the caller chooses.
func createBase(t *testing.T, repo *GORMRepository, id string, createdAt time.Time) {
	t.Helper()
	record := KnowledgeBase{
		ID: id, Name: id, Provider: ProviderNative,
		EmbeddingModel: "test-embed", OwnerID: testOwnerID, CreatedAt: createdAt,
	}
	if err := repo.CreateKnowledgeBase(context.Background(), record); err != nil {
		t.Fatalf("create knowledge base %s: %v", id, err)
	}
}

func setDefault(t *testing.T, repo *GORMRepository, kbID string) {
	t.Helper()
	if err := repo.SetDefaultKnowledgeBase(context.Background(), kbID, testCaller()); err != nil {
		t.Fatalf("set the default knowledge base to %s: %v", kbID, err)
	}
}

func listBases(t *testing.T, repo *GORMRepository) []KnowledgeBase {
	t.Helper()
	bases, err := repo.KnowledgeBases(context.Background(), testCaller())
	if err != nil {
		t.Fatalf("list knowledge bases: %v", err)
	}
	return bases
}

func baseIDs(bases []KnowledgeBase) []string {
	ids := make([]string, 0, len(bases))
	for _, base := range bases {
		ids = append(ids, base.ID)
	}
	return ids
}

// assertOneDefault reports the listing when it does not hold exactly one
// default. Every write path into these rows exists to keep that true.
func assertOneDefault(t *testing.T, repo *GORMRepository) {
	t.Helper()
	bases := listBases(t, repo)
	defaults := 0
	for _, base := range bases {
		if base.IsDefault {
			defaults++
		}
	}
	if defaults != 1 {
		t.Errorf("%d of the %d bases are default, want exactly one: %v",
			defaults, len(bases), baseIDs(bases))
	}
}

// countRows is how many rows a table holds, for what a delete is supposed to
// have taken with it.
func countRows(t *testing.T, resource *data.Resource, table string) int64 {
	t.Helper()
	var count int64
	err := resource.GORM().Raw("SELECT COUNT(*) FROM `" + table + "`").Scan(&count).Error
	if err != nil {
		t.Fatalf("count the rows of %s: %v", table, err)
	}
	return count
}

// dbDocument is one document these tests write.
type dbDocument struct {
	ID        string
	KBID      string // empty is an attachment
	FileID    string
	Filename  string
	Revision  int
	Text      string
	Chunks    []string
	CreatedAt time.Time
	// UpdatedAt is only written by the rebuild path; it is read from CreatedAt
	// when a fixture does not say, because a zero time is not a value this
	// column takes.
	UpdatedAt time.Time
}

// addDocument writes one document and its chunks through the adapter's own
// write path, so that what is read back was written the way the application
// writes it.
func addDocument(t *testing.T, repo *GORMRepository, doc dbDocument) Document {
	t.Helper()
	write := documentWrite(t, doc)
	if err := repo.CreateDocument(context.Background(), write); err != nil {
		t.Fatalf("create document %s: %v", doc.ID, err)
	}
	return write.Document
}

// chunkSpans locates each chunk in the document's text, the way the service
// lays them out, and fails when a fixture's text does not contain one of its
// own chunks — that is a mistake in the fixture, not in what is under test.
//
// The search for a chunk is by bytes, because that is what strings does, and
// the span it returns is in characters, because that is the unit the service
// stores and the unit a citation is read back in. The two are the same number
// until the text stops being ASCII, which is why the conversion is here rather
// than left to the fixtures: a Chinese fixture measured in bytes would store a
// window that reads back as the wrong text.
func chunkSpans(t *testing.T, text string, chunks []string) [][2]int64 {
	t.Helper()
	spans := make([][2]int64, 0, len(chunks))
	cursor := 0
	for _, chunk := range chunks {
		offset := strings.Index(text[cursor:], chunk)
		if offset < 0 {
			t.Fatalf("the chunk %q is not in the text after character %d", chunk, cursor)
		}
		start := cursor + offset
		cursor = start + len(chunk)
		spans = append(spans, [2]int64{
			int64(utf8.RuneCountInString(text[:start])),
			int64(utf8.RuneCountInString(text[:cursor])),
		})
	}
	return spans
}

func TestDBTheFirstBaseIsTheDefaultOne(t *testing.T) {
	repo, _ := newDBRepository(t)
	createBase(t, repo, "kb_first", atSecond(0))
	createBase(t, repo, "kb_second", atSecond(1))

	// The mark is on the first base and only on it: a second base created while
	// one is already default does not claim the mark.
	bases := listBases(t, repo)
	if len(bases) != 2 {
		t.Fatalf("listed %d bases, want 2: %v", len(bases), baseIDs(bases))
	}
	if !bases[0].IsDefault || bases[0].ID != "kb_first" {
		t.Errorf("the first base listed is %+v, want kb_first and default", bases[0])
	}
	if bases[1].IsDefault {
		t.Errorf("kb_second is default as well, want only the first base to be")
	}
	assertOneDefault(t, repo)
}

func TestDBTheListingIsDefaultFirstThenOldest(t *testing.T) {
	repo, _ := newDBRepository(t)
	createBase(t, repo, "kb_a", atSecond(1))
	createBase(t, repo, "kb_b", atSecond(2))
	createBase(t, repo, "kb_c", atSecond(3))

	want := []string{"kb_a", "kb_b", "kb_c"}
	if got := baseIDs(listBases(t, repo)); !slices.Equal(got, want) {
		t.Errorf("the listing is %v, want %v", got, want)
	}

	// Moving the mark moves the row to the front without disturbing the rest:
	// the order is the mark first, then the oldest.
	setDefault(t, repo, "kb_c")
	want = []string{"kb_c", "kb_a", "kb_b"}
	if got := baseIDs(listBases(t, repo)); !slices.Equal(got, want) {
		t.Errorf("after the mark moved the listing is %v, want %v", got, want)
	}
}

func TestDBTwoCreatorsOnAnEmptyTableLeaveOneDefault(t *testing.T) {
	repo, _ := newDBRepository(t)

	// Four creators ask at the same moment about a table with no default yet:
	// each one reads the table, finds no mark, and claims it. The lock the
	// write takes is the only thing standing between that and four defaults,
	// and a double cannot say whether it holds.
	const creators = 4
	start := make(chan struct{})
	errs := make([]error, creators)
	var wg sync.WaitGroup
	for i := range creators {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = repo.CreateKnowledgeBase(context.Background(), KnowledgeBase{
				ID: fmt.Sprintf("kb_%d", i), Name: "base", Provider: ProviderNative,
				EmbeddingModel: "test-embed", OwnerID: testOwnerID, CreatedAt: atSecond(i),
			})
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("creator %d failed: %v", i, err)
		}
	}
	assertOneDefault(t, repo)
}

func TestDBConcurrentSettersLeaveOneDefault(t *testing.T) {
	repo, _ := newDBRepository(t)
	ids := []string{"kb_a", "kb_b", "kb_c", "kb_d"}
	for i, id := range ids {
		createBase(t, repo, id, atSecond(i))
	}

	// Each setter clears the mark and sets it inside one transaction. Two that
	// interleave between those two writes leave two defaults behind, which is
	// what the lock on the read is for.
	start := make(chan struct{})
	errs := make([]error, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = repo.SetDefaultKnowledgeBase(context.Background(), id, testCaller())
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("setter for %s failed: %v", ids[i], err)
		}
	}
	assertOneDefault(t, repo)
}

func TestDBDeletingTheDefaultPromotesTheOldestRemaining(t *testing.T) {
	repo, _ := newDBRepository(t)
	createBase(t, repo, "kb_a", atSecond(2))
	createBase(t, repo, "kb_b", atSecond(3))
	createBase(t, repo, "kb_c", atSecond(4))
	setDefault(t, repo, "kb_c")

	if err := repo.DeleteKnowledgeBase(context.Background(), "kb_c", testCaller()); err != nil {
		t.Fatalf("delete the default base: %v", err)
	}

	bases := listBases(t, repo)
	if got, want := baseIDs(bases), []string{"kb_a", "kb_b"}; !slices.Equal(got, want) {
		t.Fatalf("the listing is %v, want %v", got, want)
	}
	if !bases[0].IsDefault {
		t.Error("no base took the mark the deleted one left, want the oldest remaining to")
	}
	assertOneDefault(t, repo)
}

func TestDBDeletingABaseTakesItsDocumentsAndChunks(t *testing.T) {
	repo, resource := newDBRepository(t)
	createBase(t, repo, "kb_a", atSecond(0))
	fileID := libraryFileID(1)
	addLibraryFile(t, resource, fileID)
	addDocument(t, repo, dbDocument{
		ID: "doc_1", KBID: "kb_a", FileID: fileID, CreatedAt: atSecond(0),
		Text: "alpha beta", Chunks: []string{"alpha", "beta"},
	})
	if got := countRows(t, resource, "document_chunks"); got != 2 {
		t.Fatalf("the fixture stored %d chunks, want 2", got)
	}

	if err := repo.DeleteKnowledgeBase(context.Background(), "kb_a", testCaller()); err != nil {
		t.Fatalf("delete the base: %v", err)
	}

	// The cascade runs from the base to its documents and from there into
	// document_chunks, which is the table carrying the vector index — the one
	// shape migration 00007's header says can turn a delete into an internal
	// error. The counts are what says it did not.
	if got := countRows(t, resource, "documents"); got != 0 {
		t.Errorf("%d documents survived the base, want none", got)
	}
	if got := countRows(t, resource, "document_chunks"); got != 0 {
		t.Errorf("%d chunks survived the base, want none", got)
	}
	// The file stays: it belongs to the library. Only what was parsed from it
	// for this base is gone.
	if got := countRows(t, resource, "library_files"); got != 1 {
		t.Errorf("%d library files are left, want the one the document was parsed from", got)
	}
}
