package kb

// The stand-ins and the harness every test in this package builds on: a file
// library, an embedding provider, and a service wired the way the composition
// root wires it.

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"backend/internal/authz"
	"backend/internal/embed"
	"backend/internal/parse"
)

// stubFile is one stored file, as a test writes it.
type stubFile struct {
	Filename string
	MIMEType string
	Category string
	Content  string
}

// stubFiles stands in for the file library. It hands out a fresh reader per
// Open, and records what was opened: "this file was parsed once" is a property
// worth being able to assert, because parsing is the expensive half of adding a
// document.
//
// The record is behind a lock because two callers adding one file at the same
// time is a case this package tests: the second Open of one file is a parse
// paid for and thrown away, which is a thing to see rather than to race on.
type stubFiles struct {
	mu     sync.Mutex
	files  map[string]stubFile
	err    error
	opened []string
	// openedBy is the account each Open was made on behalf of, in step with
	// opened: what a caller to this package carries has to reach the library, and
	// an id in a file's place is not the same as an account asking for it.
	openedBy []int64
}

// Open ignores the caller and records who asked, which is the one thing a
// stand-in for storage can honestly do here: a file's owner is a column in
// another domain's table, and a fake that pretended to check it would be
// asserting its own copy of a rule. What the ownership of a file does is
// checked where the file row is read, in the library package and against a real
// database. What is worth seeing from here is that the account reached this
// far — a turn's caller has to travel through the knowledge base to the file it
// parses, and this is that it did.
func (s *stubFiles) Open(_ context.Context, fileID string, caller authz.Caller) (SourceFile, error) {
	s.mu.Lock()
	s.opened = append(s.opened, fileID)
	s.openedBy = append(s.openedBy, caller.UserID)
	s.mu.Unlock()
	if s.err != nil {
		return SourceFile{}, s.err
	}
	file, ok := s.files[fileID]
	if !ok {
		return SourceFile{}, fmt.Errorf("%w: file %s", ErrNotFound, fileID)
	}
	return SourceFile{
		FileID: fileID, Filename: file.Filename, MIMEType: file.MIMEType,
		Category: file.Category, Content: io.NopCloser(strings.NewReader(file.Content)),
	}, nil
}

// stubEmbedder stands in for the provider, and can be made to fail so that the
// degradation retrieval leans on can be exercised.
type stubEmbedder struct {
	vector embed.Vector
	err    error
	// errs are handed out one per call before err is looked at, so a test can
	// fail one call and have the next succeed — which is what "the provider came
	// back" looks like from here. A nil entry is a call that goes through.
	errs  []error
	texts []string
	// calls counts the provider calls, which is how "no further request after
	// the caller stopped" is checked.
	calls int
	// onCall runs before a call is answered, which is where a test makes the
	// caller stop halfway through.
	onCall func(call int)
}

func (e *stubEmbedder) Embed(_ context.Context, texts []string) (embed.Result, error) {
	e.calls++
	if e.onCall != nil {
		e.onCall(e.calls)
	}
	e.texts = append(e.texts, texts...)
	if len(e.errs) > 0 {
		err := e.errs[0]
		e.errs = e.errs[1:]
		if err != nil {
			return embed.Result{}, err
		}
	} else if e.err != nil {
		return embed.Result{}, e.err
	}
	vectors := make([]embed.Vector, len(texts))
	for index, text := range texts {
		if len(e.vector) > 0 {
			vectors[index] = e.vector
			continue
		}
		vectors[index] = derivedVector(text)
	}
	return embed.Result{Vectors: vectors}, nil
}

// derivedVector is a text's vector when a test wants the vectors told apart
// rather than all being the same one. It is a function of the text, so a test
// can work out what a chunk's vector should be without asking the stand-in.
func derivedVector(text string) embed.Vector {
	sum := 0
	for _, r := range text {
		sum += int(r)
	}
	return embed.Vector{float32(utf8.RuneCountInString(text)), float32(sum % 9973)}
}

// harness is one service with its stand-ins, wired the way the composition root
// wires it.
type harness struct {
	service  *Service
	repo     *fakeRepository
	files    *stubFiles
	embedder *stubEmbedder
}

// testOptions are small enough that a two-line fixture has several chunks and a
// window is a few of them.
func testOptions() Options {
	return Options{
		Chunking:            Chunking{Size: 20, Overlap: 5},
		RetrievalTopK:       3,
		Candidates:          10,
		RRFK:                60,
		MaxCharsPerDocument: 1000,
		EmbeddingModel:      "test-embed",
	}
}

func newHarness(t *testing.T, options ...func(*Options)) *harness {
	t.Helper()
	settings := testOptions()
	for _, adjust := range options {
		adjust(&settings)
	}
	registry, err := parse.NewRegistry(parse.Options{
		DefaultEngine: parse.EngineText, Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("build the parser registry: %v", err)
	}
	files := &stubFiles{files: map[string]stubFile{}}
	embedder := &stubEmbedder{vector: embed.Vector{1, 0}}
	repository := newFakeRepository()
	service, err := NewService(settings, repository, registry, files, embedder)
	if err != nil {
		t.Fatalf("build the service: %v", err)
	}
	return &harness{service: service, repo: repository, files: files, embedder: embedder}
}

func (h *harness) addFile(fileID, name, content string) {
	h.files.files[fileID] = stubFile{
		Filename: name, MIMEType: "text/markdown", Category: parse.CategoryMarkdown,
		Content: content,
	}
}

// base creates a knowledge base, asserting it was created.
func (h *harness) base(t *testing.T, name string) KnowledgeBase {
	t.Helper()
	base, err := h.service.CreateKnowledgeBase(context.Background(), name, "", testCaller())
	if err != nil {
		t.Fatalf("CreateKnowledgeBase(%q) error = %v", name, err)
	}
	return base
}

// addDocument parses a fixture into a collection and returns the document.
func (h *harness) addDocument(t *testing.T, kbID, fileID, content string) Document {
	t.Helper()
	h.addFile(fileID, fileID+".md", content)
	document, err := h.service.AddDocument(context.Background(), fileID, kbID, testCaller())
	if err != nil {
		t.Fatalf("AddDocument(%q) error = %v", fileID, err)
	}
	return document
}

const sampleText = "闭包是一个函数和它捕获的环境。\n\n第二段说的是另一件事，比较长一些，用来产生多个分块。"

// worker builds an index worker over the same stand-ins the service uses, wired
// the way the composition root wires it.
func (h *harness) worker(t *testing.T, adjust ...func(*WorkerOptions)) *Worker {
	t.Helper()
	options := WorkerOptions{
		Lease: time.Second, Workers: 1, EmbedBatchSize: 2, Poll: time.Millisecond,
	}
	for _, apply := range adjust {
		apply(&options)
	}
	// The other tests hand out one vector for every text, which is all a
	// retrieval cares about. An index test wants to say which chunk got which
	// vector, so the stand-in switches to the text-derived one.
	h.embedder.vector = nil
	worker, err := NewWorker(options, h.repo, h.embedder)
	if err != nil {
		t.Fatalf("build the index worker: %v", err)
	}
	return worker
}

// stored reads a document back, asserting it is still there.
func (h *harness) stored(t *testing.T, documentID string) Document {
	t.Helper()
	document, err := h.repo.Document(context.Background(), documentID, testCaller())
	if err != nil {
		t.Fatalf("read document %s: %v", documentID, err)
	}
	return document
}
