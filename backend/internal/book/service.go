package book

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"backend/internal/authz"
	"backend/internal/llm"
	"backend/internal/usage"
)

// Options is what a Service is assembled from. Repository and Sources are
// required; a build without a provider still lists, reads and exports books and
// answers generation requests with ErrGenerationUnavailable rather than
// pretending the feature is missing.
type Options struct {
	Repository Repository
	Sources    SourceLoader
	Provider   llm.Provider
	Model      string
	Logger     *slog.Logger
}

// Service owns the book lifecycle: a source snapshotted once, an outline the
// reader confirms, and one body per chapter generated on request.
//
// Generation is synchronous and per chapter. There is no queue and no stored
// "generating" state; a request either produced a body or failed, and the
// client asks again. The two guards that make that safe are the process-local
// keyed locks and the outline revision checked in every chapter write.
type Service struct {
	repository Repository
	sources    SourceLoader
	provider   llm.Provider
	model      string
	logger     *slog.Logger
	locks      *keyedLocks
}

// NewService builds the book service.
func NewService(options Options) (*Service, error) {
	if options.Repository == nil {
		return nil, fmt.Errorf("book: the repository is required")
	}
	if options.Sources == nil {
		return nil, fmt.Errorf("book: the source loader is required")
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		repository: options.Repository,
		sources:    options.Sources,
		provider:   options.Provider,
		model:      strings.TrimSpace(options.Model),
		logger:     logger,
		locks:      newKeyedLocks(),
	}, nil
}

// List returns the caller's shelf.
func (s *Service) List(ctx context.Context, caller authz.Caller) ([]Summary, error) {
	if s == nil || s.repository == nil {
		return nil, fmt.Errorf("book: the service is not initialized")
	}
	return s.repository.List(ctx, caller)
}

// Get returns a book and its outline. A book with no outline yet answers with
// an empty array rather than null.
func (s *Service) Get(ctx context.Context, caller authz.Caller, bookID string) (Detail, error) {
	if s == nil || s.repository == nil {
		return Detail{}, fmt.Errorf("book: the service is not initialized")
	}
	record, err := s.repository.Get(ctx, bookID, caller)
	if err != nil {
		return Detail{}, err
	}
	chapters, err := s.repository.Chapters(ctx, bookID, caller)
	if err != nil {
		return Detail{}, err
	}
	summary := record.Summary
	summary.ChapterCount, summary.ReadyChapters = countChapters(chapters)
	outline := make([]OutlineItem, 0, len(chapters))
	for _, chapter := range chapters {
		outline = append(outline, OutlineItem{
			Ordinal:   chapter.Ordinal,
			Title:     chapter.Title,
			Summary:   chapter.Summary,
			Status:    chapter.Status,
			ErrorCode: chapter.ErrorCode,
		})
	}
	return Detail{Book: summary, Outline: outline}, nil
}

