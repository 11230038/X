package handler

// The cases for /api/v1/admin/users and the grants under them. What these pin is
// the two halves of AUTH-03: that an administrator can see which preset every
// account carries and move one, and that a change which would leave an account
// reaching nothing is refused with a code a client can act on rather than
// written down.
//
// The handler owns none of the rules, so nothing here asserts a rule. What it
// asserts is that the rules' *answers* survive the wire: the right status, the
// right code, the caller that was passed down, and — for the two refusals that
// matter — that the write never reached the service at all.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"backend/internal/audit"
	"backend/internal/authz"
	"backend/internal/middleware"
	"github.com/gin-gonic/gin"
)

// adminStub is the authorization service as this layer reaches it for accounts.
type adminStub struct {
	accounts    []authz.Account
	accountsErr error

	// The two sign-in writes. They only record what they were asked to do: what
	// they are supposed to mean is the domain's, and a double that decided it too
	// would let this file agree with itself about a rule it does not own.
	revokedUser  int64
	revokeCalls  int
	unlockedUser int64
	unlockCalls  int
	sessionsErr  error

	savedUserID int64
	savedPreset string
	savedCaller authz.Caller
	saveCalls   int
	saveErr     error

	assignments    []authz.Assignment
	assignedItem   authz.Item
	assignedWay    *bool
	assignedUser   int64
	assignedCaller authz.Caller
	assignCalls    int
	assignErr      error

	withdrawnItem authz.Item
	withdrawnUser int64
	withdrawCalls int
	withdrawErr   error

	assignmentCalls int
	assignmentErr   error
}

func (s *adminStub) Accounts(context.Context) ([]authz.Account, error) {
	return s.accounts, s.accountsErr
}

func (s *adminStub) SetPreset(_ context.Context, caller authz.Caller, userID int64, preset string) error {
	s.saveCalls++
	s.savedCaller, s.savedUserID, s.savedPreset = caller, userID, preset
	return s.saveErr
}

func (s *adminStub) Assignments(_ context.Context, userID int64) ([]authz.Assignment, error) {
	s.assignmentCalls++
	s.assignedUser = userID
	return s.assignments, s.assignmentErr
}

func (s *adminStub) Assign(_ context.Context, caller authz.Caller, userID int64, item authz.Item, allowed bool) error {
	s.assignCalls++
	s.assignedCaller, s.assignedUser, s.assignedItem = caller, userID, item
	s.assignedWay = &allowed
	return s.assignErr
}

func (s *adminStub) Withdraw(_ context.Context, userID int64, item authz.Item) error {
	s.withdrawCalls++
	s.withdrawnUser, s.withdrawnItem = userID, item
	return s.withdrawErr
}

func (s *adminStub) RevokeTokens(_ context.Context, userID int64) error {
	s.revokeCalls++
	s.revokedUser = userID
	return s.sessionsErr
}

func (s *adminStub) Unlock(_ context.Context, userID int64) error {
	s.unlockCalls++
	s.unlockedUser = userID
	return s.sessionsErr
}

// adminAuditStub keeps the entries the two sign-in routes write. It is a fourth
// stub of this shape in the package because each file's cases are about one
// route's entries, and a shared one would let a wiring mistake pass.
type adminAuditStub struct {
	entries []audit.Event
	err     error
	calls   int
}

func (s *adminAuditStub) Record(_ context.Context, event audit.Event) error {
	s.calls++
	if s.err != nil {
		return s.err
	}
	s.entries = append(s.entries, event)
	return nil
}

// newAdminRouter mounts the governance routes behind the real bearer middleware,
// so the token that names the actor is the same one every case reads.
func newAdminRouter(stub *adminStub) *gin.Engine {
	return newAdminRouterWithAudit(stub, &adminAuditStub{})
}

