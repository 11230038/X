package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"backend/internal/auth"
	"github.com/gin-gonic/gin"
)

const (
	middlewareToken    = "valid-token"
	middlewareInternal = "connection pool exhausted"
)

type verifierStub struct {
	user     auth.User
	err      error
	callWith string
	calls    int
}

func (s *verifierStub) Authenticate(_ context.Context, token string) (auth.User, error) {
	s.calls++
	s.callWith = token
	return s.user, s.err
}

// newAuthMiddlewareRouter mounts RequireAuth with a handler that echoes the user
// it finds in the context, so tests observe both the rejection and the pass-through.
func newAuthMiddlewareRouter(verifier TokenVerifier) *gin.Engine {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
	router := gin.New()
	router.Use(RequestID())
	router.GET("/protected", RequireAuth(verifier, logger), func(c *gin.Context) {
		user, ok := CurrentUser(c)
		if !ok {
			c.String(http.StatusInternalServerError, "no user in context")
			return
		}
		c.JSON(http.StatusOK, gin.H{"username": user.Username, "user_id": user.UserID})
	})
	return router
}

func getProtected(t *testing.T, router *gin.Engine, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func decodeErrorCode(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response %q: %v", recorder.Body.String(), err)
	}
	if body.RequestID == "" {
		t.Error("error response is missing request_id")
	}
	return body.Error.Code
}

func TestRequireAuthRejectsMalformedHeaders(t *testing.T) {
	headers := []string{
		"Basic " + middlewareToken,
		"Bearer",
		"Bearer ",
		"Bearer two tokens",
		middlewareToken,
		"",
	}

	for _, header := range headers {
		t.Run(header, func(t *testing.T) {
			verifier := &verifierStub{}
			recorder := getProtected(t, newAuthMiddlewareRouter(verifier), header)

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
			}
			if code := decodeErrorCode(t, recorder); code != "authorization_required" {
				t.Errorf("code = %q, want authorization_required", code)
			}
			if verifier.calls != 0 {
				t.Errorf("verifier called %d times for a malformed header", verifier.calls)
			}
		})
	}
}

func TestRequireAuthAcceptsCaseInsensitiveScheme(t *testing.T) {
	verifier := &verifierStub{user: auth.User{UserID: 3, Username: "alice", Role: "user"}}
	recorder := getProtected(t, newAuthMiddlewareRouter(verifier), "bEaReR "+middlewareToken)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if verifier.callWith != middlewareToken {
		t.Errorf("verifier received %q, want %q", verifier.callWith, middlewareToken)
	}
}

func TestRequireAuthPublishesTheUserAndRunsTheNextHandler(t *testing.T) {
	verifier := &verifierStub{user: auth.User{UserID: 3, Username: "alice", Role: "user"}}
	recorder := getProtected(t, newAuthMiddlewareRouter(verifier), "Bearer "+middlewareToken)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Username string `json:"username"`
		UserID   int64  `json:"user_id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Username != "alice" || body.UserID != 3 {
		t.Errorf("handler saw user %+v", body)
	}
}

func TestRequireAuthMapsVerifierErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "invalid token", err: auth.ErrInvalidToken, wantStatus: 401, wantCode: "invalid_token"},
		{name: "wrapped invalid token", err: errors.Join(auth.ErrInvalidToken, errors.New("signature mismatch")), wantStatus: 401, wantCode: "invalid_token"},
		{name: "disabled account", err: auth.ErrAccountDisabled, wantStatus: 403, wantCode: "account_disabled"},
		{name: "unexpected failure", err: errors.New(middlewareInternal), wantStatus: 500, wantCode: "authentication_failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verifier := &verifierStub{err: tt.err}
			recorder := getProtected(t, newAuthMiddlewareRouter(verifier), "Bearer "+middlewareToken)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if code := decodeErrorCode(t, recorder); code != tt.wantCode {
				t.Errorf("code = %q, want %q", code, tt.wantCode)
			}
			if strings.Contains(recorder.Body.String(), middlewareInternal) {
				t.Fatal("response leaked the internal error")
			}
			if strings.Contains(recorder.Body.String(), middlewareToken) {
				t.Fatal("response echoed the bearer token")
			}
		})
	}
}

func TestRequireAuthRejectsMissingVerifier(t *testing.T) {
	recorder := getProtected(t, newAuthMiddlewareRouter(nil), "Bearer "+middlewareToken)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	if code := decodeErrorCode(t, recorder); code != "authentication_failed" {
		t.Errorf("code = %q, want authentication_failed", code)
	}
}

func TestCurrentUserWithoutMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	if _, ok := CurrentUser(c); ok {
		t.Fatal("CurrentUser reported a user for an unauthenticated context")
	}
}

func TestBearerTokenParsing(t *testing.T) {
	tests := []struct {
		header    string
		wantToken string
		wantOK    bool
	}{
		{header: "Bearer abc", wantToken: "abc", wantOK: true},
		{header: "bearer abc", wantToken: "abc", wantOK: true},
		{header: "BEARER   abc  ", wantToken: "abc", wantOK: true},
		{header: "Basic abc", wantOK: false},
		{header: "Bearer", wantOK: false},
		{header: "Bearer ", wantOK: false},
		{header: "Bearer a b", wantOK: false},
		{header: "", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.header, func(t *testing.T) {
			token, ok := bearerToken(tt.header)
			if ok != tt.wantOK || token != tt.wantToken {
				t.Errorf("bearerToken(%q) = %q, %v; want %q, %v", tt.header, token, ok, tt.wantToken, tt.wantOK)
			}
		})
	}
}