// Create snapshots a source and inserts the book.
//
// The snapshot is taken here and never refreshed. Later generation reads it
// from storage, so deleting the conversation or re-parsing the file cannot make
// the first half of a book disagree with the second half.
func (s *Service) Create(ctx context.Context, caller authz.Caller, input CreateInput) (Detail, error) {
	if s == nil || s.repository == nil {
		return Detail{}, fmt.Errorf("book: the service is not initialized")
	}
	kind := input.SourceKind
	if !kind.Valid() {
		return Detail{}, fmt.Errorf("%w: unknown source kind %q", ErrInvalidRequest, kind)
	}
	sourceID := strings.TrimSpace(input.SourceID)
	if kind == SourceQuestionBank {
		if sourceID != "" {
			return Detail{}, fmt.Errorf("%w: the question bank is named by the kind alone", ErrInvalidRequest)
		}
	} else if sourceID == "" {
		return Detail{}, fmt.Errorf("%w: source id is required", ErrInvalidRequest)
	}

	source, err := s.sources.Load(ctx, kind, sourceID, caller)
	if err != nil {
		return Detail{}, err
	}
	text := strings.TrimSpace(source.Text)
	if text == "" {
		return Detail{}, ErrSourceEmpty
	}
	snapshot, truncatedAtBudget := clipRunes(text, SourceBudgetRunes)
	truncated := source.Truncated || truncatedAtBudget

	bookID, err := newID(bookIDPrefix)
	if err != nil {
		return Detail{}, fmt.Errorf("book: could not allocate an id: %w", err)
	}
	title := cleanTitle(input.Title)
	if title == "" {
		title = cleanTitle(source.Title)
	}
	if title == "" {
		title = defaultBookTitle(kind)
	}

	// The response is the caller's first sight of the book, so it has to carry
	// the timestamps the row was written with; the repository only fills them
	// in on its own copy of the record.
	// DATETIME keeps whole seconds; truncating here makes the response match a
	// later GET instead of showing a precision the row never stored.
	now := time.Now().UTC().Truncate(time.Second)
	record := Book{
		Summary: Summary{
			BookID:          bookID,
			Title:           title,
			SourceKind:      kind,
			SourceID:        sourceID,
			SourceTitle:     clipTitle(source.Title, SourceTitleMaxRunes),
			SourceChars:     len([]rune(snapshot)),
			SourceTruncated: truncated,
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		OwnerID:    caller.UserID,
		SourceText: snapshot,
	}
	if err := s.repository.Create(ctx, record); err != nil {
		return Detail{}, err
	}
	return Detail{Book: record.Summary, Outline: make([]OutlineItem, 0)}, nil
}

// Delete removes one of the caller's books.
func (s *Service) Delete(ctx context.Context, caller authz.Caller, bookID string) error {
	if s == nil || s.repository == nil {
		return fmt.Errorf("book: the service is not initialized")
	}
	return s.repository.Delete(ctx, bookID, caller)
}

// GenerateOutline asks the model for an outline and stores it unconfirmed.
func (s *Service) GenerateOutline(ctx context.Context, caller authz.Caller, bookID string) (Detail, error) {
	if s == nil || s.repository == nil {
		return Detail{}, fmt.Errorf("book: the service is not initialized")
	}
	release, ok := s.locks.try(outlineKey(bookID))
	if !ok {
		return Detail{}, ErrGenerationInFlight
	}
	defer release()

	record, err := s.repository.Get(ctx, bookID, caller)
	if err != nil {
		return Detail{}, err
	}
	chapters, err := s.repository.Chapters(ctx, bookID, caller)
	if err != nil {
		return Detail{}, err
	}
	if _, ready := countChapters(chapters); ready > 0 {
		return Detail{}, ErrOutlineLocked
	}
	if s.provider == nil || s.model == "" {
		return Detail{}, ErrGenerationUnavailable
	}

	material, err := s.repository.SourceText(ctx, bookID, caller)
	if err != nil {
		return Detail{}, err
	}
	ctx = usage.WithCall(ctx, usage.Call{OwnerID: caller.UserID, Source: usage.SourceBook})
	items, err := s.askOutline(ctx, record.Title, record.SourceKind, record.SourceTitle, material)
	if err != nil {
		return Detail{}, err
	}
	if _, err := s.replaceOutline(ctx, bookID, caller, false, items); err != nil {
		return Detail{}, err
	}
	return s.Get(ctx, caller, bookID)
}

// PutOutline stores an outline the reader wrote or edited. Confirmed is theirs
// to set; the same call can confirm an outline and start the book.
func (s *Service) PutOutline(ctx context.Context, caller authz.Caller, bookID string, input OutlineInput) (Detail, error) {
	if s == nil || s.repository == nil {
		return Detail{}, fmt.Errorf("book: the service is not initialized")
	}
	release, ok := s.locks.try(outlineKey(bookID))
	if !ok {
		return Detail{}, ErrGenerationInFlight
	}
	defer release()

	if _, err := s.repository.Get(ctx, bookID, caller); err != nil {
		return Detail{}, err
	}
	items, err := validateOutline(input.Chapters)
	if err != nil {
		return Detail{}, err
	}
	if _, err := s.replaceOutline(ctx, bookID, caller, input.Confirmed, items); err != nil {
		return Detail{}, err
	}
	return s.Get(ctx, caller, bookID)
}

// replaceOutline turns outline items into chapter rows and writes them.
func (s *Service) replaceOutline(ctx context.Context, bookID string, caller authz.Caller, confirmed bool, items []OutlineItemInput) (int, error) {
	chapters := make([]Chapter, 0, len(items))
	for _, item := range items {
		chapterID, err := newID(chapterIDPrefix)
		if err != nil {
			return 0, fmt.Errorf("book: could not allocate a chapter id: %w", err)
		}
		chapters = append(chapters, Chapter{
			ChapterID: chapterID,
			BookID:    bookID,
			Title:     item.Title,
			Summary:   item.Summary,
			Status:    StatusPending,
		})
	}
	return s.repository.ReplaceOutline(ctx, bookID, caller, confirmed, chapters)
}

// GenerateChapter writes one chapter and stores it before answering.
//
// The chapter is regenerated whether it was pending, failed or ready. A failure
// is recorded on the row without erasing a body that was already there, so a
// reader who retried a chapter they had does not lose it.
func (s *Service) GenerateChapter(ctx context.Context, caller authz.Caller, bookID string, ordinal int) (Chapter, error) {
	if s == nil || s.repository == nil {
		return Chapter{}, fmt.Errorf("book: the service is not initialized")
	}
	if ordinal < 1 {
		return Chapter{}, fmt.Errorf("%w: chapter ordinal %d", ErrInvalidRequest, ordinal)
	}
	release, ok := s.locks.try(chapterKey(bookID, ordinal))
	if !ok {
		return Chapter{}, ErrGenerationInFlight
	}
	defer release()

	record, err := s.repository.Get(ctx, bookID, caller)
	if err != nil {
		return Chapter{}, err
	}
	if !record.OutlineConfirmed {
		return Chapter{}, ErrOutlineNotConfirmed
	}
	outline, err := s.repository.Chapters(ctx, bookID, caller)
	if err != nil {
		return Chapter{}, err
	}
	target, found := chapterAt(outline, ordinal)
	if !found {
		return Chapter{}, fmt.Errorf("%w: chapter %d", ErrNotFound, ordinal)
	}
	if s.provider == nil || s.model == "" {
		return Chapter{}, ErrGenerationUnavailable
	}

	material, err := s.repository.SourceText(ctx, bookID, caller)
	if err != nil {
		return Chapter{}, err
	}

	revision := record.OutlineRevision
	ctx = usage.WithCall(ctx, usage.Call{OwnerID: caller.UserID, Source: usage.SourceBook})
	content, generateErr := s.askChapter(ctx, record.Title, outline, target, material)
	if generateErr != nil {
		if err := s.repository.FailChapter(ctx, target.ChapterID, bookID, revision, "generation_failed", caller); err != nil {
			s.logger.WarnContext(ctx, "could not record a failed chapter",
				"book_id", bookID, "ordinal", ordinal, "cause", err)
		}
		return Chapter{}, generateErr
	}

	saved, err := s.repository.SaveChapter(ctx, target.ChapterID, bookID, revision, content, caller)
	if err != nil {
		return Chapter{}, err
	}
	if !saved {
		return Chapter{}, ErrOutlineChanged
	}
	return s.repository.Chapter(ctx, bookID, ordinal, caller)
}

// Chapter reads one chapter with its body.
func (s *Service) Chapter(ctx context.Context, caller authz.Caller, bookID string, ordinal int) (Chapter, error) {
	if s == nil || s.repository == nil {
		return Chapter{}, fmt.Errorf("book: the service is not initialized")
	}
	if ordinal < 1 {
		return Chapter{}, fmt.Errorf("%w: chapter ordinal %d", ErrInvalidRequest, ordinal)
	}
	return s.repository.Chapter(ctx, bookID, ordinal, caller)
}

// SaveProgress stores the chapter the reader last opened. Ordinal 0 means "not
// started"; anything beyond the outline is refused rather than clipped.
func (s *Service) SaveProgress(ctx context.Context, caller authz.Caller, bookID string, ordinal int) (Summary, error) {
	if s == nil || s.repository == nil {
		return Summary{}, fmt.Errorf("book: the service is not initialized")
	}
	if ordinal < 0 {
		return Summary{}, fmt.Errorf("%w: chapter ordinal %d", ErrInvalidProgress, ordinal)
	}
	chapters, err := s.repository.Chapters(ctx, bookID, caller)
	if err != nil {
		return Summary{}, err
	}
	if ordinal > len(chapters) {
		return Summary{}, fmt.Errorf("%w: chapter ordinal %d of %d", ErrInvalidProgress, ordinal, len(chapters))
	}
	if err := s.repository.SaveProgress(ctx, bookID, ordinal, caller); err != nil {
		return Summary{}, err
	}
	record, err := s.repository.Get(ctx, bookID, caller)
	if err != nil {
		return Summary{}, err
	}
	summary := record.Summary
	summary.ChapterCount, summary.ReadyChapters = countChapters(chapters)
	return summary, nil
}

// Export renders the generated chapters as one Markdown document.
func (s *Service) Export(ctx context.Context, caller authz.Caller, bookID string) (Export, error) {
	if s == nil || s.repository == nil {
		return Export{}, fmt.Errorf("book: the service is not initialized")
	}
	record, err := s.repository.Get(ctx, bookID, caller)
	if err != nil {
		return Export{}, err
	}
	chapters, err := s.repository.ReadyChapters(ctx, bookID, caller)
	if err != nil {
		return Export{}, err
	}
	if len(chapters) == 0 {
		return Export{}, ErrNoReadyChapters
	}
	return buildExport(record.Summary, chapters), nil
}

// countChapters returns how many chapters the outline has and how many of them
// have a body.
func countChapters(chapters []Chapter) (total, ready int) {
	for _, chapter := range chapters {
		if chapter.Status == StatusReady {
			ready++
		}
	}
	return len(chapters), ready
}

func chapterAt(chapters []Chapter, ordinal int) (Chapter, bool) {
	for _, chapter := range chapters {
		if chapter.Ordinal == ordinal {
			return chapter, true
		}
	}
	return Chapter{}, false
}

// defaultBookTitle is the last fallback when neither the reader nor the source
// named the book.
func defaultBookTitle(kind SourceKind) string {
	switch kind {
	case SourceSession:
		return "未命名对话"
	case SourceKnowledgeBase:
		return "知识库"
	case SourceFile:
		return "文件"
	case SourceQuestionBank:
		return "我的题库"
	default:
		return "未命名活书"
	}
}

func clipTitle(title string, limit int) string {
	clipped, _ := clipRunes(strings.TrimSpace(title), limit)
	return clipped
}
