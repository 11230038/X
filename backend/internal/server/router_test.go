package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRouterSystemEndpoints(t *testing.T) {
	router := NewRouter(slog.Default(), "development")

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

func TestRouterErrorsAndRequestID(t *testing.T) {
	router := NewRouter(slog.Default(), "development")

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

func TestRouterRecoversFromPanic(t *testing.T) {
	router := NewRouter(slog.Default(), "development")
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
