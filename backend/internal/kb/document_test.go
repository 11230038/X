package kb

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"backend/internal/embed"
	"backend/internal/parse"
)

func TestAddDocumentParsesChunksAndStores(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")

	document := h.addDocument(t, base.ID, "lib_one", sampleText)

	if document.KBID != base.ID || document.FileID != "lib_one" {
		t.Errorf("document belongs to %q / %q", document.KBID, document.FileID)
	}
	if document.Filename != "lib_one.md" {
		t.Errorf("filename = %q, want the stored file's name", document.Filename)
	}
	if document.Engine != parse.EngineText {
		t.Errorf("engine = %q, want %q", document.Engine, parse.EngineText)
	}
	if document.ActiveRevision != 1 {
		t.Errorf("active revision = %d, want 1", document.ActiveRevision)
	}
	if document.TextChars != int64(len([]rune(sampleText))) {
		t.Errorf("text chars = %d, want %d", document.TextChars, len([]rune(sampleText)))
	}
	if document.ChunkSize != 20 || document.ChunkOverlap != 5 {
		t.Errorf("the chunking was not recorded: %d/%d", document.ChunkSize, document.ChunkOverlap)
	}
	// Extraction is synchronous and embedding is not: the document is pending
	// and its full text is already there to be searched.
	if document.VectorState != VectorStatePending {
		t.Errorf("vector state = %q, want %q", document.VectorState, VectorStatePending)
	}
	chunks := h.repo.activeChunks(document.ID)
	if len(chunks) < 2 {
		t.Fatalf("got %d active chunks, want the fixture split into several", len(chunks))
	}
	if chunks[0].CharStart != 0 {
		t.Errorf("the first chunk starts at %d", chunks[0].CharStart)
	}
	for index, chunk := range chunks {
		if chunk.Ordinal != index {
			t.Errorf("chunk %d has ordinal %d", index, chunk.Ordinal)
		}
		if !strings.HasPrefix(chunk.ChunkID, ChunkIDPrefix(document.ID, 1)) {
			t.Errorf("chunk id %q is not in this document revision's range", chunk.ChunkID)
		}
	}
}

// The same file in the same collection is the same document. Parsing it twice
// would cost two calls to the engine and put the same text in a library twice,
// which retrieval would then return as two sources.
func TestAddDocumentParsesAFileOnce(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")

	first := h.addDocument(t, base.ID, "lib_one", sampleText)
	second, err := h.service.AddDocument(context.Background(), "lib_one", base.ID)
	if err != nil {
		t.Fatalf("the second AddDocument() error = %v", err)
	}

	if second.ID != first.ID {
		t.Fatalf("the second add produced document %s, want the first one %s", second.ID, first.ID)
	}
	if len(h.files.opened) != 1 {
		t.Errorf("the file was opened %d times, want 1", len(h.files.opened))
	}
	if h.repo.documentCount() != 1 {
		t.Errorf("%d documents were stored, want 1", h.repo.documentCount())
	}
}

// The same file as an attachment and inside a collection are two documents:
// one belongs to the collection and one to the turn that carried it, and they
// differ in whether anyone may retrieve from them later.
func TestAddDocumentKeepsAnAttachmentApartFromTheCollection(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")
	h.addFile("lib_one", "one.md", sampleText)

	attachment, err := h.service.AddDocument(context.Background(), "lib_one", "")
	if err != nil {
		t.Fatalf("AddDocument(as an attachment) error = %v", err)
	}
	collected, err := h.service.AddDocument(context.Background(), "lib_one", base.ID)
	if err != nil {
		t.Fatalf("AddDocument(into a collection) error = %v", err)
	}

	if attachment.ID == collected.ID {
		t.Fatal("the attachment and the collection entry are the same document")
	}
	if attachment.KBID != "" || collected.KBID != base.ID {
		t.Errorf("kb ids: attachment %q, collection %q", attachment.KBID, collected.KBID)
	}
}

// A document over the limit is stored truncated and says so. This is the one
// failure that arrives with usable material, and losing the text or storing the
// tail silently are both worse than storing what fits and admitting the cut.
func TestAddDocumentKeepsTheTextItTruncated(t *testing.T) {
	h := newHarness(t, func(options *Options) { options.MaxCharsPerDocument = 10 })
	base := h.base(t, "kb")

	document := h.addDocument(t, base.ID, "lib_one", strings.Repeat("一二三四五", 20))

	if !document.Truncated {
		t.Error("the document does not say it was truncated")
	}
	if document.TextChars != 10 {
		t.Errorf("text chars = %d, want the 10-character limit", document.TextChars)
	}
	text, err := h.repo.DocumentText(context.Background(), document.ID, 0, 100)
	if err != nil {
		t.Fatalf("DocumentText() error = %v", err)
	}
	if got := len([]rune(text)); got != 10 {
		t.Errorf("the stored text is %d characters, want 10", got)
	}
}

