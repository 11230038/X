package kb

// The document half of the storage contract: which row a file resolves to, and
// what order a listing comes back in. None of it is knowable from a double,
// which is why it is checked here, against a real seekdb. The fixtures live in
// repository_db_test.go; the rebuild half is in
// repository_revision_contract_test.go.

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
	"unicode/utf8"

	"backend/internal/data"
	"backend/internal/data/testdb"
	"backend/internal/parse"
)

// documentWrite turns a fixture into the write the adapter takes, laying the
// chunks over the text the way the service does.
//
// UpdatedAt is written from CreatedAt when the fixture does not say: the
// rebuild path writes update_time explicitly from a time.Time, and a zero one
// is not a value this column takes.
func documentWrite(t *testing.T, doc dbDocument) DocumentWrite {
	t.Helper()
	if doc.Revision == 0 {
		doc.Revision = 1
	}
	if doc.Filename == "" {
		doc.Filename = doc.FileID + ".md"
	}
	updated := doc.UpdatedAt
	if updated.IsZero() {
		updated = doc.CreatedAt
	}
	write := DocumentWrite{
		Document: Document{
			ID: doc.ID, KBID: doc.KBID, FileID: doc.FileID, Filename: doc.Filename,
			Engine: parse.EngineText, TextChars: int64(utf8.RuneCountInString(doc.Text)),
			ChunkSize: 20, ChunkOverlap: 5, ActiveRevision: doc.Revision,
			VectorState: VectorStatePending,
			CreatedAt:   doc.CreatedAt, UpdatedAt: updated,
		},
		Text: doc.Text,
	}
	for ordinal, span := range chunkSpans(t, doc.Text, doc.Chunks) {
		write.Chunks = append(write.Chunks, ChunkRecord{
			ChunkID: ChunkID(doc.ID, doc.Revision, ordinal), DocumentID: doc.ID,
			KBID: doc.KBID, Revision: doc.Revision, Ordinal: ordinal,
			Content: doc.Chunks[ordinal], CharStart: span[0], CharEnd: span[1],
		})
	}
	return write
}

func TestDBDocumentOfFilePrefersTheAttachment(t *testing.T) {
	repo, resource := newDBRepository(t)
	createBase(t, repo, "kb_a", atSecond(0))
	fileID := libraryFileID(1)
	addLibraryFile(t, resource, fileID)

	// The attachment is written later than the base entry, so it can only win
	// by being an attachment: the ordering says `kb_id IS NULL` first, not
	// oldest first.
	addDocument(t, repo, dbDocument{
		ID: "doc_in_base", KBID: "kb_a", FileID: fileID,
		CreatedAt: atSecond(1), Text: "in the base", Chunks: []string{"in the base"},
	})
	addDocument(t, repo, dbDocument{
		ID: "doc_attachment", FileID: fileID,
		CreatedAt: atSecond(2), Text: "an attachment", Chunks: []string{"an attachment"},
	})

	document, err := repo.DocumentOfFile(context.Background(), fileID)
	if err != nil {
		t.Fatalf("document of file: %v", err)
	}
	if document.ID != "doc_attachment" {
		t.Errorf("the file resolves to %s, want the attachment", document.ID)
	}
}

