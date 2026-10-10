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
)

type authStub struct {
	result       auth.AuthResult
	err          error
	registerCall *auth.RegisterInput
	loginCall    *auth.LoginInput
}

func (s *authStub) Register(_ context.Context, input auth.RegisterInput) (auth.AuthResult, error) {
	s.registerCall = &input
	return s.result, s.err
}

func (s *authStub) Login(_ context.Context, input auth.LoginInput) (auth.AuthResult, error) {
	s.loginCall = &input
	return s.result, s.err
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
		User struct {
			UserID   int64  `json:"user_id"`
			Username string `json:"username"`
		} `json:"user"`
		Token     string `json:"token"`
		TokenType string `json:"token_type"`
		ExpiresAt string `json:"expires_at"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.User.UserID != 7 || response.User.Username != "alice" {
		t.Errorf("user = %+v", response.User)
	}
	if response.Token != handlerToken || response.TokenType != "Bearer" {
		t.Errorf("token fields = %q/%q", response.Token, response.TokenType)
	}
	if response.ExpiresAt == "" || response.RequestID == "" {
		t.Errorf("expires_at/request_id missing: %s", recorder.Body.String())
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
	router.GET("/api/v1/auth/me", middleware.RequireAuth(handlerVerifierStub{}, logger), handler.Me)
	return router
}

type handlerVerifierStub struct{}

func (handlerVerifierStub) Authenticate(_ context.Context, token string) (auth.User, error) {
	if token != handlerToken {
		return auth.User{}, auth.ErrInvalidToken
	}
	return auth.User{UserID: 7, Username: "alice", Role: "user"}, nil
}