func TestAddDocumentReportsACategoryNoEngineReads(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")
	h.files.files["lib_img"] = stubFile{Filename: "photo.png", MIMEType: "image/png", Category: "images"}

	_, err := h.service.AddDocument(context.Background(), "lib_img", base.ID)

	if parse.CodeOf(err) != parse.CodeUnsupportedFormat {
		t.Fatalf("error = %v, want an unsupported_format parse error", err)
	}
	if h.repo.documentCount() != 0 {
		t.Error("a document was stored for a file nothing can read")
	}
}

func TestAddDocumentRefusesAnUnknownCollection(t *testing.T) {
	h := newHarness(t)
	h.addFile("lib_one", "one.md", sampleText)

	_, err := h.service.AddDocument(context.Background(), "lib_one", "kb_missing")

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
	if len(h.files.opened) != 0 {
		t.Error("the file was read before the collection was found to be missing")
	}
}

func TestAddDocumentNeedsAFile(t *testing.T) {
	h := newHarness(t)
	if _, err := h.service.AddDocument(context.Background(), "   ", ""); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

func TestRebuildMovesTheDocumentOntoItsNextRevision(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")
	document := h.addDocument(t, base.ID, "lib_one", sampleText)
	before := h.repo.activeChunks(document.ID)

	rebuilt, err := h.service.RebuildDocument(context.Background(), document.ID)
	if err != nil {
		t.Fatalf("RebuildDocument() error = %v", err)
	}

	if rebuilt.ActiveRevision != 2 {
		t.Fatalf("active revision = %d, want 2", rebuilt.ActiveRevision)
	}
	// The new revision is the one that is visible, and the old one is not: a
	// reader sees one revision or the other, never both.
	chunks := h.repo.activeChunks(document.ID)
	if len(chunks) == 0 {
		t.Fatal("no chunks are active after the rebuild")
	}
	for _, chunk := range chunks {
		if chunk.Revision != 2 {
			t.Errorf("active chunk %s is from revision %d, want 2", chunk.ChunkID, chunk.Revision)
		}
		if chunk.ChunkID == before[0].ChunkID {
			t.Error("an old revision's chunk is still active")
		}
	}
	// Both revisions are still on disk: the old one is retained, and it is
	// simply not the document any more.
	old, err := h.repo.Chunks(context.Background(), document.ID, 1)
	if err != nil {
		t.Fatalf("Chunks(revision 1) error = %v", err)
	}
	if len(old) != len(before) {
		t.Errorf("revision 1 kept %d chunks, want the %d it had", len(old), len(before))
	}
}

// A rebuild that cannot parse leaves the document serving the revision it had.
// This is KB-03's "a build in progress does not break the version in use",
// with the failure at the earliest point it can happen.
func TestRebuildLeavesTheCurrentRevisionWhenItCannotParse(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")
	document := h.addDocument(t, base.ID, "lib_one", sampleText)
	before := h.repo.activeChunks(document.ID)

	h.files.files["lib_one"] = stubFile{Filename: "one.md", Category: parse.CategoryMarkdown, Content: "not valid \xff utf-8"}
	if _, err := h.service.RebuildDocument(context.Background(), document.ID); parse.CodeOf(err) == "" {
		t.Fatalf("error = %v, want a parse failure", err)
	}

	stored, err := h.service.Document(context.Background(), document.ID)
	if err != nil {
		t.Fatalf("Document() error = %v", err)
	}
	if stored.ActiveRevision != 1 {
		t.Errorf("active revision = %d, want the rebuild to have changed nothing", stored.ActiveRevision)
	}
	if h.repo.switches != 0 {
		t.Error("a revision switch happened for a document that failed to parse")
	}
	if len(h.repo.activeChunks(document.ID)) != len(before) {
		t.Error("the active chunks changed")
	}
}

func TestRemoveDocumentTakesItsChunksWithIt(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")
	document := h.addDocument(t, base.ID, "lib_one", sampleText)

	if err := h.service.RemoveDocument(context.Background(), document.ID); err != nil {
		t.Fatalf("RemoveDocument() error = %v", err)
	}
	if _, err := h.service.Document(context.Background(), document.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("the document is still there: %v", err)
	}
	if _, err := h.service.AddDocument(context.Background(), "lib_one", base.ID); err != nil {
		t.Errorf("the file cannot be added again after removal: %v", err)
	}
}

// Which document a file id resolves to is a question about the rows — one file
// can have been read into several places, and the rule that picks between them
// is the query's ordering. It is checked where the query runs, in
// repository_document_contract_test.go; the service only trims the id and hands
// it on, which is what the two cases below are.
func TestDocumentOfFileNeedsAFile(t *testing.T) {
	h := newHarness(t)

	if _, err := h.service.DocumentOfFile(context.Background(), "  "); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

func TestDocumentOfFilePassesAStorageFailureThrough(t *testing.T) {
	h := newHarness(t)
	failure := errors.New("connection reset")
	h.repo.setError("DocumentOfFile", failure)

	if _, err := h.service.DocumentOfFile(context.Background(), "lib_one"); !errors.Is(err, failure) {
		t.Fatalf("error = %v, want the storage failure", err)
	}
}

// The boundaries a citation's character span is read against are the active
// revision's: after a rebuild the spans move to the new chunks, which is what
// keeps an old citation opening on the text it was made from only until the
// document changes, and on the new text after.
func TestChunkSpansAreTheActiveRevisionsBoundaries(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")
	document := h.addDocument(t, base.ID, "lib_one", sampleText)

	spans, err := h.service.ChunkSpans(context.Background(), document.ID)
	if err != nil {
		t.Fatalf("ChunkSpans() error = %v", err)
	}
	chunks := h.repo.activeChunks(document.ID)
	if len(spans) != len(chunks) {
		t.Fatalf("got %d boundaries for %d chunks", len(spans), len(chunks))
	}
	for index, span := range spans {
		if span.Ordinal != index {
			t.Errorf("boundary %d has ordinal %d", index, span.Ordinal)
		}
		if span.ChunkID != chunks[index].ChunkID || span.CharStart != chunks[index].CharStart ||
			span.CharEnd != chunks[index].CharEnd {
			t.Errorf("boundary %d = %+v, want the chunk's own coordinates", index, span)
		}
	}

	if _, err := h.service.RebuildDocument(context.Background(), document.ID); err != nil {
		t.Fatalf("RebuildDocument() error = %v", err)
	}
	after, err := h.service.ChunkSpans(context.Background(), document.ID)
	if err != nil {
		t.Fatalf("ChunkSpans() after the rebuild error = %v", err)
	}
	if len(after) == 0 {
		t.Fatal("the rebuilt document has no boundaries")
	}
	if after[0].ChunkID == spans[0].ChunkID {
		t.Error("the boundaries still point at the revision the rebuild replaced")
	}
}

func TestChunkSpansOfAnUnknownDocumentAreNotFound(t *testing.T) {
	h := newHarness(t)

	if _, err := h.service.ChunkSpans(context.Background(), "doc_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

func TestNewServiceRefusesIncompleteWiring(t *testing.T) {
	registry, err := parse.NewRegistry(parse.Options{DefaultEngine: parse.EngineText, Timeout: time.Second})
	if err != nil {
		t.Fatalf("build the parser registry: %v", err)
	}
	files := &stubFiles{files: map[string]stubFile{}}

	cases := []struct {
		name     string
		options  Options
		repo     Repository
		parsers  *parse.Registry
		files    FileSource
		embedder embed.Embedder
	}{
		{name: "no repository", options: testOptions(), parsers: registry, files: files, embedder: &stubEmbedder{}},
		{name: "no registry", options: testOptions(), repo: newFakeRepository(), files: files, embedder: &stubEmbedder{}},
		{name: "no file source", options: testOptions(), repo: newFakeRepository(), parsers: registry, embedder: &stubEmbedder{}},
		{name: "no embedder", options: testOptions(), repo: newFakeRepository(), parsers: registry, files: files},
		{name: "no embedding model", options: func() Options {
			options := testOptions()
			options.EmbeddingModel = " "
			return options
		}(), repo: newFakeRepository(), parsers: registry, files: files, embedder: &stubEmbedder{}},
		{name: "overlap as large as the window", options: func() Options {
			options := testOptions()
			options.Chunking = Chunking{Size: 10, Overlap: 10}
			return options
		}(), repo: newFakeRepository(), parsers: registry, files: files, embedder: &stubEmbedder{}},
		{name: "top k above the candidates", options: func() Options {
			options := testOptions()
			options.RetrievalTopK = 20
			return options
		}(), repo: newFakeRepository(), parsers: registry, files: files, embedder: &stubEmbedder{}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := NewService(testCase.options, testCase.repo, testCase.parsers, testCase.files, testCase.embedder); err == nil {
				t.Fatal("NewService() error = nil, want a refusal")
			}
		})
	}
}
