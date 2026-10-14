package handler

// What the memory routes do with a request before the domain ever sees it.
//
// The claims are §2.14's and MEM-04's: that every route is behind the token, that
// the account is the token's and cannot be named by the caller, that the filter
// words travel as they arrived to be refused where the vocabulary lives, that an
// edit changes only what it sent, and that a failure becomes the status a client
// can act on. The domain here is a stub, because what is being read is the HTTP
// layer's own decisions; whether the rows a real service writes are the rows the
// engine keeps is the memory package's question.

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"backend/internal/memory"
	"backend/internal/middleware"
	"github.com/gin-gonic/gin"
)

func TestEveryMemoryRouteNeedsAnAccount(t *testing.T) {
	for _, route := range memoryRoutes {
		stub := &memoryStub{}
		// No bearer header: what RequireAuth answers with, and whether the domain
		// was reached at all.
		recorder := call(t, newMemoryRouter(stub), route.method, route.path, "")

		if recorder.Code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d without a token, want 401", route.method, route.path, recorder.Code)
		}
		if stub.calls != 0 {
			t.Errorf("%s %s reached the domain without an account", route.method, route.path)
		}
	}
}

// TestMemoryReadsAreTheAccountsOwn is MEM-04's pin at this layer: the account is
// the token's, and a caller who names one is not naming anything.
func TestMemoryReadsAreTheAccountsOwn(t *testing.T) {
	stub := &memoryStub{}
	router := newMemoryRouter(stub)

	// An `owner_id` on the query string is not a filter this API has. The stub
	// verifier's account is 7, so every read below must arrive as 7 whatever the
	// caller wrote.
	callAs(t, router, http.MethodGet, "/api/v1/memory/traces?owner_id=8", "")
	if stub.traceQuery.OwnerID != 7 {
		t.Errorf("the observations were read for account %d, want the token's", stub.traceQuery.OwnerID)
	}
	callAs(t, router, http.MethodGet, "/api/v1/memory/synthesis?owner_id=8", "")
	if stub.readOwner != 7 {
		t.Errorf("the synthesis was computed for account %d, want the token's", stub.readOwner)
	}

	callAs(t, router, http.MethodGet, "/api/v1/memory/facts?owner_id=8", "")
	if stub.factQuery.OwnerID != 7 {
		t.Errorf("the listing asked for account %d, want the token's", stub.factQuery.OwnerID)
	}
	callAs(t, router, http.MethodGet, "/api/v1/memory/audit?owner_id=8", "")
	if stub.auditQuery.OwnerID != 7 {
		t.Errorf("the trail asked for account %d, want the token's", stub.auditQuery.OwnerID)
	}
	callAs(t, router, http.MethodGet, "/api/v1/memory/facts/mf_1?owner_id=8", "")
	if stub.readOwner != 7 || stub.readFact != "mf_1" {
		t.Errorf("the fact read was %d/%s, want the token's account and the path's id", stub.readOwner, stub.readFact)
	}

	// And a body that names one: the field is not in the request this handler
	// reads, so it cannot reach the domain however it is spelled.
	callAs(t, router, http.MethodPost, "/api/v1/memory/facts", `{"statement":"我在学 Go","owner_id":8}`)
	if stub.stateRequest.OwnerID != 7 {
		t.Errorf("a stating body naming an owner wrote for account %d", stub.stateRequest.OwnerID)
	}
	callAs(t, router, http.MethodGet, "/api/v1/memory/export", "")
	if stub.exportRequest.OwnerID != 7 {
		t.Errorf("the export asked for account %d, want the token's", stub.exportRequest.OwnerID)
	}
}

func TestMemoryFiltersAreHandedToTheDomain(t *testing.T) {
	stub := &memoryStub{}
	router := newMemoryRouter(stub)
	september := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	october := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	callAs(t, router, http.MethodGet,
		"/api/v1/memory/traces?surface=mastery&kind=goal&since=2026-09-01T00:00:00Z&until=2026-10-01T00:00:00Z&limit=20", "")
	want := memory.TraceQuery{OwnerID: 7, Surface: memory.SurfaceMastery, Kind: "goal",
		Since: september, Until: october, Limit: 20}
	if stub.traceQuery != want {
		t.Errorf("the listing asked for %+v, want %+v", stub.traceQuery, want)
	}

	// The status filter is named once per status, and the words travel as they
	// arrived: the three of them are the domain's vocabulary, not a copy of it.
	callAs(t, router, http.MethodGet,
		"/api/v1/memory/facts?status=active&status=retracted&surface=reading&before=2026-10-01T00:00:00Z&limit=5", "")
	wantFacts := memory.FactQuery{
		OwnerID: 7, Statuses: []memory.Status{memory.StatusActive, memory.StatusRetracted},
		Surface: memory.SurfaceReading, Before: october, Limit: 5,
	}
	if !reflect.DeepEqual(stub.factQuery, wantFacts) {
		t.Errorf("the listing asked for %+v, want %+v", stub.factQuery, wantFacts)
	}

	callAs(t, router, http.MethodGet, "/api/v1/memory/audit?action=fact.edited&limit=3", "")
	wantAudit := memory.AuditQuery{OwnerID: 7, Action: memory.ActionFactEdited, Limit: 3}
	if stub.auditQuery != wantAudit {
		t.Errorf("the trail asked for %+v, want %+v", stub.auditQuery, wantAudit)
	}
}

