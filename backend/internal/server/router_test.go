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
	"backend/internal/authz"
	"backend/internal/handler"
	"backend/internal/kb"
	"backend/internal/library"
	"backend/internal/middleware"
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
	return newTestRouterWith(t, checker, nil)
}

// newTestRouterWith is the same router with a reviewing failure to hand back.
// The bank and the review path are wired to stubs whose answers are recognisable
// one from another, so that a route that was registered against the wrong
// handler is a failing assertion rather than a plausible body.
func newTestRouterWith(t *testing.T, checker readyChecker, practiceErr error) *gin.Engine {
	t.Helper()
	return newTestRouterWithAuthorizer(t, checker, practiceErr, routerAuthorizerStub{})
}

// newTestRouterWithAuthorizer is the same router over a chosen authorizer, so
// that a case about a refusal can hand one in without every other case in this
// file having to describe a policy it is not about.
func newTestRouterWithAuthorizer(
	t *testing.T, checker readyChecker, practiceErr error, authorizer middleware.Authorizer,
) *gin.Engine {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
	system := handler.NewSystemHandler(checker, 50*time.Millisecond, logger)
	libraryFiles := handler.NewLibraryFilesHandler(routerUploaderStub{}, 1024, logger)
	authenticator := routerAuthStub{}
	return NewRouter(logger, "development", Handlers{
		System:        system,
		LibraryFiles:  libraryFiles,
		LibraryReads:  handler.NewLibraryReadHandler(routerFileStub{}, routerMaterialStub{}, logger),
		Auth:          handler.NewAuthHandler(authenticator, logger),
		Questions:     handler.NewQuestionHandler(routerQuestionsStub{}, logger),
		Practice:      handler.NewPracticeHandler(routerPracticeStub{err: practiceErr}, routerQueueSize, logger),
		Permissions:   handler.NewPermissionsHandler(routerPermissionsStub{}, logger),
		TokenVerifier: authenticator,
		Authorizer:    authorizer,
		// KnowledgeBase is deliberately left out. This file is about which
		// routes exist and which group they sit in; what a knowledge base
		// endpoint answers is handler/kb_test.go's business, and wiring a stub
		// here would say nothing the other file does not already say.
	})
}

type routerFileStub struct{}

// routerAuthorizerStub is the policy as most cases in this file need it: every
// request allowed. A case about a route being registered is not also a case
// about what the policy says about it, and the two questions have their own
// files — internal/authz for the second, this one for the first.
type routerAuthorizerStub struct {
	// deny makes every decision a refusal, which is what the case about the 403
	// boundary hands in.
	deny bool
}

func (s routerAuthorizerStub) Decide(
	context.Context, authz.Caller, authz.Action, int64,
) (authz.Decision, error) {
	if s.deny {
		return authz.Decision{Layer: authz.LayerPreset, Reason: authz.ReasonBaselineClosed}, nil
	}
	return authz.Decision{Allowed: true, Layer: authz.LayerSelf, Reason: authz.ReasonOwnMaterial}, nil
}

func (routerFileStub) Find(context.Context, string, authz.Caller) (library.UploadResult, error) {
	return library.UploadResult{ID: "lib_1", SHA256: strings.Repeat("a", 64), Filename: "手册.pdf"}, nil
}

type routerMaterialStub struct{}

func (routerMaterialStub) DocumentOfFile(context.Context, string, authz.Caller) (kb.Document, error) {
	return kb.Document{ID: "doc_1", KBID: "kb_1", FileID: "lib_1", TextChars: 12}, nil
}

func (routerMaterialStub) ReadWindow(context.Context, string, int64, int64, authz.Caller) (kb.Window, error) {
	return kb.Window{Document: kb.Document{ID: "doc_1", TextChars: 12}, Text: "保修期是两年。", CharEnd: 7, TotalChars: 12}, nil
}

func (routerMaterialStub) ChunkSpans(context.Context, string, authz.Caller) ([]kb.ChunkSpan, error) {
	return nil, nil
}

type routerUploaderStub struct{}

func (routerUploaderStub) Upload(context.Context, library.UploadInput) (library.UploadResult, error) {
	return library.UploadResult{}, nil
}

// routerPermissionsStub answers a picture of the caller's reach. This file is
// about the route being reachable at all, so what the picture *says* is
// handler/permissions_test.go's business; all that matters here is that the
// route is mounted and answers behind a token.
type routerPermissionsStub struct{}

