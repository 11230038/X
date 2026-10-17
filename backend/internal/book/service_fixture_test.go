package book

import (
	"context"
	"errors"
	"sync"
	"testing"

	"backend/internal/authz"
	"backend/internal/llm"
	"backend/internal/usage"
)

// memoryRepository is the book store for these cases. It keeps the same rules
// the SQL repository enforces — the owner narrows every read, and the outline
// revision guards every chapter write — without a database, because what is
// being tested is the service's order of operations, not the query it compiles
// to. The SQL itself is tested against a real seekdb in its own file.
type memoryRepository struct {
	mu       sync.Mutex
	books    map[string]Book
	chapters map[string][]Chapter
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{books: map[string]Book{}, chapters: map[string][]Chapter{}}
}

func (r *memoryRepository) owns(book Book, caller authz.Caller) bool {
	return book.OwnerID == caller.UserID
}

func (r *memoryRepository) List(_ context.Context, caller authz.Caller) ([]Summary, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	listed := make([]Summary, 0, len(r.books))
	for _, record := range r.books {
		if !r.owns(record, caller) {
			continue
		}
		summary := record.Summary
		summary.ChapterCount, summary.ReadyChapters = countChapters(r.chapters[record.BookID])
		listed = append(listed, summary)
	}
	return listed, nil
}

func (r *memoryRepository) Get(_ context.Context, bookID string, caller authz.Caller) (Book, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ok := r.books[bookID]
	if !ok || !r.owns(record, caller) {
		return Book{}, ErrNotFound
	}
	return record, nil
}

func (r *memoryRepository) SourceText(_ context.Context, bookID string, caller authz.Caller) (string, error) {
	record, err := r.Get(context.Background(), bookID, caller)
	return record.SourceText, err
}

func (r *memoryRepository) Chapters(_ context.Context, bookID string, caller authz.Caller) ([]Chapter, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.books[bookID]; !ok || !r.owns(r.books[bookID], caller) {
		return nil, ErrNotFound
	}
	return append([]Chapter(nil), r.chapters[bookID]...), nil
}

func (r *memoryRepository) Chapter(_ context.Context, bookID string, ordinal int, caller authz.Caller) (Chapter, error) {
	chapters, err := r.Chapters(context.Background(), bookID, caller)
	if err != nil {
		return Chapter{}, err
	}
	chapter, ok := chapterAt(chapters, ordinal)
	if !ok {
		return Chapter{}, ErrNotFound
	}
	return chapter, nil
}

func (r *memoryRepository) Create(_ context.Context, record Book) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.books[record.BookID] = record
	return nil
}

func (r *memoryRepository) Delete(_ context.Context, bookID string, caller authz.Caller) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ok := r.books[bookID]
	if !ok || !r.owns(record, caller) {
		return ErrNotFound
	}
	delete(r.books, bookID)
	delete(r.chapters, bookID)
	return nil
}

func (r *memoryRepository) ReplaceOutline(_ context.Context, bookID string, caller authz.Caller, confirmed bool, chapters []Chapter) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ok := r.books[bookID]
	if !ok || !r.owns(record, caller) {
		return 0, ErrNotFound
	}
	if _, ready := countChapters(r.chapters[bookID]); ready > 0 {
		return 0, ErrOutlineLocked
	}
	revision := record.OutlineRevision + 1
	record.OutlineConfirmed = confirmed
	record.OutlineRevision = revision
	record.LastOrdinal = 0
	r.books[bookID] = record

	stored := make([]Chapter, 0, len(chapters))
	for index, chapter := range chapters {
		chapter.Ordinal = index + 1
		chapter.Status = StatusPending
		chapter.Content = ""
		stored = append(stored, chapter)
	}
	r.chapters[bookID] = stored
	return revision, nil
}

func (r *memoryRepository) SaveChapter(_ context.Context, chapterID, bookID string, revision int, content string, caller authz.Caller) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ok := r.books[bookID]
	if !ok || !r.owns(record, caller) || record.OutlineRevision != revision {
		return false, nil
	}
	chapters := r.chapters[bookID]
	for index := range chapters {
		if chapters[index].ChapterID == chapterID {
			chapters[index].Content = content
			chapters[index].Status = StatusReady
			chapters[index].ErrorCode = ""
			return true, nil
		}
	}
	return false, nil
}

func (r *memoryRepository) FailChapter(_ context.Context, chapterID, bookID string, revision int, code string, caller authz.Caller) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ok := r.books[bookID]
	if !ok || !r.owns(record, caller) || record.OutlineRevision != revision {
		return nil
	}
	chapters := r.chapters[bookID]
	for index := range chapters {
		if chapters[index].ChapterID == chapterID && chapters[index].Content == "" {
			chapters[index].Status = StatusFailed
			chapters[index].ErrorCode = code
		}
	}
	return nil
}

func (r *memoryRepository) SaveProgress(_ context.Context, bookID string, ordinal int, caller authz.Caller) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ok := r.books[bookID]
	if !ok || !r.owns(record, caller) {
		return ErrNotFound
	}
	record.LastOrdinal = ordinal
	r.books[bookID] = record
	return nil
}

func (r *memoryRepository) ReadyChapters(_ context.Context, bookID string, caller authz.Caller) ([]Chapter, error) {
	chapters, err := r.Chapters(context.Background(), bookID, caller)
	if err != nil {
		return nil, err
	}
	ready := make([]Chapter, 0, len(chapters))
	for _, chapter := range chapters {
		if chapter.Status == StatusReady {
			ready = append(ready, chapter)
		}
	}
	return ready, nil
}

type fakeSources struct {
	source Source
	err    error
}

func (s fakeSources) Load(context.Context, SourceKind, string, authz.Caller) (Source, error) {
	return s.source, s.err
}

// fakeProvider answers from a script and keeps what the call carried.
type fakeProvider struct {
	mu      sync.Mutex
	answers []string
	calls   int
	sources []string

	// started is closed when the first call arrives, so a concurrency case knows
	// the first request has actually reached the provider.
	started chan struct{}
	release chan struct{}
}

func (p *fakeProvider) Stream(ctx context.Context, _ llm.Request, onDelta func(llm.Delta) error) (llm.Response, error) {
	p.mu.Lock()
	index := p.calls
	p.calls++
	if call, ok := usage.CallFrom(ctx); ok {
		p.sources = append(p.sources, call.Source)
	}
	answer := ""
	if len(p.answers) > 0 {
		answer = p.answers[index%len(p.answers)]
	}
	started, release := p.started, p.release
	p.mu.Unlock()

	if started != nil {
		select {
		case <-started:
		default:
			close(started)
		}
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return llm.Response{}, ctx.Err()
		}
	}
	if answer == "" {
		return llm.Response{}, errors.New("no answer was scripted")
	}
	if err := onDelta(llm.Delta{Content: answer}); err != nil {
		return llm.Response{}, err
	}
	return llm.Response{}, nil
}

const oneChapterOutline = `{"chapters":[{"title":"安装","summary":"怎么安装"}]}`

func newTestService(t *testing.T, repository Repository, sources SourceLoader, provider llm.Provider) *Service {
	t.Helper()
	service, err := NewService(Options{
		Repository: repository,
		Sources:    sources,
		Provider:   provider,
		Model:      "test-model",
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

func createTestBook(t *testing.T, service *Service, caller authz.Caller) Detail {
	t.Helper()
	created, err := service.Create(context.Background(), caller, CreateInput{
		SourceKind: SourceSession,
		SourceID:   "ses_1",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return created
}
