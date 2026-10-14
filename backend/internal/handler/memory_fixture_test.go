package handler

// The memory HTTP tests' fixtures: the domain as this layer reaches it, the
// routes mounted over it, and one fact as the domain would hand it back.
//
// They are in a file of their own because more than one file of cases uses them —
// what the routes do with a request and what they put on the wire are two
// readings of the same fixture — and because a fixture that lives beside the
// cases it happens to have started with reads as though it belonged to them.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"backend/internal/memory"
	"backend/internal/middleware"
	"github.com/gin-gonic/gin"
)

// memoryStub is the domain as this layer reaches it: what each call answers, and
// what it was asked for.
type memoryStub struct {
	traces    []memory.Trace
	facts     []memory.Fact
	held      memory.FactWithEvidence
	audits    []memory.Audit
	edited    memory.FactWithEvidence
	stated    memory.FactWithEvidence
	moved     memory.Fact
	merge     memory.DedupeReport
	purged    memory.CleanupReport
	computed  memory.Synthesis
	exported  memory.Export
	err       error
	calls     int
	readOwner int64
	readFact  string

	// What it was asked for, which is where most of these cases read.
	traceQuery    memory.TraceQuery
	factQuery     memory.FactQuery
	auditQuery    memory.AuditQuery
	editRequest   memory.EditRequest
	stateRequest  memory.StateRequest
	factRequests  []memory.FactRequest
	dedupeRequest memory.DedupeRequest
	cleanupQuery  memory.CleanupQuery
	exportRequest memory.ExportRequest
}

func (s *memoryStub) Traces(_ context.Context, query memory.TraceQuery) ([]memory.Trace, error) {
	s.calls++
	s.traceQuery = query
	return s.traces, s.err
}

func (s *memoryStub) Facts(_ context.Context, query memory.FactQuery) ([]memory.Fact, error) {
	s.calls++
	s.factQuery = query
	return s.facts, s.err
}

func (s *memoryStub) Fact(_ context.Context, ownerID int64, factID string) (memory.FactWithEvidence, error) {
	s.calls++
	s.readOwner, s.readFact = ownerID, factID
	return s.held, s.err
}

func (s *memoryStub) Audits(_ context.Context, query memory.AuditQuery) ([]memory.Audit, error) {
	s.calls++
	s.auditQuery = query
	return s.audits, s.err
}

func (s *memoryStub) Edit(_ context.Context, request memory.EditRequest) (memory.FactWithEvidence, error) {
	s.calls++
	s.editRequest = request
	return s.edited, s.err
}

func (s *memoryStub) State(_ context.Context, request memory.StateRequest) (memory.FactWithEvidence, error) {
	s.calls++
	s.stateRequest = request
	return s.stated, s.err
}

func (s *memoryStub) Retract(_ context.Context, request memory.FactRequest) (memory.Fact, error) {
	s.calls++
	s.factRequests = append(s.factRequests, request)
	return s.moved, s.err
}

func (s *memoryStub) Restore(_ context.Context, request memory.FactRequest) (memory.Fact, error) {
	s.calls++
	s.factRequests = append(s.factRequests, request)
	return s.moved, s.err
}

func (s *memoryStub) Dedupe(_ context.Context, request memory.DedupeRequest) (memory.DedupeReport, error) {
	s.calls++
	s.dedupeRequest = request
	return s.merge, s.err
}

func (s *memoryStub) Cleanup(_ context.Context, query memory.CleanupQuery) (memory.CleanupReport, error) {
	s.calls++
	s.cleanupQuery = query
	return s.purged, s.err
}

func (s *memoryStub) Synthesis(_ context.Context, ownerID int64) (memory.Synthesis, error) {
	s.calls++
	s.readOwner = ownerID
	return s.computed, s.err
}

func (s *memoryStub) Export(_ context.Context, request memory.ExportRequest) (memory.Export, error) {
	s.calls++
	s.exportRequest = request
	return s.exported, s.err
}

// memoryRoutes is the twelve of §2.14, in the order the routes are registered.
var memoryRoutes = []struct {
	method string
	path   string
}{
	{http.MethodGet, "/api/v1/memory/traces"},
	{http.MethodGet, "/api/v1/memory/facts"},
	{http.MethodGet, "/api/v1/memory/facts/mf_1"},
	{http.MethodPatch, "/api/v1/memory/facts/mf_1"},
	{http.MethodPost, "/api/v1/memory/facts"},
	{http.MethodPost, "/api/v1/memory/facts/mf_1/retract"},
	{http.MethodPost, "/api/v1/memory/facts/mf_1/restore"},
	{http.MethodPost, "/api/v1/memory/facts/dedupe"},
	{http.MethodPost, "/api/v1/memory/cleanup"},
	{http.MethodGet, "/api/v1/memory/synthesis"},
	{http.MethodGet, "/api/v1/memory/audit"},
	{http.MethodGet, "/api/v1/memory/export"},
}

// newMemoryRouter mounts every memory route behind the real bearer middleware,
// over one stub, so that the route and the token are exercised together.
func newMemoryRouter(stub *memoryStub) *gin.Engine {
	gin.SetMode(gin.TestMode)
	logger := testHandlerLogger()
	handler := NewMemoryHandler(stub, logger)
	guarded := middleware.RequireAuth(handlerVerifierStub{}, logger)
	router := gin.New()
	router.Use(middleware.RequestID())
	router.GET("/api/v1/memory/traces", guarded, handler.Traces)
	router.GET("/api/v1/memory/facts", guarded, handler.Facts)
	router.GET("/api/v1/memory/facts/:factID", guarded, handler.Fact)
	router.PATCH("/api/v1/memory/facts/:factID", guarded, handler.EditFact)
	router.POST("/api/v1/memory/facts", guarded, handler.StateFact)
	router.POST("/api/v1/memory/facts/:factID/retract", guarded, handler.RetractFact)
	router.POST("/api/v1/memory/facts/:factID/restore", guarded, handler.RestoreFact)
	router.POST("/api/v1/memory/facts/dedupe", guarded, handler.Dedupe)
	router.POST("/api/v1/memory/cleanup", guarded, handler.Cleanup)
	router.GET("/api/v1/memory/synthesis", guarded, handler.Synthesis)
	router.GET("/api/v1/memory/audit", guarded, handler.Audit)
	router.GET("/api/v1/memory/export", guarded, handler.Export)
	return router
}

// callAs makes one request bearing the stub verifier's token, which is the only
// way to reach a memory route at all.
func callAs(t *testing.T, router *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer "+handlerToken)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

// aStoredFact is one fact as the domain would hand it back.
func aStoredFact() memory.Fact {
	return memory.Fact{
		ID: "mf_1", OwnerID: 7, Surface: memory.SurfaceMastery, Scope: "career",
		Statement: "我在学 Go", Fingerprint: strings.Repeat("a", 64), Confidence: 80,
		Status: memory.StatusActive, ConfirmedBy: memory.ConfirmerGate, Version: 1,
		CreatedAt: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC),
	}
}

// aMemoryFailure is the error envelope, for the cases that read which code came
// back rather than which status.
type aMemoryFailure struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}
