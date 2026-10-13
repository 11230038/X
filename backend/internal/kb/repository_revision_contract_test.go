package kb

// The rebuild half of the storage contract: ReplaceRevision is the one write
// that moves a document from one revision to the next, and the guard on it is
// what decides which of two rebuilds started from the same revision lands. The
// fixtures live in repository_db_test.go, and the fixtures the two files share
// live in repository_document_contract_test.go.

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestDBReplaceRevisionMovesVisibilityInOneStep(t *testing.T) {
	repo, resource := newDBRepository(t)
	first := writeRevision(t, repo, resource, "doc_1", atSecond(1), "alpha beta", []string{"alpha", "beta"})
	second := documentWrite(t, dbDocument{
		ID: "doc_1", KBID: "kb_a", FileID: first.FileID, Revision: 2,
		CreatedAt: atSecond(2), Text: "alpha beta gamma", Chunks: []string{"alpha", "beta gamma"},
	})

	before := chunkIDsOf(t, repo, "doc_1", 1)
	if got := activeChunkCount(t, repo, before); got != 2 {
		t.Fatalf("%d of the first revision's chunks are active, want 2", got)
	}
	if err := repo.ReplaceRevision(context.Background(), second, 1); err != nil {
		t.Fatalf("replace the revision: %v", err)
	}

	// The first revision's rows are still there — a rebuild does not delete
	// what a citation may already point at — but nothing reads them as current.
	if got := len(chunkIDsOf(t, repo, "doc_1", 1)); got != 2 {
		t.Errorf("the first revision has %d chunks after the switch, want 2 kept", got)
	}
	if got := activeChunkCount(t, repo, before); got != 0 {
		t.Errorf("%d of the first revision's chunks are still active, want none", got)
	}
	after := chunkIDsOf(t, repo, "doc_1", 2)
	if len(after) != 2 {
		t.Fatalf("the second revision has %d chunks, want 2", len(after))
	}
	if got := activeChunkCount(t, repo, after); got != 2 {
		t.Errorf("%d of the second revision's chunks are active, want all of them", got)
	}

	// The pointer and the flag are one decision: the document says which
	// revision is current, and the chunks it points at are exactly the active
	// ones.
	document, err := repo.Document(context.Background(), "doc_1")
	if err != nil {
		t.Fatalf("document: %v", err)
	}
	if document.ActiveRevision != 2 {
		t.Errorf("the active revision is %d, want 2", document.ActiveRevision)
	}
	if document.TextChars != int64(len("alpha beta gamma")) {
		t.Errorf("text_chars is %d, want the new revision's %d",
			document.TextChars, len("alpha beta gamma"))
	}
}

func TestDBReplaceRevisionRefusesARevisionSomebodyElseMoved(t *testing.T) {
	repo, resource := newDBRepository(t)
	first := writeRevision(t, repo, resource, "doc_1", atSecond(1), "alpha beta", []string{"alpha", "beta"})
	second := documentWrite(t, dbDocument{
		ID: "doc_1", KBID: "kb_a", FileID: first.FileID, Revision: 2,
		CreatedAt: atSecond(2), Text: "second", Chunks: []string{"second"},
	})
	if err := repo.ReplaceRevision(context.Background(), second, 1); err != nil {
		t.Fatalf("replace the revision: %v", err)
	}

	// A third caller that read revision 1 before the switch now writes against
	// a document that has moved on. Nothing of it may land: no chunks of a
	// revision 3, and no text from a parse nobody asked for any more.
	stale := documentWrite(t, dbDocument{
		ID: "doc_1", KBID: "kb_a", FileID: first.FileID, Revision: 3,
		CreatedAt: atSecond(3), Text: "stale text", Chunks: []string{"stale"},
	})
	if err := repo.ReplaceRevision(context.Background(), stale, 1); !errors.Is(err, ErrConflict) {
		t.Errorf("a rebuild against a moved document: %v, want %v", err, ErrConflict)
	}
	if got := len(chunkIDsOf(t, repo, "doc_1", 3)); got != 0 {
		t.Errorf("%d chunks of the refused revision were written, want none", got)
	}
	document, err := repo.Document(context.Background(), "doc_1")
	if err != nil {
		t.Fatalf("document: %v", err)
	}
	if document.ActiveRevision != 2 || document.TextChars != int64(len("second")) {
		t.Errorf("the document is at revision %d with %d characters, want the second revision untouched",
			document.ActiveRevision, document.TextChars)
	}
}

func TestDBTwoRebuildsLeaveOneCoherentRevision(t *testing.T) {
	repo, resource := newDBRepository(t)
	first := writeRevision(t, repo, resource, "doc_1", atSecond(1), "alpha beta", []string{"alpha", "beta"})

	// Two rebuilds of one document, started from the same revision. The chunk
	// ids of a revision are derived from it, so both would write the same
	// primary keys; the guard is what has to let exactly one through, and the
	// loser's transaction has to leave nothing behind.
	start := make(chan struct{})
	errs := make([]error, 2)
	writes := make([]DocumentWrite, 2)
	var wg sync.WaitGroup
	for i := range writes {
		text := "first rebuild"
		if i == 1 {
			text = "second rebuild"
		}
		writes[i] = documentWrite(t, dbDocument{
			ID: "doc_1", KBID: "kb_a", FileID: first.FileID, Revision: 2,
			CreatedAt: atSecond(2), Text: text, Chunks: []string{text},
		})
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = repo.ReplaceRevision(context.Background(), writes[i], 1)
		}()
	}
	close(start)
	wg.Wait()

	refused := 0
	for i, err := range errs {
		switch {
		case err == nil:
		case errors.Is(err, ErrConflict):
			refused++
		default:
			t.Errorf("rebuild %d failed for a reason that is not a lost race: %v", i, err)
		}
	}
	if refused != 1 {
		t.Fatalf("%d of the two rebuilds were refused, want exactly one", refused)
	}

	// Whichever won, the document is at revision 2 with that rebuild's text and
	// one set of chunks — never both, never neither.
	document, err := repo.Document(context.Background(), "doc_1")
	if err != nil {
		t.Fatalf("document: %v", err)
	}
	if document.ActiveRevision != 2 {
		t.Fatalf("the active revision is %d, want 2", document.ActiveRevision)
	}
	chunks, err := repo.Chunks(context.Background(), "doc_1", 2)
	if err != nil {
		t.Fatalf("the second revision's chunks: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("the second revision has %d chunks, want the winner's one", len(chunks))
	}
	if chunk := chunks[0]; chunk.Content != writes[0].Text && chunk.Content != writes[1].Text {
		t.Errorf("the stored chunk is %q, which is neither rebuild's text", chunk.Content)
	}
	if document.TextChars != int64(len(chunks[0].Content)) {
		t.Errorf("text_chars is %d but the stored chunk holds %d characters",
			document.TextChars, len(chunks[0].Content))
	}
}

// activeChunkCount is how many of these chunks a read of the active revision
// would hand a citation, which is the flag the rebuild flips.
func activeChunkCount(t *testing.T, repo *GORMRepository, chunkIDs []string) int {
	t.Helper()
	sources, err := repo.ChunkSources(context.Background(), chunkIDs)
	if err != nil {
		t.Fatalf("the sources of %v: %v", chunkIDs, err)
	}
	return len(sources)
}
