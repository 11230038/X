package kb

// The retrieval half of the storage contract, which is the half shaped by what
// this seekdb build plans: a primary-key range instead of a LIKE pattern, a
// full-text index that tokenises in two-character steps, a vector column of a
// fixed width, and two spellings (`APPROXIMATE`, a bare MATCH) that are
// load-bearing and fail silently when dropped.
//
// None of it is knowable from a double, so all of it is checked against a real
// seekdb. The fixtures live in repository_db_test.go; the vector helpers are in
// repository_vector_contract_test.go.

import (
	"context"
	"slices"
	"testing"
	"unicode/utf8"

	"backend/internal/data"
)

// storeParsed writes one library file and one document parsed from it, and
// returns the document. number tells the file, the path and the second apart
// between calls, because two documents cannot share a file in one place.
func storeParsed(t *testing.T, repo *GORMRepository, resource *data.Resource,
	id, kbID string, number int, text string, chunks []string) Document {
	t.Helper()
	fileID := libraryFileID(number)
	addLibraryFile(t, resource, fileID)
	return addDocument(t, repo, dbDocument{
		ID: id, KBID: kbID, FileID: fileID, CreatedAt: atSecond(number),
		Text: text, Chunks: chunks,
	})
}

// chunkContents is one revision's chunks by text, which is what tells one
// revision's rows from another's when the ids of both are in hand.
func chunkContents(t *testing.T, repo *GORMRepository, documentID string, revision int) []string {
	t.Helper()
	chunks, err := repo.Chunks(context.Background(), documentID, revision)
	if err != nil {
		t.Fatalf("the chunks of %s revision %d: %v", documentID, revision, err)
	}
	contents := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		contents = append(contents, chunk.Content)
	}
	return contents
}

// saveVector puts one vector on one chunk through the adapter's own write path.
func saveVector(t *testing.T, repo *GORMRepository, chunkID, vector string) {
	t.Helper()
	err := repo.SaveEmbeddings(context.Background(), []ChunkRecord{
		{ChunkID: chunkID, Embedding: vector},
	})
	if err != nil {
		t.Fatalf("store the vector of %s: %v", chunkID, err)
	}
}

func TestDBAChunkRangeIsOneRevisionAndNoOther(t *testing.T) {
	repo, resource := newDBRepository(t)

	// writeRevision stores kb_a and the file the rebuild below starts from.
	rebuilt := writeRevision(t, repo, resource, "doc_r", atSecond(1), "闭包是一个函数", []string{"闭包是一个函数"})

	// Two ids where one is a prefix of the other, which is the pair the raised
	// upper bound exists for: `doc_a`'s range ends one byte after its own
	// prefix, and `doc_a1`'s rows sit below that byte rather than inside it.
	storeParsed(t, repo, resource, "doc_a", "kb_a", 2, "alpha beta", []string{"alpha", "beta"})
	storeParsed(t, repo, resource, "doc_a1", "kb_a", 3, "gamma", []string{"gamma"})

	// A revision number is a prefix of the next one as a number: 1 of 11. A
	// read that ranged over `chunk_id LIKE prefix%` would return revision 11's
	// chunks as revision 1's — and the underscore in every chunk id is a
	// single-character wildcard in LIKE, which is why the read is a range.
	next := documentWrite(t, dbDocument{
		ID: "doc_r", KBID: "kb_a", FileID: rebuilt.FileID, Revision: 11,
		CreatedAt: atSecond(4), Text: "闭包是一个函数和它捕获的环境",
		Chunks: []string{"闭包是一个函数和它捕获的环境"},
	})
	err := repo.ReplaceRevision(context.Background(), next, 1)
	if err != nil {
		t.Fatalf("replace the revision: %v", err)
	}

	for _, scenario := range []struct {
		documentID string
		revision   int
		want       []string
	}{
		{"doc_a", 1, []string{"alpha", "beta"}},
		{"doc_a1", 1, []string{"gamma"}},
		{"doc_r", 1, []string{"闭包是一个函数"}},
		{"doc_r", 11, []string{"闭包是一个函数和它捕获的环境"}},
	} {
		got := chunkContents(t, repo, scenario.documentID, scenario.revision)
		if !slices.Equal(got, scenario.want) {
			t.Errorf("%s revision %d reads %q, want %q", scenario.documentID, scenario.revision, got, scenario.want)
		}
	}
}

