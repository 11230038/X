package handler

// The cases for GET /api/v1/audit.
//
// What this route is for is a person answering "what happened here", so what
// these pin is that the answer is readable and narrowable: that an entry's
// moment is served in one zone, that a query nobody can interpret is refused
// rather than dropped, and that a page which is not the whole history says so.
//
// Whether an account may read it at all is not here. It is the route in front
// of this handler that decides that, and router_test.go is where it is asked.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"backend/internal/audit"
	"backend/internal/middleware"
	"github.com/gin-gonic/gin"
)

// auditStub is the audit as this layer reaches it.
type auditStub struct {
	events   []audit.Event
	cap      int
	err      error
	calls    int
	filter   audit.Filter
	askedFor bool
}

func (s *auditStub) List(_ context.Context, filter audit.Filter) ([]audit.Event, error) {
	s.calls++
	s.askedFor = true
	s.filter = filter
	if s.err != nil {
		return nil, s.err
	}
	return s.events, nil
}

func (s *auditStub) MaxPageSize() int {
	if s.cap == 0 {
		return 200
	}
	return s.cap
}

func newAuditRouter(reader AuditReader) *gin.Engine {
	gin.SetMode(gin.TestMode)
	logger := testHandlerLogger()
	handler := NewAuditHandler(reader, logger)
	guarded := guard()

	router := gin.New()
	router.Use(middleware.RequestID())
	router.GET("/api/v1/audit", guarded, handler.List)
	return router
}

// anEntry is one entry on the way back out, with its moment in a non-UTC zone
// on purpose: what the wire carries has to be one moment, not the caller's.
func anEntry() audit.Event {
	return audit.Event{
		ID:        12,
		Actor:     audit.Actor{ID: 7, Name: "wei", Role: "user"},
		Action:    audit.ActionPresetChanged,
		Target:    audit.Ref{Kind: audit.TargetAccount, ID: "42"},
		SubjectID: 42,
		Result:    audit.ResultOK,
		Detail:    audit.Detail{Reason: "administrator", Layer: "role"},
		RequestID: "req_1",
		CreatedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.FixedZone("CST", 8*3600)),
	}
}

