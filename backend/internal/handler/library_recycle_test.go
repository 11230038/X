package handler

// The five routes §2.14 adds beyond the upload and the two reads: the listing,
// the download, and the recycle bin's three moves.
//
// What the domain *does* with these calls is internal/library's business. What
// is tested here is the boundary: which query a listing reads, which headers a
// download sets, what a client is handed when a purge is refused, and that
// every one of the three writes goes out as the account on the token rather than
// as an account the request asked for.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"backend/internal/authz"
	"backend/internal/library"
	"backend/internal/middleware"
	"github.com/gin-gonic/gin"
)

// lifecycleStub answers the four lifecycle methods and remembers what it was
// asked. Every test below reads one of its fields, which is what makes these
// cases about the boundary rather than about the handler's own arithmetic.
type lifecycleStub struct {
	files []library.FileSummary
	err   error
	// inUse, when set, is what Purge refuses with — the shape AC-15's answer
	// takes at the boundary.
	inUse error

	query  library.SearchQuery
	asked  []string
	actor  library.Actor
	target string
}

func (s *lifecycleStub) Search(_ context.Context, query library.SearchQuery) ([]library.FileSummary, error) {
	s.query, s.asked = query, append(s.asked, "search")
	return s.files, s.err
}

func (s *lifecycleStub) SoftDelete(_ context.Context, id string, actor library.Actor) (library.FileSummary, error) {
	s.actor, s.target, s.asked = actor, id, append(s.asked, "soft_delete")
	return summaryOf(id), s.err
}

func (s *lifecycleStub) Restore(_ context.Context, id string, actor library.Actor) (library.FileSummary, error) {
	s.actor, s.target, s.asked = actor, id, append(s.asked, "restore")
	return summaryOf(id), s.err
}

func (s *lifecycleStub) Purge(_ context.Context, id string, actor library.Actor) error {
	s.actor, s.target, s.asked = actor, id, append(s.asked, "purge")
	return s.inUse
}

// openerStub hands back one StoredFile, or refuses the way the library does.
type openerStub struct {
	file   library.StoredFile
	err    error
	target string
	// caller is who the handler said was asking, which is the half of this route
	// that is not about the bytes: the same id belongs to different people.
	caller authz.Caller
	opened bool
}

func (s *openerStub) Open(_ context.Context, id string, caller authz.Caller) (library.StoredFile, error) {
	s.opened, s.target, s.caller = true, id, caller
	return s.file, s.err
}

func summaryOf(id string) library.FileSummary {
	return library.FileSummary{ID: id, Filename: "手册.pdf", MIMEType: "application/pdf", SizeBytes: 4}
}

// newLifecycleRouter mounts the five routes the way the server does.
func newLifecycleRouter(files LibraryLifecycle, opener FileOpener) *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := NewLibraryLifecycleHandler(files, opener, testHandlerLogger())
	router := gin.New()
	router.Use(middleware.RequestID())
	router.GET("/api/v1/library/files", guard(), handler.List)
	router.GET("/api/v1/library/files/:fileID/content", guard(), handler.Download)
	router.DELETE("/api/v1/library/files/:fileID", guard(), handler.SoftDelete)
	router.POST("/api/v1/library/files/:fileID/restore", guard(), handler.Restore)
	router.DELETE("/api/v1/library/files/:fileID/purge", guard(), handler.Purge)
	return router
}

