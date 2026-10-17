package book

// The storage claims in this file are the ones a test double cannot make: that
// an owner's rows are the only rows a read returns, that deleting a book takes
// its chapters with it, that an outline cannot be replaced once a chapter is
// ready, and that a chapter write is narrowed by the revision it was generated
// under. They run against a real seekdb in this package's own database (see
// internal/data/testdb), so nothing here calls t.Parallel and every test starts
// from empty tables.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"backend/internal/authz"
	"backend/internal/data"
	"backend/internal/data/testdb"
)

const (
	dbOwnerID int64 = 7
	dbOtherID int64 = 8
)

var bookDBTables = []string{"book_chapters", "books", "users"}

func newDBRepository(t *testing.T) (*GORMRepository, *data.Resource) {
	t.Helper()
	resource := testdb.Open(t, "book")
	testdb.Reset(t, resource, bookDBTables...)
	testdb.Account(t, resource, dbOwnerID, "book_owner")
	testdb.Account(t, resource, dbOtherID, "book_other")
	return NewGORMRepository(resource.GORM()), resource
}

func storedBook(ownerID int64, bookID, title, sourceText string) Book {
	return Book{
		Summary: Summary{
			BookID:      bookID,
			Title:       title,
			SourceKind:  SourceSession,
			SourceID:    "ses_1",
			SourceTitle: "一段对话",
			SourceChars: len([]rune(sourceText)),
		},
		OwnerID:    ownerID,
		SourceText: sourceText,
	}
}

func outlineOf(bookID string, titles ...string) []Chapter {
	chapters := make([]Chapter, 0, len(titles))
	for index, title := range titles {
		chapters = append(chapters, Chapter{
			ChapterID: "bc_" + bookID + "_" + string(rune('a'+index)),
			BookID:    bookID,
			Title:     title,
		})
	}
	return chapters
}