func TestAuditServesAnEntryWithEveryColumnOnIt(t *testing.T) {
	stub := &auditStub{events: []audit.Event{anEntry()}}
	recorder := callAs(t, newAuditRouter(stub), http.MethodGet, "/api/v1/audit", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}

	var body struct {
		Data struct {
			Events []struct {
				AuditID    int64  `json:"audit_id"`
				ActorID    int64  `json:"actor_id"`
				ActorName  string `json:"actor_name"`
				ActorRole  string `json:"actor_role"`
				Action     string `json:"action"`
				TargetKind string `json:"target_kind"`
				TargetID   string `json:"target_id"`
				SubjectID  int64  `json:"subject_id"`
				Result     string `json:"result"`
				Detail     struct {
					Reason string `json:"reason"`
					Layer  string `json:"layer"`
				} `json:"detail"`
				RequestID string `json:"request_id"`
				CreatedAt string `json:"created_at"`
			} `json:"events"`
			MaxPageSize int `json:"max_page_size"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &body)
	if len(body.Data.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(body.Data.Events))
	}
	got := body.Data.Events[0]
	if got.AuditID != 12 || got.ActorID != 7 || got.ActorName != "wei" || got.ActorRole != "user" {
		t.Errorf("the entry's identity is not on the wire: %+v", got)
	}
	if got.Action != "account.preset_changed" {
		t.Errorf("action = %q", got.Action)
	}
	if got.TargetKind != "account" || got.TargetID != "42" || got.SubjectID != 42 {
		t.Errorf("the entry's target is not on the wire: %+v", got)
	}
	if got.Result != "ok" {
		t.Errorf("result = %q", got.Result)
	}
	if got.Detail.Reason != "administrator" || got.Detail.Layer != "role" {
		t.Errorf("detail = %+v, want the two codes the decision carried", got.Detail)
	}
	if got.RequestID != "req_1" {
		t.Errorf("request_id = %q", got.RequestID)
	}
}

// An entry's moment is served as RFC 3339 in UTC. The column has no zone, so
// the alternative is a timestamp that reads as a different moment in every
// place it is read — and which one is right is not something a reader can work
// out after the fact.
func TestAuditServesTheMomentInUTC(t *testing.T) {
	stub := &auditStub{events: []audit.Event{anEntry()}}
	recorder := callAs(t, newAuditRouter(stub), http.MethodGet, "/api/v1/audit", "")

	var body struct {
		Data struct {
			Events []struct {
				CreatedAt string `json:"created_at"`
			} `json:"events"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &body)
	if got := body.Data.Events[0].CreatedAt; got != "2026-10-01T04:00:00Z" {
		t.Errorf("created_at = %q, want the same moment in UTC", got)
	}
}

// An empty page is an empty list and not null. A client that had to tell "no
// entries" from "no field" would be reading the audit wrongly exactly when it
// has the least to go on.
func TestAuditServesAnEmptyPageAsAnEmptyList(t *testing.T) {
	stub := &auditStub{events: nil, cap: 50}
	recorder := callAs(t, newAuditRouter(stub), http.MethodGet, "/api/v1/audit", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"events":[]`) {
		t.Errorf("an empty audit was not served as an empty list: %s", body)
	}
	if !strings.Contains(body, `"max_page_size":50`) {
		t.Errorf("the cap did not travel with the answer: %s", body)
	}
}

func TestAuditReadsEveryNarrowingIntoTheFilter(t *testing.T) {
	stub := &auditStub{}
	path := "/api/v1/audit?actor_id=7&subject_id=42&action=guardian.linked" +
		"&target_kind=guardian_link&target_id=lnk_1&limit=20" +
		"&since=2026-10-01T00:00:00Z&until=2026-10-02T00:00:00Z"
	recorder := callAs(t, newAuditRouter(stub), http.MethodGet, path, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}

	want := audit.Filter{
		ActorID: 7, SubjectID: 42,
		Action:     audit.ActionGuardianLinked,
		TargetKind: audit.TargetGuardianLink, TargetID: "lnk_1",
		Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		Until: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC),
		Limit: 20,
	}
	if !stub.askedFor {
		t.Fatal("the audit was never asked")
	}
	if stub.filter.ActorID != want.ActorID || stub.filter.SubjectID != want.SubjectID ||
		stub.filter.Action != want.Action || stub.filter.TargetKind != want.TargetKind ||
		stub.filter.TargetID != want.TargetID || stub.filter.Limit != want.Limit {
		t.Errorf("filter = %+v, want %+v", stub.filter, want)
	}
	if !stub.filter.Since.Equal(want.Since) || !stub.filter.Until.Equal(want.Until) {
		t.Errorf("window = [%s, %s), want [%s, %s)",
			stub.filter.Since, stub.filter.Until, want.Since, want.Until)
	}
}

// A query the route cannot read is refused rather than dropped. A parameter
// that was quietly ignored answers a broader question than the one that was
// asked, and broader is the direction that looks like an answer.
func TestAuditRefusesANarrowingItCannotRead(t *testing.T) {
	for _, tt := range []struct {
		name  string
		query string
	}{
		{name: "an account id that is a word", query: "actor_id=wei"},
		{name: "an account id of zero", query: "actor_id=0"},
		{name: "a negative account id", query: "actor_id=-1"},
		// Percent-encoded because it has to arrive as the word it is: a raw space
		// in a request target is not a URL, and the request line would be read as
		// a malformed one rather than reaching the handler at all.
		{name: "a subject that is not an account", query: "subject_id=the%20learner"},
		{name: "an action nobody records", query: "action=authorization.denied"},
		{name: "an action spelled differently", query: "action=Session.Claimed"},
		{name: "a target kind nobody records", query: "target_kind=prompt"},
		{name: "a moment that is not one", query: "since=yesterday"},
		{name: "a moment in another format", query: "until=2026-10-01"},
		{name: "a page size of zero", query: "limit=0"},
		{name: "a page size that is a word", query: "limit=all"},
		{name: "a page size that is negative", query: "limit=-5"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stub := &auditStub{}
			recorder := callAs(t, newAuditRouter(stub), http.MethodGet, "/api/v1/audit?"+tt.query, "")
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			decodeBody(t, recorder, &body)
			if body.Error.Code != "invalid_request" {
				t.Errorf("code = %q, want invalid_request", body.Error.Code)
			}
			if stub.askedFor {
				t.Error("the audit was asked a question the route had refused")
			}
		})
	}
}

// A page larger than the deployment serves is refused and not shortened. A
// silently shortened page reads as "this is everything", which on an audit is
// the one wrong answer it must never give.
func TestAuditRefusesAPageLargerThanTheCap(t *testing.T) {
	stub := &auditStub{err: audit.ErrPageTooLarge}
	recorder := callAs(t, newAuditRouter(stub), http.MethodGet, "/api/v1/audit?limit=1000", "")
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAuditRefusesANarrowingTheStoreDoesNotKnow(t *testing.T) {
	stub := &auditStub{err: audit.ErrUnknownAction}
	recorder := callAs(t, newAuditRouter(stub), http.MethodGet, "/api/v1/audit", "")
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
}

// A failure the caller cannot act on says nothing about itself. What is stored
// behind it is a query and a table name, and neither belongs in a response.
func TestAuditSaysNothingAboutAFailure(t *testing.T) {
	stub := &auditStub{err: errors.New("audit_events: the table is missing")}
	recorder := callAs(t, newAuditRouter(stub), http.MethodGet, "/api/v1/audit", "")
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeBody(t, recorder, &body)
	if body.Error.Code != "audit_read_failed" {
		t.Errorf("code = %q, want audit_read_failed", body.Error.Code)
	}
	if strings.Contains(body.Error.Message, "audit_events") {
		t.Errorf("the message carries the failure: %q", body.Error.Message)
	}
}

// A deployment that wired no audit answers that it has none, which is a
// different sentence from a read that failed: only one of them a retry fixes.
func TestAuditIsUnavailableWithoutAStore(t *testing.T) {
	recorder := callAs(t, newAuditRouter(nil), http.MethodGet, "/api/v1/audit", "")
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeBody(t, recorder, &body)
	if body.Error.Code != "audit_unavailable" {
		t.Errorf("code = %q, want audit_unavailable", body.Error.Code)
	}
}

func TestAuditNeedsAToken(t *testing.T) {
	stub := &auditStub{}
	recorder := call(t, newAuditRouter(stub), http.MethodGet, "/api/v1/audit", "")
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.askedFor {
		t.Error("the audit was read for a request with no token")
	}
}

// The response is the shared envelope, so the request id is in it and a client
// can join an audit read to the log line that produced it.
func TestAuditAnswersInTheSharedEnvelope(t *testing.T) {
	stub := &auditStub{}
	recorder := callAs(t, newAuditRouter(stub), http.MethodGet, "/api/v1/audit", "")

	var body struct {
		Data      json.RawMessage `json:"data"`
		RequestID string          `json:"request_id"`
	}
	decodeBody(t, recorder, &body)
	if body.RequestID == "" {
		t.Error("the response carries no request id")
	}
	if len(body.Data) == 0 {
		t.Error("the response carries no data")
	}
}
