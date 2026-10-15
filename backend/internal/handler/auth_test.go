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

	"backend/internal/auth"
	"backend/internal/middleware"
	"github.com/gin-gonic/gin"
)

const (
	handlerPassword   = "correct-horse"
	handlerToken      = "header.payload.signature"
	handlerInternal   = "private database details"
	handlerUserJSON   = `"username":"alice"`
	handlerBodyAccept = "application/json"
	// handlerAccountID is the account the stub verifier's token stands for. Tests
	// that assert the account reached the domain compare against it, so that
	// changing it here is one edit rather than a hunt.
	handlerAccountID int64 = 7
)

type authStub struct {
	result       auth.AuthResult
	err          error
	registerCall *auth.RegisterInput
	loginCall    *auth.LoginInput
	revokeCalls  int
	revokedUser  int64

	// passwordCall is the change-password request as it arrived, and those four
	// values are the whole of what the handler has to get right: the account from
	// the token, the two passwords from the body, and neither of them swapped.
	passwordCall struct {
		userID int64
		old    string
		new    string
	}
	passwordCalls int
}

func (s *authStub) ChangePassword(_ context.Context, userID int64, oldPassword, newPassword string) error {
	s.passwordCalls++
	s.passwordCall.userID = userID
	s.passwordCall.old = oldPassword
	s.passwordCall.new = newPassword
	return s.err
}

func (s *authStub) Register(_ context.Context, input auth.RegisterInput) (auth.AuthResult, error) {
	s.registerCall = &input
	return s.result, s.err
}

func (s *authStub) Login(_ context.Context, input auth.LoginInput) (auth.AuthResult, error) {
	s.loginCall = &input
	return s.result, s.err
}

func (s *authStub) RevokeTokens(_ context.Context, userID int64) error {
	s.revokeCalls++
	s.revokedUser = userID
	return s.err
}

func sampleResult() auth.AuthResult {
	return auth.AuthResult{
		User:      auth.User{UserID: 7, Username: "alice", Role: "user"},
		Token:     handlerToken,
		ExpiresAt: time.Unix(1_800_000_000, 0).UTC(),
	}
}

