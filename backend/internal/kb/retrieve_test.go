package kb

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"backend/internal/event"
)

// chunkIDs lists a document's active chunk ids in order, which is how a test
// names the chunks it wants a retrieval path to return.
func (h *harness) chunkIDs(documentID string) []string {
	chunks := h.repo.activeChunks(documentID)
	ids := make([]string, len(chunks))
	for index, chunk := range chunks {
		ids[index] = chunk.ChunkID
	}
	return ids
}

func TestRetrieveFusesBothPathsAndNumbersTheSources(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")
	document := h.addDocument(t, base.ID, "lib_one", sampleText)
	h.repo.embedChunks(document.ID)
	ids := h.chunkIDs(document.ID)
	if len(ids) < 3 {
		t.Fatalf("the fixture produced %d chunks, want at least 3", len(ids))
	}
	// b is second in one path and first in the other, which beats a and c.
	h.repo.vectorHits = []string{ids[1], ids[0]}
	h.repo.textHits = []string{ids[1], ids[2]}

	retrieval, err := h.service.Retrieve(context.Background(), RetrieveInput{
		KBIDs: []string{base.ID}, Query: "闭包",
	})
	if err != nil {
		t.Fatalf("Retrieve() error = %v", err)
	}

	if len(retrieval.Sources) != 3 {
		t.Fatalf("got %d sources, want 3", len(retrieval.Sources))
	}
	if len(retrieval.Unavailable) != 0 {
		t.Errorf("unavailable = %v, want none", retrieval.Unavailable)
	}

	// ids[1] leads both paths, ids[0] is second in the vector path and absent
	// from the text path, ids[2] is the other way round — and those two tie, so
	// the order between them comes from their ids.
	wantOrder := []string{ids[1], ids[0], ids[2]}
	wantScores := []float64{
		1.0/(60+1) + 1.0/(60+1),
		1.0 / (60 + 2),
		1.0 / (60 + 2),
	}
	for index, source := range retrieval.Sources {
		if source.Index != index+1 {
			t.Errorf("source %d has index %d", index, source.Index)
		}
		if source.Locator.ChunkID != wantOrder[index] {
			t.Errorf("source %d is chunk %s, want %s", index, source.Locator.ChunkID, wantOrder[index])
		}
		if math.Abs(source.Score-wantScores[index]) > 1e-12 {
			t.Errorf("source %d score = %v, want %v", index, source.Score, wantScores[index])
		}
		// Every coordinate comes out of the stored row.
		if source.DocumentID != document.ID || source.FileID != "lib_one" {
			t.Errorf("source %d points at document %s / file %s", index, source.DocumentID, source.FileID)
		}
		if source.Title != "lib_one.md" || source.Engine != "text" {
			t.Errorf("source %d is %q read by %q", index, source.Title, source.Engine)
		}
		if source.Revision != 1 || source.Locator.Kind != locatorChunk {
			t.Errorf("source %d is from revision %d, kind %q", index, source.Revision, source.Locator.Kind)
		}
		if source.Locator.CharEnd <= source.Locator.CharStart {
			t.Errorf("source %d has the span [%d, %d)", index, source.Locator.CharStart, source.Locator.CharEnd)
		}
		if source.Snippet == "" {
			t.Errorf("source %d has no snippet", index)
		}
	}
	if retrieval.Sources[0].Locator.Ordinal != 1 {
		t.Errorf("the middle chunk has ordinal %d, want 1", retrieval.Sources[0].Locator.Ordinal)
	}
}

// The whole reason the vector index is optional: a document whose vectors are
// not computed yet is still retrievable, and a query that cannot be embedded
// still answers.
func TestRetrieveWithoutVectorsFallsBackToTheTextPath(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")
	document := h.addDocument(t, base.ID, "lib_one", sampleText)
	ids := h.chunkIDs(document.ID)
	h.repo.textHits = ids
	// The vector path is asked and has nothing to give, because no chunk of
	// this document has an embedding yet.
	h.repo.vectorHits = ids

	retrieval, err := h.service.Retrieve(context.Background(), RetrieveInput{
		KBIDs: []string{base.ID}, Query: "闭包",
	})
	if err != nil {
		t.Fatalf("Retrieve() error = %v", err)
	}

	if len(retrieval.Sources) != len(ids) {
		t.Fatalf("got %d sources, want the %d the text path found", len(retrieval.Sources), len(ids))
	}
	for index, source := range retrieval.Sources {
		if source.Locator.ChunkID != ids[index] {
			t.Errorf("source %d is %s, want the text path's order %s", index, source.Locator.ChunkID, ids[index])
		}
		if math.Abs(source.Score-1.0/float64(60+index+1)) > 1e-12 {
			t.Errorf("source %d score = %v, want the one-path score", index, source.Score)
		}
	}
}