func TestDBDocumentOfFileTakesTheOldestThenTheLowestID(t *testing.T) {
	repo, resource := newDBRepository(t)
	createBase(t, repo, "kb_a", atSecond(0))
	createBase(t, repo, "kb_b", atSecond(0))

	// One file in two collections is two documents — one per place — and that
	// is the only way this file gets more than one row, so the ordering rules
	// are checked across places.
	//
	// The first file's two rows disagree about which rule should win: the
	// newest has the lower id. The oldest is what the ordering takes.
	oldest, tied := libraryFileID(1), libraryFileID(2)
	addLibraryFile(t, resource, oldest)
	addLibraryFile(t, resource, tied)
	addDocument(t, repo, dbDocument{
		ID: "doc_z", KBID: "kb_a", FileID: oldest, CreatedAt: atSecond(1),
		Text: "oldest", Chunks: []string{"oldest"},
	})
	addDocument(t, repo, dbDocument{
		ID: "doc_a", KBID: "kb_b", FileID: oldest, CreatedAt: atSecond(5),
		Text: "newer but lower id", Chunks: []string{"newer"},
	})

	// The second file's two rows are of the same second, which DATETIME keeps
	// at whole seconds, so the id is what has to decide.
	addDocument(t, repo, dbDocument{
		ID: "doc_z2", KBID: "kb_a", FileID: tied, CreatedAt: atSecond(6),
		Text: "same second, higher id", Chunks: []string{"same second"},
	})
	addDocument(t, repo, dbDocument{
		ID: "doc_a2", KBID: "kb_b", FileID: tied, CreatedAt: atSecond(6),
		Text: "same second, lower id", Chunks: []string{"same second"},
	})

	for _, scenario := range []struct{ fileID, want string }{
		{fileID: oldest, want: "doc_z"},
		{fileID: tied, want: "doc_a2"},
	} {
		document, err := repo.DocumentOfFile(context.Background(), scenario.fileID)
		if err != nil {
			t.Fatalf("document of file %s: %v", scenario.fileID, err)
		}
		if document.ID != scenario.want {
			t.Errorf("file %s resolves to %s, want %s", scenario.fileID, document.ID, scenario.want)
		}
	}
}

func TestDBAListingIsOneBaseOrTheAttachments(t *testing.T) {
	repo, resource := newDBRepository(t)
	createBase(t, repo, "kb_a", atSecond(0))
	createBase(t, repo, "kb_b", atSecond(0))
	for _, file := range []string{libraryFileID(1), libraryFileID(2), libraryFileID(3)} {
		addLibraryFile(t, resource, file)
	}

	// Two of kb_a's documents are of the same second, so the id decides between
	// them; one file is in both collections, which is allowed, because a
	// document is one per file per place.
	addDocument(t, repo, dbDocument{
		ID: "doc_a3", KBID: "kb_a", FileID: libraryFileID(1), CreatedAt: atSecond(2),
		Text: "third", Chunks: []string{"third"},
	})
	addDocument(t, repo, dbDocument{
		ID: "doc_a1", KBID: "kb_a", FileID: libraryFileID(2), CreatedAt: atSecond(1),
		Text: "first", Chunks: []string{"first"},
	})
	addDocument(t, repo, dbDocument{
		ID: "doc_a2", KBID: "kb_a", FileID: libraryFileID(3), CreatedAt: atSecond(2),
		Text: "second", Chunks: []string{"second"},
	})
	addDocument(t, repo, dbDocument{
		ID: "doc_b1", KBID: "kb_b", FileID: libraryFileID(2), CreatedAt: atSecond(1),
		Text: "other base", Chunks: []string{"other base"},
	})
	addDocument(t, repo, dbDocument{
		ID: "doc_attachment", FileID: libraryFileID(1), CreatedAt: atSecond(1),
		Text: "attached", Chunks: []string{"attached"},
	})

	// A base lists its own documents in the order they were added, and the same
	// second is broken by id.
	documents, err := repo.Documents(context.Background(), "kb_a")
	if err != nil {
		t.Fatalf("documents of kb_a: %v", err)
	}
	if got, want := documentIDs(documents), []string{"doc_a1", "doc_a2", "doc_a3"}; !slices.Equal(got, want) {
		t.Errorf("kb_a lists %v, want %v", got, want)
	}

	// An empty id asks for the attachments, which is a different question from
	// the documents of a base named "".
	attachments, err := repo.Documents(context.Background(), "")
	if err != nil {
		t.Fatalf("documents without a base: %v", err)
	}
	if got, want := documentIDs(attachments), []string{"doc_attachment"}; !slices.Equal(got, want) {
		t.Errorf("the attachments are %v, want %v", got, want)
	}
}