func postCredentials(t *testing.T, router *gin.Engine, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", handlerBodyAccept)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestAuthHandlerRegisterReturnsSession(t *testing.T) {
	stub := &authStub{result: sampleResult()}
	router := newAuthHandlerRouter(stub)

	body := `{"username":"alice","password":"` + handlerPassword + `"}`
	recorder := postCredentials(t, router, "/api/v1/auth/register", body)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			User struct {
				UserID   int64  `json:"user_id"`
				Username string `json:"username"`
			} `json:"user"`
			Token     string `json:"token"`
			TokenType string `json:"token_type"`
			ExpiresAt string `json:"expires_at"`
		} `json:"data"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.User.UserID != 7 || response.Data.User.Username != "alice" {
		t.Errorf("user = %+v", response.Data.User)
	}
	if response.Data.Token != handlerToken || response.Data.TokenType != "Bearer" {
		t.Errorf("token fields = %q/%q", response.Data.Token, response.Data.TokenType)
	}
	if response.Data.ExpiresAt == "" || response.RequestID == "" {
		t.Errorf("expires_at/request_id missing: %s", recorder.Body.String())
	}
	if response.RequestID != recorder.Header().Get("X-Request-ID") {
		t.Errorf("body request_id = %q, want the response header", response.RequestID)
	}
	if stub.registerCall == nil || stub.registerCall.Username != "alice" || stub.registerCall.Password != handlerPassword {
		t.Errorf("service input = %+v", stub.registerCall)
	}
}

func TestAuthHandlerResponseNeverExposesCredentials(t *testing.T) {
	for _, path := range []string{"/api/v1/auth/register", "/api/v1/auth/login"} {
		stub := &authStub{result: sampleResult()}
		router := newAuthHandlerRouter(stub)
		recorder := postCredentials(t, router, path, `{"username":"alice","password":"`+handlerPassword+`"}`)

		body := recorder.Body.String()
		for _, forbidden := range []string{handlerPassword, "password", "$2a$", "bcrypt"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s response contains %q: %s", path, forbidden, body)
			}
		}
	}
}

func TestAuthHandlerLoginReturnsSession(t *testing.T) {
	stub := &authStub{result: sampleResult()}
	router := newAuthHandlerRouter(stub)

	recorder := postCredentials(t, router, "/api/v1/auth/login", `{"username":"alice","password":"`+handlerPassword+`"}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), handlerUserJSON) {
		t.Fatalf("body = %s, want the user", recorder.Body.String())
	}
	var response struct {
		Data struct {
			User  auth.User `json:"user"`
			Token string    `json:"token"`
		} `json:"data"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.User.Username != "alice" || response.Data.Token != handlerToken {
		t.Errorf("data = %+v", response.Data)
	}
	if response.RequestID == "" {
		t.Error("envelope is missing request_id")
	}
	if stub.loginCall == nil || stub.loginCall.Username != "alice" {
		t.Errorf("service input = %+v", stub.loginCall)
	}
}

func TestAuthHandlerRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{name: "malformed json", path: "/api/v1/auth/register", body: `{"username":`, wantStatus: 400, wantCode: "invalid_request"},
		{name: "unknown field", path: "/api/v1/auth/register", body: `{"username":"alice","password":"` + handlerPassword + `","role":"admin"}`, wantStatus: 400, wantCode: "invalid_request"},
		{name: "two documents", path: "/api/v1/auth/login", body: `{"username":"alice","password":"` + handlerPassword + `"}{"username":"bob"}`, wantStatus: 400, wantCode: "invalid_request"},
		{name: "empty body", path: "/api/v1/auth/login", body: ``, wantStatus: 400, wantCode: "invalid_request"},
		{name: "oversized body", path: "/api/v1/auth/login", body: `{"username":"alice","password":"` + strings.Repeat("a", 8192) + `"}`, wantStatus: 413, wantCode: "request_too_large"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &authStub{result: sampleResult()}
			recorder := postCredentials(t, newAuthHandlerRouter(stub), tt.path, tt.body)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), `"code":"`+tt.wantCode+`"`) {
				t.Fatalf("body = %s, want code %s", recorder.Body.String(), tt.wantCode)
			}
			if stub.registerCall != nil || stub.loginCall != nil {
				t.Fatal("invalid request reached the authentication service")
			}
		})
	}
}

func TestAuthHandlerMapsRegisterErrors(t *testing.T) {
	tests := []struct {
		err        error
		wantStatus int
		wantCode   string
	}{
		{err: auth.ErrInvalidUsername, wantStatus: 400, wantCode: "invalid_username"},
		{err: auth.ErrInvalidPassword, wantStatus: 400, wantCode: "invalid_password"},
		{err: auth.ErrUsernameTaken, wantStatus: 409, wantCode: "username_taken"},
		{err: errors.New(handlerInternal), wantStatus: 500, wantCode: "registration_failed"},
	}

	for _, tt := range tests {
		t.Run(tt.wantCode, func(t *testing.T) {
			router := newAuthHandlerRouter(&authStub{err: tt.err})
			recorder := postCredentials(t, router, "/api/v1/auth/register", `{"username":"alice","password":"`+handlerPassword+`"}`)

			if recorder.Code != tt.wantStatus || !strings.Contains(recorder.Body.String(), `"code":"`+tt.wantCode+`"`) {
				t.Fatalf("status/body = %d %s, want %d %s", recorder.Code, recorder.Body.String(), tt.wantStatus, tt.wantCode)
			}
			if strings.Contains(recorder.Body.String(), handlerInternal) {
				t.Fatal("response leaked the internal error")
			}
		})
	}
}

func TestAuthHandlerMapsLoginErrors(t *testing.T) {
	tests := []struct {
		err        error
		wantStatus int
		wantCode   string
	}{
		{err: auth.ErrInvalidCredentials, wantStatus: 401, wantCode: "invalid_credentials"},
		{err: auth.ErrAccountDisabled, wantStatus: 403, wantCode: "account_disabled"},
		// Locked is its own code rather than sharing account_disabled: the two are
		// both 403 and nothing else about them is the same. This one ends on its
		// own, so a client is being told to wait, not to ask an administrator.
		{err: auth.ErrAccountLocked, wantStatus: 403, wantCode: "account_locked"},
		{err: errors.New(handlerInternal), wantStatus: 500, wantCode: "login_failed"},
	}

	for _, tt := range tests {
		t.Run(tt.wantCode, func(t *testing.T) {
			router := newAuthHandlerRouter(&authStub{err: tt.err})
			recorder := postCredentials(t, router, "/api/v1/auth/login", `{"username":"alice","password":"`+handlerPassword+`"}`)

			if recorder.Code != tt.wantStatus || !strings.Contains(recorder.Body.String(), `"code":"`+tt.wantCode+`"`) {
				t.Fatalf("status/body = %d %s, want %d %s", recorder.Code, recorder.Body.String(), tt.wantStatus, tt.wantCode)
			}
			if strings.Contains(recorder.Body.String(), handlerInternal) {
				t.Fatal("response leaked the internal error")
			}
		})
	}
}

func TestAuthHandlerLogoutRevokesTheAccountInTheToken(t *testing.T) {
	stub := &authStub{}
	router := newAuthHandlerRouter(stub)

	recorder := logout(t, router, true)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", recorder.Code, recorder.Body.String())
	}
	// The body is the whole of what a client is told: the tokens it holds are
	// gone and nothing replaces them, so there is nothing to hand back.
	if recorder.Body.Len() != 0 {
		t.Errorf("body = %q, want it empty", recorder.Body.String())
	}
	// The id is the token's, not the request's: a body cannot name whose tokens
	// stop counting, because there is no body.
	if stub.revokeCalls != 1 || stub.revokedUser != handlerAccountID {
		t.Errorf("revoked %d account(s), last = %d, want one call for %d",
			stub.revokeCalls, stub.revokedUser, handlerAccountID)
	}
}

func TestAuthHandlerLogoutWithoutATokenRevokesNothing(t *testing.T) {
	stub := &authStub{}
	recorder := logout(t, newAuthHandlerRouter(stub), false)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.revokeCalls != 0 {
		t.Fatalf("an unauthenticated request reached the service %d time(s)", stub.revokeCalls)
	}
}

func TestAuthHandlerMapsLogoutFailure(t *testing.T) {
	stub := &authStub{err: errors.New(handlerInternal)}
	recorder := logout(t, newAuthHandlerRouter(stub), true)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"code":"logout_failed"`) {
		t.Fatalf("body = %s, want code logout_failed", recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), handlerInternal) {
		t.Fatal("response leaked the internal error")
	}
}

