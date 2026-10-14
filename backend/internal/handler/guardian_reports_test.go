package handler

// The cases for GET /api/v1/guardian/learners/{userID}/reports (AC-11).
//
// This is the one route in the product that answers with another account's
// material, so what these pin is that the account in the path is the one whose
// material is read, that the read is recorded before it is served, and that a
// report which could not be recorded is not served at all. Whether the caller
// may ask is decided by the route in front of this handler, and the middleware's
// own tests plus the router's are where that is asked.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"backend/internal/audit"
	"backend/internal/authz"
	"backend/internal/dashboard"
	"backend/internal/middleware"
	"github.com/gin-gonic/gin"
)

// reportsDashboardStub reads for whoever it is asked about, and records who it
// was asked about — which is the whole question of the first case below.
type reportsDashboardStub struct {
	view  dashboard.View
	err   error
	asked []authz.Caller
}

func (s *reportsDashboardStub) Read(
	_ context.Context, actor authz.Caller,
) (dashboard.View, error) {
	s.asked = append(s.asked, actor)
	if s.err != nil {
		return dashboard.View{}, s.err
	}
	return s.view, nil
}

// reportsAuditStub keeps the entries the route writes.
type reportsAuditStub struct {
	entries []audit.Event
	err     error
	calls   int
}

func (s *reportsAuditStub) Record(_ context.Context, event audit.Event) error {
	s.calls++
	if s.err != nil {
		return s.err
	}
	s.entries = append(s.entries, event)
	return nil
}

// reportsAuthorizerStub is the policy as this route reaches it.
type reportsAuthorizerStub struct {
	decision authz.Decision
}

func (s reportsAuthorizerStub) Decide(
	_ context.Context, _ authz.Caller, _ authz.Action, _ int64,
) (authz.Decision, error) {
	return s.decision, nil
}

// newReportsRouter mounts the route the way the server does — the guard, then
// the policy with the permission, then the handler — because one of these cases
// is about what the audit entry says the decision was, and the decision is
// published by the middleware rather than by the handler.
func newReportsRouter(
	t *testing.T, view Dashboard, writer AuditWriter, decision authz.Decision,
) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	logger := testHandlerLogger()
	handler := NewReportsHandler(view, writer, logger)
	requirement := middleware.Requirement{
		Surface:    authz.SurfaceGuardian,
		Permission: authz.PermViewReports,
		Subject:    func(c *gin.Context) int64 { return learnerInPath(c) },
	}

	router := gin.New()
	router.Use(middleware.RequestID())
	router.GET("/api/v1/guardian/learners/:userID/reports",
		guard(), middleware.AuthorizeWith(reportsAuthorizerStub{decision: decision},
			requirement, nil, logger), handler.Get)
	return router
}

// learnerInPath is the server's own reader of the path segment, written out here
// because it is unexported in another package: it is the subject the policy is
// asked about, and a test that asked about somebody else would be testing a
// different route.
func learnerInPath(c *gin.Context) int64 {
	value := c.Param("userID")
	if value == "42" {
		return 42
	}
	if value == "7" {
		return 7
	}
	return 0
}

// theLearnersReport is what the dashboard answers for the learner: one session,
// which is enough to tell the learner's report from an empty one.
func theLearnersReport() dashboard.View {
	return dashboard.View{
		Sessions: []dashboard.Entry{{ID: "ses_1", Title: "algebra", Context: "session"}},
	}
}

func thePermissionThatAllowedIt() authz.Decision {
	return authz.Decision{
		Item:    authz.Item{Kind: authz.KindSurface, ID: authz.SurfaceGuardian},
		Allowed: true,
		Layer:   authz.LayerGuardian,
		Reason:  authz.ReasonPermissionHeld,
	}
}

func getReports(t *testing.T, router *gin.Engine, userID string) *httptest.ResponseRecorder {
	t.Helper()
	return callAs(t, router, http.MethodGet, "/api/v1/guardian/learners/"+userID+"/reports", "")
}

