package handler

// The cases for the four account-lifecycle routes: create, delete, reset a
// password, enable/disable.
//
// The service behind them is a double, because the rules it applies — the
// username and password policies, whether a role or preset exists, what bcrypt
// writes — are internal/auth's, where they are checked over a real database.
// What this file pins is the boundary: the status and code each refusal arrives
// as, that the target the caller named is not one the allowlist protects, that
// a refused change never reached the service, and that a change which did
// happen left the entry that makes it findable.

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"backend/internal/audit"
	"backend/internal/auth"
	"backend/internal/authz"
	"backend/internal/middleware"
	"github.com/gin-gonic/gin"
)

// accountLifecycleStub records what it was asked to do and answers what the
// case set. It decides nothing: a double that also decided the rules would let
// this file agree with itself about rules it does not own.
type accountLifecycleStub struct {
	createInput  auth.CreateAccountInput
	createCalls  int
	createReturn auth.User
	createErr    error

	deleteCalls int
	deletedUser int64
	deleteErr   error

	resetCalls    int
	resetUser     int64
	resetPassword string
	resetErr      error

	setDisabledCalls int
	disabledUser     int64
	disabledValue    bool
	setDisabledErr   error
}

func (s *accountLifecycleStub) CreateAccount(
	_ context.Context, input auth.CreateAccountInput,
) (auth.User, error) {
	s.createCalls++
	s.createInput = input
	return s.createReturn, s.createErr
}

func (s *accountLifecycleStub) DeleteAccount(_ context.Context, userID int64) error {
	s.deleteCalls++
	s.deletedUser = userID
	return s.deleteErr
}

func (s *accountLifecycleStub) ResetPassword(_ context.Context, userID int64, password string) error {
	s.resetCalls++
	s.resetUser, s.resetPassword = userID, password
	return s.resetErr
}

func (s *accountLifecycleStub) SetDisabled(_ context.Context, userID int64, disabled bool) error {
	s.setDisabledCalls++
	s.disabledUser, s.disabledValue = userID, disabled
	return s.setDisabledErr
}

// accountActorVerifier is the token the lifecycle cases carry: an administrator
// who is also on the allowlist. The gate in front of these routes is
// middleware's to test; here the token stands for a caller the routes are for.
type accountActorVerifier struct{}

func (accountActorVerifier) Authenticate(_ context.Context, token string) (auth.User, error) {
	if token != handlerToken {
		return auth.User{}, auth.ErrInvalidToken
	}
	return auth.User{
		UserID: handlerAccountID, Username: "alice", Role: authz.RoleAdmin, SuperAdmin: true,
	}, nil
}

// newAccountLifecycleRouter mounts the four routes behind the real bearer middleware.
// The super-administrator gate is not mounted: that is middleware's own case,
// and mounting it here would make every refusal below a refusal by the gate.
func newAccountLifecycleRouter(
	stub *accountLifecycleStub, writer AuditWriter, superAdmin func(int64) bool,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	logger := testHandlerLogger()
	handler := NewAdminHandler(nil, nil, nil, stub, writer, logger, superAdmin)
	guarded := middleware.RequireAuth(accountActorVerifier{}, logger)
	router := gin.New()
	router.Use(middleware.RequestID())
	router.POST("/api/v1/admin/users", guarded, handler.CreateUser)
	router.DELETE("/api/v1/admin/users/:userID", guarded, handler.DeleteUser)
	router.PUT("/api/v1/admin/users/:userID/password", guarded, handler.ResetPassword)
	router.PUT("/api/v1/admin/users/:userID/disabled", guarded, handler.SetDisabled)
	return router
}

const validCreateBody = `{"username":"newcomer","password":"correct-horse","role":"user","preset":"standard"}`

