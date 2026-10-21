package handler

import (
	"context"
	"encoding/json"
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
	// createdKinds records the partition the handler asked for. The zero value of
	// session.ListOptions is chat, but a create always names its kind explicitly,
	// so the handler cannot silently create the wrong one.
	createdKinds []session.Kind
	// account is the account the last call was made for. Every method here takes
	// one, and the point of recording it is that the handler has to have taken it
	// from the token rather than from anything in the request.
	account authz.Caller
}

type renameCall struct {
	sessionID string
	title     string
}

func (s *sessionStub) Create(_ context.Context, caller authz.Caller, kind session.Kind) (session.Session, error) {
	s.account = caller
	s.createdKinds = append(s.createdKinds, kind)
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
		Kind:      session.KindChat,
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
