package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"backend/internal/authz"
	"backend/internal/middleware"
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
	claims   []string
	callName string
	// account is the account the last call was made for. Every method here takes
	// one, and the point of recording it is that the handler has to have taken it
	// from the token rather than from anything in the request.
	account authz.Caller
}

type renameCall struct {
	sessionID string
	title     string
}

func (s *sessionStub) Create(_ context.Context, caller authz.Caller) (session.Session, error) {
	s.account = caller
	return s.record, s.err
}

func (s *sessionStub) Get(_ context.Context, sessionID string, caller authz.Caller) (session.Session, error) {
	s.callName, s.account = sessionID, caller
	return s.record, s.err
}

func (s *sessionStub) List(_ context.Context, options session.ListOptions) ([]session.Session, error) {
	s.lists, s.account = append(s.lists, options), options.Caller
	return s.listing, s.err
}

func (s *sessionStub) Rename(_ context.Context, sessionID, title string, caller authz.Caller) (session.Session, error) {
	s.renamed, s.account = &renameCall{sessionID: sessionID, title: title}, caller
	return s.record, s.err
}

func (s *sessionStub) SoftDelete(_ context.Context, sessionID string, caller authz.Caller) error {
	s.touched, s.account = append(s.touched, sessionID), caller
	return s.err
}

func (s *sessionStub) Restore(_ context.Context, sessionID string, caller authz.Caller) error {
	s.touched, s.account = append(s.touched, sessionID), caller
	return s.err
}

func (s *sessionStub) DeletePermanently(_ context.Context, sessionID string, caller authz.Caller) error {
	s.touched, s.account = append(s.touched, sessionID), caller
	return s.err
}

func (s *sessionStub) Claim(_ context.Context, sessionID string, caller authz.Caller) error {
	s.claims, s.account = append(s.claims, sessionID), caller
	return s.err
}

func (s *sessionStub) Messages(_ context.Context, sessionID string, caller authz.Caller) ([]session.Message, error) {
	s.callName, s.account = sessionID, caller
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

// handlerCaller is the account the stub verifier's token stands for: what the
// handlers under test should be handing the domain.
func handlerCaller() authz.Caller {
	return authz.Caller{UserID: handlerAccountID, Role: authz.RoleUser}
}

// guard is the middleware the server puts in front of every route that needs an
// account. The handlers here read the account out of the context it fills in, so
// a test route without it answers 401 to every request — which would be a test
// of the middleware, not of the handler.
func guard() gin.HandlerFunc {
	return middleware.RequireAuth(handlerVerifierStub{}, testHandlerLogger())
}

// newSessionRouter mounts the session routes the way the server does.
func newSessionRouter(sessions Sessions) *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := NewSessionHandler(sessions, testHandlerLogger())
	router := gin.New()
	router.GET("/api/v1/sessions", guard(), handler.List)
	router.POST("/api/v1/sessions", guard(), handler.Create)
	router.GET("/api/v1/sessions/:sessionID", guard(), handler.Get)
	router.PATCH("/api/v1/sessions/:sessionID", guard(), handler.Rename)
	router.DELETE("/api/v1/sessions/:sessionID", guard(), handler.SoftDelete)
	router.POST("/api/v1/sessions/:sessionID/restore", guard(), handler.Restore)
	router.DELETE("/api/v1/sessions/:sessionID/permanent", guard(), handler.DeletePermanently)
	router.POST("/api/v1/sessions/:sessionID/claim", guard(), handler.Claim)
	router.GET("/api/v1/sessions/:sessionID/messages", guard(), handler.Messages)
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
	recorder := callAs(t, newSessionRouter(stub), http.MethodPost, "/api/v1/sessions", "")

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
		{name: "no query lists live sessions", query: "", want: session.ListOptions{Caller: handlerCaller()}},
		{
			name:  "deleted lists the bin",
			query: "?deleted=true&limit=20&offset=40",
			want:  session.ListOptions{Caller: handlerCaller(), Deleted: true, Limit: 20, Offset: 40},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &sessionStub{listing: []session.Session{sampleSession()}}
			recorder := callAs(t, newSessionRouter(stub), http.MethodGet, "/api/v1/sessions"+test.query, "")

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
			recorder := callAs(t, newSessionRouter(stub), http.MethodGet, "/api/v1/sessions"+test.query, "")

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

	recorder := callAs(t, newSessionRouter(stub), http.MethodGet, "/api/v1/sessions/sess_1", "")

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
	recorder := callAs(t, newSessionRouter(stub), http.MethodPatch, "/api/v1/sessions/sess_1", `{"title":"新标题"}`)

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
			recorder := callAs(t, newSessionRouter(stub), http.MethodPatch, "/api/v1/sessions/sess_1", test.body)

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
			recorder := callAs(t, newSessionRouter(stub), test.method, test.path, "")

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
	recorder := callAs(t, newSessionRouter(stub), http.MethodPost, "/api/v1/sessions/sess_1/restore", "")

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
	recorder := callAs(t, newSessionRouter(stub), http.MethodGet, "/api/v1/sessions/sess_1/messages", "")

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
			recorder := callAs(t, newSessionRouter(stub), http.MethodPatch, "/api/v1/sessions/sess_1", `{"title":"新标题"}`)

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
	recorder := callAs(t, func() *gin.Engine {
		gin.SetMode(gin.TestMode)
		router := gin.New()
		router.GET("/api/v1/sessions", handler.List)
		return router
	}(), http.MethodGet, "/api/v1/sessions", "")

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
}

// sessionRoutes is every route this handler owns, for the two tests that walk
// them. A route missing from here is a route whose account is not checked by
// anything, so the list is kept beside the router above rather than written out
// where it is used.
var sessionRoutes = []struct{ method, path, body string }{
	{http.MethodPost, "/api/v1/sessions", ""},
	{http.MethodGet, "/api/v1/sessions", ""},
	{http.MethodGet, "/api/v1/sessions/sess_1", ""},
	{http.MethodPatch, "/api/v1/sessions/sess_1", `{"title":"新标题"}`},
	{http.MethodDelete, "/api/v1/sessions/sess_1", ""},
	{http.MethodPost, "/api/v1/sessions/sess_1/restore", ""},
	{http.MethodPost, "/api/v1/sessions/sess_1/claim", ""},
	{http.MethodDelete, "/api/v1/sessions/sess_1/permanent", ""},
	{http.MethodGet, "/api/v1/sessions/sess_1/messages", ""},
}

func TestEverySessionRouteNeedsAnAccount(t *testing.T) {
	// The routes are behind the token, and this is the half of that a handler
	// test can see: a request with no account is not served, and never reaches
	// the domain with an id nobody chose.
	for _, route := range sessionRoutes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			stub := &sessionStub{}
			// No bearer header: what RequireAuth answers with, and whether the
			// domain was reached at all.
			recorder := call(t, newSessionRouter(stub), route.method, route.path, route.body)

			if recorder.Code != http.StatusUnauthorized {
				t.Errorf("status = %d without a token, want 401; body=%s", recorder.Code, recorder.Body.String())
			}
			if stub.account.Valid() || len(stub.touched)+len(stub.claims) != 0 {
				t.Errorf("the domain was reached without an account: %+v", stub)
			}
		})
	}
}

