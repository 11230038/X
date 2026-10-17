package book

import (
	"context"
	"errors"
	"testing"

	"backend/internal/authz"
	"backend/internal/usage"
)

func TestCreateSnapshotsTheSourceAndCountsTheSnapshot(t *testing.T) {
	repository := newMemoryRepository()
	source := Source{Kind: SourceSession, ID: "ses_1", Title: "对话标题", Text: repeatRunes("字", SourceBudgetRunes+10)}
	service := newTestService(t, repository, fakeSources{source: source}, nil)

	created := createTestBook(t, service, authz.Caller{UserID: 7})

	if created.Book.Title != "对话标题" {
		t.Errorf("Title = %q, want the source title as the fallback", created.Book.Title)
	}
	if created.Book.SourceChars != SourceBudgetRunes {
		t.Errorf("SourceChars = %d, want the stored snapshot's length %d", created.Book.SourceChars, SourceBudgetRunes)
	}
	if !created.Book.SourceTruncated {
		t.Error("SourceTruncated = false, want true: the source was longer than the budget")
	}
	if created.Outline == nil {
		t.Error("Outline = nil, want an empty array")
	}
	if created.Book.CreatedAt.IsZero() || created.Book.UpdatedAt.IsZero() {
		t.Errorf("timestamps = %v / %v, want them filled in on the create response",
			created.Book.CreatedAt, created.Book.UpdatedAt)
	}
}

func TestCreateRefusesASourceWithNoText(t *testing.T) {
	service := newTestService(t, newMemoryRepository(),
		fakeSources{source: Source{Text: "   "}}, nil)

	_, err := service.Create(context.Background(), authz.Caller{UserID: 7}, CreateInput{
		SourceKind: SourceFile, SourceID: "lib_1",
	})

	if !errors.Is(err, ErrSourceEmpty) {
		t.Fatalf("Create() error = %v, want ErrSourceEmpty", err)
	}
}

func TestGenerateOutlineStoresItUnconfirmedAndChargesTheBookSource(t *testing.T) {
	repository := newMemoryRepository()
	provider := &fakeProvider{answers: []string{oneChapterOutline}}
	service := newTestService(t, repository,
		fakeSources{source: Source{Text: "材料", Title: "对话"}}, provider)
	caller := authz.Caller{UserID: 7}
	created := createTestBook(t, service, caller)

	outlined, err := service.GenerateOutline(context.Background(), caller, created.Book.BookID)

	if err != nil {
		t.Fatalf("GenerateOutline() error = %v", err)
	}
	if outlined.Book.OutlineConfirmed {
		t.Error("OutlineConfirmed = true, want the generated outline to wait for the reader")
	}
	if len(outlined.Outline) != 1 || outlined.Outline[0].Title != "安装" {
		t.Errorf("Outline = %+v, want the generated chapter", outlined.Outline)
	}
	if len(provider.sources) != 1 || provider.sources[0] != usage.SourceBook {
		t.Errorf("sources = %v, want the call charged to %q", provider.sources, usage.SourceBook)
	}
}

func TestABrokenOutlineIsRepairedOnce(t *testing.T) {
	provider := &fakeProvider{answers: []string{"这不是 JSON", oneChapterOutline}}
	service := newTestService(t, newMemoryRepository(),
		fakeSources{source: Source{Text: "材料", Title: "对话"}}, provider)
	caller := authz.Caller{UserID: 7}
	created := createTestBook(t, service, caller)

	if _, err := service.GenerateOutline(context.Background(), caller, created.Book.BookID); err != nil {
		t.Fatalf("GenerateOutline() error = %v", err)
	}
	if provider.calls != 2 {
		t.Errorf("calls = %d, want the one repair", provider.calls)
	}
}

func TestATwiceBrokenOutlineIsAGenerationFailure(t *testing.T) {
	provider := &fakeProvider{answers: []string{"这不是 JSON"}}
	service := newTestService(t, newMemoryRepository(),
		fakeSources{source: Source{Text: "材料", Title: "对话"}}, provider)
	caller := authz.Caller{UserID: 7}
	created := createTestBook(t, service, caller)

	_, err := service.GenerateOutline(context.Background(), caller, created.Book.BookID)

	if !errors.Is(err, ErrGenerationFailed) {
		t.Fatalf("GenerateOutline() error = %v, want ErrGenerationFailed", err)
	}
	if provider.calls != 2 {
		t.Errorf("calls = %d, want exactly one repair", provider.calls)
	}
}