func TestDBDocumentForFileTellsAnAttachmentFromABaseEntry(t *testing.T) {
	repo, resource := newDBRepository(t)
	createBase(t, repo, "kb_a", atSecond(0))
	fileID := libraryFileID(1)
	addLibraryFile(t, resource, fileID)
	addDocument(t, repo, dbDocument{
		ID: "doc_in_base", KBID: "kb_a", FileID: fileID,
		CreatedAt: atSecond(1), Text: "in the base", Chunks: []string{"in the base"},
	})
	addDocument(t, repo, dbDocument{
		ID: "doc_attachment", FileID: fileID,
		CreatedAt: atSecond(1), Text: "attached", Chunks: []string{"attached"},
	})

	// An empty base means the attachment, and it has to reach a row whose kb_id
	// is NULL: `= NULL` matches nothing at all, which is what the null-safe
	// comparison in the query is there for.
	attachment, err := repo.DocumentForFile(context.Background(), fileID, "")
	if err != nil {
		t.Fatalf("the attachment of the file: %v", err)
	}
	if attachment.ID != "doc_attachment" {
		t.Errorf("an empty base resolves to %s, want the attachment", attachment.ID)
	}

	inBase, err := repo.DocumentForFile(context.Background(), fileID, "kb_a")
	if err != nil {
		t.Fatalf("the file inside kb_a: %v", err)
	}
	if inBase.ID != "doc_in_base" {
		t.Errorf("kb_a resolves to %s, want the base's own document", inBase.ID)
	}

	// A file that was parsed for one base is not parsed for another, and that
	// is not an error the caller has to guess at.
	if _, err := repo.DocumentForFile(context.Background(), fileID, "kb_nowhere"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a file in an unknown base: %v, want %v", err, ErrNotFound)
	}
}

func TestDBADocumentCannotNameAFileThatIsNotThere(t *testing.T) {
	repo, resource := newDBRepository(t)

	// documents.file_id is a real foreign key, so the fixtures of every test
	// here have to store a library row first — and a document referencing a
	// file that was never stored is refused rather than left dangling.
	write := documentWrite(t, dbDocument{
		ID: "doc_1", KBID: "", FileID: libraryFileID(9),
		CreatedAt: atSecond(1), Text: "orphan", Chunks: []string{"orphan"},
	})
	if err := repo.CreateDocument(context.Background(), write); err == nil {
		t.Error("a document for a file that is not in the library was accepted, want a foreign key failure")
	}
	if got := countRows(t, resource, "documents"); got != 0 {
		t.Errorf("%d documents were written, want none", got)
	}
	if got := countRows(t, resource, "document_chunks"); got != 0 {
		t.Errorf("%d chunks were written, want none", got)
	}
}