// The account in the path is the one whose material is read, and the caller's
// own is not. A version that passed the caller's own caller through would answer
// the guardian's dashboard with the learner's id in the URL — wrong in the one
// way a reader cannot tell.
func TestGuardianReportsReadsTheLearnerInThePath(t *testing.T) {
	stub := &reportsDashboardStub{view: theLearnersReport()}
	router := newReportsRouter(t, stub, &reportsAuditStub{}, thePermissionThatAllowedIt())

	recorder := getReports(t, router, "42")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if len(stub.asked) != 1 {
		t.Fatalf("the report was read %d times, want once", len(stub.asked))
	}
	if got := stub.asked[0].UserID; got != 42 {
		t.Errorf("read for account %d, want the learner in the path, 42", got)
	}
	if !strings.Contains(recorder.Body.String(), `"title":"algebra"`) {
		t.Errorf("the answer does not carry the learner's report: %s", recorder.Body.String())
	}
}

// The answer is the learner's own dashboard rather than a second shape built
// for guardians: one question, one answer, and every list present so that a
// client reads a report with nothing in it the same way (§2.12).
func TestGuardianReportsServesTheSameShapeAsTheDashboard(t *testing.T) {
	router := newReportsRouter(t, &reportsDashboardStub{}, &reportsAuditStub{}, thePermissionThatAllowedIt())

	recorder := getReports(t, router, "42")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	for _, want := range []string{`"sessions":[]`, `"paths":[]`, `"reviews":[]`, `"next":[]`} {
		if !strings.Contains(recorder.Body.String(), want) {
			t.Errorf("body = %s, want it to carry %s", recorder.Body.String(), want)
		}
	}
}

// The entry says who read whose report. It is the only record of one account
// looking at another's material without an administrator deciding each time,
// which is the question this table exists to be able to answer.
func TestGuardianReportsRecordsWhoReadWhoseReport(t *testing.T) {
	writer := &reportsAuditStub{}
	router := newReportsRouter(t, &reportsDashboardStub{view: theLearnersReport()}, writer,
		thePermissionThatAllowedIt())

	if recorder := getReports(t, router, "42"); recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if len(writer.entries) != 1 {
		t.Fatalf("the audit was written %d times, want once", len(writer.entries))
	}
	entry := writer.entries[0]

	if entry.Actor.ID != handlerAccountID {
		t.Errorf("Actor.ID = %d, want the caller, %d", entry.Actor.ID, handlerAccountID)
	}
	if entry.Actor.Name != "alice" {
		t.Errorf("Actor.Name = %q, want the name the request was served under", entry.Actor.Name)
	}
	if entry.Action != audit.ActionReportsViewed {
		t.Errorf("Action = %q, want %q", entry.Action, audit.ActionReportsViewed)
	}
	if entry.SubjectID != 42 {
		t.Errorf("SubjectID = %d, want the learner", entry.SubjectID)
	}
	if entry.Target.Kind != audit.TargetAccount || entry.Target.ID != "42" {
		t.Errorf("Target = %+v, want a pointer at the learner", entry.Target)
	}
	if entry.Result != audit.ResultOK {
		t.Errorf("Result = %q, want %q: this is the read that happened", entry.Result, audit.ResultOK)
	}
	if entry.RequestID == "" {
		t.Error("RequestID is empty: the entry cannot be joined to its log line")
	}
}

// The layer and the reason are the decision that allowed the request, read from
// what the middleware published rather than asked for a second time. An entry
// that re-derived the answer could record a policy that was never applied.
func TestGuardianReportsRecordsTheDecisionThatAllowedIt(t *testing.T) {
	writer := &reportsAuditStub{}
	router := newReportsRouter(t, &reportsDashboardStub{}, writer, thePermissionThatAllowedIt())

	if recorder := getReports(t, router, "42"); recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	detail := writer.entries[0].Detail
	if detail.Layer != string(authz.LayerGuardian) {
		t.Errorf("Layer = %q, want %q", detail.Layer, authz.LayerGuardian)
	}
	if detail.Reason != authz.ReasonPermissionHeld {
		t.Errorf("Reason = %q, want %q", detail.Reason, authz.ReasonPermissionHeld)
	}
}

// The two codes the entry carries are the two the middleware decided under, and
// this is the case that says so: a different decision in the stub is a different
// pair in the entry.
func TestGuardianReportsRecordsWhateverDecided(t *testing.T) {
	writer := &reportsAuditStub{}
	decision := thePermissionThatAllowedIt()
	decision.Layer = authz.LayerSelf
	decision.Reason = authz.ReasonOwnMaterial
	router := newReportsRouter(t, &reportsDashboardStub{}, writer, decision)

	if recorder := getReports(t, router, "42"); recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	detail := writer.entries[0].Detail
	if detail.Layer != string(authz.LayerSelf) || detail.Reason != authz.ReasonOwnMaterial {
		t.Errorf("Detail = %+v, want the decision the middleware published", detail)
	}
}