func TestASecondOutlineGenerationIsRefusedWhileTheFirstRuns(t *testing.T) {
	provider := &fakeProvider{
		answers: []string{oneChapterOutline},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	service := newTestService(t, newMemoryRepository(),
		fakeSources{source: Source{Text: "材料", Title: "对话"}}, provider)
	caller := authz.Caller{UserID: 7}
	created := createTestBook(t, service, caller)

	first := make(chan error, 1)
	go func() {
		_, err := service.GenerateOutline(context.Background(), caller, created.Book.BookID)
		first <- err
	}()
	<-provider.started

	_, err := service.GenerateOutline(context.Background(), caller, created.Book.BookID)
	if !errors.Is(err, ErrGenerationInFlight) {
		t.Errorf("second GenerateOutline() error = %v, want ErrGenerationInFlight", err)
	}
	close(provider.release)
	if err := <-first; err != nil {
		t.Fatalf("first GenerateOutline() error = %v", err)
	}
}

func TestAChapterCannotBeGeneratedBeforeTheOutlineIsConfirmed(t *testing.T) {
	service := newTestService(t, newMemoryRepository(),
		fakeSources{source: Source{Text: "材料", Title: "对话"}}, &fakeProvider{answers: []string{oneChapterOutline}})
	caller := authz.Caller{UserID: 7}
	created := createTestBook(t, service, caller)
	if _, err := service.GenerateOutline(context.Background(), caller, created.Book.BookID); err != nil {
		t.Fatalf("GenerateOutline() error = %v", err)
	}

	_, err := service.GenerateChapter(context.Background(), caller, created.Book.BookID, 1)

	if !errors.Is(err, ErrOutlineNotConfirmed) {
		t.Fatalf("GenerateChapter() error = %v, want ErrOutlineNotConfirmed", err)
	}
}

func TestTheOutlineIsLockedOnceAChapterIsReady(t *testing.T) {
	repository := newMemoryRepository()
	service := newTestService(t, repository,
		fakeSources{source: Source{Text: "材料", Title: "对话"}}, &fakeProvider{answers: []string{oneChapterOutline}})
	caller := authz.Caller{UserID: 7}
	created := createTestBook(t, service, caller)
	if _, err := service.PutOutline(context.Background(), caller, created.Book.BookID, OutlineInput{
		Chapters: []OutlineItemInput{{Title: "安装", Summary: "怎么装"}}, Confirmed: true,
	}); err != nil {
		t.Fatalf("PutOutline() error = %v", err)
	}
	if _, err := service.GenerateChapter(context.Background(), caller, created.Book.BookID, 1); err != nil {
		t.Fatalf("GenerateChapter() error = %v", err)
	}

	_, err := service.PutOutline(context.Background(), caller, created.Book.BookID, OutlineInput{
		Chapters: []OutlineItemInput{{Title: "别的", Summary: ""}}, Confirmed: true,
	})

	if !errors.Is(err, ErrOutlineLocked) {
		t.Fatalf("PutOutline() error = %v, want ErrOutlineLocked", err)
	}
}

func TestAChapterGenerationStoresItsBodyUnderTheCurrentRevision(t *testing.T) {
	repository := newMemoryRepository()
	service := newTestService(t, repository,
		fakeSources{source: Source{Text: "材料", Title: "对话"}},
		&fakeProvider{answers: []string{"# 安装\n正文。"}})
	caller := authz.Caller{UserID: 7}
	created := createTestBook(t, service, caller)
	if _, err := service.PutOutline(context.Background(), caller, created.Book.BookID, OutlineInput{
		Chapters: []OutlineItemInput{{Title: "安装", Summary: "怎么装"}}, Confirmed: true,
	}); err != nil {
		t.Fatalf("PutOutline() error = %v", err)
	}

	chapter, err := service.GenerateChapter(context.Background(), caller, created.Book.BookID, 1)

	if err != nil {
		t.Fatalf("GenerateChapter() error = %v", err)
	}
	if chapter.Status != StatusReady || chapter.Content != "正文。" {
		t.Errorf("Chapter = %+v, want a ready body with the repeated heading removed", chapter)
	}
}

func TestProgressBeyondTheOutlineIsRefused(t *testing.T) {
	service := newTestService(t, newMemoryRepository(),
		fakeSources{source: Source{Text: "材料", Title: "对话"}}, &fakeProvider{answers: []string{oneChapterOutline}})
	caller := authz.Caller{UserID: 7}
	created := createTestBook(t, service, caller)
	if _, err := service.PutOutline(context.Background(), caller, created.Book.BookID, OutlineInput{
		Chapters: []OutlineItemInput{{Title: "安装", Summary: ""}}, Confirmed: true,
	}); err != nil {
		t.Fatalf("PutOutline() error = %v", err)
	}

	if _, err := service.SaveProgress(context.Background(), caller, created.Book.BookID, 2); !errors.Is(err, ErrInvalidProgress) {
		t.Errorf("SaveProgress(2) error = %v, want ErrInvalidProgress", err)
	}
	summary, err := service.SaveProgress(context.Background(), caller, created.Book.BookID, 1)
	if err != nil {
		t.Fatalf("SaveProgress(1) error = %v", err)
	}
	if summary.LastOrdinal != 1 {
		t.Errorf("LastOrdinal = %d, want 1", summary.LastOrdinal)
	}
}

func TestGetWithoutAnOutlineAnswersAnEmptyArray(t *testing.T) {
	service := newTestService(t, newMemoryRepository(),
		fakeSources{source: Source{Text: "材料", Title: "对话"}}, nil)
	caller := authz.Caller{UserID: 7}
	created := createTestBook(t, service, caller)

	detail, err := service.Get(context.Background(), caller, created.Book.BookID)

	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if detail.Outline == nil {
		t.Error("Outline = nil, want an empty array")
	}
}

func repeatRunes(text string, count int) string {
	result := make([]rune, 0, count)
	runes := []rune(text)
	for index := 0; index < count; index++ {
		result = append(result, runes[index%len(runes)])
	}
	return string(result)
}