func TestAccountLifecycleCreatesAnAccountAndSaysWhichOne(t *testing.T) {
	stub := &accountLifecycleStub{
		createReturn: auth.User{UserID: 99, Username: "newcomer", Role: authz.RoleUser},
	}
	ledger := &adminAuditStub{}
	router := newAccountLifecycleRouter(stub, ledger, func(int64) bool { return false })

	recorder := callAs(t, router, http.MethodPost, "/api/v1/admin/users", validCreateBody)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.createCalls != 1 {
		t.Fatalf("CreateAccount calls = %d, want 1", stub.createCalls)
	}
	if stub.createInput.Username != "newcomer" || stub.createInput.Role != authz.RoleUser ||
		stub.createInput.Preset != authz.PresetStandard {
		t.Errorf("input = %+v, want newcomer/user/standard", stub.createInput)
	}
	// The password travels to the service and nowhere else: the response is a
	// projection, and a body that echoed the credential would be the one place
	// it is written down twice.
	if strings.Contains(recorder.Body.String(), "password") {
		t.Errorf("the response carries the password: %s", recorder.Body.String())
	}
	var body struct {
		Data struct {
			User struct {
				UserID   int64  `json:"user_id"`
				Username string `json:"username"`
				Role     string `json:"role"`
				Preset   string `json:"preset"`
				PresetOf string `json:"preset_of"`
			} `json:"user"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &body)
	if body.Data.User.UserID != 99 || body.Data.User.Username != "newcomer" {
		t.Errorf("user = %+v, want the account the service returned", body.Data.User)
	}
	if body.Data.User.PresetOf != authz.PresetStandard {
		t.Errorf("preset_of = %q, want standard", body.Data.User.PresetOf)
	}

	if len(ledger.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(ledger.entries))
	}
	entry := ledger.entries[0]
	if entry.Action != audit.ActionAccountProvisioned {
		t.Errorf("action = %q, want %q", entry.Action, audit.ActionAccountProvisioned)
	}
	// The subject is the account that was made, not the one that made it.
	if entry.SubjectID != 99 {
		t.Errorf("subject = %d, want 99", entry.SubjectID)
	}
	if entry.Actor.ID != handlerAccountID {
		t.Errorf("actor = %d, want %d", entry.Actor.ID, handlerAccountID)
	}
}

func TestAccountLifecycleCreateRefusalsCarryTheirOwnCodes(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"a username outside the policy", auth.ErrInvalidUsername, http.StatusBadRequest, "invalid_username"},
		{"a password outside the policy", auth.ErrInvalidPassword, http.StatusBadRequest, "invalid_password"},
		{"a username already in use", auth.ErrUsernameTaken, http.StatusConflict, "username_taken"},
		{"a role this build does not write", auth.ErrUnknownRole, http.StatusBadRequest, "invalid_request"},
		{"a preset this build does not write", authz.ErrUnknownPreset, http.StatusBadRequest, "invalid_request"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			stub := &accountLifecycleStub{createErr: testCase.err}
			ledger := &adminAuditStub{}
			recorder := callAs(t, newAccountLifecycleRouter(stub, ledger, nil),
				http.MethodPost, "/api/v1/admin/users", validCreateBody)
			if recorder.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s",
					recorder.Code, testCase.wantStatus, recorder.Body.String())
			}
			if code := decodeRefusal(t, recorder).Error.Code; code != testCase.wantCode {
				t.Errorf("code = %q, want %q", code, testCase.wantCode)
			}
			// A creation that did not happen has nothing to record, and recording
			// an attempt as if it were a change would make the audit lie.
			if len(ledger.entries) != 0 {
				t.Errorf("recorded %d entries for a refused creation", len(ledger.entries))
			}
		})
	}
}

func TestAccountLifecycleDeleteRemovesTheAccountNamedInThePath(t *testing.T) {
	stub := &accountLifecycleStub{}
	ledger := &adminAuditStub{}
	recorder := callAs(t, newAccountLifecycleRouter(stub, ledger, nil),
		http.MethodDelete, "/api/v1/admin/users/42", "")

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.deleteCalls != 1 || stub.deletedUser != 42 {
		t.Errorf("deleted user=%d in %d calls, want 42 in one", stub.deletedUser, stub.deleteCalls)
	}
	if len(ledger.entries) != 1 || ledger.entries[0].Action != audit.ActionAccountDeleted {
		t.Fatalf("entries = %+v, want one account.deleted", ledger.entries)
	}
	if ledger.entries[0].SubjectID != 42 {
		t.Errorf("subject = %d, want 42", ledger.entries[0].SubjectID)
	}
}

func TestAccountLifecycleRefusesToChangeAProtectedTarget(t *testing.T) {
	// Every change that could make an account unreachable is refused for the
	// allowlisted accounts and for the caller itself; the two are one rule here
	// because they are one answer. Self is handlerAccountID; 3 is allowlisted.
	cases := []struct{ name, method, suffix, body string }{
		{"deleting", http.MethodDelete, "", ""},
		{"resetting a password", http.MethodPut, "/password", `{"password":"a-much-longer-one"}`},
		{"disabling", http.MethodPut, "/disabled", `{"disabled":true}`},
	}
	for _, testCase := range cases {
		for _, target := range []struct {
			name string
			id   int64
		}{
			{"the caller itself", handlerAccountID},
			{"an allowlisted account", 3},
		} {
			t.Run(testCase.name+", "+target.name, func(t *testing.T) {
				stub := &accountLifecycleStub{}
				ledger := &adminAuditStub{}
				router := newAccountLifecycleRouter(stub, ledger, func(userID int64) bool {
					return userID == 3
				})
				path := "/api/v1/admin/users/" + strconv.FormatInt(target.id, 10) + testCase.suffix
				recorder := callAs(t, router, testCase.method, path, testCase.body)
				if recorder.Code != http.StatusForbidden {
					t.Fatalf("status = %d, want 403; body=%s", recorder.Code, recorder.Body.String())
				}
				if code := decodeRefusal(t, recorder).Error.Code; code != "protected_account" {
					t.Errorf("code = %q, want protected_account", code)
				}
				if stub.deleteCalls+stub.resetCalls+stub.setDisabledCalls != 0 {
					t.Error("a protected target reached the service")
				}
			})
		}
	}
}

func TestAccountLifecycleResetsAPassword(t *testing.T) {
	stub := &accountLifecycleStub{}
	ledger := &adminAuditStub{}
	recorder := callAs(t, newAccountLifecycleRouter(stub, ledger, nil),
		http.MethodPut, "/api/v1/admin/users/42/password", `{"password":"a-much-longer-one"}`)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.resetUser != 42 || stub.resetPassword != "a-much-longer-one" {
		t.Errorf("reset user=%d to %q, want 42 to the new password",
			stub.resetUser, stub.resetPassword)
	}
	if len(ledger.entries) != 1 || ledger.entries[0].Action != audit.ActionPasswordReset {
		t.Fatalf("entries = %+v, want one account.password_reset", ledger.entries)
	}
}

func TestAccountLifecycleResetRefusesThePasswordPolicy(t *testing.T) {
	stub := &accountLifecycleStub{resetErr: auth.ErrInvalidPassword}
	recorder := callAs(t, newAccountLifecycleRouter(stub, &adminAuditStub{}, nil),
		http.MethodPut, "/api/v1/admin/users/42/password", `{"password":"short"}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	if code := decodeRefusal(t, recorder).Error.Code; code != "invalid_password" {
		t.Errorf("code = %q, want invalid_password", code)
	}
}

func TestAccountLifecycleNamesTheTwoDirectionsOfTheSignInFlag(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantValue  bool
		wantAction audit.Action
	}{
		{"disabling", `{"disabled":true}`, true, audit.ActionAccountDisabled},
		{"enabling", `{"disabled":false}`, false, audit.ActionAccountEnabled},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			stub := &accountLifecycleStub{}
			ledger := &adminAuditStub{}
			recorder := callAs(t, newAccountLifecycleRouter(stub, ledger, nil),
				http.MethodPut, "/api/v1/admin/users/42/disabled", testCase.body)
			if recorder.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204; body=%s", recorder.Code, recorder.Body.String())
			}
			if stub.disabledUser != 42 || stub.disabledValue != testCase.wantValue {
				t.Errorf("wrote user=%d disabled=%t, want 42 and %t",
					stub.disabledUser, stub.disabledValue, testCase.wantValue)
			}
			if len(ledger.entries) != 1 || ledger.entries[0].Action != testCase.wantAction {
				t.Errorf("entries = %+v, want one %s", ledger.entries, testCase.wantAction)
			}
		})
	}
}

