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
	// handlerTicket is what the stub mints.
	handlerTicket = "signed.ws.ticket"
)

// handlerTicketExpiry is the moment the stub's ticket stops working — a fixed
// instant, so that a case can assert what the handler put in the body rather
// than that it put something there.
var handlerTicketExpiry = time.Unix(1_800_000_030, 0).UTC()

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

	// ticketCall is the account a ticket was minted for. It is the whole of what
	// the handler has to get right: the account comes from the token, so a route
	// that minted for anybody else would show up here as the wrong number.
	ticketCall  int64
	ticketCalls int
	ticket      string
	ticketErr   error
}

func (s *authStub) IssueWebSocketTicket(_ context.Context, userID int64) (string, time.Time, error) {
	s.ticketCalls++
	s.ticketCall = userID
	if s.ticketErr != nil {
		return "", time.Time{}, s.ticketErr
	}
	ticket := s.ticket
	if ticket == "" {
		ticket = handlerTicket
	}
	return ticket, handlerTicketExpiry, nil
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

// postTicket asks for a websocket ticket as the stub verifier's account.
func postTicket(t *testing.T, router *gin.Engine, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/ws-ticket", nil)
	if authenticated {
		request.Header.Set("Authorization", "Bearer "+handlerToken)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestAuthHandlerIssueWebSocketTicketMintsForTheCaller(t *testing.T) {
	stub := &authStub{}
	recorder := postTicket(t, newAuthHandlerRouter(stub), true)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			Ticket    string `json:"ticket"`
			ExpiresAt string `json:"expires_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Ticket != handlerTicket {
		t.Errorf("ticket = %q, want %q", response.Data.Ticket, handlerTicket)
	}
	if response.Data.ExpiresAt != handlerTicketExpiry.Format(time.RFC3339) {
		t.Errorf("expires_at = %q, want %q", response.Data.ExpiresAt, handlerTicketExpiry.Format(time.RFC3339))
	}
	// The account is the token's. There is no field in the request that could
	// have named a different one, which is the property being pinned: this is not
	// a route for minting somebody else's ticket.
	if stub.ticketCalls != 1 || stub.ticketCall != handlerAccountID {
		t.Errorf("minted %d ticket(s) for account %d, want 1 for %d",
			stub.ticketCalls, stub.ticketCall, handlerAccountID)
	}
}

// TestAuthHandlerIssueWebSocketTicketCarriesNoAccount is the body's shape as an
// assertion. A ticket that described its owner would be a second place that
// description could go stale, and the caller already has one.
func TestAuthHandlerIssueWebSocketTicketCarriesNoAccount(t *testing.T) {
	recorder := postTicket(t, newAuthHandlerRouter(&authStub{}), true)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	for _, field := range []string{`"username"`, `"role"`, `"user_id"`, `"user"`} {
		if strings.Contains(recorder.Body.String(), field) {
			t.Errorf("body carries %s: %s", field, recorder.Body.String())
		}
	}
}

func TestAuthHandlerIssueWebSocketTicketWithoutAuthenticationFails(t *testing.T) {
	stub := &authStub{}
	recorder := postTicket(t, newAuthHandlerRouter(stub), false)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"code":"authorization_required"`) {
		t.Errorf("body = %s", recorder.Body.String())
	}
	// Nothing was minted. The route's whole value is that the credential comes
	// from an authenticated request, so a tokenless one must not reach the domain.
	if stub.ticketCalls != 0 {
		t.Errorf("minted %d ticket(s) without a token", stub.ticketCalls)
	}
}

func TestAuthHandlerIssueWebSocketTicketFailureIsGeneric(t *testing.T) {
	stub := &authStub{ticketErr: errors.New(handlerInternal)}
	recorder := postTicket(t, newAuthHandlerRouter(stub), true)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"code":"ticket_issue_failed"`) {
		t.Errorf("body = %s", recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), handlerInternal) {
		t.Fatal("response leaked the internal error")
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
	router.POST("/api/v1/ws-ticket", middleware.RequireAuth(handlerVerifierStub{}, logger), handler.IssueWebSocketTicket)
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
