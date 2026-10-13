package kb

// The stand-ins and the harness every test in this package builds on: a file
// library, an embedding provider, and a service wired the way the composition
// root wires it.

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

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
type stubFiles struct {
	files  map[string]stubFile
	err    error
	opened []string
}

func (s *stubFiles) Open(_ context.Context, fileID string) (SourceFile, error) {
	s.opened = append(s.opened, fileID)
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
	texts  []string
}

func (e *stubEmbedder) Embed(_ context.Context, texts []string) (embed.Result, error) {
	e.texts = append(e.texts, texts...)
	if e.err != nil {
		return embed.Result{}, e.err
	}
	vectors := make([]embed.Vector, len(texts))
	for index := range texts {
		vectors[index] = e.vector
	}
	return embed.Result{Vectors: vectors}, nil
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
	base, err := h.service.CreateKnowledgeBase(context.Background(), name, "")
	if err != nil {
		t.Fatalf("CreateKnowledgeBase(%q) error = %v", name, err)
	}
	return base
}

// addDocument parses a fixture into a collection and returns the document.
func (h *harness) addDocument(t *testing.T, kbID, fileID, content string) Document {
	t.Helper()
	h.addFile(fileID, fileID+".md", content)
	document, err := h.service.AddDocument(context.Background(), fileID, kbID)
	if err != nil {
		t.Fatalf("AddDocument(%q) error = %v", fileID, err)
	}
	return document
}

const sampleText = "闭包是一个函数和它捕获的环境。\n\n第二段说的是另一件事，比较长一些，用来产生多个分块。"