func TestEverySessionRouteCarriesTheAccountsOwnIdentity(t *testing.T) {
	// The account is the token's and not the request's (AUTH-06): a query
	// parameter or a body field naming another account is not a filter this API
	// has. Each route is walked with a body that tries it.
	for _, route := range sessionRoutes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			stub := &sessionStub{record: sampleSession()}
			body := route.body
			if body == "" {
				body = `{"owner_id":9999}`
			} else {
				body = strings.Replace(body, "{", `{"owner_id":9999,`, 1)
			}
			callAs(t, newSessionRouter(stub), route.method, route.path, body)

			if stub.account != handlerCaller() {
				t.Errorf("the domain was told %+v, want the token's account", stub.account)
			}
		})
	}
}

func TestSessionHandlerClaimHandsOverAnOwnerlessSession(t *testing.T) {
	// §2.5's exit: a session written before V5 belongs to nobody, and this is how
	// one becomes somebody's. The answer is the session rather than a 204,
	// because the row the caller now holds was not in the list they were looking
	// at a moment ago.
	stub := &sessionStub{record: sampleSession()}
	recorder := callAs(t, newSessionRouter(stub), http.MethodPost, "/api/v1/sessions/sess_1/claim", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if len(stub.claims) != 1 || stub.claims[0] != "sess_1" {
		t.Errorf("the domain was asked to claim %v, want the session in the path", stub.claims)
	}
	var envelope struct {
		Data sessionView `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if envelope.Data.SessionID != "sess_1" {
		t.Errorf("data = %+v, want the session that was claimed", envelope.Data)
	}

	// A session that is somebody else's is not there to be claimed, and there is
	// no second answer for it: the same 404 an id that was never issued gets.
	// Saying "it exists and is not yours" would be telling one account about
	// another's row.
	denied := &sessionStub{err: fmt.Errorf("%w: sess_9", session.ErrNotFound)}
	recorder = callAs(t, newSessionRouter(denied), http.MethodPost, "/api/v1/sessions/sess_9/claim", "")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d for a session that cannot be claimed, want 404; body=%s",
			recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"code":"not_found"`) {
		t.Errorf("body = %s", recorder.Body.String())
	}
}
