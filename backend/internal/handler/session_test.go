package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"backend/internal/session"
	"github.com/gin-gonic/gin"
)

var handlerSessionTime = time.Unix(1_800_000_000, 0).UTC()

// sessionStub is the session use case, recording what it was asked for.
type sessionStub struct {
	record   session.Session
	listing  []session.Session
	history  []session.Message
	err      error
	lists    []session.ListOptions
	renamed  *renameCall
	touched  []string
	callName string
}

type renameCall struct {
	sessionID string
	title     string
}

func (s *sessionStub) Create(context.Context) (session.Session, error) {
	return s.record, s.err
}

func (s *sessionStub) Get(_ context.Context, sessionID string) (session.Session, error) {
	s.callName = sessionID
	return s.record, s.err
}

func (s *sessionStub) List(_ context.Context, options session.ListOptions) ([]session.Session, error) {
	s.lists = append(s.lists, options)
	return s.listing, s.err
}

func (s *sessionStub) Rename(_ context.Context, sessionID, title string) (session.Session, error) {
	s.renamed = &renameCall{sessionID: sessionID, title: title}
	return s.record, s.err
}

func (s *sessionStub) SoftDelete(_ context.Context, sessionID string) error {
	s.touched = append(s.touched, sessionID)
	return s.err
}

func (s *sessionStub) Restore(_ context.Context, sessionID string) error {
	s.touched = append(s.touched, sessionID)
	return s.err
}

func (s *sessionStub) DeletePermanently(_ context.Context, sessionID string) error {
	s.touched = append(s.touched, sessionID)
	return s.err
}

func (s *sessionStub) Messages(_ context.Context, sessionID string) ([]session.Message, error) {
	s.callName = sessionID
	return s.history, s.err
}

func sampleSession() session.Session {
	return session.Session{
		ID:        "sess_1",
		Title:     "闭包是什么",
		CreatedAt: handlerSessionTime,
		UpdatedAt: handlerSessionTime,
	}
}

// newSessionRouter mounts the session routes the way the server does.
func newSessionRouter(sessions Sessions) *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := NewSessionHandler(sessions, testHandlerLogger())
	router := gin.New()
	router.GET("/api/v1/sessions", handler.List)
	router.POST("/api/v1/sessions", handler.Create)
	router.GET("/api/v1/sessions/:sessionID", handler.Get)
	router.PATCH("/api/v1/sessions/:sessionID", handler.Rename)
	router.DELETE("/api/v1/sessions/:sessionID", handler.SoftDelete)
	router.POST("/api/v1/sessions/:sessionID/restore", handler.Restore)
	router.DELETE("/api/v1/sessions/:sessionID/permanent", handler.DeletePermanently)
	router.GET("/api/v1/sessions/:sessionID/messages", handler.Messages)
	return router
}

// testHandlerLogger logs nowhere: what these tests assert on is the response,
// and a logger writing to the test output would only add noise.
func testHandlerLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
}

