package book

import (
	"context"
	"errors"
	"fmt"
	"time"

	"backend/internal/authz"
	"backend/internal/data"
	"backend/internal/data/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GORMRepository reads and writes books through GORM (seekdb over the MySQL
// protocol).
//
// The two rules this file exists to keep are: a list never selects source_text,
// and a chapter write is always narrowed both by the owner and by the outline
// revision the generation started under.
type GORMRepository struct {
	db *gorm.DB
}

// NewGORMRepository creates a storage adapter over an existing GORM handle.
func NewGORMRepository(db *gorm.DB) *GORMRepository {
	return &GORMRepository{db: db}
}

var _ Repository = (*GORMRepository)(nil)

// bookColumns is every column the metadata reads select. source_text is
// deliberately absent; SourceText is the only method that reads it.
const bookColumns = "book_id, owner_id, title, source_kind, source_id, source_title, source_chars, source_truncated, outline_confirmed, outline_revision, last_ordinal, creat_time, update_time"

const chapterColumns = "chapter_id, book_id, ordinal, title, summary, status, error_code, outline_revision, creat_time, update_time"

type shelfRow struct {
	BookID           string    `gorm:"column:book_id"`
	Title            string    `gorm:"column:title"`
	SourceKind       string    `gorm:"column:source_kind"`
	SourceID         string    `gorm:"column:source_id"`
	SourceTitle      string    `gorm:"column:source_title"`
	SourceChars      int       `gorm:"column:source_chars"`
	SourceTruncated  bool      `gorm:"column:source_truncated"`
	OutlineConfirmed bool      `gorm:"column:outline_confirmed"`
	OutlineRevision  int       `gorm:"column:outline_revision"`
	LastOrdinal      int       `gorm:"column:last_ordinal"`
	ChapterCount     int64     `gorm:"column:chapter_count"`
	ReadyChapters    int64     `gorm:"column:ready_chapters"`
	CreatedAt        time.Time `gorm:"column:creat_time"`
	UpdatedAt        time.Time `gorm:"column:update_time"`
}

// List returns the caller's shelf with its chapter counts.
func (r *GORMRepository) List(ctx context.Context, caller authz.Caller) ([]Summary, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	query := r.db.WithContext(ctx).
		Table("books AS b").
		Select(`b.book_id, b.title, b.source_kind, b.source_id, b.source_title,
			b.source_chars, b.source_truncated, b.outline_confirmed, b.outline_revision,
			b.last_ordinal, b.creat_time, b.update_time,
			COUNT(c.chapter_id) AS chapter_count,
			COALESCE(SUM(CASE WHEN c.status = 'ready' THEN 1 ELSE 0 END), 0) AS ready_chapters`).
		Joins("LEFT JOIN book_chapters AS c ON c.book_id = b.book_id")
	query = data.OwnerScope(query, caller.UserID, caller.IsAdmin(), "b.owner_id")

	var rows []shelfRow
	err := query.
		Group(`b.book_id, b.title, b.source_kind, b.source_id, b.source_title,
			b.source_chars, b.source_truncated, b.outline_confirmed, b.outline_revision,
			b.last_ordinal, b.creat_time, b.update_time`).
		Order("b.update_time DESC, b.book_id DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list books: %w", err)
	}
	books := make([]Summary, 0, len(rows))
	for _, row := range rows {
		books = append(books, Summary{
			BookID:           row.BookID,
			Title:            row.Title,
			SourceKind:       SourceKind(row.SourceKind),
			SourceID:         row.SourceID,
			SourceTitle:      row.SourceTitle,
			SourceChars:      row.SourceChars,
			SourceTruncated:  row.SourceTruncated,
			OutlineConfirmed: row.OutlineConfirmed,
			OutlineRevision:  row.OutlineRevision,
			LastOrdinal:      row.LastOrdinal,
			ChapterCount:     int(row.ChapterCount),
			ReadyChapters:    int(row.ReadyChapters),
			CreatedAt:        row.CreatedAt,
			UpdatedAt:        row.UpdatedAt,
		})
	}
	return books, nil
}