func (routerPermissionsStub) Self(context.Context, authz.Caller) (authz.Self, error) {
	return authz.Self{
		Preset: authz.PresetStandard,
		Items: []authz.Decision{{
			Item: authz.Item{Kind: authz.KindSurface, ID: authz.SurfaceChat}, Allowed: true,
		}},
	}, nil
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

// TestRouterRegistersTheKnowledgeBaseAndReadRoutes pins the two things this
// file is the only place for: that each route exists at the path and method a
// client was told, and that it is behind the token.
//
// Reading a file is reading the material a turn was grounded on, and the
// knowledge bases beside it are the collections that material lives in, so
// neither can be public the way an upload still is.
func TestRouterRegistersTheKnowledgeBaseAndReadRoutes(t *testing.T) {
	router := newTestRouter(t, readyChecker{})

	routes := []struct{ name, method, path string }{
		{"file", http.MethodGet, "/api/v1/library/files/lib_1"},
		{"file text", http.MethodGet, "/api/v1/library/files/lib_1/text"},
		{"create base", http.MethodPost, "/api/v1/knowledge-bases"},
		{"list bases", http.MethodGet, "/api/v1/knowledge-bases"},
		{"get base", http.MethodGet, "/api/v1/knowledge-bases/kb_1"},
		{"delete base", http.MethodDelete, "/api/v1/knowledge-bases/kb_1"},
		{"add document", http.MethodPost, "/api/v1/knowledge-bases/kb_1/documents"},
		{"remove document", http.MethodDelete, "/api/v1/knowledge-bases/kb_1/documents/doc_1"},
		{"rebuild", http.MethodPost, "/api/v1/knowledge-bases/kb_1/rebuild"},
		{"search", http.MethodPost, "/api/v1/knowledge-bases/kb_1/search"},
		{"set default", http.MethodPost, "/api/v1/knowledge-bases/kb_1/default"},
	}

	for _, route := range routes {
		t.Run(route.name+" without a token", func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(route.method, route.path, nil))
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), `"code":"authorization_required"`) {
				t.Fatalf("body = %s", recorder.Body.String())
			}
		})

		t.Run(route.name+" with a token", func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(route.method, route.path, nil)
			request.Header.Set("Authorization", "Bearer "+routerAcceptedToken)
			router.ServeHTTP(recorder, request)
			// Anything but these two means the route is there and a handler
			// answered it: a wrong path is 404 and a wrong method is 405.
			if recorder.Code == http.StatusNotFound || recorder.Code == http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestRouterReadsAFileForWhoeverHoldsTheToken(t *testing.T) {
	router := newTestRouter(t, readyChecker{})

	for _, test := range []struct{ path, want string }{
		{"/api/v1/library/files/lib_1", `"filename":"手册.pdf"`},
		{"/api/v1/library/files/lib_1/text", `"text":"保修期是两年。"`},
	} {
		t.Run(test.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request.Header.Set("Authorization", "Bearer "+routerAcceptedToken)
			router.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), test.want) {
				t.Fatalf("body = %s, want %s", recorder.Body.String(), test.want)
			}
		})
	}
}

// TestRouterRefusesAnAccountThePolicyDenies pins the 403/401 boundary on a real
// mounted route (§2.16).
//
// The two are deliberately not collapsed, and the only place that can be shown
// is here: the middleware answering them is written per route, so a route that
// mounted the authorizer without the token guard would answer 403 to a stranger
// — telling somebody who is not logged in what they may not do — and a route that
// mounted neither would serve them.
func TestRouterRefusesAnAccountThePolicyDenies(t *testing.T) {
	router := newTestRouterWithAuthorizer(t, readyChecker{}, nil, routerAuthorizerStub{deny: true})
	const path = "/api/v1/library/files/lib_1"

	t.Run("with a token it is forbidden and says why", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer "+routerAcceptedToken)
		router.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", recorder.Code, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), `"code":"permission_denied"`) {
			t.Fatalf("body = %s", recorder.Body.String())
		}
		// Which layer said no, and why, are in the body because they are what a
		// screen has to say instead of "no".
		for _, want := range []string{`"surface":"library"`, `"layer":"preset"`, `"reason":"baseline_closed"`} {
			if !strings.Contains(recorder.Body.String(), want) {
				t.Errorf("body = %s, want it to carry %s", recorder.Body.String(), want)
			}
		}
	})

	t.Run("without a token it is unauthenticated instead", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))

		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), `"code":"authorization_required"`) {
			t.Fatalf("body = %s", recorder.Body.String())
		}
	})

	t.Run("registering is not behind the policy", func(t *testing.T) {
		// A policy that refuses everything must not lock a person out of the two
		// routes that make an account. They are mounted outside both middlewares,
		// and this is the case that would catch one of them moving inside.
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register",
			strings.NewReader(`{"username":"alice","password":"correct-horse"}`))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
		}
	})
}

// TestRouterRegistersThePermissionsRoute pins the one route an account can reach
// with nothing else.
//
// It is mounted without a surface requirement on purpose: an account restricted
// out of every area must still be able to ask what it may reach, otherwise the
// screen that explains the refusal is itself refused.
func TestRouterRegistersThePermissionsRoute(t *testing.T) {
	router := newTestRouterWithAuthorizer(t, readyChecker{}, nil, routerAuthorizerStub{deny: true})
	const path = "/api/v1/me/permissions"

	t.Run("without a token", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("a denial elsewhere does not close it", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer "+routerAcceptedToken)
		router.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), `"preset":"standard"`) {
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