func TestDBChunkSpansAreTheSameRangeInCharacters(t *testing.T) {
	repo, resource := newDBRepository(t)
	text := "闭包是一个函数和它捕获的环境。"
	writeRevision(t, repo, resource, "doc_1", atSecond(1), text,
		[]string{"闭包是一个函数", "和它捕获的环境。"})

	ctx := context.Background()
	chunks, err := repo.Chunks(ctx, "doc_1", 1)
	if err != nil {
		t.Fatalf("the chunks of doc_1: %v", err)
	}
	spans, err := repo.ChunkSpans(ctx, "doc_1", 1)
	if err != nil {
		t.Fatalf("the spans of doc_1: %v", err)
	}
	if len(spans) != len(chunks) {
		t.Fatalf("%d spans for %d chunks, want one each", len(spans), len(chunks))
	}

	for index, span := range spans {
		chunk := chunks[index]
		if span.ChunkID != chunk.ChunkID || span.Ordinal != chunk.Ordinal {
			t.Errorf("span %d is %+v, want the chunk's own id and ordinal (%s, %d)",
				index, span, chunk.ChunkID, chunk.Ordinal)
		}
		if span.CharStart != chunk.CharStart || span.CharEnd != chunk.CharEnd {
			t.Errorf("span %d is %d..%d, want the chunk's %d..%d",
				index, span.CharStart, span.CharEnd, chunk.CharStart, chunk.CharEnd)
		}

		// The coordinates are characters, and this text is two bytes per
		// character: a span counted in bytes would be twice as long, which is
		// what the window read below would then return the wrong text for.
		width := utf8.RuneCountInString(chunk.Content)
		if got := span.CharEnd - span.CharStart; got != int64(width) {
			t.Errorf("span %d covers %d characters for a chunk of %d", index, got, width)
		}

		// And a span is where its chunk is: the window a citation opens is the
		// text the chunk holds, read through the other unit in this pair —
		// 0-based here, 1-based in the SUBSTRING underneath.
		window, err := repo.DocumentText(ctx, "doc_1", span.CharStart, span.CharEnd-span.CharStart)
		if err != nil {
			t.Fatalf("read the window at %d: %v", span.CharStart, err)
		}
		if window != chunk.Content {
			t.Errorf("the window at %d..%d is %q, want the chunk %q",
				span.CharStart, span.CharEnd, window, chunk.Content)
		}
	}
}

func TestDBDocumentTextCountsCharactersFromOne(t *testing.T) {
	repo, resource := newDBRepository(t)
	text := "闭包是一个函数和它捕获的环境"
	writeRevision(t, repo, resource, "doc_1", atSecond(1), text,
		[]string{"闭包是一个函数", "和它捕获的环境"})
	runes := []rune(text)

	// The caller's offsets are 0-based and SUBSTRING's are 1-based, and the
	// adapter is where the two meet: asking for three characters from offset 2
	// is the third character onwards. Counted in bytes it would be off by a
	// factor of two and off by one at the start.
	window, err := repo.DocumentText(context.Background(), "doc_1", 2, 3)
	if err != nil {
		t.Fatalf("read a window: %v", err)
	}
	if want := string(runes[2:5]); window != want {
		t.Errorf("the window at offset 2 is %q, want %q", window, want)
	}

	// A window longer than what is left is what is left: a preview asks for a
	// page and takes what the document has.
	window, err = repo.DocumentText(context.Background(), "doc_1", 0, int64(len(runes)+10))
	if err != nil || window != text {
		t.Errorf("a window past the end reads %q (err %v), want the whole text", window, err)
	}

	// Past the end there is nothing left to draw, and that is empty text rather
	// than an error: the service checks the offset against the document first,
	// so this is the adapter's own answer and not a path a request takes.
	window, err = repo.DocumentText(context.Background(), "doc_1", int64(len(runes))+1, 5)
	if err != nil || window != "" {
		t.Errorf("a window beyond the text reads %q (err %v), want nothing", window, err)
	}

	// A document that is not there reads the same way, which is the same
	// statement about the same query and not a second contract.
	window, err = repo.DocumentText(context.Background(), "doc_missing", 0, 5)
	if err != nil || window != "" {
		t.Errorf("a window of a missing document reads %q (err %v), want nothing", window, err)
	}
}