func TestTheShelfIsOneOwnersBooks(t *testing.T) {
	repository, _ := newDBRepository(t)
	ctx := context.Background()
	mine := storedBook(dbOwnerID, "bk_mine", "我的书", "材料")
	theirs := storedBook(dbOtherID, "bk_theirs", "别人的书", "材料")
	if err := repository.Create(ctx, mine); err != nil {
		t.Fatalf("Create(mine) error = %v", err)
	}
	if err := repository.Create(ctx, theirs); err != nil {
		t.Fatalf("Create(theirs) error = %v", err)
	}
	if _, err := repository.ReplaceOutline(ctx, mine.BookID, authz.Caller{UserID: dbOwnerID}, true,
		outlineOf(mine.BookID, "安装", "维修")); err != nil {
		t.Fatalf("ReplaceOutline() error = %v", err)
	}
	chapter, err := repository.Chapter(ctx, mine.BookID, 1, authz.Caller{UserID: dbOwnerID})
	if err != nil {
		t.Fatalf("Chapter() error = %v", err)
	}
	if ok, err := repository.SaveChapter(ctx, chapter.ChapterID, mine.BookID, 1, "正文", authz.Caller{UserID: dbOwnerID}); err != nil || !ok {
		t.Fatalf("SaveChapter() = %v, %v; want true", ok, err)
	}

	listed, err := repository.List(ctx, authz.Caller{UserID: dbOwnerID})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 || listed[0].BookID != mine.BookID {
		t.Fatalf("List() = %+v, want only the caller's book", listed)
	}
	if listed[0].ChapterCount != 2 || listed[0].ReadyChapters != 1 {
		t.Errorf("counts = %d/%d, want 1 ready of 2", listed[0].ReadyChapters, listed[0].ChapterCount)
	}
	if _, err := repository.Get(ctx, theirs.BookID, authz.Caller{UserID: dbOwnerID}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(other's book) error = %v, want ErrNotFound", err)
	}
}

func TestDeletingABookDeletesItsChapters(t *testing.T) {
	repository, resource := newDBRepository(t)
	ctx := context.Background()
	record := storedBook(dbOwnerID, "bk_1", "书", "材料")
	if err := repository.Create(ctx, record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := repository.ReplaceOutline(ctx, record.BookID, authz.Caller{UserID: dbOwnerID}, false,
		outlineOf(record.BookID, "第一章", "第二章")); err != nil {
		t.Fatalf("ReplaceOutline() error = %v", err)
	}

	if err := repository.Delete(ctx, record.BookID, authz.Caller{UserID: dbOwnerID}); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	var chapters int64
	if err := resource.GORM().Table("book_chapters").Count(&chapters).Error; err != nil {
		t.Fatalf("count chapters: %v", err)
	}
	if chapters != 0 {
		t.Errorf("chapters = %d after deleting the book, want 0", chapters)
	}
}

func TestAnOutlineCannotBeReplacedOnceAChapterIsReady(t *testing.T) {
	repository, _ := newDBRepository(t)
	ctx := context.Background()
	caller := authz.Caller{UserID: dbOwnerID}
	record := storedBook(dbOwnerID, "bk_1", "书", "材料")
	if err := repository.Create(ctx, record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := repository.ReplaceOutline(ctx, record.BookID, caller, true,
		outlineOf(record.BookID, "安装")); err != nil {
		t.Fatalf("ReplaceOutline() error = %v", err)
	}
	chapter, err := repository.Chapter(ctx, record.BookID, 1, caller)
	if err != nil {
		t.Fatalf("Chapter() error = %v", err)
	}
	if ok, err := repository.SaveChapter(ctx, chapter.ChapterID, record.BookID, 1, "正文", caller); err != nil || !ok {
		t.Fatalf("SaveChapter() = %v, %v", ok, err)
	}

	_, err = repository.ReplaceOutline(ctx, record.BookID, caller, true,
		outlineOf(record.BookID, "别的"))

	if !errors.Is(err, ErrOutlineLocked) {
		t.Fatalf("ReplaceOutline() error = %v, want ErrOutlineLocked", err)
	}
}

func TestReplacingAnOutlineBumpsTheRevisionAndClearsProgress(t *testing.T) {
	repository, _ := newDBRepository(t)
	ctx := context.Background()
	caller := authz.Caller{UserID: dbOwnerID}
	record := storedBook(dbOwnerID, "bk_1", "书", "材料")
	if err := repository.Create(ctx, record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := repository.ReplaceOutline(ctx, record.BookID, caller, true,
		outlineOf(record.BookID, "安装", "维修")); err != nil {
		t.Fatalf("ReplaceOutline() error = %v", err)
	}
	if err := repository.SaveProgress(ctx, record.BookID, 2, caller); err != nil {
		t.Fatalf("SaveProgress() error = %v", err)
	}

	revision, err := repository.ReplaceOutline(ctx, record.BookID, caller, false,
		outlineOf(record.BookID, "只有一章"))
	if err != nil {
		t.Fatalf("second ReplaceOutline() error = %v", err)
	}

	if revision != 2 {
		t.Errorf("revision = %d, want 2", revision)
	}
	stored, err := repository.Get(ctx, record.BookID, caller)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if stored.OutlineRevision != 2 || stored.OutlineConfirmed || stored.LastOrdinal != 0 {
		t.Errorf("book = %+v, want revision 2, unconfirmed, progress cleared", stored.Summary)
	}
	chapters, err := repository.Chapters(ctx, record.BookID, caller)
	if err != nil {
		t.Fatalf("Chapters() error = %v", err)
	}
	if len(chapters) != 1 || chapters[0].Ordinal != 1 {
		t.Errorf("chapters = %+v, want the one replacement chapter", chapters)
	}
}

func TestAChapterWriteNeedsTheRevisionItStartedUnder(t *testing.T) {
	repository, _ := newDBRepository(t)
	ctx := context.Background()
	caller := authz.Caller{UserID: dbOwnerID}
	record := storedBook(dbOwnerID, "bk_1", "书", "材料")
	if err := repository.Create(ctx, record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	revision, err := repository.ReplaceOutline(ctx, record.BookID, caller, true,
		outlineOf(record.BookID, "安装", "维修"))
	if err != nil {
		t.Fatalf("ReplaceOutline() error = %v", err)
	}
	target, err := repository.Chapter(ctx, record.BookID, 1, caller)
	if err != nil {
		t.Fatalf("Chapter() error = %v", err)
	}

	stale, err := repository.SaveChapter(ctx, target.ChapterID, record.BookID, revision-1, "旧版本写的", caller)
	if err != nil {
		t.Fatalf("SaveChapter(stale) error = %v", err)
	}
	if stale {
		t.Error("SaveChapter(stale) = true, want the revision guard to refuse it")
	}

	fresh, err := repository.SaveChapter(ctx, target.ChapterID, record.BookID, revision, "新版本写的", caller)
	if err != nil {
		t.Fatalf("SaveChapter(current) error = %v", err)
	}
	if !fresh {
		t.Error("SaveChapter(current) = false, want it stored")
	}
	stored, err := repository.Chapter(ctx, record.BookID, 1, caller)
	if err != nil {
		t.Fatalf("Chapter() error = %v", err)
	}
	if stored.Content != "新版本写的" || stored.Status != StatusReady {
		t.Errorf("chapter = %+v, want the current body", stored)
	}
}

func TestAFailedRegenerationKeepsTheOldBody(t *testing.T) {
	repository, _ := newDBRepository(t)
	ctx := context.Background()
	caller := authz.Caller{UserID: dbOwnerID}
	record := storedBook(dbOwnerID, "bk_1", "书", "材料")
	if err := repository.Create(ctx, record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	revision, err := repository.ReplaceOutline(ctx, record.BookID, caller, true,
		outlineOf(record.BookID, "安装", "维修"))
	if err != nil {
		t.Fatalf("ReplaceOutline() error = %v", err)
	}
	first, err := repository.Chapter(ctx, record.BookID, 1, caller)
	if err != nil {
		t.Fatalf("Chapter(1) error = %v", err)
	}
	second, err := repository.Chapter(ctx, record.BookID, 2, caller)
	if err != nil {
		t.Fatalf("Chapter(2) error = %v", err)
	}
	if ok, err := repository.SaveChapter(ctx, first.ChapterID, record.BookID, revision, "旧正文", caller); err != nil || !ok {
		t.Fatalf("SaveChapter(first) = %v, %v", ok, err)
	}

	if err := repository.FailChapter(ctx, first.ChapterID, record.BookID, revision, "generation_failed", caller); err != nil {
		t.Fatalf("FailChapter(ready) error = %v", err)
	}
	if err := repository.FailChapter(ctx, second.ChapterID, record.BookID, revision, "generation_failed", caller); err != nil {
		t.Fatalf("FailChapter(pending) error = %v", err)
	}

	kept, err := repository.Chapter(ctx, record.BookID, 1, caller)
	if err != nil {
		t.Fatalf("Chapter(1) error = %v", err)
	}
	if kept.Content != "旧正文" || kept.Status != StatusReady {
		t.Errorf("chapter 1 = %+v, want the old body kept", kept)
	}
	failed, err := repository.Chapter(ctx, record.BookID, 2, caller)
	if err != nil {
		t.Fatalf("Chapter(2) error = %v", err)
	}
	if failed.Status != StatusFailed || failed.ErrorCode != "generation_failed" {
		t.Errorf("chapter 2 = %+v, want it recorded as failed", failed)
	}
}

func TestExportReadsOnlyReadyChaptersInOrder(t *testing.T) {
	repository, _ := newDBRepository(t)
	ctx := context.Background()
	caller := authz.Caller{UserID: dbOwnerID}
	record := storedBook(dbOwnerID, "bk_1", "书", "材料")
	if err := repository.Create(ctx, record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	revision, err := repository.ReplaceOutline(ctx, record.BookID, caller, true,
		outlineOf(record.BookID, "三", "一", "二"))
	if err != nil {
		t.Fatalf("ReplaceOutline() error = %v", err)
	}
	third, _ := repository.Chapter(ctx, record.BookID, 3, caller)
	first, _ := repository.Chapter(ctx, record.BookID, 1, caller)
	if ok, err := repository.SaveChapter(ctx, third.ChapterID, record.BookID, revision, "第三章正文", caller); err != nil || !ok {
		t.Fatalf("SaveChapter(3) = %v, %v", ok, err)
	}
	if ok, err := repository.SaveChapter(ctx, first.ChapterID, record.BookID, revision, "第一章正文", caller); err != nil || !ok {
		t.Fatalf("SaveChapter(1) = %v, %v", ok, err)
	}

	ready, err := repository.ReadyChapters(ctx, record.BookID, caller)
	if err != nil {
		t.Fatalf("ReadyChapters() error = %v", err)
	}

	if len(ready) != 2 || ready[0].Ordinal != 1 || ready[1].Ordinal != 3 {
		t.Fatalf("ready = %+v, want chapters 1 and 3 in order", ready)
	}
	if ready[1].Content != "第三章正文" {
		t.Errorf("chapter 3 content = %q, want it read back", ready[1].Content)
	}
}

func TestALargeSnapshotIsStoredAndReadBackWithoutTheShelfSelectingIt(t *testing.T) {
	repository, _ := newDBRepository(t)
	ctx := context.Background()
	caller := authz.Caller{UserID: dbOwnerID}
	// Longer than a VARCHAR would take, which is why the column is MEDIUMTEXT
	// and why a shelf query must not be tempted to select it.
	record := storedBook(dbOwnerID, "bk_1", "书", strings.Repeat("材料", SourceBudgetRunes/2))
	if err := repository.Create(ctx, record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := repository.List(ctx, caller); err != nil {
		t.Fatalf("List() error = %v", err)
	}

	text, err := repository.SourceText(ctx, record.BookID, caller)

	if err != nil {
		t.Fatalf("SourceText() error = %v", err)
	}
	if text != record.SourceText {
		t.Errorf("SourceText() returned %d runes, want %d", len([]rune(text)), len([]rune(record.SourceText)))
	}
	if _, err := repository.SourceText(ctx, record.BookID, authz.Caller{UserID: dbOtherID}); !errors.Is(err, ErrNotFound) {
		t.Errorf("SourceText(other account) error = %v, want ErrNotFound", err)
	}
}

func TestProgressIsStoredAndBoundedByTheOutlineTheServiceSees(t *testing.T) {
	repository, _ := newDBRepository(t)
	ctx := context.Background()
	caller := authz.Caller{UserID: dbOwnerID}
	record := storedBook(dbOwnerID, "bk_1", "书", "材料")
	if err := repository.Create(ctx, record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := repository.ReplaceOutline(ctx, record.BookID, caller, true,
		outlineOf(record.BookID, "安装", "维修")); err != nil {
		t.Fatalf("ReplaceOutline() error = %v", err)
	}

	if err := repository.SaveProgress(ctx, record.BookID, 2, caller); err != nil {
		t.Fatalf("SaveProgress() error = %v", err)
	}

	stored, err := repository.Get(ctx, record.BookID, caller)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if stored.LastOrdinal != 2 {
		t.Errorf("LastOrdinal = %d, want 2", stored.LastOrdinal)
	}
	if err := repository.SaveProgress(ctx, record.BookID, 0, authz.Caller{UserID: dbOtherID}); !errors.Is(err, ErrNotFound) {
		t.Errorf("SaveProgress(other account) error = %v, want ErrNotFound", err)
	}
}

// The book row's update_time moves when progress is written, which is what puts
// a book the reader is in the middle of at the top of the shelf.
func TestWritingProgressMovesTheBookUpTheShelf(t *testing.T) {
	repository, resource := newDBRepository(t)
	ctx := context.Background()
	caller := authz.Caller{UserID: dbOwnerID}
	older := storedBook(dbOwnerID, "bk_older", "先写的", "材料")
	newer := storedBook(dbOwnerID, "bk_newer", "后写的", "材料")
	for _, record := range []Book{older, newer} {
		if err := repository.Create(ctx, record); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}
	// Both books are pushed back an hour first, so the only thing that can put
	// one in front of the other is the progress write below. DATETIME keeps
	// whole seconds, hence the explicit value rather than a sleep.
	if err := resource.GORM().Exec(
		"UPDATE books SET update_time = ?", time.Now().Add(-time.Hour),
	).Error; err != nil {
		t.Fatalf("age the books: %v", err)
	}
	if _, err := repository.ReplaceOutline(ctx, older.BookID, caller, true,
		outlineOf(older.BookID, "安装")); err != nil {
		t.Fatalf("ReplaceOutline() error = %v", err)
	}
	if err := repository.SaveProgress(ctx, older.BookID, 1, caller); err != nil {
		t.Fatalf("SaveProgress() error = %v", err)
	}

	listed, err := repository.List(ctx, caller)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	if len(listed) != 2 || listed[0].BookID != older.BookID {
		t.Errorf("shelf = %+v, want the book progress was written to first", listed)
	}
}