func TestABadMemoryQueryIsRefusedBeforeTheDomain(t *testing.T) {
	stub := &memoryStub{}
	router := newMemoryRouter(stub)

	for _, test := range []struct {
		name string
		path string
	}{
		// A timestamp the handler cannot read is the handler's to refuse: it is
		// the one filter here the domain never sees as a string.
		{"a date that is a word", "/api/v1/memory/traces?since=yesterday"},
		{"a date in the wrong format", "/api/v1/memory/facts?before=01/10/2026"},
		{"a limit that is not a number", "/api/v1/memory/traces?limit=many"},
		{"a limit below zero", "/api/v1/memory/audit?limit=-1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			stub.calls = 0
			recorder := callAs(t, router, http.MethodGet, test.path, "")

			if recorder.Code != http.StatusBadRequest {
				t.Errorf("%s = %d, want 400", test.path, recorder.Code)
			}
			if stub.calls != 0 {
				t.Errorf("%s reached the domain with a query it cannot carry", test.path)
			}
		})
	}
}

// TestAMemoryEditChangesOnlyWhatItSent pins the PATCH: a body that sends one
// field is not sending the other, and nil is what says so.
func TestAMemoryEditChangesOnlyWhatItSent(t *testing.T) {
	stub := &memoryStub{edited: memory.FactWithEvidence{Fact: aStoredFact()}}
	router := newMemoryRouter(stub)

	recorder := callAs(t, router, http.MethodPatch, "/api/v1/memory/facts/mf_1", `{"scope":"  career  "}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if edit := stub.editRequest; edit.Statement != nil {
		t.Errorf("a body that sent no statement sent %q", *edit.Statement)
	}
	if edit := stub.editRequest; edit.Scope == nil || *edit.Scope != "career" {
		// A scope is a name the synthesis groups by, so it is read by its trimmed
		// spelling: "  career  " and "career" are one group, not two.
		t.Errorf("the edit carried the scope %v, want it trimmed", edit.Scope)
	}
	if stub.editRequest.FactID != "mf_1" || stub.editRequest.OwnerID != 7 ||
		stub.editRequest.Actor != memory.ActorUser {
		t.Errorf("the edit named %+v, want the path's fact, the token's account and the user", stub.editRequest)
	}

	// A statement, on the other hand, is prose a person wrote about themselves and
	// is shown back to them: it travels exactly as it was typed.
	callAs(t, router, http.MethodPatch, "/api/v1/memory/facts/mf_1", `{"statement":"  我在学 Go  "}`)
	if statement := stub.editRequest.Statement; statement == nil || *statement != "  我在学 Go  " {
		t.Errorf("the edit carried %v, want the statement as it was typed", statement)
	}
	if stub.editRequest.Scope != nil {
		t.Errorf("a body that sent no scope sent %q", *stub.editRequest.Scope)
	}

	// A body that changes nothing is the domain's refusal rather than a default
	// invented here, so the handler passes it on and reports what came back.
	stub.err = memory.ErrInvalidRequest
	recorder = callAs(t, router, http.MethodPatch, "/api/v1/memory/facts/mf_1", `{}`)
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("an edit that changed nothing = %d, want the domain's refusal", recorder.Code)
	}
}

func TestStatingAFactIsTheUsersOwnRoute(t *testing.T) {
	stub := &memoryStub{stated: memory.FactWithEvidence{Fact: aStoredFact()}}
	router := newMemoryRouter(stub)

	recorder := callAs(t, router, http.MethodPost, "/api/v1/memory/facts",
		`{"statement":"我在准备考试","scope":" career "}`)
	// 200 and not 201: stating something the account already believes does not
	// create anything — the observation joins the fact in force.
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	state := stub.stateRequest
	if state.Statement != "我在准备考试" || state.Scope != "career" {
		t.Errorf("the statement was written as %+v, want it as it was typed with the scope trimmed", state)
	}
	if state.OwnerID != 7 || state.Actor != memory.ActorUser {
		t.Errorf("the statement was written as %+v, want the token's account and the user", state)
	}

	var envelope struct {
		Data struct {
			Fact factView `json:"fact"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if envelope.Data.Fact.FactID != "mf_1" {
		t.Errorf("the answer carried %+v, want the fact the domain handed back", envelope.Data.Fact)
	}
}

func TestTheLifecycleRoutesNameTheFactAndTheActor(t *testing.T) {
	for _, direction := range []string{"retract", "restore"} {
		t.Run(direction, func(t *testing.T) {
			stub := &memoryStub{moved: aStoredFact()}
			recorder := callAs(t, newMemoryRouter(stub), http.MethodPost,
				"/api/v1/memory/facts/mf_1/"+direction, "")

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
			}
			if len(stub.factRequests) != 1 {
				t.Fatalf("the domain was asked %d times, want once", len(stub.factRequests))
			}
			asked := stub.factRequests[0]
			// The id is the path's and the account is the token's, which is what
			// makes a request unable to name somebody else's fact.
			if asked.FactID != "mf_1" || asked.OwnerID != 7 || asked.Actor != memory.ActorUser {
				t.Errorf("the domain was asked %+v, want the path's fact, the token's account and the user", asked)
			}
		})
	}

	// A fact the account does not have is a 404 and not a 403: another account's
	// id is indistinguishable from one that does not exist, so the answer cannot
	// be used to ask whether it does.
	stub := &memoryStub{err: memory.ErrNotFound}
	recorder := callAs(t, newMemoryRouter(stub), http.MethodPost, "/api/v1/memory/facts/mf_1/retract", "")
	if recorder.Code != http.StatusNotFound {
		t.Errorf("retracting a fact that is not there = %d, want 404", recorder.Code)
	}
}

