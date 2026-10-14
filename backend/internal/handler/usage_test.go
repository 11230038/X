package handler

// The cases for GET /api/v1/usage.
//
// Three things are pinned here and they are the three §2.8 and AC-13 are about.
// The numbers on the wire are the columns and never usage_json; the window and
// the breakdown travel with them, because a total without its window is one
// nobody can check; and the answer has nowhere to put an endpoint — which is
// what the case about the base URL asserts by reading the whole body rather
// than a field.
//
// Whether an account may reach this route at all is the router's business, and
// router_test.go is where it is asked. What is here is the one thing the route
// cannot decide for itself: whose calls the report is over. That comes from the
// token, and the cases below are the ones that would fail if it ever came from
// the query string.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"backend/internal/auth"
	"backend/internal/authz"
	"backend/internal/middleware"
	"backend/internal/usage"
	"github.com/gin-gonic/gin"
)

// usageStub is the usage report as this layer reaches it. It records the query
// it was asked rather than answering one, because most of these cases are about
// what the route did with the request before the domain ever saw it.
type usageStub struct {
	report usage.Report
	err    error
	asked  bool
	query  usage.Query
}

func (s *usageStub) Read(_ context.Context, query usage.Query) (usage.Report, error) {
	s.asked, s.query = true, query
	if s.err != nil {
		return usage.Report{}, s.err
	}
	return s.report, nil
}

// usageAdminVerifier is the shared stub with the one difference that matters
// here: the token stands for the administrator, who is the account that also
// sees the rows belonging to nobody.
type usageAdminVerifier struct{}

func (usageAdminVerifier) Authenticate(_ context.Context, token string) (auth.User, error) {
	if token != handlerToken {
		return auth.User{}, auth.ErrInvalidToken
	}
	return auth.User{UserID: handlerAccountID, Username: "root", Role: authz.RoleAdmin}, nil
}

func newUsageRouter(reader UsageReader, verifier middleware.TokenVerifier) *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := NewUsageHandler(reader, testHandlerLogger())
	router := gin.New()
	router.Use(middleware.RequestID())
	router.GET("/api/v1/usage", middleware.RequireAuth(verifier, testHandlerLogger()), handler.Get)
	return router
}

// aReport is one answer as the domain would hand it back: a window, a breakdown
// by model, three calls that came back and one that did not.
func aReport() usage.Report {
	since := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	return usage.Report{
		Since:   &since,
		GroupBy: usage.GroupByModel,
		Finished: usage.Totals{
			Calls: 3, Ok: 2, Error: 1,
			PromptTokens: 120, CompletionTokens: 45, TotalTokens: 165,
			DurationMs: 30000, TTFTMs: 420,
		},
		Unfinished: 1,
		Groups: []usage.Group{
			{
				Key: "deepseek-chat",
				Finished: usage.Totals{
					Calls: 2, Ok: 2, PromptTokens: 100, CompletionTokens: 40, TotalTokens: 140,
					DurationMs: 20000, TTFTMs: 300,
				},
				Unfinished: 1,
			},
			{
				Key: "deepseek-reasoner",
				Finished: usage.Totals{
					Calls: 1, Error: 1, PromptTokens: 20, CompletionTokens: 5, TotalTokens: 25,
					DurationMs: 10000, TTFTMs: 120,
				},
			},
		},
	}
}

