package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"backend/internal/authz"
	"backend/internal/book"
	"backend/internal/httpx"
	"github.com/gin-gonic/gin"
)

// Books is the book use case consumed by the HTTP handler.
type Books interface {
	List(context.Context, authz.Caller) ([]book.Summary, error)
	Get(context.Context, authz.Caller, string) (book.Detail, error)
	Create(context.Context, authz.Caller, book.CreateInput) (book.Detail, error)
	Delete(context.Context, authz.Caller, string) error
	GenerateOutline(context.Context, authz.Caller, string) (book.Detail, error)
	PutOutline(context.Context, authz.Caller, string, book.OutlineInput) (book.Detail, error)
	GenerateChapter(context.Context, authz.Caller, string, int) (book.Chapter, error)
	Chapter(context.Context, authz.Caller, string, int) (book.Chapter, error)
	SaveProgress(context.Context, authz.Caller, string, int) (book.Summary, error)
	Export(context.Context, authz.Caller, string) (book.Export, error)
}

// BookHandler serves the book shelf, the outline and the chapter reader.
//
// The two generation endpoints deliberately read no request body. Everything
// they need is in the path and in the book's own row — which chapter, and what
// that book was made from — and a body that had to be present but empty is a
// client-side detail this API does not need.
type BookHandler struct {
	books  Books
	logger *slog.Logger
}

// NewBookHandler constructs the book HTTP handler.
func NewBookHandler(books Books, logger *slog.Logger) *BookHandler {
	return &BookHandler{books: books, logger: logger}
}

