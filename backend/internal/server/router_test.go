package server

// The router's own edges: the two routes that answer without an account, the
// envelope every answer comes back in, and what happens to a request that
// matches no route at all.
//
// The stubs and the builder are in router_stubs_test.go. Which routes exist
// behind the token is router_routes_test.go, and which group each one sits in —
// the thing a refusal's surface is the only witness to — is
// router_surfaces_test.go.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRouterSystemEndpoints(t *testing.T) {
	router := newTestRouter(t, readyChecker{})

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantData   string
	}{
		{name: "root", path: "/", wantStatus: http.StatusOK, wantData: `{"message":"Gin server is running"}`},
		{name: "legacy health", path: "/health", wantStatus: http.StatusOK, wantData: `{"status":"ok"}`},
		{name: "liveness", path: "/healthz", wantStatus: http.StatusOK, wantData: `{"status":"ok"}`},
		{name: "readiness", path: "/readyz", wantStatus: http.StatusOK, wantData: `{"status":"ready"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			router.ServeHTTP(recorder, request)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			var body struct {
				Data      json.RawMessage `json:"data"`
				RequestID string          `json:"request_id"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode success envelope %q: %v", recorder.Body.String(), err)
			}
			if got := strings.TrimSpace(string(body.Data)); got != tt.wantData {
				t.Errorf("data = %s, want %s", got, tt.wantData)
			}
			responseID := recorder.Header().Get("X-Request-ID")
			if body.RequestID != responseID {
				t.Errorf("body request_id = %q, want %q", body.RequestID, responseID)
			}
			if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
				t.Errorf("Content-Type = %q, want application/json", got)
			}
			if responseID == "" {
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
		// A trailing slash must fall through to the envelope, not to Gin's built-in
		// redirect, which answers with an HTML body and no request ID.
		{name: "trailing slash", method: http.MethodGet, path: "/health/", wantStatus: http.StatusNotFound, wantCode: "not_found"},
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

func TestRouterRegistersLibraryUploadAndListAndRejectsWrongMethod(t *testing.T) {
	router := newTestRouter(t, readyChecker{})

	// Both methods on the collection are mounted, and neither is the other's
	// answer: the upload is behind the rate limiter on top of the token, and the
	// listing is the token alone. A GET that reached the uploader would be a
	// listing request answered by a write.
	t.Run("the upload is mounted", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/library/files", nil))
		if recorder.Code == http.StatusMethodNotAllowed || recorder.Code == http.StatusNotFound {
			t.Fatalf("status = %d, want the upload route to exist", recorder.Code)
		}
	})

	t.Run("the listing is mounted", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/library/files", nil))
		if recorder.Code == http.StatusMethodNotAllowed || recorder.Code == http.StatusNotFound {
			t.Fatalf("status = %d, want the listing route to exist", recorder.Code)
		}
	})

	t.Run("a method that is neither", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/v1/library/files", nil))
		if recorder.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", recorder.Code)
		}
		if !strings.Contains(recorder.Body.String(), `"code":"method_not_allowed"`) {
			t.Fatalf("body = %s", recorder.Body.String())
		}
	})
}

// The two routes that make an account, and the one that reads it back.
//
// Register and login are mounted outside both the token guard and the policy,
// which is why they are here rather than in router_routes_test.go: a deployment
// whose policy refuses everything must still let a person in.
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