func TestTheTwoMemoryBatchesRunAsAPreviewAndAsThemselves(t *testing.T) {
	stub := &memoryStub{}
	router := newMemoryRouter(stub)

	callAs(t, router, http.MethodPost, "/api/v1/memory/facts/dedupe", `{"dry_run":true}`)
	if merge := stub.dedupeRequest; !merge.DryRun || merge.OwnerID != 7 || merge.Actor != memory.ActorUser {
		t.Errorf("the preview asked for %+v, want a preview of the token's account", merge)
	}
	callAs(t, router, http.MethodPost, "/api/v1/memory/facts/dedupe", `{}`)
	if stub.dedupeRequest.DryRun {
		t.Error("a body that did not ask for a preview ran one")
	}

	callAs(t, router, http.MethodPost, "/api/v1/memory/cleanup",
		`{"before":"2026-05-01T00:00:00Z","surface":"mastery","status":["retracted","superseded"],"dry_run":true}`)
	want := memory.CleanupQuery{
		OwnerID: 7, Before: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		Surface: memory.SurfaceMastery,
		Statuses: []memory.Status{
			memory.StatusRetracted, memory.StatusSuperseded,
		},
		DryRun: true, Actor: memory.ActorUser,
	}
	if !reflect.DeepEqual(stub.cleanupQuery, want) {
		t.Errorf("the cleanup asked for %+v, want %+v", stub.cleanupQuery, want)
	}

	// A cutoff that is not a timestamp is the handler's to refuse, because the
	// domain takes a time rather than a string.
	stub.calls = 0
	recorder := callAs(t, router, http.MethodPost, "/api/v1/memory/cleanup", `{"before":"last year"}`)
	if recorder.Code != http.StatusBadRequest || stub.calls != 0 {
		t.Errorf("a cleanup with an unreadable cutoff = %d with %d calls, want 400 and none", recorder.Code, stub.calls)
	}
	// A cleanup with no cutoff at all reaches the domain as the zero time, which
	// is the domain's own refusal — the rule lives where the requirement does.
	stub.err = memory.ErrInvalidRequest
	callAs(t, router, http.MethodPost, "/api/v1/memory/cleanup", `{}`)
	if !stub.cleanupQuery.Before.IsZero() {
		t.Errorf("a cleanup with no cutoff carried %v, want nothing to have been invented", stub.cleanupQuery.Before)
	}
}

func TestMemoryFailuresBecomeStatuses(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"a fact that is not there", memory.ErrNotFound, http.StatusNotFound, "not_found"},
		{"a request that is not valid", memory.ErrInvalidRequest, http.StatusBadRequest, "invalid_request"},
		{"a fact that says what is already on record", memory.ErrDuplicate, http.StatusConflict, "conflict"},
		// A write that lost a race is not a failure: the caller reads the fact
		// again and decides, which is why it is a conflict and not a 500.
		{"a fact that moved underneath a write", memory.ErrStaleState, http.StatusConflict, "stale_state"},
		{"storage that will not answer", errors.New("the disk is on fire"), http.StatusInternalServerError, "get_fact_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			stub := &memoryStub{err: test.err}
			recorder := callAs(t, newMemoryRouter(stub), http.MethodGet, "/api/v1/memory/facts/mf_1", "")

			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.status, recorder.Body.String())
			}
			var failure aMemoryFailure
			decodeBody(t, recorder, &failure)
			if failure.Error.Code != test.code {
				t.Errorf("code = %q, want %q", failure.Error.Code, test.code)
			}
			// A failure nobody outside can act on is answered with a sentence and
			// not with the error's own text.
			if strings.Contains(recorder.Body.String(), "the disk is on fire") {
				t.Error("the internal error reached the client")
			}
		})
	}
}

func TestAMemoryHandlerWithNoLayerAnswersUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewMemoryHandler(nil, testHandlerLogger())
	router := gin.New()
	router.Use(middleware.RequestID())
	router.GET("/api/v1/memory/synthesis", handler.Synthesis)

	recorder := call(t, router, http.MethodGet, "/api/v1/memory/synthesis", "")
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	var failure aMemoryFailure
	decodeBody(t, recorder, &failure)
	if failure.Error.Code != "memory_unavailable" {
		t.Errorf("code = %q, want the layer's own", failure.Error.Code)
	}
}