// bookSummaryView is one book as the shelf and the detail header show it.
// source_text is not here and must not be: the snapshot is input to generation,
// not a thing the reader asked to see again.
type bookSummaryView struct {
	BookID           string    `json:"book_id"`
	Title            string    `json:"title"`
	SourceKind       string    `json:"source_kind"`
	SourceID         string    `json:"source_id"`
	SourceTitle      string    `json:"source_title"`
	SourceChars      int       `json:"source_chars"`
	SourceTruncated  bool      `json:"source_truncated"`
	OutlineConfirmed bool      `json:"outline_confirmed"`
	OutlineRevision  int       `json:"outline_revision"`
	LastOrdinal      int       `json:"last_ordinal"`
	ChapterCount     int       `json:"chapter_count"`
	ReadyChapters    int       `json:"ready_chapters"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type bookOutlineItemView struct {
	Ordinal   int    `json:"ordinal"`
	Title     string `json:"title"`
	Summary   string `json:"summary"`
	Status    string `json:"status"`
	ErrorCode string `json:"error_code"`
}

type bookDetailView struct {
	bookSummaryView
	Outline []bookOutlineItemView `json:"outline"`
}

type bookChapterView struct {
	ChapterID string    `json:"chapter_id"`
	BookID    string    `json:"book_id"`
	Ordinal   int       `json:"ordinal"`
	Title     string    `json:"title"`
	Summary   string    `json:"summary"`
	Content   string    `json:"content"`
	Status    string    `json:"status"`
	ErrorCode string    `json:"error_code"`
	UpdatedAt time.Time `json:"updated_at"`
}

type bookListView struct {
	Books []bookSummaryView `json:"books"`
}

type bookExportView struct {
	Filename string `json:"filename"`
	Content  string `json:"content"`
}

type createBookRequest struct {
	Title      string `json:"title"`
	SourceKind string `json:"source_kind"`
	SourceID   string `json:"source_id"`
}

type outlineItemRequest struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

type outlineRequest struct {
	Chapters  []outlineItemRequest `json:"chapters"`
	Confirmed bool                 `json:"confirmed"`
}

// progressRequest uses a pointer because 0 is a real value — "not started" —
// and a missing field is a different request from one that says zero.
type progressRequest struct {
	Ordinal *int `json:"ordinal"`
}

// List returns the caller's shelf.
func (h *BookHandler) List(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	found, err := h.books.List(c.Request.Context(), account)
	if err != nil {
		h.fail(c, "list_books_failed", err)
		return
	}
	listed := make([]bookSummaryView, 0, len(found))
	for _, record := range found {
		listed = append(listed, toBookSummaryView(record))
	}
	httpx.WriteSuccess(c, http.StatusOK, bookListView{Books: listed})
}

// Create snapshots a source and answers with the new book.
func (h *BookHandler) Create(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	var request createBookRequest
	if !readJSON(c, maxBookRequestBytes, &request) {
		return
	}
	created, err := h.books.Create(c.Request.Context(), account, book.CreateInput{
		Title:      request.Title,
		SourceKind: book.SourceKind(request.SourceKind),
		SourceID:   request.SourceID,
	})
	if err != nil {
		h.fail(c, "create_book_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusCreated, toBookDetailView(created))
}

// Get returns one book and its outline.
func (h *BookHandler) Get(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	detail, err := h.books.Get(c.Request.Context(), account, c.Param("bookID"))
	if err != nil {
		h.fail(c, "get_book_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, toBookDetailView(detail))
}

// Delete removes one book.
func (h *BookHandler) Delete(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	if err := h.books.Delete(c.Request.Context(), account, c.Param("bookID")); err != nil {
		h.fail(c, "delete_book_failed", err)
		return
	}
	c.Status(http.StatusNoContent)
}

// GenerateOutline asks the model for an outline. It reads no body.
func (h *BookHandler) GenerateOutline(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	detail, err := h.books.GenerateOutline(c.Request.Context(), account, c.Param("bookID"))
	if err != nil {
		h.fail(c, "generate_outline_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, toBookDetailView(detail))
}

// PutOutline stores the reader's outline, confirmed or not.
func (h *BookHandler) PutOutline(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	var request outlineRequest
	if !readJSON(c, maxBookRequestBytes, &request) {
		return
	}
	input := book.OutlineInput{Confirmed: request.Confirmed, Chapters: make([]book.OutlineItemInput, 0, len(request.Chapters))}
	for _, chapter := range request.Chapters {
		input.Chapters = append(input.Chapters, book.OutlineItemInput{Title: chapter.Title, Summary: chapter.Summary})
	}
	detail, err := h.books.PutOutline(c.Request.Context(), account, c.Param("bookID"), input)
	if err != nil {
		h.fail(c, "save_outline_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, toBookDetailView(detail))
}

// GenerateChapter writes one chapter. It reads no body.
func (h *BookHandler) GenerateChapter(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	ordinal, ok := bookOrdinal(c)
	if !ok {
		return
	}
	chapter, err := h.books.GenerateChapter(c.Request.Context(), account, c.Param("bookID"), ordinal)
	if err != nil {
		h.fail(c, "generate_chapter_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, toBookChapterView(chapter))
}

// Chapter returns one chapter with its body.
func (h *BookHandler) Chapter(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	ordinal, ok := bookOrdinal(c)
	if !ok {
		return
	}
	chapter, err := h.books.Chapter(c.Request.Context(), account, c.Param("bookID"), ordinal)
	if err != nil {
		h.fail(c, "get_chapter_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, toBookChapterView(chapter))
}

// SaveProgress records the chapter the reader last opened.
func (h *BookHandler) SaveProgress(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	var request progressRequest
	if !readJSON(c, maxBookRequestBytes, &request) {
		return
	}
	if request.Ordinal == nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "ordinal is required")
		return
	}
	summary, err := h.books.SaveProgress(c.Request.Context(), account, c.Param("bookID"), *request.Ordinal)
	if err != nil {
		h.fail(c, "save_progress_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, toBookSummaryView(summary))
}

// Export returns the generated chapters as one Markdown document.
func (h *BookHandler) Export(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	document, err := h.books.Export(c.Request.Context(), account, c.Param("bookID"))
	if err != nil {
		h.fail(c, "export_book_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, bookExportView{Filename: document.Filename, Content: document.Content})
}

func (h *BookHandler) ready(c *gin.Context) bool {
	if h != nil && h.books != nil {
		return true
	}
	httpx.WriteError(c, http.StatusInternalServerError, "books_unavailable", "books are not configured")
	return false
}

// fail maps the book domain's states onto the answers a client acts on.
func (h *BookHandler) fail(c *gin.Context, code string, err error) {
	if writeQuotaRefusal(c, err) {
		return
	}
	switch {
	case errors.Is(err, book.ErrNotFound):
		httpx.WriteError(c, http.StatusNotFound, "not_found", "no such book or chapter")
	case errors.Is(err, book.ErrInvalidRequest):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", detail(err, book.ErrInvalidRequest))
	case errors.Is(err, book.ErrInvalidOutline):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_outline", detail(err, book.ErrInvalidOutline))
	case errors.Is(err, book.ErrInvalidProgress):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_progress", detail(err, book.ErrInvalidProgress))
	case errors.Is(err, book.ErrSourceEmpty):
		httpx.WriteError(c, http.StatusBadRequest, "source_empty", "the source has no text to generate from")
	case errors.Is(err, book.ErrSourceUnavailable):
		httpx.WriteError(c, http.StatusConflict, "source_unavailable", "the source cannot be read; a file may not be parsed yet")
	case errors.Is(err, book.ErrOutlineNotConfirmed):
		httpx.WriteError(c, http.StatusConflict, "outline_not_confirmed", "confirm the outline before generating chapters")
	case errors.Is(err, book.ErrOutlineLocked):
		httpx.WriteError(c, http.StatusConflict, "outline_locked", "the outline cannot change after a chapter has been generated")
	case errors.Is(err, book.ErrOutlineChanged):
		httpx.WriteError(c, http.StatusConflict, "outline_changed", "the outline changed while this chapter was being generated")
	case errors.Is(err, book.ErrGenerationInFlight):
		httpx.WriteError(c, http.StatusConflict, "generation_in_flight", "a generation for this book is already running")
	case errors.Is(err, book.ErrNoReadyChapters):
		httpx.WriteError(c, http.StatusConflict, "no_ready_chapters", "no chapter has been generated yet")
	case errors.Is(err, book.ErrGenerationUnavailable):
		httpx.WriteError(c, http.StatusServiceUnavailable, "generation_unavailable", "generation is not configured in this build")
	case errors.Is(err, book.ErrGenerationFailed):
		httpx.WriteError(c, http.StatusServiceUnavailable, "generation_failed", "the model did not produce a usable chapter")
	default:
		if h.logger != nil {
			h.logger.Error("book request failed", "error", err, "request_id", httpx.RequestID(c))
		}
		httpx.WriteError(c, http.StatusInternalServerError, code, "the book request failed")
	}
}

// bookOrdinal reads the chapter number from the path.
func bookOrdinal(c *gin.Context) (int, bool) {
	ordinal, err := strconv.Atoi(c.Param("ordinal"))
	if err != nil || ordinal < 1 {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "the chapter ordinal must be a positive integer")
		return 0, false
	}
	return ordinal, true
}

func toBookSummaryView(record book.Summary) bookSummaryView {
	return bookSummaryView{
		BookID:           record.BookID,
		Title:            record.Title,
		SourceKind:       string(record.SourceKind),
		SourceID:         record.SourceID,
		SourceTitle:      record.SourceTitle,
		SourceChars:      record.SourceChars,
		SourceTruncated:  record.SourceTruncated,
		OutlineConfirmed: record.OutlineConfirmed,
		OutlineRevision:  record.OutlineRevision,
		LastOrdinal:      record.LastOrdinal,
		ChapterCount:     record.ChapterCount,
		ReadyChapters:    record.ReadyChapters,
		CreatedAt:        record.CreatedAt,
		UpdatedAt:        record.UpdatedAt,
	}
}

func toBookDetailView(detail book.Detail) bookDetailView {
	view := bookDetailView{bookSummaryView: toBookSummaryView(detail.Book)}
	view.Outline = make([]bookOutlineItemView, 0, len(detail.Outline))
	for _, item := range detail.Outline {
		view.Outline = append(view.Outline, bookOutlineItemView{
			Ordinal:   item.Ordinal,
			Title:     item.Title,
			Summary:   item.Summary,
			Status:    string(item.Status),
			ErrorCode: item.ErrorCode,
		})
	}
	return view
}

func toBookChapterView(record book.Chapter) bookChapterView {
	return bookChapterView{
		ChapterID: record.ChapterID,
		BookID:    record.BookID,
		Ordinal:   record.Ordinal,
		Title:     record.Title,
		Summary:   record.Summary,
		Content:   record.Content,
		Status:    string(record.Status),
		ErrorCode: record.ErrorCode,
		UpdatedAt: record.UpdatedAt,
	}
}
