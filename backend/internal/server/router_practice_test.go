package server

import (
	"backend/internal/authz"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"backend/internal/notebook"
	"backend/internal/practice"
)

// routerQueueSize is the queue length this file's router was built with, so that
// the practice route's body says which handler answered it.
const routerQueueSize = 13

type routerQuestionsStub struct{}

func (routerQuestionsStub) Get(_ context.Context, questionID string, _ authz.Caller) (notebook.Entry, error) {
	return routerQuestionBankEntry(questionID), nil
}

func (routerQuestionsStub) List(context.Context, notebook.Filter) ([]notebook.Entry, error) {
	return []notebook.Entry{routerQuestionBankEntry("qn_1")}, nil
}

func (routerQuestionsStub) Mark(context.Context, string, notebook.Change, authz.Caller) error {
	return nil
}

func (routerQuestionsStub) SetCategories(context.Context, string, []string, authz.Caller) error {
	return nil
}

func (routerQuestionsStub) Categories(context.Context, authz.Caller) ([]notebook.Category, error) {
	return []notebook.Category{{Name: "数学", Count: 3}}, nil
}

// Import answers the batch import with an empty report: this file is about which
// routes exist, and what the import says about a file is handler/question_import_test.go's.
func (routerQuestionsStub) Import(context.Context, string, bool, authz.Caller) (notebook.ImportReport, error) {
	return notebook.ImportReport{Entries: []notebook.ImportEntry{}}, nil
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

func (s routerPracticeStub) Queue(context.Context, int, authz.Caller) ([]practice.QueueItem, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []practice.QueueItem{{
		Question: notebook.Question{QuestionID: "qn_9", Text: "该复习的题", Type: notebook.QuestionFillBlank},
		Reason:   practice.ReasonNew,
	}}, nil
}

func (s routerPracticeStub) Review(context.Context, practice.Review, authz.Caller) (practice.ReviewResult, error) {
	if s.err != nil {
		return practice.ReviewResult{}, s.err
	}
	return routerReviewResult(), nil
}

func (s routerPracticeStub) Recorded(context.Context, string, authz.Caller) (practice.ReviewResult, error) {
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

func (s routerPracticeStub) Mistakes(context.Context, time.Time, time.Time, authz.Caller) (practice.MistakeProgress, error) {
	if s.err != nil {
		return practice.MistakeProgress{}, s.err
	}
	return practice.MistakeProgress{Reviewed: 1, Total: 2}, nil
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