func TestDBFullTextTokenisesInTwoCharacterSteps(t *testing.T) {
	repo, resource := newDBRepository(t)

	// writeRevision stores kb_a; the second base goes in beside it.
	found := writeRevision(t, repo, resource, "doc_found", atSecond(1), "闭包是一个函数",
		[]string{"闭包是一个函数"})
	storeParsed(t, repo, resource, "doc_other", "kb_a", 2, "另一件事说的是别的",
		[]string{"另一件事说的是别的"})
	createBase(t, repo, "kb_b", atSecond(0))
	elsewhere := storeParsed(t, repo, resource, "doc_elsewhere", "kb_b", 3, "闭包是另一个库里的",
		[]string{"闭包是另一个库里的"})

	ctx := context.Background()
	searched := func(query string, kbIDs ...string) []string {
		t.Helper()
		ids, err := repo.SearchFullText(ctx, kbIDs, query, 10)
		if err != nil {
			t.Fatalf("search for %q: %v", query, err)
		}
		return ids
	}

	// One character is below the token size: the index is built WITH PARSER
	// ngram and this build's ngram_token_size is 2, so the query produces no
	// token and matches nothing. That is the fact minQueryChars is derived
	// from, and the service refuses the query before it reaches here.
	if ids := searched("闭", "kb_a"); len(ids) != 0 {
		t.Errorf("a one-character query matched %v, want nothing", ids)
	}

	// Two characters is the first length that answers, and only the rows of the
	// bases asked about: the same words in another base are not a result.
	ids := searched("闭包", "kb_a")
	chunk := ChunkID(found.ID, 1, 0)
	if !slices.Contains(ids, chunk) {
		t.Errorf("the chunks matching 闭包 are %v, want %s among them", ids, chunk)
	}
	if foreign := ChunkID(elsewhere.ID, 1, 0); slices.Contains(ids, foreign) {
		t.Errorf("the chunks matching 闭包 in kb_a are %v, want nothing from kb_b", ids)
	}

	// A two-character query that is not in the text is nothing at all: the
	// step is a token size, not a rounding.
	if ids := searched("量子", "kb_a"); len(ids) != 0 {
		t.Errorf("a query that matches nothing returned %v", ids)
	}

	// The limit is the search's, not the caller's afterwards: the rows of the
	// bases asked about are all candidates, and one of them is left out here.
	across := searched("闭包", "kb_a", "kb_b")
	if len(across) != 2 {
		t.Fatalf("%d chunks match 闭包 across both bases, want 2", len(across))
	}
	limited, err := repo.SearchFullText(ctx, []string{"kb_a", "kb_b"}, "闭包", 1)
	if err != nil {
		t.Fatalf("search with a limit: %v", err)
	}
	if len(limited) != 1 || !slices.Contains(across, limited[0]) {
		t.Errorf("a search limited to one returned %v, want one of %v", limited, across)
	}
}