// Get returns one book's metadata, without its source snapshot.
func (r *GORMRepository) Get(ctx context.Context, bookID string, caller authz.Caller) (Book, error) {
	if err := r.ready(); err != nil {
		return Book{}, err
	}
	var record models.Book
	query := r.db.WithContext(ctx).
		Select(bookColumns).
		Where("book_id = ?", bookID)
	query = data.OwnerScope(query, caller.UserID, caller.IsAdmin(), "owner_id")
	err := query.Take(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Book{}, fmt.Errorf("%w: %s", ErrNotFound, bookID)
	}
	if err != nil {
		return Book{}, fmt.Errorf("read book: %w", err)
	}
	return toBook(record), nil
}

// SourceText reads the snapshot. It is separate from Get so a shelf or a detail
// read cannot accidentally select it.
func (r *GORMRepository) SourceText(ctx context.Context, bookID string, caller authz.Caller) (string, error) {
	if err := r.ready(); err != nil {
		return "", err
	}
	var record models.Book
	query := r.db.WithContext(ctx).
		Select("book_id, source_text").
		Where("book_id = ?", bookID)
	query = data.OwnerScope(query, caller.UserID, caller.IsAdmin(), "owner_id")
	err := query.Take(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", fmt.Errorf("%w: %s", ErrNotFound, bookID)
	}
	if err != nil {
		return "", fmt.Errorf("read book source: %w", err)
	}
	return record.SourceText, nil
}

// Chapters lists the outline without bodies.
func (r *GORMRepository) Chapters(ctx context.Context, bookID string, caller authz.Caller) ([]Chapter, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	query := r.db.WithContext(ctx).
		Table("book_chapters AS c").
		Select(replaceAllColumns(chapterColumns, "c.")).
		Joins("JOIN books AS b ON b.book_id = c.book_id").
		Where("c.book_id = ?", bookID)
	query = data.OwnerScope(query, caller.UserID, caller.IsAdmin(), "b.owner_id")

	var rows []models.BookChapter
	if err := query.Order("c.ordinal ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list book chapters: %w", err)
	}
	chapters := make([]Chapter, 0, len(rows))
	for _, row := range rows {
		chapters = append(chapters, toChapter(row))
	}
	return chapters, nil
}

// Chapter returns one chapter with its body.
func (r *GORMRepository) Chapter(ctx context.Context, bookID string, ordinal int, caller authz.Caller) (Chapter, error) {
	if err := r.ready(); err != nil {
		return Chapter{}, err
	}
	query := r.db.WithContext(ctx).
		Table("book_chapters AS c").
		Select("c.chapter_id, c.book_id, c.ordinal, c.title, c.summary, c.content, c.status, c.error_code, c.outline_revision, c.creat_time, c.update_time").
		Joins("JOIN books AS b ON b.book_id = c.book_id").
		Where("c.book_id = ? AND c.ordinal = ?", bookID, ordinal)
	query = data.OwnerScope(query, caller.UserID, caller.IsAdmin(), "b.owner_id")

	var record models.BookChapter
	err := query.Take(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Chapter{}, fmt.Errorf("%w: %s chapter %d", ErrNotFound, bookID, ordinal)
	}
	if err != nil {
		return Chapter{}, fmt.Errorf("read book chapter: %w", err)
	}
	return toChapter(record), nil
}

// Create inserts one book. Its chapters arrive with the first outline.
func (r *GORMRepository) Create(ctx context.Context, record Book) error {
	if err := r.ready(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = now
	}
	model := models.Book{
		BookID:           record.BookID,
		OwnerID:          record.OwnerID,
		Title:            record.Title,
		SourceKind:       string(record.SourceKind),
		SourceID:         record.SourceID,
		SourceTitle:      record.SourceTitle,
		SourceText:       record.SourceText,
		SourceChars:      record.SourceChars,
		SourceTruncated:  record.SourceTruncated,
		OutlineConfirmed: record.OutlineConfirmed,
		OutlineRevision:  record.OutlineRevision,
		LastOrdinal:      record.LastOrdinal,
		CreatedAt:        record.CreatedAt,
		UpdatedAt:        record.UpdatedAt,
	}
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return fmt.Errorf("create book: %w", err)
	}
	return nil
}