func TestAccountLifecycleDisableRefusesAnAbsentBoolean(t *testing.T) {
	stub := &accountLifecycleStub{}
	recorder := callAs(t, newAccountLifecycleRouter(stub, &adminAuditStub{}, nil),
		http.MethodPut, "/api/v1/admin/users/42/disabled", `{}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.setDisabledCalls != 0 {
		t.Error("an absent boolean reached the service; defaulting it would turn a malformed request into an enabling")
	}
}

func TestAccountLifecycleAnswersAMissingAccountWith404(t *testing.T) {
	cases := []struct{ method, path, body string }{
		{http.MethodDelete, "/api/v1/admin/users/42", ""},
		{http.MethodPut, "/api/v1/admin/users/42/password", `{"password":"a-much-longer-one"}`},
		{http.MethodPut, "/api/v1/admin/users/42/disabled", `{"disabled":true}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.method+" "+testCase.path, func(t *testing.T) {
			stub := &accountLifecycleStub{
				deleteErr: auth.ErrUserNotFound, resetErr: auth.ErrUserNotFound,
				setDisabledErr: auth.ErrUserNotFound,
			}
			recorder := callAs(t, newAccountLifecycleRouter(stub, &adminAuditStub{}, nil),
				testCase.method, testCase.path, testCase.body)
			if recorder.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404; body=%s", recorder.Code, recorder.Body.String())
			}
			if code := decodeRefusal(t, recorder).Error.Code; code != "not_found" {
				t.Errorf("code = %q, want not_found", code)
			}
		})
	}
}