func TestLibraryLifecycleHandlerListsWhatTheTokenOwns(t *testing.T) {
	stub := &lifecycleStub{files: []library.FileSummary{
		summaryOf("lib_1"), summaryOf("lib_2"),
	}}
	recorder := callAs(t, newLifecycleRouter(stub, &openerStub{}), http.MethodGet,
		"/api/v1/library/files", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Data struct {
			Files []library.FileSummary `json:"files"`
		} `json:"data"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.Files) != 2 || body.Data.Files[0].ID != "lib_1" {
		t.Errorf("the listing answered %+v, want the two files the domain returned", body.Data.Files)
	}
	// The listing is scoped to whoever held the token, and the request cannot
	// say otherwise: there is no owner parameter, and a route that read one would
	// let a caller read somebody else's library.
	if stub.query.Caller.UserID != handlerAccountID {
		t.Errorf("the listing asked for account %d, want the token's %d",
			stub.query.Caller.UserID, handlerAccountID)
	}
}

func TestLibraryLifecycleHandlerReadsTheListingQuery(t *testing.T) {
	since := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		query string
		want  library.SearchQuery
	}{
		{"nothing at all", "", library.SearchQuery{}},
		{"the recycle bin", "?deleted=true", library.SearchQuery{Deleted: true}},
		{"a name, trimmed", "?filename=%20%E7%AC%94%E8%AE%B0%20",
			library.SearchQuery{Filename: "笔记"}},
		{"a category", "?category=pdf", library.SearchQuery{Category: "pdf"}},
		{"a window", "?since=2026-10-01T12:00:00Z&until=2026-10-02T12:00:00Z",
			library.SearchQuery{Since: since, Until: since.Add(24 * time.Hour)}},
		{"a page", "?limit=3&offset=4", library.SearchQuery{Limit: 3, Offset: 4}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &lifecycleStub{}
			recorder := callAs(t, newLifecycleRouter(stub, &openerStub{}), http.MethodGet,
				"/api/v1/library/files"+test.query, "")
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
			}
			test.want.Caller = handlerCaller()
			if stub.query != test.want {
				t.Errorf("the listing read %+v, want %+v", stub.query, test.want)
			}
		})
	}
}

func TestLibraryLifecycleHandlerRejectsAListingQueryItCannotRead(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{"a window that is not a timestamp", "?since=yesterday"},
		{"a window that is nearly a timestamp", "?until=2026-10-01"},
		{"a bin flag that is not a flag", "?deleted=maybe"},
		{"a page size that is not a number", "?limit=many"},
		{"a negative page size", "?limit=-1"},
		{"an offset that is not a number", "?offset=two"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &lifecycleStub{}
			recorder := callAs(t, newLifecycleRouter(stub, &openerStub{}), http.MethodGet,
				"/api/v1/library/files"+test.query, "")
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), `"code":"invalid_query"`) {
				t.Errorf("body = %s, want invalid_query", recorder.Body.String())
			}
			// And nothing was asked of the library. A query string that cannot be
			// read is not a query with one part missing: the missing part would be
			// the filter the client thought it had sent.
			if len(stub.asked) != 0 {
				t.Errorf("a rejected query still reached the library as %v", stub.asked)
			}
		})
	}
}

func TestLibraryLifecycleHandlerDownloadsAFileNamedAndTyped(t *testing.T) {
	content := "保修期是两年。"
	// An ASCII name, so that the header this pins is the plain one. A name that is
	// not ASCII cannot travel as itself at all, and that case is the one below.
	opener := &openerStub{file: library.StoredFile{
		ID: "lib_1", Filename: "manual.pdf", MIMEType: "application/pdf",
		SizeBytes: int64(len(content)), Content: io.NopCloser(strings.NewReader(content)),
	}}
	recorder := callAs(t, newLifecycleRouter(&lifecycleStub{}, opener), http.MethodGet,
		"/api/v1/library/files/lib_1/content", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Body.String() != content {
		t.Errorf("body = %q, want the stored bytes", recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/pdf") {
		t.Errorf("Content-Type = %q, want the stored file's type", got)
	}
	// The two headers that make a browser download this rather than render it,
	// and that keep it from deciding for itself what an uploaded file is.
	if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := recorder.Header().Get("Content-Disposition"); got != "attachment; filename=manual.pdf" {
		t.Errorf("Content-Disposition = %q, want an attachment named what the user named it", got)
	}
	if !opener.opened || opener.target != "lib_1" {
		t.Errorf("opened %q (called: %v), want the id from the path", opener.target, opener.opened)
	}
	// The bytes and the account travel together, because one id can name a file
	// in two libraries: what makes the answer "not found" rather than a download
	// is who is asking, and the handler has to say so.
	if opener.caller.UserID != handlerAccountID {
		t.Errorf("opened as account %d, want the token's %d",
			opener.caller.UserID, handlerAccountID)
	}
	// A download is not one of the envelope answers: it is the file itself, and a
	// JSON wrapper around bytes would be a client parsing a response that is not
	// what its content type says.
	if strings.Contains(recorder.Body.String(), "request_id") {
		t.Error("the download was wrapped in the API envelope")
	}
}

// TestLibraryLifecycleHandlerNamesANonAsciiDownloadTheWayRFC2231Does: a
// filename is the one value on this response that comes from the user, and a
// header cannot hold 报告.pdf as bytes. FormatMediaType writes the RFC 2231 form
// instead, which is the difference between a browser saving a file under its
// name and saving it under a row of percent signs.
func TestLibraryLifecycleHandlerNamesANonAsciiDownloadTheWayRFC2231Does(t *testing.T) {
	opener := &openerStub{file: library.StoredFile{
		ID: "lib_1", Filename: "报告.pdf", MIMEType: "application/pdf",
		SizeBytes: 1, Content: io.NopCloser(strings.NewReader("x")),
	}}
	recorder := callAs(t, newLifecycleRouter(&lifecycleStub{}, opener), http.MethodGet,
		"/api/v1/library/files/lib_1/content", "")

	found := recorder.Header().Get("Content-Disposition")
	if !strings.Contains(found, "filename*=utf-8''%E6%8A%A5%E5%91%8A.pdf") {
		t.Errorf("Content-Disposition = %q, want the RFC 2231 form of 报告.pdf", found)
	}
}

func TestLibraryLifecycleHandlerMapsDownloadRefusals(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"a file that is not there", library.ErrNotFound, http.StatusNotFound, "not_found"},
		{"an id this library never issued", library.ErrInvalidFile, http.StatusBadRequest, "invalid_request"},
		{"storage that is unreachable", errors.New("private database details"),
			http.StatusInternalServerError, "download_file_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opener := &openerStub{err: test.err}
			recorder := callAs(t, newLifecycleRouter(&lifecycleStub{}, opener), http.MethodGet,
				"/api/v1/library/files/lib_1/content", "")
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), `"code":"`+test.wantCode+`"`) {
				t.Errorf("body = %s, want code %s", recorder.Body.String(), test.wantCode)
			}
			if strings.Contains(recorder.Body.String(), "private database") {
				t.Error("the response leaked an internal error")
			}
			// A refused download sets no attachment header. A 404 that came with
			// one would be a browser offering to save a file the server just said
			// it does not have.
			if disposition := recorder.Header().Get("Content-Disposition"); disposition != "" {
				t.Errorf("a refused download set Content-Disposition = %q", disposition)
			}
		})
	}
}

func TestLibraryLifecycleHandlerActsAsTheAccountOnTheToken(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		want   string
	}{
		{"into the bin", http.MethodDelete, "/api/v1/library/files/lib_1", "soft_delete"},
		{"back out of the bin", http.MethodPost, "/api/v1/library/files/lib_1/restore", "restore"},
		{"destroyed", http.MethodDelete, "/api/v1/library/files/lib_1/purge", "purge"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &lifecycleStub{}
			recorder := callAs(t, newLifecycleRouter(stub, &openerStub{}), test.method, test.path, "")
			// Purging answers 204 and the other two answer the file, but all three
			// arrive at the library as the same actor, which is what this is about.
			if recorder.Code != http.StatusOK && recorder.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 200 or 204; body=%s", recorder.Code, recorder.Body.String())
			}
			if len(stub.asked) != 1 || stub.asked[0] != test.want {
				t.Fatalf("the library was asked %v, want exactly %s", stub.asked, test.want)
			}
			if stub.target != "lib_1" {
				t.Errorf("acted on %q, want the id from the path", stub.target)
			}
			// Both halves of the actor, for the two reasons they exist: the id is
			// what the operation is scoped by, and the name is the snapshot the
			// entry stores (§2.6). A request cannot supply either — there is no
			// body on any of these three routes.
			if stub.actor.UserID != handlerAccountID || stub.actor.Name != "alice" {
				t.Errorf("the operation ran as %+v, want the token's account and name", stub.actor)
			}
		})
	}
}

// TestLibraryLifecycleHandlerAnswersTheTwoReversibleWritesWithTheFile: both
// answers carry the file as it now is, so that a client showing a bin does not
// have to ask again what the row it just acted on looks like. A purge carries
// nothing, because after it there is no file to describe — and a body there
// would be a description of something that no longer exists.
func TestLibraryLifecycleHandlerAnswersTheTwoReversibleWritesWithTheFile(t *testing.T) {
	for _, mount := range []struct {
		name   string
		method string
		path   string
	}{
		{"into the bin", http.MethodDelete, "/api/v1/library/files/lib_1"},
		{"back out of the bin", http.MethodPost, "/api/v1/library/files/lib_1/restore"},
	} {
		t.Run(mount.name, func(t *testing.T) {
			recorder := callAs(t, newLifecycleRouter(&lifecycleStub{}, &openerStub{}),
				mount.method, mount.path, "")
			var body struct {
				Data struct {
					File library.FileSummary `json:"file"`
				} `json:"data"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("the response is not the API envelope: %v (%s)", err, recorder.Body.String())
			}
			if body.Data.File.ID != "lib_1" || body.Data.File.Filename != "手册.pdf" {
				t.Errorf("the answer was %+v, want the file the operation returned", body.Data.File)
			}
		})
	}

	t.Run("destroyed", func(t *testing.T) {
		recorder := callAs(t, newLifecycleRouter(&lifecycleStub{}, &openerStub{}),
			http.MethodDelete, "/api/v1/library/files/lib_1/purge", "")
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", recorder.Code)
		}
		if body := strings.TrimSpace(recorder.Body.String()); body != "" {
			t.Errorf("the purge answered with a body: %s", body)
		}
	})
}