// Delete removes one of the caller's books; chapters follow by cascade.
func (r *GORMRepository) Delete(ctx context.Context, bookID string, caller authz.Caller) error {
	if err := r.ready(); err != nil {
		return err
	}
	query := r.db.WithContext(ctx).Where("book_id = ?", bookID)
	query = data.OwnerScope(query, caller.UserID, caller.IsAdmin(), "owner_id")
	result := query.Delete(&models.Book{})
	if result.Error != nil {
		return fmt.Errorf("delete book: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, bookID)
	}
	return nil
}

// ReplaceOutline locks the book row, refuses once anything is ready, increments
// the revision, clears progress and replaces every chapter in one transaction.
func (r *GORMRepository) ReplaceOutline(ctx context.Context, bookID string, caller authz.Caller, confirmed bool, chapters []Chapter) (int, error) {
	if err := r.ready(); err != nil {
		return 0, err
	}
	revision := 0
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record models.Book
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("book_id, outline_revision").
			Where("book_id = ?", bookID)
		query = data.OwnerScope(query, caller.UserID, caller.IsAdmin(), "owner_id")
		err := query.Take(&record).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: %s", ErrNotFound, bookID)
		}
		if err != nil {
			return fmt.Errorf("lock book: %w", err)
		}

		var ready int64
		if err := tx.Table("book_chapters").
			Where("book_id = ? AND status = ?", bookID, StatusReady).
			Count(&ready).Error; err != nil {
			return fmt.Errorf("count ready chapters: %w", err)
		}
		if ready > 0 {
			return ErrOutlineLocked
		}

		revision = record.OutlineRevision + 1
		if err := tx.Model(&models.Book{}).
			Where("book_id = ?", bookID).
			Updates(map[string]any{
				"outline_confirmed":    confirmed,
				"outline_revision":     revision,
				"last_ordinal":         0,
				"progress_update_time": nil,
			}).Error; err != nil {
			return fmt.Errorf("update book outline: %w", err)
		}
		if err := tx.Where("book_id = ?", bookID).Delete(&models.BookChapter{}).Error; err != nil {
			return fmt.Errorf("clear book chapters: %w", err)
		}

		now := time.Now().UTC()
		for index, chapter := range chapters {
			ordinal := index + 1
			model := models.BookChapter{
				ChapterID:       chapter.ChapterID,
				BookID:          bookID,
				Ordinal:         ordinal,
				Title:           chapter.Title,
				Summary:         chapter.Summary,
				Status:          string(StatusPending),
				ErrorCode:       "",
				OutlineRevision: revision,
				CreatedAt:       now,
				UpdatedAt:       now,
			}
			if err := tx.Create(&model).Error; err != nil {
				return fmt.Errorf("create book chapter %d: %w", ordinal, err)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return revision, nil
}

// SaveChapter writes a generated body under the outline revision the generation
// started from. The owner is checked through the book.
func (r *GORMRepository) SaveChapter(ctx context.Context, chapterID, bookID string, revision int, content string, caller authz.Caller) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	owned := r.ownedBooks(ctx, bookID, caller)
	result := r.db.WithContext(ctx).Model(&models.BookChapter{}).
		Where("chapter_id = ? AND book_id = ? AND outline_revision = ?", chapterID, bookID, revision).
		Where("book_id IN (?)", owned).
		Updates(map[string]any{
			"content":    content,
			"status":     StatusReady,
			"error_code": "",
		})
	if result.Error != nil {
		return false, fmt.Errorf("save book chapter: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		return true, nil
	}

	// MySQL reports zero changed rows for a write that wrote back what was
	// already there. Read the guard to tell "already stored" from "the outline
	// moved underneath this generation".
	var matched int64
	err := r.db.WithContext(ctx).Model(&models.BookChapter{}).
		Where("chapter_id = ? AND book_id = ? AND outline_revision = ?", chapterID, bookID, revision).
		Where("book_id IN (?)", owned).
		Count(&matched).Error
	if err != nil {
		return false, fmt.Errorf("verify book chapter: %w", err)
	}
	return matched > 0, nil
}

// FailChapter records a failure only when there is no body to keep.
func (r *GORMRepository) FailChapter(ctx context.Context, chapterID, bookID string, revision int, code string, caller authz.Caller) error {
	if err := r.ready(); err != nil {
		return err
	}
	owned := r.ownedBooks(ctx, bookID, caller)
	result := r.db.WithContext(ctx).Model(&models.BookChapter{}).
		Where("chapter_id = ? AND book_id = ? AND outline_revision = ?", chapterID, bookID, revision).
		Where("(content IS NULL OR content = '')").
		Where("book_id IN (?)", owned).
		Updates(map[string]any{
			"status":     StatusFailed,
			"error_code": code,
		})
	if result.Error != nil {
		return fmt.Errorf("fail book chapter: %w", result.Error)
	}
	return nil
}

// SaveProgress stores the last ordinal the owner opened.
func (r *GORMRepository) SaveProgress(ctx context.Context, bookID string, ordinal int, caller authz.Caller) error {
	if err := r.ready(); err != nil {
		return err
	}
	query := r.db.WithContext(ctx).Model(&models.Book{}).Where("book_id = ?", bookID)
	query = data.OwnerScope(query, caller.UserID, caller.IsAdmin(), "owner_id")
	result := query.Updates(map[string]any{
		"last_ordinal":         ordinal,
		"progress_update_time": time.Now().UTC(),
	})
	if result.Error != nil {
		return fmt.Errorf("save book progress: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		return nil
	}
	if _, err := r.Get(ctx, bookID, caller); err != nil {
		return err
	}
	return nil
}

// ReadyChapters returns the generated bodies for export.
func (r *GORMRepository) ReadyChapters(ctx context.Context, bookID string, caller authz.Caller) ([]Chapter, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	query := r.db.WithContext(ctx).
		Table("book_chapters AS c").
		Select("c.chapter_id, c.book_id, c.ordinal, c.title, c.summary, c.content, c.status, c.error_code, c.outline_revision, c.creat_time, c.update_time").
		Joins("JOIN books AS b ON b.book_id = c.book_id").
		Where("c.book_id = ? AND c.status = ?", bookID, StatusReady)
	query = data.OwnerScope(query, caller.UserID, caller.IsAdmin(), "b.owner_id")

	var rows []models.BookChapter
	if err := query.Order("c.ordinal ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("read ready chapters: %w", err)
	}
	chapters := make([]Chapter, 0, len(rows))
	for _, row := range rows {
		chapters = append(chapters, toChapter(row))
	}
	return chapters, nil
}

func (r *GORMRepository) ownedBooks(ctx context.Context, bookID string, caller authz.Caller) *gorm.DB {
	query := r.db.WithContext(ctx).Table("books").Select("book_id").Where("book_id = ?", bookID)
	return data.OwnerScope(query, caller.UserID, caller.IsAdmin(), "owner_id")
}

func (r *GORMRepository) ready() error {
	if r == nil || r.db == nil {
		return fmt.Errorf("book repository is not initialized")
	}
	return nil
}

func toBook(record models.Book) Book {
	return Book{
		Summary: Summary{
			BookID:           record.BookID,
			Title:            record.Title,
			SourceKind:       SourceKind(record.SourceKind),
			SourceID:         record.SourceID,
			SourceTitle:      record.SourceTitle,
			SourceChars:      record.SourceChars,
			SourceTruncated:  record.SourceTruncated,
			OutlineConfirmed: record.OutlineConfirmed,
			OutlineRevision:  record.OutlineRevision,
			LastOrdinal:      record.LastOrdinal,
			CreatedAt:        record.CreatedAt,
			UpdatedAt:        record.UpdatedAt,
		},
		OwnerID:    record.OwnerID,
		SourceText: record.SourceText,
	}
}

func toChapter(record models.BookChapter) Chapter {
	content := ""
	if record.Content != nil {
		content = *record.Content
	}
	return Chapter{
		ChapterID: record.ChapterID,
		BookID:    record.BookID,
		Ordinal:   record.Ordinal,
		Title:     record.Title,
		Summary:   record.Summary,
		Content:   content,
		Status:    Status(record.Status),
		ErrorCode: record.ErrorCode,
		CreatedAt: record.CreatedAt,
		UpdatedAt: record.UpdatedAt,
	}
}

// replaceAllColumns prefixes a comma-separated column list with a table alias.
func replaceAllColumns(columns, prefix string) string {
	result := ""
	start := 0
	for index := 0; index <= len(columns); index++ {
		if index == len(columns) || columns[index] == ',' {
			part := columns[start:index]
			for len(part) > 0 && part[0] == ' ' {
				part = part[1:]
			}
			if result != "" {
				result += ", "
			}
			result += prefix + part
			start = index + 1
		}
	}
	return result
}