// logout posts to the route the way a client does: nothing but the token.
func logout(t *testing.T, router *gin.Engine, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	if authenticated {
		request.Header.Set("Authorization", "Bearer "+handlerToken)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestAuthHandlerChangesThePasswordOfTheAccountOnTheToken(t *testing.T) {
	stub := &authStub{}
	router := newAuthHandlerRouter(stub)

	recorder := changePassword(t, router, `{"old_password":"`+handlerPassword+`","new_password":"a-much-longer-one"}`, true)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", recorder.Code, recorder.Body.String())
	}
	// Nothing comes back, and that is the whole of §2.2: every token this account
	// held has just stopped counting, this request's included, so there is no
	// session to hand over. A body carrying a replacement token is exactly what
	// this version decided not to issue.
	if recorder.Body.Len() != 0 {
		t.Errorf("body = %q, want it empty", recorder.Body.String())
	}
	call := stub.passwordCall
	if stub.passwordCalls != 1 || call.userID != handlerAccountID {
		t.Errorf("reached the service %d time(s) for account %d, want once for %d",
			stub.passwordCalls, call.userID, handlerAccountID)
	}
	// The two passwords are in the fields they arrived in: a handler that passed
	// them the other way round would set the account's password to the string an
	// attacker sent and hand the real one back to them.
	if call.old != handlerPassword || call.new != "a-much-longer-one" {
		t.Errorf("passed old=%q new=%q, want the old one then the new one", call.old, call.new)
	}
	if strings.Contains(recorder.Body.String(), handlerPassword) {
		t.Error("the response echoed the password")
	}
}

func TestAuthHandlerChangePasswordWithoutATokenChangesNothing(t *testing.T) {
	stub := &authStub{}
	recorder := changePassword(t, newAuthHandlerRouter(stub),
		`{"old_password":"`+handlerPassword+`","new_password":"a-much-longer-one"}`, false)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.passwordCalls != 0 {
		t.Fatal("an unauthenticated request reached the service")
	}
}

func TestAuthHandlerRejectsAMalformedPasswordChange(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantCode string
	}{
		{name: "malformed json", body: `{"old_password":`, wantCode: "invalid_request"},
		// An unknown field is refused rather than ignored, exactly as it is on
		// registration and sign-in: a client that sent `password` instead of
		// `new_password` would otherwise have its request read as a change to the
		// empty password.
		{name: "unknown field", body: `{"old_password":"a","password":"b"}`, wantCode: "invalid_request"},
		{name: "two documents", body: `{"old_password":"a","new_password":"b"}{"old_password":"c"}`, wantCode: "invalid_request"},
		{name: "empty body", body: ``, wantCode: "invalid_request"},
		{name: "oversized body", body: `{"old_password":"` + strings.Repeat("a", 8192) + `"}`, wantCode: "request_too_large"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &authStub{}
			recorder := changePassword(t, newAuthHandlerRouter(stub), tt.body, true)

			if !strings.Contains(recorder.Body.String(), `"code":"`+tt.wantCode+`"`) {
				t.Fatalf("body = %s, want code %s", recorder.Body.String(), tt.wantCode)
			}
			if stub.passwordCalls != 0 {
				t.Fatal("a malformed request reached the service")
			}
		})
	}
}

