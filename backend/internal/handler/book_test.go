package handler

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"backend/internal/authz"
	"backend/internal/book"
	"backend/internal/middleware"
	"backend/internal/usage"
	"github.com/gin-gonic/gin"
)

// bookStub is the book use case, recording what it was asked for. Which account
// it records is half of what these cases are about: it has to come from the
// token, not from the request.
type bookStub struct {
	err     error
	listing []book.Summary
	detail  book.Detail
	chapter book.Chapter
	summary book.Summary
	export  book.Export

	calls   []string
	account authz.Caller
	id      string
	ordinal int
	input   book.OutlineInput
	created book.CreateInput
}

func (s *bookStub) List(_ context.Context, caller authz.Caller) ([]book.Summary, error) {
	s.calls, s.account = append(s.calls, "list"), caller
	return s.listing, s.err
}

func (s *bookStub) Get(_ context.Context, caller authz.Caller, bookID string) (book.Detail, error) {
	s.calls, s.account, s.id = append(s.calls, "get"), caller, bookID
	return s.detail, s.err
}

func (s *bookStub) Create(_ context.Context, caller authz.Caller, input book.CreateInput) (book.Detail, error) {
	s.calls, s.account, s.created = append(s.calls, "create"), caller, input
	return s.detail, s.err
}

func (s *bookStub) Delete(_ context.Context, caller authz.Caller, bookID string) error {
	s.calls, s.account, s.id = append(s.calls, "delete"), caller, bookID
	return s.err
}

func (s *bookStub) GenerateOutline(_ context.Context, caller authz.Caller, bookID string) (book.Detail, error) {
	s.calls, s.account, s.id = append(s.calls, "generate-outline"), caller, bookID
	return s.detail, s.err
}

func (s *bookStub) PutOutline(_ context.Context, caller authz.Caller, bookID string, input book.OutlineInput) (book.Detail, error) {
	s.calls, s.account, s.id, s.input = append(s.calls, "put-outline"), caller, bookID, input
	return s.detail, s.err
}

func (s *bookStub) GenerateChapter(_ context.Context, caller authz.Caller, bookID string, ordinal int) (book.Chapter, error) {
	s.calls, s.account, s.id, s.ordinal = append(s.calls, "generate-chapter"), caller, bookID, ordinal
	return s.chapter, s.err
}

func (s *bookStub) Chapter(_ context.Context, caller authz.Caller, bookID string, ordinal int) (book.Chapter, error) {
	s.calls, s.account, s.id, s.ordinal = append(s.calls, "chapter"), caller, bookID, ordinal
	return s.chapter, s.err
}

func (s *bookStub) SaveProgress(_ context.Context, caller authz.Caller, bookID string, ordinal int) (book.Summary, error) {
	s.calls, s.account, s.id, s.ordinal = append(s.calls, "progress"), caller, bookID, ordinal
	return s.summary, s.err
}

func (s *bookStub) Export(_ context.Context, caller authz.Caller, bookID string) (book.Export, error) {
	s.calls, s.account, s.id = append(s.calls, "export"), caller, bookID
	return s.export, s.err
}

func newBookRouter(books Books) *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := NewBookHandler(books, testHandlerLogger())
	router := gin.New()
	// Mounted as the server mounts it, so the export case can assert the
	// envelope's request id rather than only its data.
	router.Use(middleware.RequestID())
	router.GET("/api/v1/books", guard(), handler.List)
	router.POST("/api/v1/books", guard(), handler.Create)
	router.GET("/api/v1/books/:bookID", guard(), handler.Get)
	router.DELETE("/api/v1/books/:bookID", guard(), handler.Delete)
	router.POST("/api/v1/books/:bookID/outline", guard(), handler.GenerateOutline)
	router.PUT("/api/v1/books/:bookID/outline", guard(), handler.PutOutline)
	router.POST("/api/v1/books/:bookID/chapters/:ordinal/generate", guard(), handler.GenerateChapter)
	router.GET("/api/v1/books/:bookID/chapters/:ordinal", guard(), handler.Chapter)
	router.PUT("/api/v1/books/:bookID/progress", guard(), handler.SaveProgress)
	router.GET("/api/v1/books/:bookID/export", guard(), handler.Export)
	return router
}