// No entry, no report. The read has happened by this point and nothing has been
// sent, which is the moment the choice is made: serving the report and logging
// the failure would be an instance where the audit's availability decides
// whether one account reads another's material unrecorded.
func TestGuardianReportsDoesNotServeAReadItCouldNotRecord(t *testing.T) {
	writer := &reportsAuditStub{err: errors.New("the audit is unreachable")}
	router := newReportsRouter(t, &reportsDashboardStub{view: theLearnersReport()}, writer,
		thePermissionThatAllowedIt())

	recorder := getReports(t, router, "42")
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "algebra") {
		t.Errorf("the report was served without being recorded: %s", recorder.Body.String())
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

// A route wired without an audit is a wiring mistake, and the one thing it must
// not do is serve the report anyway.
func TestGuardianReportsWithoutAnAuditServesNothing(t *testing.T) {
	router := newReportsRouter(t, &reportsDashboardStub{view: theLearnersReport()}, nil,
		thePermissionThatAllowedIt())

	recorder := getReports(t, router, "42")
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "algebra") {
		t.Errorf("the report was served with no audit to record it in: %s", recorder.Body.String())
	}
}

// A learner id that is not an account is refused before anything is read. The
// policy has already been asked about it and answered about account zero — the
// caller's own — so the handler is the last place this can stop.
func TestGuardianReportsRefusesALearnerIdThatIsNotAnAccount(t *testing.T) {
	for _, userID := range []string{"abc", "0", "-1", "4.2"} {
		t.Run(userID, func(t *testing.T) {
			view := &reportsDashboardStub{}
			writer := &reportsAuditStub{}
			router := newReportsRouter(t, view, writer, thePermissionThatAllowedIt())

			recorder := getReports(t, router, userID)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
			if len(view.asked) != 0 {
				t.Error("a report was read for a learner id that is not an account")
			}
			if writer.calls != 0 {
				t.Error("an audit entry was written for a request that was refused")
			}
		})
	}
}

// A failure to read says nothing about itself: what is behind it is a query and
// a table name, and neither belongs in a response.
func TestGuardianReportsSaysNothingAboutAFailure(t *testing.T) {
	router := newReportsRouter(t, &reportsDashboardStub{err: errors.New("sessions: no such table")},
		&reportsAuditStub{}, thePermissionThatAllowedIt())

	recorder := getReports(t, router, "42")
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "no such table") {
		t.Errorf("the message carries the failure: %s", recorder.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeBody(t, recorder, &body)
	if body.Error.Code != "read_reports_failed" {
		t.Errorf("code = %q, want read_reports_failed", body.Error.Code)
	}
}

// A read that failed is not a read to record: nothing was seen, and an entry
// saying otherwise would be about an access that never happened.
func TestGuardianReportsRecordsNothingWhenTheReadFailed(t *testing.T) {
	writer := &reportsAuditStub{}
	router := newReportsRouter(t, &reportsDashboardStub{err: errors.New("nope")}, writer,
		thePermissionThatAllowedIt())

	getReports(t, router, "42")

	if writer.calls != 0 {
		t.Errorf("an entry was written for a read that failed: %d", writer.calls)
	}
}

func TestGuardianReportsIsUnavailableWithoutAView(t *testing.T) {
	router := newReportsRouter(t, nil, &reportsAuditStub{}, thePermissionThatAllowedIt())

	recorder := getReports(t, router, "42")
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeBody(t, recorder, &body)
	if body.Error.Code != "reports_unavailable" {
		t.Errorf("code = %q, want reports_unavailable", body.Error.Code)
	}
}

func TestGuardianReportsNeedsAToken(t *testing.T) {
	view := &reportsDashboardStub{}
	router := newReportsRouter(t, view, &reportsAuditStub{}, thePermissionThatAllowedIt())

	recorder := call(t, router, http.MethodGet, "/api/v1/guardian/learners/42/reports", "")
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
	}
	if len(view.asked) != 0 {
		t.Error("a report was read for a request with no token")
	}
}
