package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"backend/internal/auth"
	"backend/internal/handler"
	"backend/internal/kb"
	"backend/internal/library"
	"backend/internal/notebook"
	"backend/internal/practice"
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
		TokenVerifier: authenticator,
		// KnowledgeBase is deliberately left out. This file is about which
		// routes exist and which group they sit in; what a knowledge base
		// endpoint answers is handler/kb_test.go's business, and wiring a stub
		// here would say nothing the other file does not already say.
	})
}

// routerQueueSize is the queue length this file's router was built with, so that
// the practice route's body says which handler answered it.
const routerQueueSize = 13

type routerQuestionsStub struct{}

func (routerQuestionsStub) Get(_ context.Context, questionID string) (notebook.Entry, error) {
	return routerQuestionBankEntry(questionID), nil
}

func (routerQuestionsStub) List(context.Context, notebook.Filter) ([]notebook.Entry, error) {
	return []notebook.Entry{routerQuestionBankEntry("qn_1")}, nil
}

func (routerQuestionsStub) Mark(context.Context, string, notebook.Change) error { return nil }

func (routerQuestionsStub) SetCategories(context.Context, string, []string) error { return nil }

func (routerQuestionsStub) Categories(context.Context) ([]notebook.Category, error) {
	return []notebook.Category{{Name: "数学", Count: 3}}, nil
}

func routerQuestionBankEntry(questionID string) notebook.Entry {
	return notebook.Entry{
		Question: notebook.Question{QuestionID: questionID, Text: "什么是闭包", Type: notebook.QuestionChoice},
	}
}

// routerPracticeStub answers every reviewing call, with an optional failure so
// that a refusal can be followed from the handler to the status a client reads.
type routerPracticeStub struct {
	err error
}

func (s routerPracticeStub) Queue(context.Context, int) ([]practice.QueueItem, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []practice.QueueItem{{
		Question: notebook.Question{QuestionID: "qn_9", Text: "该复习的题", Type: notebook.QuestionFillBlank},
		Reason:   practice.ReasonNew,
	}}, nil
}

func (s routerPracticeStub) Review(context.Context, practice.Review) (practice.ReviewResult, error) {
	if s.err != nil {
		return practice.ReviewResult{}, s.err
	}
	return routerReviewResult(), nil
}

func (s routerPracticeStub) Recorded(context.Context, string) (practice.ReviewResult, error) {
	if s.err != nil {
		return practice.ReviewResult{}, s.err
	}
	return routerReviewResult(), nil
}

func (s routerPracticeStub) Stats(context.Context, practice.StatsQuery) (practice.Stats, error) {
	if s.err != nil {
		return practice.Stats{}, s.err
	}
	return practice.Stats{
		Bucket: practice.BucketDay,
		Series: []practice.BucketStat{{Label: "2026-10-05", Reviews: 1, Correct: 1}},
	}, nil
}

func routerReviewResult() practice.ReviewResult {
	correct := true
	return practice.ReviewResult{
		RequestID:  "req_1",
		QuestionID: "qn_9",
		Judgment:   &practice.Judgment{Kind: practice.KindObjective, IsCorrect: correct, Rubric: []string{}},
		State:      practice.ReviewState{Version: 1},
		Entry:      practice.AnswerState{IsCorrect: &correct, AttemptCount: 1, Result: "correct"},
	}
}

type routerFileStub struct{}

func (routerFileStub) Find(context.Context, string) (library.UploadResult, error) {
	return library.UploadResult{ID: "lib_1", SHA256: strings.Repeat("a", 64), Filename: "手册.pdf"}, nil
}

type routerMaterialStub struct{}

func (routerMaterialStub) DocumentOfFile(context.Context, string) (kb.Document, error) {
	return kb.Document{ID: "doc_1", KBID: "kb_1", FileID: "lib_1", TextChars: 12}, nil
}

func (routerMaterialStub) ReadWindow(context.Context, string, int64, int64) (kb.Window, error) {
	return kb.Window{Document: kb.Document{ID: "doc_1", TextChars: 12}, Text: "保修期是两年。", CharEnd: 7, TotalChars: 12}, nil
}