func TestDBOneDocumentPerFilePerPlace(t *testing.T) {
	repo, resource := newDBRepository(t)
	createBase(t, repo, "kb_a", atSecond(0))
	createBase(t, repo, "kb_b", atSecond(0))
	fileID, otherFile := libraryFileID(1), libraryFileID(2)
	addLibraryFile(t, resource, fileID)
	addLibraryFile(t, resource, otherFile)

	first := addDocument(t, repo, dbDocument{
		ID: "doc_in_a", KBID: "kb_a", FileID: fileID, CreatedAt: atSecond(1),
		Text: "alpha", Chunks: []string{"alpha"},
	})

	// The same file in the same place a second time: refused, and reported as
	// the lost race it is rather than as a database error. This is the branch
	// AddDocument recovers from.
	again := documentWrite(t, dbDocument{
		ID: "doc_in_a_again", KBID: "kb_a", FileID: fileID, CreatedAt: atSecond(2),
		Text: "alpha again", Chunks: []string{"alpha"},
	})
	if err := repo.CreateDocument(context.Background(), again); !errors.Is(err, ErrConflict) {
		t.Errorf("a second document of one file in one base: %v, want %v", err, ErrConflict)
	}
	if got := countRows(t, resource, "documents"); got != 1 {
		t.Errorf("%d documents are stored, want the first one only", got)
	}

	// The same file in another base is a different place, and a different
	// document.
	second := addDocument(t, repo, dbDocument{
		ID: "doc_in_b", KBID: "kb_b", FileID: fileID, CreatedAt: atSecond(3),
		Text: "beta", Chunks: []string{"beta"},
	})
	if second.ID != "doc_in_b" {
		t.Fatalf("the document in kb_b is %s, want doc_in_b", second.ID)
	}

	// The attachment of that file is a third place, and the one case a plain
	// unique index over (file_id, kb_id) would have let through: its kb_id is
	// NULL, and a unique index treats every NULL as distinct.
	attachment := addDocument(t, repo, dbDocument{
		ID: "doc_attachment", FileID: fileID, CreatedAt: atSecond(4),
		Text: "gamma", Chunks: []string{"gamma"},
	})
	// A second attachment of the same file is refused like any other second
	// document in one place.
	secondAttachment := documentWrite(t, dbDocument{
		ID: "doc_attachment_again", FileID: fileID, CreatedAt: atSecond(5),
		Text: "gamma again", Chunks: []string{"gamma"},
	})
	if err := repo.CreateDocument(context.Background(), secondAttachment); !errors.Is(err, ErrConflict) {
		t.Errorf("a second attachment of one file: %v, want %v", err, ErrConflict)
	}

	// Which leaves the file with one document in each of the three places it
	// was put, and the read paths resolving each of them.
	stored, err := repo.DocumentForFile(context.Background(), fileID, "kb_a")
	if err != nil || stored.ID != first.ID {
		t.Errorf("kb_a resolves to %+v (err %v), want %s", stored, err, first.ID)
	}
	stored, err = repo.DocumentForFile(context.Background(), fileID, "")
	if err != nil || stored.ID != attachment.ID {
		t.Errorf("the attachment resolves to %+v (err %v), want %s", stored, err, attachment.ID)
	}
	if got := countRows(t, resource, "documents"); got != 3 {
		t.Errorf("%d documents are stored, want one per place", got)
	}
}

func TestDBDeletingAFileTakesWhatWasParsedFromIt(t *testing.T) {
	repo, resource := newDBRepository(t)
	createBase(t, repo, "kb_a", atSecond(0))
	fileID := libraryFileID(1)
	addLibraryFile(t, resource, fileID)
	addDocument(t, repo, dbDocument{
		ID: "doc_1", KBID: "kb_a", FileID: fileID, CreatedAt: atSecond(0),
		Text: "alpha beta", Chunks: []string{"alpha", "beta"},
	})

	// documents.file_id is indexed now — the unique key that makes one document
	// per file per place — and it is also a foreign key. That combination is
	// what migration 00007's header warns about, for the table that carries the
	// vector index: deleting from the parent then fails with an internal error.
	// documents carries no vector index, and this is the test that says so.
	testdb.Exec(t, resource, "DELETE FROM library_files WHERE id = ?", fileID)

	if got := countRows(t, resource, "documents"); got != 0 {
		t.Errorf("%d documents outlived the file they were parsed from, want none", got)
	}
	if got := countRows(t, resource, "document_chunks"); got != 0 {
		t.Errorf("%d chunks outlived the file they were parsed from, want none", got)
	}
}

// writeRevision stores a document with one revision of chunks and returns what
// it wrote, which the tests about rebuilding start from.
func writeRevision(t *testing.T, repo *GORMRepository, resource *data.Resource,
	id string, at time.Time, text string, chunks []string) Document {
	t.Helper()
	createBase(t, repo, "kb_a", atSecond(0))
	fileID := libraryFileID(1)
	addLibraryFile(t, resource, fileID)
	return addDocument(t, repo, dbDocument{
		ID: id, KBID: "kb_a", FileID: fileID, CreatedAt: at, Text: text, Chunks: chunks,
	})
}

func documentIDs(documents []Document) []string {
	ids := make([]string, 0, len(documents))
	for _, document := range documents {
		ids = append(ids, document.ID)
	}
	return ids
}

func chunkIDsOf(t *testing.T, repo *GORMRepository, documentID string, revision int) []string {
	t.Helper()
	chunks, err := repo.Chunks(context.Background(), documentID, revision)
	if err != nil {
		t.Fatalf("the chunks of %s revision %d: %v", documentID, revision, err)
	}
	ids := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		ids = append(ids, chunk.ChunkID)
	}
	return ids
}