func TestUsageServesEveryColumnOfTheReport(t *testing.T) {
	stub := &usageStub{report: aReport()}
	recorder := callAs(t, newUsageRouter(stub, handlerVerifierStub{}), http.MethodGet, "/api/v1/usage", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}

	var body struct {
		Data struct {
			Since      string `json:"since"`
			GroupBy    string `json:"group_by"`
			Finished   map[string]int64
			Unfinished int64 `json:"unfinished"`
			Groups     []struct {
				Key        string           `json:"key"`
				Finished   map[string]int64 `json:"finished"`
				Unfinished int64            `json:"unfinished"`
			} `json:"groups"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &body)

	if body.Data.Since != "2026-10-01T00:00:00Z" {
		t.Errorf("since = %q, want the window the report is over", body.Data.Since)
	}
	if body.Data.GroupBy != "model" {
		t.Errorf("group_by = %q", body.Data.GroupBy)
	}
	want := map[string]int64{
		"calls": 3, "ok": 2, "error": 1, "cancelled": 0,
		"prompt_tokens": 120, "completion_tokens": 45, "total_tokens": 165,
		"duration_ms": 30000, "ttft_ms": 420,
	}
	for field, value := range want {
		if body.Data.Finished[field] != value {
			t.Errorf("finished.%s = %d, want %d", field, body.Data.Finished[field], value)
		}
	}
	if body.Data.Unfinished != 1 {
		t.Errorf("unfinished = %d, want the call that did not come back", body.Data.Unfinished)
	}
	if len(body.Data.Groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(body.Data.Groups))
	}
	if body.Data.Groups[0].Key != "deepseek-chat" || body.Data.Groups[0].Finished["total_tokens"] != 140 {
		t.Errorf("the first bucket is not the model it was asked to be: %+v", body.Data.Groups[0])
	}
	if body.Data.Groups[0].Unfinished != 1 {
		t.Errorf("the bucket does not carry the call that hung: %+v", body.Data.Groups[0])
	}
	if body.Data.Groups[1].Finished["error"] != 1 {
		t.Errorf("the second bucket does not say its call failed: %+v", body.Data.Groups[1])
	}
}

// AC-13: numeric usage with no prompt, no response, no credential and no
// endpoint. The check is on the whole body rather than on a field, because the
// requirement is about what the answer cannot carry — a case that looked for a
// named field would pass the day somebody added a differently named one.
func TestUsageAnswersWithNoEndpointAnywhere(t *testing.T) {
	stub := &usageStub{report: aReport()}
	recorder := callAs(t, newUsageRouter(stub, handlerVerifierStub{}), http.MethodGet, "/api/v1/usage", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}

	body := recorder.Body.String()
	for _, forbidden := range []string{"http", "base_url", "api_key", "authorization", "bearer"} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Errorf("the response carries %q, which is a thing usage does not record: %s", forbidden, body)
		}
	}
}

// The report is over the caller's calls and nobody else's, and the account
// comes from the token: there is no parameter on this route that names one.
func TestUsageReportsTheCallersOwnCalls(t *testing.T) {
	stub := &usageStub{}
	recorder := callAs(t, newUsageRouter(stub, handlerVerifierStub{}),
		http.MethodGet, "/api/v1/usage?model=deepseek-chat", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if !stub.asked {
		t.Fatal("the report was never asked")
	}
	if stub.query.OwnerID != handlerAccountID {
		t.Errorf("OwnerID = %d, want the account on the token, %d", stub.query.OwnerID, handlerAccountID)
	}
	if stub.query.Admin {
		t.Error("a plain account was reported as the administrator")
	}
}

// An administrator sees their own calls plus the rows that belong to nobody,
// and that is the whole of the widening: it is not another account's calls, and
// the query says which of the two it is rather than leaving it to the store.
func TestUsageWidensToTheUnownedRowsForTheAdministrator(t *testing.T) {
	stub := &usageStub{}
	recorder := callAs(t, newUsageRouter(stub, usageAdminVerifier{}), http.MethodGet, "/api/v1/usage", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if !stub.query.Admin {
		t.Errorf("query = %+v, want the administrator's widening", stub.query)
	}
	if stub.query.OwnerID != handlerAccountID {
		t.Errorf("OwnerID = %d, want the administrator's own account too", stub.query.OwnerID)
	}
}

func TestUsageReadsEveryNarrowingIntoTheQuery(t *testing.T) {
	stub := &usageStub{}
	path := "/api/v1/usage?group_by=source&model=deepseek-chat&provider=deepseek" +
		"&source=practice.judge&session_id=ses_1" +
		"&since=2026-10-01T00:00:00Z&until=2026-10-02T00:00:00Z"
	recorder := callAs(t, newUsageRouter(stub, handlerVerifierStub{}), http.MethodGet, path, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}

	want := usage.Query{
		OwnerID: handlerAccountID, GroupBy: usage.GroupBySource,
		Model: "deepseek-chat", Provider: "deepseek",
		Source: "practice.judge", SessionID: "ses_1",
		Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		Until: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC),
	}
	if !stub.asked {
		t.Fatal("the report was never asked")
	}
	if stub.query.GroupBy != want.GroupBy || stub.query.Model != want.Model ||
		stub.query.Provider != want.Provider || stub.query.Source != want.Source ||
		stub.query.SessionID != want.SessionID {
		t.Errorf("query = %+v, want %+v", stub.query, want)
	}
	if !stub.query.Since.Equal(want.Since) || !stub.query.Until.Equal(want.Until) {
		t.Errorf("window = [%s, %s), want [%s, %s)",
			stub.query.Since, stub.query.Until, want.Since, want.Until)
	}
}

// A narrowing the route cannot read is refused rather than dropped, and the
// report is never asked the broader question the caller did not ask.
func TestUsageRefusesANarrowingItCannotRead(t *testing.T) {
	for _, tt := range []struct {
		name  string
		query string
	}{
		{name: "a breakdown nobody serves", query: "group_by=session_turn"},
		{name: "a breakdown spelled differently", query: "group_by=Model"},
		{name: "a moment that is not one", query: "since=yesterday"},
		{name: "a moment in another format", query: "until=2026-10-01"},
		{name: "a moment with no zone", query: "since=2026-10-01T00:00:00"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stub := &usageStub{}
			recorder := callAs(t, newUsageRouter(stub, handlerVerifierStub{}),
				http.MethodGet, "/api/v1/usage?"+tt.query, "")
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
			if stub.asked {
				t.Error("the report was asked a question the route had refused")
			}
		})
	}
}

// A window that ends before it starts is refused and not served as a report of
// zero: a report of nothing reads as "you spent nothing", which is an answer to
// a question nobody asked.
func TestUsageRefusesAWindowThatEndsBeforeItStarts(t *testing.T) {
	stub := &usageStub{err: usage.ErrEmptyWindow}
	path := "/api/v1/usage?since=2026-10-02T00:00:00Z&until=2026-10-01T00:00:00Z"
	recorder := callAs(t, newUsageRouter(stub, handlerVerifierStub{}), http.MethodGet, path, "")
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
}

// A window with nothing in it is an answer, and it is one shape: an empty list
// rather than a missing field, so that a client drawing a breakdown reads what
// it always reads.
func TestUsageServesAnEmptyWindowAsAnEmptyList(t *testing.T) {
	stub := &usageStub{report: usage.Report{Groups: []usage.Group{}}}
	recorder := callAs(t, newUsageRouter(stub, handlerVerifierStub{}),
		http.MethodGet, "/api/v1/usage?group_by=model", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"groups":[]`) {
		t.Errorf("an empty breakdown was not served as an empty list: %s", body)
	}
	if strings.Contains(body, `"since"`) {
		t.Errorf("a report that was given no window served one: %s", body)
	}
}

// A failure the caller cannot act on says nothing about itself. What is behind
// it is a query and a table name, and neither belongs in a response.
func TestUsageSaysNothingAboutAFailure(t *testing.T) {
	stub := &usageStub{err: errors.New("llm_calls: the table is missing")}
	recorder := callAs(t, newUsageRouter(stub, handlerVerifierStub{}), http.MethodGet, "/api/v1/usage", "")
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
	if body.Error.Code != "usage_read_failed" {
		t.Errorf("code = %q, want usage_read_failed", body.Error.Code)
	}
	if strings.Contains(body.Error.Message, "llm_calls") {
		t.Errorf("the message carries the failure: %q", body.Error.Message)
	}
}

// A deployment that wired no report answers that it has none, which is a
// different sentence from a read that failed: only one of them a retry fixes.
func TestUsageIsUnavailableWithoutAReport(t *testing.T) {
	recorder := callAs(t, newUsageRouter(nil, handlerVerifierStub{}), http.MethodGet, "/api/v1/usage", "")
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeBody(t, recorder, &body)
	if body.Error.Code != "usage_unavailable" {
		t.Errorf("code = %q, want usage_unavailable", body.Error.Code)
	}
}

func TestUsageNeedsAToken(t *testing.T) {
	stub := &usageStub{}
	recorder := call(t, newUsageRouter(stub, handlerVerifierStub{}), http.MethodGet, "/api/v1/usage", "")
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.asked {
		t.Error("the usage report was read for a request with no token")
	}
}

// The shared envelope, so that a report can be joined to the log line that
// answered it.
func TestUsageAnswersInTheSharedEnvelope(t *testing.T) {
	stub := &usageStub{}
	recorder := callAs(t, newUsageRouter(stub, handlerVerifierStub{}), http.MethodGet, "/api/v1/usage", "")

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