func (routerMaterialStub) ChunkSpans(context.Context, string) ([]kb.ChunkSpan, error) {
	return nil, nil
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

// TestRouterRegistersTheQuestionBankAndReviewRoutes is the two things this file
// is the only place for: that each route exists at the path and method a client
// was told, and that it sits behind the token.
//
// The question bank is what the user has been asked and what they have marked,
// and reviewing is the answer they are about to give; neither is anyone else's
// to read or write, so both go through the token like the rest of a person's own
// material.
//
// The marker is what makes this more than a routing table. Each route's stub
// answers with a shape only that endpoint's handler produces, so a route
// registered against the wrong handler is a failing assertion rather than a
// plausible body — a listing and a single question both answer with a question,
// and only the wrapper tells them apart.
func TestRouterRegistersTheQuestionBankAndReviewRoutes(t *testing.T) {
	router := newTestRouter(t, readyChecker{})

	routes := []struct {
		name, method, path, body, want string
	}{
		{
			name: "list questions", method: http.MethodGet, path: "/api/v1/questions",
			want: `"data":{"questions":[`,
		},
		{
			name: "one question", method: http.MethodGet, path: "/api/v1/questions/qn_1",
			want: `"data":{"question":{"question_id":"qn_1"`,
		},
		{
			name: "mark a question", method: http.MethodPatch, path: "/api/v1/questions/qn_1",
			body: `{"bookmarked":true}`,
			want: `"data":{"question":{"question_id":"qn_1"`,
		},
		{
			name: "list categories", method: http.MethodGet, path: "/api/v1/question-categories",
			want: `"data":{"categories":[`,
		},
		{
			name: "the practice queue", method: http.MethodGet, path: "/api/v1/practice/queue",
			want: `"data":{"queue":[`,
		},
		{
			name: "submit a review", method: http.MethodPost, path: "/api/v1/practice/reviews",
			body: `{"request_id":"req_1","question_id":"qn_9","user_answer":"答"}`,
			want: `"data":{"request_id":"req_1"`,
		},
		{
			name: "read a review", method: http.MethodGet, path: "/api/v1/practice/reviews/req_1",
			want: `"data":{"request_id":"req_1"`,
		},
		{
			name: "practice statistics", method: http.MethodGet, path: "/api/v1/practice/stats",
			want: `"data":{"from":`,
		},
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
			request := httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
			request.Header.Set("Authorization", "Bearer "+routerAcceptedToken)
			if route.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			router.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), route.want) {
				t.Fatalf("body = %s, want it to contain %s", recorder.Body.String(), route.want)
			}
		})
	}
}

// TestRouterMapsTheReviewRefusalsToTheirCodes follows the two conflicts and the
// one retryable failure from the domain out to the wire.
//
// It is here rather than only beside the handler because the envelope is written
// in two places: the handler chooses the status and the code, and this file's
// error middleware is what a client actually reads. A code that was right in the
// handler and lost on the way out is a client acting on the wrong sentence.
func TestRouterMapsTheReviewRefusalsToTheirCodes(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantBody   []string
	}{
		{
			name:       "a version that moved",
			err:        &practice.StaleReview{State: practice.ReviewState{Streak: 2, Version: 9}},
			wantStatus: http.StatusConflict,
			// The code and the sentence together, because the state it carries
			// is only useful to a client that recognised the refusal.
			wantBody: []string{`"code":"stale_review"`, `"details":{"state":{`, `"version":9`},
		},
		{
			name:       "a request id spent on something else",
			err:        fmt.Errorf("%w: req_1 was used elsewhere", practice.ErrConflictingRequestID),
			wantStatus: http.StatusConflict,
			wantBody:   []string{`"code":"conflicting_request_id"`},
		},
		{
			name:       "a judge that is not there",
			err:        fmt.Errorf("%w: the provider did not answer", practice.ErrJudgeUnavailable),
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   []string{`"code":"judge_unavailable"`, "the provider did not answer"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := newTestRouterWith(t, readyChecker{}, test.err)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/practice/reviews",
				strings.NewReader(`{"request_id":"req_1","question_id":"qn_9","user_answer":"答"}`))
			request.Header.Set("Authorization", "Bearer "+routerAcceptedToken)
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)

			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s",
					recorder.Code, test.wantStatus, recorder.Body.String())
			}
			for _, want := range test.wantBody {
				if !strings.Contains(recorder.Body.String(), want) {
					t.Errorf("body = %s, want it to contain %s", recorder.Body.String(), want)
				}
			}
			if !strings.Contains(recorder.Body.String(), `"request_id":`) {
				t.Error("a refusal was answered without the envelope's request id")
			}
		})
	}
}
