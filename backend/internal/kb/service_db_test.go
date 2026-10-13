package kb

// The service's own promises about storage, checked where they are decided: the
// database. The stand-ins are the same ones the rest of this package uses — the
// file library and the provider are not what these tests are about — and only
// the repository is real, wired the way the composition root wires it.

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"backend/internal/data"
	"backend/internal/data/testdb"
	"backend/internal/embed"
	"backend/internal/parse"
)

// dbHarness is one service over this package's own database.
type dbHarness struct {
	service  *Service
	repo     *GORMRepository
	files    *stubFiles
	embedder *stubEmbedder
	resource *data.Resource
}

func newDBHarness(t *testing.T) *dbHarness {
	t.Helper()
	resource := testdb.Open(t, "kb")
	testdb.Reset(t, resource, dbTables...)
	registry, err := parse.NewRegistry(parse.Options{
		DefaultEngine: parse.EngineText, Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("build the parser registry: %v", err)
	}
	repository := NewGORMRepository(resource.GORM())
	files := &stubFiles{files: map[string]stubFile{}}
	embedder := &stubEmbedder{vector: embed.Vector{1, 0}}
	service, err := NewService(testOptions(), repository, registry, files, embedder)
	if err != nil {
		t.Fatalf("build the service: %v", err)
	}
	return &dbHarness{
		service: service, repo: repository, resource: resource,
		files: files, embedder: embedder,
	}
}

// storeFile puts one file in the library and registers the bytes the service
// will read.
//
// It is two steps because the id has to be one the library could have issued: a
// document references the file's row (documents_file_fk), so a fixture cannot
// invent an id — the same rule the repository-level fixtures obey.
func (h *dbHarness) storeFile(t *testing.T, fileID, content string) {
	t.Helper()
	addLibraryFile(t, h.resource, fileID)
	h.files.files[fileID] = stubFile{
		Filename: fileID + ".md", MIMEType: "text/markdown",
		Category: parse.CategoryMarkdown, Content: content,
	}
}

func (h *dbHarness) createBase(t *testing.T) KnowledgeBase {
	t.Helper()
	base, err := h.service.CreateKnowledgeBase(context.Background(), "材料", "")
	if err != nil {
		t.Fatalf("create the knowledge base: %v", err)
	}
	return base
}

func TestDBTwoCallersAddingOneFileGetOneDocument(t *testing.T) {
	h := newDBHarness(t)
	base := h.createBase(t)
	fileID := libraryFileID(1)
	h.storeFile(t, fileID, "alpha beta gamma delta epsilon")

	// Both callers ask for the same thing — "make sure this file is in this
	// base" — at the same moment, so both read that it is not there yet and both
	// write. Which one wins is not the point; that there is one document is.
	start := make(chan struct{})
	documents := make([]Document, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range documents {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			documents[i], errs[i] = h.service.AddDocument(context.Background(), fileID, base.ID)
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("caller %d: AddDocument error = %v", i, err)
		}
	}
	if documents[0].ID == "" || documents[0].ID != documents[1].ID {
		t.Errorf("the callers got %q and %q, want one document between them",
			documents[0].ID, documents[1].ID)
	}
	if got := countRows(t, h.resource, "documents"); got != 1 {
		t.Errorf("%d documents are stored for one file in one base, want 1", got)
	}

	// One document means one set of chunks: the loser's chunks are gone with its
	// document, so retrieval cannot return the same text twice as two sources.
	chunks, err := h.repo.Chunks(context.Background(), documents[0].ID, documents[0].ActiveRevision)
	if err != nil {
		t.Fatalf("read the document's chunks: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("the stored document has no chunks")
	}
	if got := countRows(t, h.resource, "document_chunks"); got != int64(len(chunks)) {
		t.Errorf("%d chunks are stored but the document has %d, want no second set",
			got, len(chunks))
	}
	for _, chunk := range chunks {
		if chunk.Content == "" || chunk.CharEnd <= chunk.CharStart {
			t.Errorf("chunk %s is %+v, want text and a span", chunk.ChunkID, chunk)
		}
	}
}

func TestDBOnlyOneOfTwoWorkersIndexesADocument(t *testing.T) {
	h := newDBHarness(t)
	base := h.createBase(t)
	fileID := libraryFileID(1)
	h.storeFile(t, fileID, "alpha beta gamma delta epsilon")
	document, err := h.service.AddDocument(context.Background(), fileID, base.ID)
	if err != nil {
		t.Fatalf("add the file: %v", err)
	}

	// Two workers, each with its own provider, over one document. Whichever
	// claims it embeds it and the other finds nothing to do: a claim is a row
	// another worker has to lose on, not a convention between them.
	//
	// The vectors are the width the column is. A two-dimensional stand-in would
	// be refused by the database, which is worth knowing about the stand-in the
	// rest of this package embeds with.
	dimensions := make([]float32, vectorDimensions)
	dimensions[0] = 1
	workers := make([]*Worker, 2)
	embedders := make([]*stubEmbedder, 2)
	for i := range workers {
		embedders[i] = &stubEmbedder{vector: embed.Vector(dimensions)}
		worker, err := NewWorker(WorkerOptions{
			Lease: time.Minute, Workers: 1, EmbedBatchSize: 2, Poll: time.Millisecond,
		}, h.repo, embedders[i])
		if err != nil {
			t.Fatalf("build the index worker: %v", err)
		}
		workers[i] = worker
	}

	start := make(chan struct{})
	worked := make([]bool, len(workers))
	errs := make([]error, len(workers))
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			worked[i], errs[i] = workers[i].IndexOne(context.Background())
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("worker %d: %v", i, err)
		}
	}
	if worked[0] == worked[1] {
		t.Errorf("the workers report %v, want exactly one of them to have found the document", worked)
	}

	// The document is done, and every chunk of it carries a vector — written
	// once, by the worker that held it.
	stored, err := h.repo.Document(context.Background(), document.ID)
	if err != nil {
		t.Fatalf("read the document back: %v", err)
	}
	if stored.VectorState != VectorStateReady {
		t.Errorf("the indexed document is %q, want %q", stored.VectorState, VectorStateReady)
	}
	chunks, err := h.repo.Chunks(context.Background(), document.ID, document.ActiveRevision)
	if err != nil {
		t.Fatalf("read the document's chunks: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("the document has no chunks")
	}
	for _, chunk := range chunks {
		if chunk.Embedding == "" {
			t.Errorf("chunk %s has no vector, want every chunk of a ready document to have one", chunk.ChunkID)
		}
	}
	// The work was done once: no provider call by the worker that lost.
	if called := embedders[0].calls + embedders[1].calls; called == 0 {
		t.Error("neither worker asked the provider for anything, want the one that claimed the document to")
	} else if (embedders[0].calls > 0) == (embedders[1].calls > 0) {
		t.Errorf("both workers called the provider (%d and %d calls), want one document embedded once",
			embedders[0].calls, embedders[1].calls)
	}
}

func TestDBAddingAFileToTwoBasesParsesItOnceEach(t *testing.T) {
	h := newDBHarness(t)
	first, second := h.createBase(t), h.createBase(t)
	fileID := libraryFileID(1)
	h.storeFile(t, fileID, "alpha beta gamma")

	// A file in two collections is two documents, and the second one is written
	// from the same bytes: the parse is per place, not per file.
	for _, base := range []KnowledgeBase{first, second} {
		if _, err := h.service.AddDocument(context.Background(), fileID, base.ID); err != nil {
			t.Fatalf("add the file to %s: %v", base.ID, err)
		}
	}
	documents, err := h.repo.Documents(context.Background(), first.ID)
	if err != nil {
		t.Fatalf("list the documents of %s: %v", first.ID, err)
	}
	if len(documents) != 1 {
		t.Fatalf("%s lists %d documents, want 1", first.ID, len(documents))
	}

	// Asking again for a file that is already there returns the document that
	// is there, and does not parse it a second time.
	again, err := h.service.AddDocument(context.Background(), fileID, first.ID)
	if err != nil {
		t.Fatalf("add the file again: %v", err)
	}
	if again.ID != documents[0].ID {
		t.Errorf("the second request returned %s, want the document already stored (%s)",
			again.ID, documents[0].ID)
	}
	if got := countRows(t, h.resource, "documents"); got != 2 {
		t.Errorf("%d documents are stored, want one per base", got)
	}
	if opened := h.files.opened; !slices.Equal(opened, []string{fileID, fileID}) {
		t.Errorf("the file was opened %v, want once per place: %d times", opened, 2)
	}
}