// TestLibraryLifecycleHandlerRefusesToPurgeAFileStillInUse is AC-15 at the
// boundary, and the shape of the refusal is the point: 409 and not 400 or 500 —
// the request was well formed and the server could have done it, but the state
// of the world says no — and the references arrive with it, so a client can show
// the user what is in the way rather than telling them to go and find it.
func TestLibraryLifecycleHandlerRefusesToPurgeAFileStillInUse(t *testing.T) {
	stub := &lifecycleStub{inUse: &library.InUseError{References: []library.Reference{
		{Kind: library.ReferenceDocument, ID: "doc_1", Name: "attachment"},
		{Kind: library.ReferenceReadingMaterial, ID: "mat_1", Name: "第一课"},
	}}}
	recorder := callAs(t, newLifecycleRouter(stub, &openerStub{}), http.MethodDelete,
		"/api/v1/library/files/lib_1/purge", "")

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Details struct {
				References []library.Reference `json:"references"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "file_in_use" {
		t.Errorf("code = %q, want file_in_use", body.Error.Code)
	}
	if len(body.Error.Details.References) != 2 {
		t.Fatalf("the refusal carried %d references, want both", len(body.Error.Details.References))
	}
	if body.Error.Details.References[1].Kind != library.ReferenceReadingMaterial ||
		body.Error.Details.References[1].Name != "第一课" {
		t.Errorf("the refusal carried %+v, want the material that is in the way",
			body.Error.Details.References[1])
	}
	// The message is a sentence and not the error's own text: the error names
	// rows by id, which is what a client acts on rather than what a person reads.
	if !strings.Contains(body.Error.Message, "still used") {
		t.Errorf("message = %q", body.Error.Message)
	}
}

func TestLibraryLifecycleHandlerMapsTheLibrarysRefusals(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"a file in the bin is not found", library.ErrNotFound, http.StatusNotFound, "not_found"},
		{"an id this library never issued", library.ErrInvalidFile, http.StatusBadRequest, "invalid_request"},
		{"nobody behind the operation", library.ErrNoActor, http.StatusBadRequest, "invalid_request"},
		{"storage that is unreachable", errors.New("private database details"),
			http.StatusInternalServerError, "delete_file_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &lifecycleStub{err: test.err}
			recorder := callAs(t, newLifecycleRouter(stub, &openerStub{}), http.MethodDelete,
				"/api/v1/library/files/lib_1", "")
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), `"code":"`+test.wantCode+`"`) {
				t.Errorf("body = %s, want code %s", recorder.Body.String(), test.wantCode)
			}
			if strings.Contains(recorder.Body.String(), "private database") {
				t.Error("the response leaked an internal error")
			}
		})
	}
}

// TestLibraryLifecycleHandlerRefusesToActWithoutAnAccount: the guard answers
// 401 before any of this, and these routes are behind it. But the guard is not
// what this file proves — the handlers are, because the check is in each one
// rather than in the middleware alone. A handler that read the account loosely
// would be one that acted for account zero, and that is a write nobody asked for
// on somebody's library.
//
// The routes are mounted without the guard, which is the only way to reach a
// handler with no account behind it.
func TestLibraryLifecycleHandlerRefusesToActWithoutAnAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewLibraryLifecycleHandler(&lifecycleStub{}, &openerStub{}, testHandlerLogger())
	for _, mount := range []struct {
		method  string
		pattern string
		route   gin.HandlerFunc
	}{
		{http.MethodDelete, "/library/files/:fileID", handler.SoftDelete},
		{http.MethodPost, "/library/files/:fileID/restore", handler.Restore},
		{http.MethodDelete, "/library/files/:fileID/purge", handler.Purge},
		{http.MethodGet, "/library/files", handler.List},
		{http.MethodGet, "/library/files/:fileID/content", handler.Download},
	} {
		t.Run(mount.method+" "+mount.pattern, func(t *testing.T) {
			router := gin.New()
			router.Handle(mount.method, mount.pattern, mount.route)
			recorder := httptest.NewRecorder()
			// The path is the pattern with its one parameter filled in, rather than
			// one path for every mount: a request to a route that is not mounted is
			// a 404, which would pass for the wrong reason.
			path := strings.Replace(mount.pattern, ":fileID", "lib_1", 1)
			router.ServeHTTP(recorder, httptest.NewRequest(mount.method, path, nil))
			if recorder.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}