func TestRetrieveDegradesWhenTheQueryCannotBeEmbedded(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")
	document := h.addDocument(t, base.ID, "lib_one", sampleText)
	h.repo.textHits = h.chunkIDs(document.ID)
	h.repo.vectorHits = h.repo.textHits
	h.embedder.err = errors.New("the embedding provider is down")

	retrieval, err := h.service.Retrieve(context.Background(), RetrieveInput{
		KBIDs: []string{base.ID}, Query: "闭包",
	})

	if err != nil {
		t.Fatalf("Retrieve() error = %v, want the text path's answer", err)
	}
	if len(retrieval.Sources) == 0 {
		t.Fatal("nothing was retrieved although the text path has results")
	}
	if h.repo.searched == nil {
		t.Error("the text path was never asked")
	}
}

// A base the caller named and no longer has is reported, and the search does
// not go looking in the base that is still there in its place. KB-07 is the
// difference between "your material is gone" and "here is something else".
func TestRetrieveReportsAMissingBaseAndSearchesOnlyTheRest(t *testing.T) {
	h := newHarness(t)
	live := h.base(t, "live")
	document := h.addDocument(t, live.ID, "lib_one", sampleText)
	h.repo.textHits = h.chunkIDs(document.ID)

	retrieval, err := h.service.Retrieve(context.Background(), RetrieveInput{
		KBIDs: []string{live.ID, "kb_deleted"}, Query: "闭包",
	})
	if err != nil {
		t.Fatalf("Retrieve() error = %v", err)
	}

	if len(retrieval.Unavailable) != 1 || retrieval.Unavailable[0] != "kb_deleted" {
		t.Fatalf("unavailable = %v, want the missing base named", retrieval.Unavailable)
	}
	if len(h.repo.searched) != 1 || h.repo.searched[0] != live.ID {
		t.Errorf("the search was given %v, want only the base that exists", h.repo.searched)
	}
	if len(retrieval.Sources) == 0 {
		t.Error("the base that exists returned nothing")
	}
}

func TestRetrieveWithEveryBaseGoneSearchesNothing(t *testing.T) {
	h := newHarness(t)
	h.base(t, "other")

	retrieval, err := h.service.Retrieve(context.Background(), RetrieveInput{
		KBIDs: []string{"kb_deleted"}, Query: "闭包",
	})
	if err != nil {
		t.Fatalf("Retrieve() error = %v", err)
	}

	if len(retrieval.Sources) != 0 {
		t.Errorf("got %d sources from an empty scope", len(retrieval.Sources))
	}
	if len(retrieval.Unavailable) != 1 {
		t.Errorf("unavailable = %v, want the missing base named", retrieval.Unavailable)
	}
	if h.repo.searched != nil {
		t.Errorf("a search was run with %v", h.repo.searched)
	}
}

// The full-text index tokenises with two-character grams, so a one-character
// query matches nothing. Returning no sources for it would say "the library has
// nothing about this", which is a much stronger and wrong claim.
func TestRetrieveRefusesAQueryTheIndexCannotMatch(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")

	for _, query := range []string{"", " ", "闭", "a"} {
		_, err := h.service.Retrieve(context.Background(), RetrieveInput{
			KBIDs: []string{base.ID}, Query: query,
		})
		if !errors.Is(err, ErrQueryTooShort) {
			t.Errorf("Retrieve(%q) error = %v, want ErrQueryTooShort", query, err)
		}
	}
}