func TestAuthHandlerMapsPasswordErrors(t *testing.T) {
	tests := []struct {
		err        error
		wantStatus int
		wantCode   string
	}{
		// The token is good — this is the account it names — so the failure is
		// not an authentication one. A 401 here would have a client drop a
		// credential that still works.
		{err: auth.ErrWrongPassword, wantStatus: 403, wantCode: "invalid_credentials"},
		// The same code registration answers with, because it is the same rule:
		// a new password is judged by the policy every password is judged by.
		{err: auth.ErrInvalidPassword, wantStatus: 400, wantCode: "invalid_password"},
		{err: errors.New(handlerInternal), wantStatus: 500, wantCode: "password_change_failed"},
	}

	for _, tt := range tests {
		t.Run(tt.wantCode, func(t *testing.T) {
			router := newAuthHandlerRouter(&authStub{err: tt.err})
			recorder := changePassword(t, router,
				`{"old_password":"`+handlerPassword+`","new_password":"a-much-longer-one"}`, true)

			if recorder.Code != tt.wantStatus || !strings.Contains(recorder.Body.String(), `"code":"`+tt.wantCode+`"`) {
				t.Fatalf("status/body = %d %s, want %d %s",
					recorder.Code, recorder.Body.String(), tt.wantStatus, tt.wantCode)
			}
			if strings.Contains(recorder.Body.String(), handlerInternal) {
				t.Fatal("response leaked the internal error")
			}
		})
	}
}

// changePassword puts to the route the way a client does: a body with the two
// passwords, and the token when the case is about an authenticated one.
func changePassword(t *testing.T, router *gin.Engine, body string, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/me/password", strings.NewReader(body))
	request.Header.Set("Content-Type", handlerBodyAccept)
	if authenticated {
		request.Header.Set("Authorization", "Bearer "+handlerToken)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestAuthHandlerMeReturnsTheAuthenticatedUser(t *testing.T) {
	router := newAuthHandlerRouter(&authStub{})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	request.Header.Set("Authorization", "Bearer "+handlerToken)
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), handlerUserJSON) {
		t.Fatalf("body = %s", recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), handlerToken) {
		t.Fatal("response echoed the access token")
	}
}

func TestAuthHandlerMeWithoutAuthenticationFails(t *testing.T) {
	router := newAuthHandlerRouter(&authStub{})
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil))

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}

// newAuthHandlerRouter mounts the auth handler behind the real bearer middleware,
// so the pair is exercised together.
func newAuthHandlerRouter(authenticator Authenticator) *gin.Engine {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
	handler := NewAuthHandler(authenticator, logger)
	router := gin.New()
	router.Use(middleware.RequestID())
	router.POST("/api/v1/auth/register", handler.Register)
	router.POST("/api/v1/auth/login", handler.Login)
	router.POST("/api/v1/auth/logout", middleware.RequireAuth(handlerVerifierStub{}, logger), handler.Logout)
	router.PUT("/api/v1/me/password", middleware.RequireAuth(handlerVerifierStub{}, logger), handler.ChangePassword)
	router.GET("/api/v1/auth/me", middleware.RequireAuth(handlerVerifierStub{}, logger), handler.Me)
	return router
}

type handlerVerifierStub struct{}

func (handlerVerifierStub) Authenticate(_ context.Context, token string) (auth.User, error) {
	if token != handlerToken {
		return auth.User{}, auth.ErrInvalidToken
	}
	return auth.User{UserID: handlerAccountID, Username: "alice", Role: "user"}, nil
}