func TestAccountLifecycleAnswersAnUnrecordedChangeWith500(t *testing.T) {
	stub := &accountLifecycleStub{}
	ledger := &adminAuditStub{err: errors.New("ledger unavailable")}
	recorder := callAs(t, newAccountLifecycleRouter(stub, ledger, nil),
		http.MethodDelete, "/api/v1/admin/users/42", "")
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	if code := decodeRefusal(t, recorder).Error.Code; code != "audit_unavailable" {
		t.Errorf("code = %q, want audit_unavailable", code)
	}
}

func TestAccountLifecycleWithoutAServiceAnswersAdminUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := testHandlerLogger()
	handler := NewAdminHandler(nil, nil, nil, nil, &adminAuditStub{}, logger, nil)
	guarded := middleware.RequireAuth(accountActorVerifier{}, logger)
	router := gin.New()
	router.Use(middleware.RequestID())
	router.POST("/api/v1/admin/users", guarded, handler.CreateUser)
	router.DELETE("/api/v1/admin/users/:userID", guarded, handler.DeleteUser)

	for _, request := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/admin/users", validCreateBody},
		{http.MethodDelete, "/api/v1/admin/users/42", ""},
	} {
		recorder := callAs(t, router, request.method, request.path, request.body)
		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("%s: status = %d, want 500; body=%s",
				request.path, recorder.Code, recorder.Body.String())
		}
		if code := decodeRefusal(t, recorder).Error.Code; code != "admin_unavailable" {
			t.Errorf("%s: code = %q, want admin_unavailable", request.path, code)
		}
	}
}

func TestAdminAccountListSaysWhichAccountsTheAllowlistNames(t *testing.T) {
	stub := &adminStub{accounts: []authz.Account{
		{UserID: 1, Username: "root", Role: authz.RoleAdmin, Preset: authz.PresetStandard},
		{UserID: 2, Username: "old", Role: authz.RoleUser},
	}}
	gin.SetMode(gin.TestMode)
	logger := testHandlerLogger()
	handler := NewAdminHandler(stub, stub, stub, nil, &adminAuditStub{}, logger,
		func(userID int64) bool { return userID == 2 })
	guarded := middleware.RequireAuth(handlerVerifierStub{}, logger)
	router := gin.New()
	router.Use(middleware.RequestID())
	router.GET("/api/v1/admin/users", guarded, handler.ListUsers)

	recorder := callAs(t, router, http.MethodGet, "/api/v1/admin/users", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Data struct {
			Users []struct {
				UserID     int64 `json:"user_id"`
				SuperAdmin bool  `json:"super_admin"`
			} `json:"users"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &body)
	if len(body.Data.Users) != 2 {
		t.Fatalf("users = %d, want 2", len(body.Data.Users))
	}
	if body.Data.Users[0].SuperAdmin || !body.Data.Users[1].SuperAdmin {
		t.Errorf("super_admin flags = %t, %t; want false, true",
			body.Data.Users[0].SuperAdmin, body.Data.Users[1].SuperAdmin)
	}
}