// newAdminRouterWithAudit is the above with the ledger handed in, for the cases
// about what the two sign-in writes leave behind.
func newAdminRouterWithAudit(stub *adminStub, writer AuditWriter) *gin.Engine {
	gin.SetMode(gin.TestMode)
	logger := testHandlerLogger()
	// The lifecycle half is nil here: these cases are about the governance routes
	// that predate it, and a stub would be a fourth thing to keep in step without
	// asserting anything. A nil lifecycle is answered as admin_unavailable by the
	// routes that need it, which is handler/admin_accounts_test.go's subject.
	handler := NewAdminHandler(stub, stub, stub, nil, writer, logger, nil)
	guarded := middleware.RequireAuth(handlerVerifierStub{}, logger)
	router := gin.New()
	router.Use(middleware.RequestID())
	router.GET("/api/v1/admin/users", guarded, handler.ListUsers)
	router.PATCH("/api/v1/admin/users/:userID", guarded, handler.UpdateUser)
	router.DELETE("/api/v1/admin/users/:userID/sessions", guarded, handler.RevokeSessions)
	router.DELETE("/api/v1/admin/users/:userID/lock", guarded, handler.Unlock)
	router.GET("/api/v1/admin/users/:userID/grants", guarded, handler.ListGrants)
	router.POST("/api/v1/admin/users/:userID/grants", guarded, handler.SetGrant)
	router.DELETE("/api/v1/admin/users/:userID/grants", guarded, handler.WithdrawGrant)
	return router
}

// refusal is the error envelope, for the cases that read which code came back
// rather than which status.
type refusal struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func decodeRefusal(t *testing.T, recorder *httptest.ResponseRecorder) refusal {
	t.Helper()
	var envelope refusal
	decodeBody(t, recorder, &envelope)
	return envelope
}

func TestAdminListsEveryAccountWithBothItsPresets(t *testing.T) {
	stub := &adminStub{accounts: []authz.Account{
		{UserID: 1, Username: "root", Role: authz.RoleAdmin, Preset: authz.PresetStandard},
		// Written before the column existed, so the stored value is empty and
		// the meaning is the whole product. A client drawing the raw string would
		// show this account as having no preset at all.
		{UserID: 2, Username: "old", Role: authz.RoleUser, Preset: ""},
		{UserID: 3, Username: "kid", Role: authz.RoleUser, Preset: authz.PresetLearner, Disabled: true},
	}}
	recorder := callAs(t, newAdminRouter(stub), http.MethodGet, "/api/v1/admin/users", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Data struct {
			Users []struct {
				UserID   int64  `json:"user_id"`
				Username string `json:"username"`
				Role     string `json:"role"`
				Preset   string `json:"preset"`
				PresetOf string `json:"preset_of"`
				Disabled bool   `json:"disabled"`
			} `json:"users"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &body)
	if len(body.Data.Users) != 3 {
		t.Fatalf("users = %d, want 3", len(body.Data.Users))
	}
	stored := body.Data.Users[1]
	if stored.Preset != "" || stored.PresetOf != authz.PresetStandard {
		t.Errorf("the pre-V5 account reads as preset=%q preset_of=%q, want \"\" and standard",
			stored.Preset, stored.PresetOf)
	}
	if !body.Data.Users[2].Disabled {
		t.Error("the disabled account came back enabled")
	}
}

func TestAdminUpdatesAPresetAndSaysWhichAccountWasAsked(t *testing.T) {
	stub := &adminStub{}
	recorder := callAs(t, newAdminRouter(stub), http.MethodPatch,
		"/api/v1/admin/users/42", `{"preset":"learner"}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.savedUserID != 42 || stub.savedPreset != authz.PresetLearner {
		t.Errorf("wrote user=%d preset=%q, want 42 and learner", stub.savedUserID, stub.savedPreset)
	}
	// The account comes from the path and the caller from the token, and the
	// service needs both: one is who is being governed, the other is who the
	// grant would record as having governed.
	if stub.savedCaller.UserID != handlerAccountID {
		t.Errorf("caller = %d, want %d", stub.savedCaller.UserID, handlerAccountID)
	}
}

func TestAdminAnswersTheThreePresetRefusalsWithTheirOwnCodes(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"a change that would empty the policy", authz.ErrPolicyWouldBeEmpty,
			http.StatusBadRequest, "policy_would_be_empty"},
		{"a preset this build does not write", authz.ErrUnknownPreset,
			http.StatusBadRequest, "invalid_request"},
		{"an account that is not there", authz.ErrUnknownUser,
			http.StatusNotFound, "not_found"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			stub := &adminStub{saveErr: testCase.err}
			recorder := callAs(t, newAdminRouter(stub), http.MethodPatch,
				"/api/v1/admin/users/42", `{"preset":"learner"}`)
			if recorder.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s",
					recorder.Code, testCase.wantStatus, recorder.Body.String())
			}
			if code := decodeRefusal(t, recorder).Error.Code; code != testCase.wantCode {
				t.Errorf("code = %q, want %q", code, testCase.wantCode)
			}
		})
	}
}