type aBookFailure struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func TestBookHandlerWithNoBooksAnswersAnEmptyShelf(t *testing.T) {
	recorder := callAs(t, newBookRouter(&bookStub{}), http.MethodGet, "/api/v1/books", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if body := recorder.Body.String(); !strings.Contains(body, `"books":[]`) {
		t.Errorf("body = %s, want an empty list", body)
	}
}

func TestBookHandlerCreateAnswers201WithTheBook(t *testing.T) {
	stub := &bookStub{detail: book.Detail{Book: book.Summary{BookID: "bk_1", Title: "保修手册"}}}
	recorder := callAs(t, newBookRouter(stub), http.MethodPost, "/api/v1/books",
		`{"title":"保修手册","source_kind":"file","source_id":"lib_1"}`)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.created.SourceKind != book.SourceFile || stub.created.SourceID != "lib_1" || stub.created.Title != "保修手册" {
		t.Errorf("Create() was given %+v, want the request body", stub.created)
	}
	if stub.account != handlerCaller() {
		t.Errorf("the use case was asked as %+v, want the account on the token", stub.account)
	}
	if !strings.Contains(recorder.Body.String(), `"book_id":"bk_1"`) || !strings.Contains(recorder.Body.String(), `"outline":[]`) {
		t.Errorf("body = %s, want the book and an empty outline", recorder.Body.String())
	}
}

func TestBookHandlerDeleteAnswers204(t *testing.T) {
	stub := &bookStub{}
	recorder := callAs(t, newBookRouter(stub), http.MethodDelete, "/api/v1/books/bk_1", "")

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Body.Len() != 0 {
		t.Errorf("body = %q, want no body on a 204", recorder.Body.String())
	}
	if stub.id != "bk_1" {
		t.Errorf("Delete() was given %q, want the path id", stub.id)
	}
}

// The generation routes take no body at all, so an empty request is a valid
// one. A handler that ran readJSON here would answer 400 and make the client
// send a body it has nothing to put in.
func TestBookHandlerGenerationTakesNoRequestBody(t *testing.T) {
	stub := &bookStub{}
	recorder := callAs(t, newBookRouter(stub), http.MethodPost, "/api/v1/books/bk_1/outline", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if len(stub.calls) != 1 || stub.calls[0] != "generate-outline" {
		t.Errorf("calls = %v, want the outline generation", stub.calls)
	}
}

func TestBookHandlerProgressNeedsAnOrdinal(t *testing.T) {
	stub := &bookStub{summary: book.Summary{BookID: "bk_1"}}
	router := newBookRouter(stub)

	missing := callAs(t, router, http.MethodPut, "/api/v1/books/bk_1/progress", `{}`)
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", missing.Code, missing.Body.String())
	}
	var failure aBookFailure
	decodeBody(t, missing, &failure)
	if failure.Error.Code != "invalid_request" {
		t.Errorf("code = %q, want invalid_request", failure.Error.Code)
	}

	zero := callAs(t, router, http.MethodPut, "/api/v1/books/bk_1/progress", `{"ordinal":0}`)
	if zero.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for the real value 0; body=%s", zero.Code, zero.Body.String())
	}
	if stub.ordinal != 0 {
		t.Errorf("ordinal = %d, want 0", stub.ordinal)
	}
}

