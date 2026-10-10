package server

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
	"backend/internal/handler"
	"backend/internal/library"
	"github.com/gin-gonic/gin"
)

type readyChecker struct {
	err error
}

func (c readyChecker) Ping(context.Context) error {
	return c.err
}

func newTestRouter(t *testing.T, checker readyChecker) *gin.Engine {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
	system := handler.NewSystemHandler(checker, 50*time.Millisecond, logger)
	libraryFiles := handler.NewLibraryFilesHandler(routerUploaderStub{}, 1024, logger)
	authenticator := routerAuthStub{}
	return NewRouter(logger, "development", Handlers{
		System:        system,
		LibraryFiles:  libraryFiles,
		Auth:          handler.NewAuthHandler(authenticator, logger),
		TokenVerifier: authenticator,
	})
}

type routerUploaderStub struct{}

func (routerUploaderStub) Upload(context.Context, library.UploadInput) (library.UploadResult, error) {
	return library.UploadResult{}, nil
}

// routerAuthStub accepts exactly one token and rejects everything else.
type routerAuthStub struct{}

const routerAcceptedToken = "accepted-token"

func (routerAuthStub) Register(_ context.Context, input auth.RegisterInput) (auth.AuthResult, error) {
	return auth.AuthResult{
		User:  auth.User{UserID: 1, Username: input.Username, Role: "user"},
		Token: routerAcceptedToken,
	}, nil
}

func (routerAuthStub) Login(context.Context, auth.LoginInput) (auth.AuthResult, error) {
	return auth.AuthResult{
		User:  auth.User{UserID: 1, Username: "alice", Role: "user"},
		Token: routerAcceptedToken,
	}, nil
}

func (routerAuthStub) Authenticate(_ context.Context, token string) (auth.User, error) {
	if token != routerAcceptedToken {
		return auth.User{}, auth.ErrInvalidToken
	}
	return auth.User{UserID: 1, Username: "alice", Role: "user"}, nil
}

func TestRouterSystemEndpoints(t *testing.T) {
	router := newTestRouter(t, readyChecker{})

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantBody   string
	}{
		{name: "root", path: "/", wantStatus: http.StatusOK, wantBody: `{"message":"Gin server is running"}`},
		{name: "legacy health", path: "/health", wantStatus: http.StatusOK, wantBody: `{"status":"ok"}`},
		{name: "liveness", path: "/healthz", wantStatus: http.StatusOK, wantBody: `{"status":"ok"}`},
		{name: "readiness", path: "/readyz", wantStatus: http.StatusOK, wantBody: `{"status":"ready"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			router.ServeHTTP(recorder, request)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			if got := strings.TrimSpace(recorder.Body.String()); got != tt.wantBody {
				t.Errorf("body = %s, want %s", got, tt.wantBody)
			}
			if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
				t.Errorf("Content-Type = %q, want application/json", got)
			}
			if recorder.Header().Get("X-Request-ID") == "" {
				t.Error("X-Request-ID header is empty")
			}
		})
	}
}

func TestRouterReadinessFailureDoesNotAffectLiveness(t *testing.T) {
	router := newTestRouter(t, readyChecker{err: errors.New("private database details")})

	readyRecorder := httptest.NewRecorder()
	router.ServeHTTP(readyRecorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if readyRecorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want %d", readyRecorder.Code, http.StatusServiceUnavailable)
	}
	if strings.Contains(readyRecorder.Body.String(), "private database details") {
		t.Fatal("readiness response exposed database error")
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(readyRecorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode readiness response: %v", err)
	}
	if body.Error.Code != "not_ready" {
		t.Errorf("readiness error code = %q, want not_ready", body.Error.Code)
	}

	liveRecorder := httptest.NewRecorder()
	router.ServeHTTP(liveRecorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if liveRecorder.Code != http.StatusOK {
		t.Fatalf("liveness status = %d, want %d", liveRecorder.Code, http.StatusOK)
	}
}

func TestRouterErrorsAndRequestID(t *testing.T) {
	router := newTestRouter(t, readyChecker{})

	tests := []struct {
		name       string
		method     string
		path       string
		requestID  string
		wantStatus int
		wantCode   string
	}{
		{name: "not found", method: http.MethodGet, path: "/missing", wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "method not allowed", method: http.MethodPost, path: "/", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "provided request id", method: http.MethodGet, path: "/healthz", requestID: "client-request-42", wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.requestID != "" {
				request.Header.Set("X-Request-ID", tt.requestID)
			}
			router.ServeHTTP(recorder, request)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			responseID := recorder.Header().Get("X-Request-ID")
			if responseID == "" {
				t.Fatal("X-Request-ID response header is empty")
			}
			if tt.requestID != "" && responseID != tt.requestID {
				t.Errorf("X-Request-ID = %q, want %q", responseID, tt.requestID)
			}

			if tt.wantCode == "" {
				return
			}
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
				RequestID string `json:"request_id"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if body.Error.Code != tt.wantCode {
				t.Errorf("error code = %q, want %q", body.Error.Code, tt.wantCode)
			}
			if body.RequestID != responseID {
				t.Errorf("body request_id = %q, want %q", body.RequestID, responseID)
			}
		})
	}
}

func TestRouterRegistersLibraryUploadAndRejectsWrongMethod(t *testing.T) {
	router := newTestRouter(t, readyChecker{})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/library/files", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), `"code":"method_not_allowed"`) {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestRouterRegistersAuthRoutes(t *testing.T) {
	router := newTestRouter(t, readyChecker{})

	t.Run("register is public", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register",
			strings.NewReader(`{"username":"alice","password":"correct-horse"}`))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("login rejects the wrong method", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/auth/login", nil))
		if recorder.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", recorder.Code)
		}
	})

	t.Run("me without a token", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil))
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), `"code":"authorization_required"`) {
			t.Fatalf("body = %s", recorder.Body.String())
		}
	})

	t.Run("me with a valid token", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
		request.Header.Set("Authorization", "Bearer "+routerAcceptedToken)
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), `"username":"alice"`) {
			t.Fatalf("body = %s", recorder.Body.String())
		}
	})
}

func TestRouterRecoversFromPanic(t *testing.T) {
	router := newTestRouter(t, readyChecker{})
	router.GET("/panic", func(_ *gin.Context) {
		panic("secret panic details")
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/panic", nil)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	if strings.Contains(recorder.Body.String(), "secret panic details") {
		t.Fatal("panic details leaked in response")
	}
}