func call(t *testing.T, router *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func decodeBody(t *testing.T, recorder *httptest.ResponseRecorder, into any) {
	t.Helper()
	if err := json.Unmarshal(recorder.Body.Bytes(), into); err != nil {
		t.Fatalf("the response is not the API envelope: %v (%s)", err, recorder.Body.String())
	}
}

func TestSessionHandlerCreateReturnsTheNewSession(t *testing.T) {
	stub := &sessionStub{record: sampleSession()}
	recorder := call(t, newSessionRouter(stub), http.MethodPost, "/api/v1/sessions", "")

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data sessionView `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if envelope.Data.SessionID != "sess_1" || envelope.Data.Title != "闭包是什么" {
		t.Errorf("data = %+v, want the session that was created", envelope.Data)
	}
	if envelope.Data.DeletedAt != nil {
		t.Error("a session that was just created is reported as deleted")
	}
}

func TestSessionHandlerListAsksForTheBinItWasAskedFor(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  session.ListOptions
	}{
		{name: "no query lists live sessions", query: "", want: session.ListOptions{}},
		{
			name:  "deleted lists the bin",
			query: "?deleted=true&limit=20&offset=40",
			want:  session.ListOptions{Deleted: true, Limit: 20, Offset: 40},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &sessionStub{listing: []session.Session{sampleSession()}}
			recorder := call(t, newSessionRouter(stub), http.MethodGet, "/api/v1/sessions"+test.query, "")

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
			}
			if len(stub.lists) != 1 || stub.lists[0] != test.want {
				t.Errorf("List() was called with %+v, want %+v", stub.lists, test.want)
			}
			// The paging the client asked for is echoed, so a client that pages
			// can tell which page it is looking at.
			var envelope struct {
				Data sessionListView `json:"data"`
			}
			decodeBody(t, recorder, &envelope)
			if envelope.Data.Limit != test.want.Limit || envelope.Data.Offset != test.want.Offset {
				t.Errorf("data = %+v, want the paging that was asked for", envelope.Data)
			}
		})
	}
}

func TestSessionHandlerRefusesPagingItCannotRead(t *testing.T) {
	tests := []struct{ name, query string }{
		{name: "a limit that is not a number", query: "?limit=lots"},
		{name: "a negative offset", query: "?offset=-1"},
		{name: "a deleted flag that is not a boolean", query: "?deleted=perhaps"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &sessionStub{}
			recorder := call(t, newSessionRouter(stub), http.MethodGet, "/api/v1/sessions"+test.query, "")

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
			if len(stub.lists) != 0 {
				t.Error("the listing was queried with paging that could not be read")
			}
		})
	}
}

func TestSessionHandlerShowsWhenASessionIsInTheBin(t *testing.T) {
	deleted := sampleSession()
	deleted.DeletedAt = handlerSessionTime
	stub := &sessionStub{record: deleted}

	recorder := call(t, newSessionRouter(stub), http.MethodGet, "/api/v1/sessions/sess_1", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data sessionView `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if envelope.Data.DeletedAt == nil {
		t.Fatal("a session in the bin is reported as live")
	}
	if !envelope.Data.DeletedAt.Equal(handlerSessionTime) {
		t.Errorf("deleted_at = %v, want %v", envelope.Data.DeletedAt, handlerSessionTime)
	}
}

func TestSessionHandlerRenameCarriesTheTitle(t *testing.T) {
	stub := &sessionStub{record: sampleSession()}
	recorder := call(t, newSessionRouter(stub), http.MethodPatch, "/api/v1/sessions/sess_1", `{"title":"新标题"}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.renamed == nil || stub.renamed.sessionID != "sess_1" || stub.renamed.title != "新标题" {
		t.Errorf("Rename() was called with %+v, want the session and its new title", stub.renamed)
	}
}

func TestSessionHandlerRefusesARenameItCannotRead(t *testing.T) {
	tests := []struct{ name, body string }{
		{name: "no body at all", body: ""},
		{name: "a body that is not JSON", body: "title=新标题"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &sessionStub{}
			recorder := call(t, newSessionRouter(stub), http.MethodPatch, "/api/v1/sessions/sess_1", test.body)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
			if stub.renamed != nil {
				t.Error("a rename ran on a body that could not be read")
			}
		})
	}
}

func TestSessionHandlerRecycleBinRoutesAnswerWithNoContent(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "soft delete", method: http.MethodDelete, path: "/api/v1/sessions/sess_1"},
		{name: "permanent delete", method: http.MethodDelete, path: "/api/v1/sessions/sess_1/permanent"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &sessionStub{}
			recorder := call(t, newSessionRouter(stub), test.method, test.path, "")

			if recorder.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204; body=%s", recorder.Code, recorder.Body.String())
			}
			if len(stub.touched) != 1 || stub.touched[0] != "sess_1" {
				t.Errorf("the store was asked to touch %v, want the session that was named", stub.touched)
			}
		})
	}
}

func TestSessionHandlerRestoreReturnsTheRestoredSession(t *testing.T) {
	stub := &sessionStub{record: sampleSession()}
	recorder := call(t, newSessionRouter(stub), http.MethodPost, "/api/v1/sessions/sess_1/restore", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data sessionView `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if envelope.Data.DeletedAt != nil {
		t.Error("the session is still reported as deleted after being restored")
	}
}

func TestSessionHandlerMessagesReturnsTheHistory(t *testing.T) {
	stub := &sessionStub{history: []session.Message{
		{ID: "msg_1", SessionID: "sess_1", Role: session.RoleUser, Content: "闭包是什么", CreatedAt: handlerSessionTime},
		{ID: "msg_2", SessionID: "sess_1", Role: session.RoleAssistant, Content: "闭包是……", Capability: "chat", CreatedAt: handlerSessionTime},
	}}
	recorder := call(t, newSessionRouter(stub), http.MethodGet, "/api/v1/sessions/sess_1/messages", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data messageListView `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if len(envelope.Data.Messages) != 2 {
		t.Fatalf("messages = %+v, want both", envelope.Data.Messages)
	}
	// The order is the store's and is what a client renders, so it is passed
	// through rather than sorted here.
	if envelope.Data.Messages[0].MessageID != "msg_1" || envelope.Data.Messages[1].MessageID != "msg_2" {
		t.Errorf("messages = %+v, want them in the order they were stored", envelope.Data.Messages)
	}
	if envelope.Data.Messages[1].Capability != "chat" {
		t.Errorf("capability = %q, want what produced the message", envelope.Data.Messages[1].Capability)
	}
}

func TestSessionHandlerMapsWhatTheClientCanActOn(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
		code string
	}{
		{name: "a session that is not there", err: session.ErrNotFound, want: http.StatusNotFound, code: "not_found"},
		{name: "a title that cannot be read back", err: session.ErrInvalidTitle, want: http.StatusBadRequest, code: "invalid_title"},
		{
			name: "something only an operator can act on",
			err:  errors.New("private database details"),
			want: http.StatusInternalServerError,
			code: "rename_session_failed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &sessionStub{err: test.err}
			recorder := call(t, newSessionRouter(stub), http.MethodPatch, "/api/v1/sessions/sess_1", `{"title":"新标题"}`)

			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.want, recorder.Body.String())
			}
			var envelope struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			decodeBody(t, recorder, &envelope)
			if envelope.Error.Code != test.code {
				t.Errorf("error code = %q, want %q", envelope.Error.Code, test.code)
			}
			// A failure the client cannot fix must not describe us to it.
			if strings.Contains(recorder.Body.String(), "private database details") {
				t.Errorf("the response repeats the internal error: %s", recorder.Body.String())
			}
		})
	}
}

func TestSessionHandlerWithoutAStoreFailsRatherThanPanics(t *testing.T) {
	var handler *SessionHandler
	recorder := call(t, func() *gin.Engine {
		gin.SetMode(gin.TestMode)
		router := gin.New()
		router.GET("/api/v1/sessions", handler.List)
		return router
	}(), http.MethodGet, "/api/v1/sessions", "")

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
}