func TestBookHandlerRefusesAChapterOrdinalThatIsNotANumber(t *testing.T) {
	recorder := callAs(t, newBookRouter(&bookStub{}), http.MethodGet, "/api/v1/books/bk_1/chapters/first", "")

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestBookHandlerMapsItsFailures(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"no such book", book.ErrNotFound, http.StatusNotFound, "not_found"},
		{"a malformed request", fmt.Errorf("%w: source id is required", book.ErrInvalidRequest), http.StatusBadRequest, "invalid_request"},
		{"a malformed outline", fmt.Errorf("%w: too many chapters", book.ErrInvalidOutline), http.StatusBadRequest, "invalid_outline"},
		{"progress outside the outline", fmt.Errorf("%w: 9 of 3", book.ErrInvalidProgress), http.StatusBadRequest, "invalid_progress"},
		{"an empty source", book.ErrSourceEmpty, http.StatusBadRequest, "source_empty"},
		{"an unreadable source", book.ErrSourceUnavailable, http.StatusConflict, "source_unavailable"},
		{"generation before confirmation", book.ErrOutlineNotConfirmed, http.StatusConflict, "outline_not_confirmed"},
		{"an outline that cannot change", book.ErrOutlineLocked, http.StatusConflict, "outline_locked"},
		{"an outline that moved", book.ErrOutlineChanged, http.StatusConflict, "outline_changed"},
		{"a generation already running", book.ErrGenerationInFlight, http.StatusConflict, "generation_in_flight"},
		{"nothing generated", book.ErrNoReadyChapters, http.StatusConflict, "no_ready_chapters"},
		{"no model", book.ErrGenerationUnavailable, http.StatusServiceUnavailable, "generation_unavailable"},
		{"a failed generation", book.ErrGenerationFailed, http.StatusServiceUnavailable, "generation_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := callAs(t, newBookRouter(&bookStub{err: test.err}),
				http.MethodGet, "/api/v1/books/bk_1", "")

			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			var failure aBookFailure
			decodeBody(t, recorder, &failure)
			if failure.Error.Code != test.wantCode {
				t.Errorf("code = %q, want %q", failure.Error.Code, test.wantCode)
			}
		})
	}
}

func TestBookHandlerAnswersAQuotaRefusalFirst(t *testing.T) {
	refusal := &usage.Exceeded{
		Item: "tokens", Limit: 100, Used: 100,
		ResetsAt: time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC),
	}
	recorder := callAs(t, newBookRouter(&bookStub{err: refusal}),
		http.MethodPost, "/api/v1/books/bk_1/outline", "")

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"code":"quota_exceeded"`) {
		t.Errorf("body = %s, want quota_exceeded", recorder.Body.String())
	}
}

func TestBookHandlerWithoutAWiringAnswers500(t *testing.T) {
	recorder := callAs(t, newBookRouter(nil), http.MethodGet, "/api/v1/books", "")
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"code":"books_unavailable"`) {
		t.Errorf("body = %s, want books_unavailable", recorder.Body.String())
	}
}

func TestBookHandlerExportsTheDocumentInTheEnvelope(t *testing.T) {
	stub := &bookStub{export: book.Export{Filename: "保修手册.md", Content: "# 保修手册"}}
	recorder := callAs(t, newBookRouter(stub), http.MethodGet, "/api/v1/books/bk_1/export", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data bookExportView `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if envelope.Data.Filename != "保修手册.md" || envelope.Data.Content != "# 保修手册" {
		t.Errorf("data = %+v, want the export", envelope.Data)
	}
	if !strings.Contains(recorder.Body.String(), `"request_id":"`) {
		t.Errorf("body = %s, want the envelope's request id", recorder.Body.String())
	}
}

func TestBookHandlerKeepsTheSourceSnapshotOutOfTheResponse(t *testing.T) {
	stub := &bookStub{detail: book.Detail{Book: book.Summary{BookID: "bk_1", Title: "保修手册"}}}
	recorder := callAs(t, newBookRouter(stub), http.MethodGet, "/api/v1/books/bk_1", "")

	if strings.Contains(recorder.Body.String(), "source_text") {
		t.Errorf("body = %s, want no source text", recorder.Body.String())
	}
}