func TestDBFullTextLeavesAnInactiveRevisionBehind(t *testing.T) {
	repo, resource := newDBRepository(t)
	before := writeRevision(t, repo, resource, "doc_1", atSecond(1), "闭包是重建前的",
		[]string{"闭包是重建前的"})
	after := documentWrite(t, dbDocument{
		ID: "doc_1", KBID: "kb_a", FileID: before.FileID, Revision: 2,
		CreatedAt: atSecond(2), Text: "闭包是重建后的", Chunks: []string{"闭包是重建后的"},
	})
	if err := repo.ReplaceRevision(context.Background(), after, 1); err != nil {
		t.Fatalf("replace the revision: %v", err)
	}

	// Both revisions match the query and both rows are still in the table —
	// a rebuild does not delete what a citation may point at — but only the one
	// the document now serves is a result. The filter is in the search's WHERE
	// clause rather than applied to what came back, which is also why a rebuild
	// that lands mid-flight makes a chunk simply absent.
	ids, err := repo.SearchFullText(context.Background(), []string{"kb_a"}, "闭包", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if want := ChunkID("doc_1", 2, 0); !slices.Contains(ids, want) {
		t.Errorf("the chunks matching 闭包 are %v, want the current revision's %s", ids, want)
	}
	if stale := ChunkID("doc_1", 1, 0); slices.Contains(ids, stale) {
		t.Errorf("the chunks matching 闭包 are %v, want the superseded %s left out", ids, stale)
	}
}

func TestDBTheNearestVectorComesFirst(t *testing.T) {
	repo, resource := newDBRepository(t)
	createBase(t, repo, "kb_a", atSecond(0))
	createBase(t, repo, "kb_b", atSecond(0))

	onAxis := storeParsed(t, repo, resource, "doc_axis", "kb_a", 1, "alpha", []string{"alpha"})
	between := storeParsed(t, repo, resource, "doc_between", "kb_a", 2, "beta", []string{"beta"})
	far := storeParsed(t, repo, resource, "doc_far", "kb_a", 3, "gamma", []string{"gamma"})
	elsewhere := storeParsed(t, repo, resource, "doc_elsewhere", "kb_b", 4, "delta", []string{"delta"})
	pending := storeParsed(t, repo, resource, "doc_pending", "kb_a", 5, "epsilon", []string{"epsilon"})

	// The vectors go in by hand: what is under test is the ordering the
	// database computes, and an embedding provider would only add a number to
	// it. One vector points along the query's axis, one is perpendicular to it,
	// and one is spread over both — so the distance between the first and the
	// last is the whole of the range and the middle is a fact about the
	// arithmetic rather than about the order the rows were written in.
	saveVector(t, repo, ChunkID(onAxis.ID, 1, 0), vectorOf(0))
	saveVector(t, repo, ChunkID(between.ID, 1, 0), mixedVectorOf(0, 1))
	saveVector(t, repo, ChunkID(far.ID, 1, 0), vectorOf(1))
	saveVector(t, repo, ChunkID(elsewhere.ID, 1, 0), vectorOf(0))

	ctx := context.Background()
	ids, err := repo.SearchVector(ctx, []string{"kb_a"}, axisVector(0), 10)
	if err != nil {
		t.Fatalf("search by vector: %v", err)
	}
	want := []string{ChunkID(onAxis.ID, 1, 0), ChunkID(between.ID, 1, 0), ChunkID(far.ID, 1, 0)}
	if !slices.Equal(ids, want) {
		t.Errorf("the nearest chunks are %v, want %v", ids, want)
	}

	// A chunk with no vector yet is not a result: it is waiting for the worker,
	// not lying at distance zero, and the filter that says so is in the search.
	if slices.Contains(ids, ChunkID(pending.ID, 1, 0)) {
		t.Errorf("the results are %v, want the chunk with no vector left out", ids)
	}
	// Nor is the identical vector in another base: kb_id is a scalar filter in
	// the same table as the vector index, which is what lets the planner filter
	// first and search the index afterwards.
	if slices.Contains(ids, ChunkID(elsewhere.ID, 1, 0)) {
		t.Errorf("the results are %v, want nothing from kb_b", ids)
	}

	// The limit is applied after the ordering, so it takes the nearest ones.
	nearest, err := repo.SearchVector(ctx, []string{"kb_a"}, axisVector(0), 2)
	if err != nil {
		t.Fatalf("search by vector with a limit: %v", err)
	}
	if !slices.Equal(nearest, want[:2]) {
		t.Errorf("the two nearest chunks are %v, want the first two of %v", nearest, want)
	}
}

func TestDBTheVectorColumnTakesOnlyItsOwnWidth(t *testing.T) {
	repo, resource := newDBRepository(t)
	document := writeRevision(t, repo, resource, "doc_1", atSecond(1), "alpha", []string{"alpha"})
	chunkID := ChunkID(document.ID, 1, 0)

	// Handing the column a vector of another width is refused rather than
	// stored and compared wrongly — and this is the statement the adapter makes
	// when a provider answers with the wrong width, so the failure arrives
	// while the document is being indexed instead of at search time.
	err := repo.SaveEmbeddings(context.Background(), []ChunkRecord{
		{ChunkID: chunkID, Embedding: "[1,0]"},
	})
	if err == nil {
		t.Error("a two-dimensional vector was stored in a VECTOR(1024) column")
	}
	if got := countVectors(t, resource); got != 0 {
		t.Errorf("%d chunks carry a vector after the refusal, want none", got)
	}

	// The same call at the column's width goes through, so what refused the
	// first one is the width and not the statement. It is also why the stand-in
	// in service_db_test.go had to be built at 1024 dimensions.
	saveVector(t, repo, chunkID, vectorOf(0))
	if got := countVectors(t, resource); got != 1 {
		t.Errorf("%d chunks carry a vector, want the one just stored", got)
	}
}

// countVectors is how many chunks have a vector, which is how a refused write
// is told from one that landed.
func countVectors(t *testing.T, resource *data.Resource) int64 {
	t.Helper()
	var count int64
	err := resource.GORM().Raw(
		"SELECT COUNT(*) FROM document_chunks WHERE embedding IS NOT NULL").Scan(&count).Error
	if err != nil {
		t.Fatalf("count the chunks with a vector: %v", err)
	}
	return count
}

// The two searches say nothing about a base that holds no chunks, and neither
// reading a base that is not there is an error: an empty base is an empty
// answer, which is what the retrieval above them leans on. The reads of one
// document's own rows are here for the same reason — a document whose chunks
// are not there is a document with no chunks, not a failure, because a preview
// of something still being written has to draw.
func TestDBAnEmptyBaseIsAnEmptyAnswer(t *testing.T) {
	repo, _ := newDBRepository(t)
	createBase(t, repo, "kb_empty", atSecond(0))

	ctx := context.Background()
	if ids, err := repo.SearchFullText(ctx, []string{"kb_empty"}, "闭包", 10); err != nil || len(ids) != 0 {
		t.Errorf("the text search of an empty base returned %v (err %v)", ids, err)
	}
	if ids, err := repo.SearchVector(ctx, []string{"kb_empty"}, axisVector(0), 10); err != nil || len(ids) != 0 {
		t.Errorf("the vector search of an empty base returned %v (err %v)", ids, err)
	}
	// A base nobody created is the same answer by the same query, and a
	// document whose chunks are not there reads as no chunks rather than as a
	// failure — the preview of a document still being written has to draw.
	if ids, err := repo.SearchVector(ctx, []string{"kb_nowhere"}, axisVector(0), 10); err != nil || len(ids) != 0 {
		t.Errorf("the vector search of an unknown base returned %v (err %v)", ids, err)
	}
	chunks, err := repo.Chunks(ctx, "doc_missing", 1)
	if err != nil || len(chunks) != 0 {
		t.Errorf("the chunks of a missing document are %v (err %v)", chunks, err)
	}
	spans, err := repo.ChunkSpans(ctx, "doc_missing", 1)
	if err != nil || len(spans) != 0 {
		t.Errorf("the spans of a missing document are %v (err %v)", spans, err)
	}
	// An empty list of ids is no query at all, and it is answered without one:
	// the caller built this list from what a turn retrieved, so an empty one is
	// a turn that retrieved nothing, not a statement with no ids in it.
	sources, err := repo.ChunkSources(ctx, nil)
	if err != nil || len(sources) != 0 {
		t.Errorf("the sources of no chunks are %v (err %v)", sources, err)
	}
}