func TestAdminListsTheGrantsRecordedAboutOneAccount(t *testing.T) {
	stub := &adminStub{assignments: []authz.Assignment{
		{Item: authz.Item{Kind: authz.KindSurface, ID: authz.SurfaceReading}, Allowed: true},
		{Item: authz.Item{Kind: authz.KindTool}, Allowed: false},
	}}
	recorder := callAs(t, newAdminRouter(stub), http.MethodGet, "/api/v1/admin/users/9/grants", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.assignedUser != 9 || stub.assignmentCalls != 1 {
		t.Fatalf("asked about %d in %d calls, want 9 in one", stub.assignedUser, stub.assignmentCalls)
	}
	var body struct {
		Data struct {
			UserID int64 `json:"user_id"`
			Grants []struct {
				Kind    string `json:"kind"`
				ID      string `json:"id"`
				Allowed bool   `json:"allowed"`
			} `json:"grants"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &body)
	if len(body.Data.Grants) != 2 {
		t.Fatalf("grants = %d, want 2", len(body.Data.Grants))
	}
	// The kind-wide grant keeps its empty id, which is a value and not a
	// missing field: dropping it would turn "no tool at all" into a grant about
	// an item nobody named.
	if body.Data.Grants[1].ID != "" || body.Data.Grants[1].Kind != string(authz.KindTool) {
		t.Errorf("the kind-wide grant came back as %+v", body.Data.Grants[1])
	}
}

func TestAdminOpensAndClosesOneItemWithTheSameWrite(t *testing.T) {
	for _, allowed := range []bool{true, false} {
		t.Run(map[bool]string{true: "open", false: "close"}[allowed], func(t *testing.T) {
			stub := &adminStub{}
			body := `{"kind":"surface","id":"reading","allowed":` + boolText(allowed) + `}`
			recorder := callAs(t, newAdminRouter(stub), http.MethodPost, "/api/v1/admin/users/9/grants", body)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
			}
			if stub.assignedItem != (authz.Item{Kind: authz.KindSurface, ID: authz.SurfaceReading}) {
				t.Errorf("item = %+v, want surface reading", stub.assignedItem)
			}
			if stub.assignedWay == nil || *stub.assignedWay != allowed {
				t.Errorf("allowed = %v, want %v", stub.assignedWay, allowed)
			}
		})
	}
}

func TestAdminRefusesAGrantWithNoDecisionInIt(t *testing.T) {
	// An absent boolean is not "false". If it defaulted, a malformed request
	// would silently become a revocation — which is a write nobody asked for,
	// and the one this route must not perform by accident.
	stub := &adminStub{}
	recorder := callAs(t, newAdminRouter(stub), http.MethodPost,
		"/api/v1/admin/users/9/grants", `{"kind":"surface","id":"reading"}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	if code := decodeRefusal(t, recorder).Error.Code; code != "invalid_request" {
		t.Errorf("code = %q, want invalid_request", code)
	}
	if stub.assignCalls != 0 {
		t.Error("the request reached the service without saying which way to decide")
	}
}

func TestAdminAnswersTheFourGrantRefusalsWithTheirOwnCodes(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode string
	}{
		{"a change that would empty the policy", authz.ErrPolicyWouldBeEmpty, "policy_would_be_empty"},
		{"something no learner may be given", authz.ErrNeverForLearners, "invalid_request"},
		{"the governance area itself", authz.ErrItemNotGrantable, "invalid_request"},
		{"a kind this build does not decide about", authz.ErrUnknownItem, "invalid_request"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			stub := &adminStub{assignErr: testCase.err}
			recorder := callAs(t, newAdminRouter(stub), http.MethodPost,
				"/api/v1/admin/users/9/grants", `{"kind":"surface","id":"reading","allowed":true}`)
			// Every one of these is a 400 and not a 403: the caller was allowed
			// to make the request — it is the administrator — and what is wrong
			// is the change. A 403 would say the administrator may not govern.
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
			if code := decodeRefusal(t, recorder).Error.Code; code != testCase.wantCode {
				t.Errorf("code = %q, want %q", code, testCase.wantCode)
			}
		})
	}
}

func TestAdminWithdrawsAnItemBackToTheBaseline(t *testing.T) {
	stub := &adminStub{}
	recorder := callAs(t, newAdminRouter(stub), http.MethodDelete,
		"/api/v1/admin/users/9/grants", `{"kind":"capability","id":"tools"}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.withdrawnUser != 9 {
		t.Errorf("withdrew about account %d, want 9", stub.withdrawnUser)
	}
	if stub.withdrawnItem != (authz.Item{Kind: authz.KindCapability, ID: "tools"}) {
		t.Errorf("withdrew %+v, want capability tools", stub.withdrawnItem)
	}
	// Removing a decision is not a decision about the caller, so this route
	// takes no caller — but it must not have reached Assign either.
	if stub.assignCalls != 0 {
		t.Error("the withdrawal went through the write path")
	}
}

func TestAdminRefusesAKindThisBuildDoesNotDecideAbout(t *testing.T) {
	stub := &adminStub{}
	recorder := callAs(t, newAdminRouter(stub), http.MethodPost,
		"/api/v1/admin/users/9/grants", `{"kind":"mood","id":"","allowed":true}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.assignCalls != 0 {
		t.Error("a kind this build has no vocabulary for reached the service")
	}
}

func TestAdminRefusesAnAccountIdThatIsNotAnAccount(t *testing.T) {
	for _, path := range []string{
		"/api/v1/admin/users/abc/grants",
		"/api/v1/admin/users/0/grants",
		"/api/v1/admin/users/-1/grants",
	} {
		stub := &adminStub{}
		recorder := callAs(t, newAdminRouter(stub), http.MethodGet, path, "")
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", path, recorder.Code)
		}
		if stub.assignmentCalls != 0 {
			t.Errorf("%s: an unreadable account id reached the service", path)
		}
	}
}

func TestAdminNeedsAToken(t *testing.T) {
	stub := &adminStub{}
	recorder := call(t, newAdminRouter(stub), http.MethodGet, "/api/v1/admin/users", "")
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.assignmentCalls != 0 || stub.saveCalls != 0 {
		t.Error("an account-less request reached the service")
	}
}

func TestAdminSaysNothingAboutAFailure(t *testing.T) {
	stub := &adminStub{accountsErr: errors.New("grant table is unreachable at 10.0.0.4")}
	recorder := callAs(t, newAdminRouter(stub), http.MethodGet, "/api/v1/admin/users", "")

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	if code := decodeRefusal(t, recorder).Error.Code; code != "list_accounts_failed" {
		t.Errorf("code = %q, want list_accounts_failed", code)
	}
	if strings.Contains(recorder.Body.String(), "10.0.0.4") {
		t.Error("the response exposed the internal failure")
	}
}

// boolText writes a boolean as the body a client would send.
func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