func TestRetrieveRefusesAnEmptyScope(t *testing.T) {
	h := newHarness(t)
	_, err := h.service.Retrieve(context.Background(), RetrieveInput{Query: "闭包"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

// A chunk that a path found and whose revision has since been replaced is not
// cited: the text is no longer what the document says, and a citation has to
// open on what the document says.
func TestRetrieveSkipsAChunkThatIsNoLongerActive(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")
	document := h.addDocument(t, base.ID, "lib_one", sampleText)
	stale := h.chunkIDs(document.ID)[0]
	h.repo.textHits = []string{stale}

	if _, err := h.service.RebuildDocument(context.Background(), document.ID, testCaller()); err != nil {
		t.Fatalf("RebuildDocument() error = %v", err)
	}

	retrieval, err := h.service.Retrieve(context.Background(), RetrieveInput{
		KBIDs: []string{base.ID}, Query: "闭包",
	})
	if err != nil {
		t.Fatalf("Retrieve() error = %v", err)
	}
	for _, source := range retrieval.Sources {
		if source.Locator.ChunkID == stale {
			t.Fatalf("the source list contains %s, which is from the replaced revision", stale)
		}
	}
}

func TestRetrieveCutsSnippetsOnACharacterBoundary(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")
	document := h.addDocument(t, base.ID, "lib_one", strings.Repeat("闭包 ", 20))
	h.repo.textHits = h.chunkIDs(document.ID)

	retrieval, err := h.service.Retrieve(context.Background(), RetrieveInput{
		KBIDs: []string{base.ID}, Query: "闭包", SnippetChars: 6,
	})
	if err != nil {
		t.Fatalf("Retrieve() error = %v", err)
	}

	for _, source := range retrieval.Sources {
		snippet := []rune(source.Snippet)
		// The ellipsis is the last character, and what precedes it is what fit.
		if snippet[len(snippet)-1] != '…' {
			t.Errorf("snippet %q does not say it was cut", source.Snippet)
		}
		if len(snippet) > 6 {
			t.Errorf("snippet %q is longer than the 6 characters asked for", source.Snippet)
		}
	}
}

func TestRetrievePassesAStorageFailureThrough(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")
	h.repo.setError("SearchFullText", errors.New("the database went away"))

	_, err := h.service.Retrieve(context.Background(), RetrieveInput{
		KBIDs: []string{base.ID}, Query: "闭包",
	})
	if err == nil || !strings.Contains(err.Error(), "the database went away") {
		t.Fatalf("error = %v, want the storage failure", err)
	}
}

func TestReadWindowReadsOnlyWhatItWasAskedFor(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")
	document := h.addDocument(t, base.ID, "lib_one", sampleText)
	runes := []rune(sampleText)

	window, err := h.service.ReadWindow(context.Background(), document.ID, 4, 6, testCaller())
	if err != nil {
		t.Fatalf("ReadWindow() error = %v", err)
	}
	if window.CharStart != 4 || window.CharEnd != 10 {
		t.Errorf("window span = [%d, %d), want [4, 10)", window.CharStart, window.CharEnd)
	}
	if window.Text != string(runes[4:10]) {
		t.Errorf("window text = %q, want %q", window.Text, string(runes[4:10]))
	}
	if window.TotalChars != int64(len(runes)) {
		t.Errorf("total chars = %d, want %d", window.TotalChars, len(runes))
	}
	if window.Document.ID != document.ID || window.Document.Filename != "lib_one.md" {
		t.Errorf("the window does not carry its document: %+v", window.Document)
	}

	// The last window of a document is short rather than out of range.
	last, err := h.service.ReadWindow(context.Background(), document.ID, int64(len(runes))-3, 50, testCaller())
	if err != nil {
		t.Fatalf("ReadWindow(at the end) error = %v", err)
	}
	if last.CharStart != int64(len(runes))-3 || last.CharEnd != int64(len(runes)) {
		t.Errorf("the last window is [%d, %d), want it clamped to the text", last.CharStart, last.CharEnd)
	}

	// Past the end is an empty window at the end, not a failure.
	past, err := h.service.ReadWindow(context.Background(), document.ID, int64(len(runes))+100, 50, testCaller())
	if err != nil {
		t.Fatalf("ReadWindow(past the end) error = %v", err)
	}
	if past.Text != "" || past.CharStart != int64(len(runes)) {
		t.Errorf("a window past the end is %q at %d", past.Text, past.CharStart)
	}

	if _, err := h.service.ReadWindow(context.Background(), document.ID, -1, 10, testCaller()); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("a negative offset gave %v, want ErrInvalidInput", err)
	}
}

// The coordinates of a citation are the document's, not the answer's. This is
// the case where a model writes a page number: it cannot affect what the source
// says, because the page was never the model's to write.
func TestASourcePageNeverComesFromTheAnswer(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")
	document := h.addDocument(t, base.ID, "lib_one", sampleText)
	h.repo.textHits = h.chunkIDs(document.ID)

	retrieval, err := h.service.Retrieve(context.Background(), RetrieveInput{
		KBIDs: []string{base.ID}, Query: "闭包",
	})
	if err != nil {
		t.Fatalf("Retrieve() error = %v", err)
	}
	answer := "闭包捕获的是环境 [1]。这一点见第 3 页与 [2] 的表 4。"

	citations := ParseCitations(answer, len(retrieval.Sources))
	if len(citations.Cited) != 2 {
		t.Fatalf("cited = %v, want [1 2]", citations.Cited)
	}
	for _, source := range retrieval.Sources {
		if source.Locator.Page != nil {
			t.Errorf("source %d has page %d, and no engine put one there", source.Index, *source.Locator.Page)
		}
	}
}

func TestWindowSourceCarriesTheWindowsOwnSpan(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")
	document := h.addDocument(t, base.ID, "lib_one", sampleText)

	window, err := h.service.ReadWindow(context.Background(), document.ID, 2, 8, testCaller())
	if err != nil {
		t.Fatalf("ReadWindow() error = %v", err)
	}
	source := window.Source(1, 0)

	want := event.Locator{Kind: locatorWindow, CharStart: 2, CharEnd: 10}
	if source.Locator != want {
		t.Errorf("locator = %+v, want %+v", source.Locator, want)
	}
	if source.Snippet != window.Text {
		t.Errorf("snippet = %q, want the window's text", source.Snippet)
	}
	if source.FileID != "lib_one" || source.DocumentID != document.ID {
		t.Errorf("the source points at %s / %s", source.DocumentID, source.FileID)
	}
}
