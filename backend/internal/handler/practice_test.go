package handler

// The reviewing surface against a stub. Two things are being pinned here.
//
// The first is what the URL decides and what it does not: the queue's length is
// the deployment's number rather than the request's, the two instants of a
// statistics window are read as either a timestamp or a date, and a submission
// travels to the domain as the shape it already is rather than through a second
// struct whose only job would be to have the same fields.
//
// The second, and the reason most of this file is one table, is the mapping from
// a refusal to a status and a code. The interesting failures of an HTTP surface
// are the ones a client has to act on differently, and two of these are both
// conflicts: a submission made against a version that has moved is a client that
// has to decide again, and it is answered with the state it missed; a request id
// already spent on a different submission is a client that has lost track of its
// own ids, and no state fixes that.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"backend/internal/notebook"
	"backend/internal/practice"
	"github.com/gin-gonic/gin"
)

var handlerPracticeTime = time.Unix(1_800_000_000, 0).UTC()

// practiceStub is the reviewing use case, recording what it was asked for.
type practiceStub struct {
	items  []practice.QueueItem
	result practice.ReviewResult
	stats  practice.Stats
	err    error

	sizes      []int
	submitted  []practice.Review
	readIDs    []string
	statsAsked []practice.StatsQuery
}

func (s *practiceStub) Queue(_ context.Context, size int) ([]practice.QueueItem, error) {
	s.sizes = append(s.sizes, size)
	return s.items, s.err
}

func (s *practiceStub) Review(_ context.Context, review practice.Review) (practice.ReviewResult, error) {
	s.submitted = append(s.submitted, review)
	return s.result, s.err
}

func (s *practiceStub) Recorded(_ context.Context, requestID string) (practice.ReviewResult, error) {
	s.readIDs = append(s.readIDs, requestID)
	return s.result, s.err
}

func (s *practiceStub) Stats(_ context.Context, query practice.StatsQuery) (practice.Stats, error) {
	s.statsAsked = append(s.statsAsked, query)
	return s.stats, s.err
}

// sampleResult is one recorded review with every part filled in, so that a case
// about one field can assert the others survived the trip.
func sampleReviewResult(replayed bool) practice.ReviewResult {
	correct := true
	open := false
	return practice.ReviewResult{
		RequestID:  "req_1",
		QuestionID: "qn_1",
		Replayed:   replayed,
		Judgment: &practice.Judgment{
			Kind:      practice.KindModelJudgment,
			IsCorrect: correct,
			Reason:    "答对了，闭包捕获的是变量本身",
			Rubric:    []string{"提到自由变量"},
		},
		State: practice.ReviewState{
			DueTime: handlerPracticeTime, Streak: 2, Lapses: 0,
			ReviewCount: 3, Ease: 2.5, IsMistake: false, Version: 4,
		},
		Entry: practice.AnswerState{
			IsCorrect: &correct, AttemptCount: 3, Resolved: &open, Result: "correct",
		},
	}
}

// sampleQueueItem is one question to practise now.
func sampleQueueItem(reason practice.QueueReason, state *practice.ReviewState) practice.QueueItem {
	return practice.QueueItem{
		Question: notebook.Question{
			QuestionID: "qn_1",
			Text:       "什么是闭包",
			Type:       notebook.QuestionShortAnswer,
		},
		Answer:    notebook.Answer{AttemptCount: 1},
		Reason:    reason,
		IsMistake: reason == practice.ReasonDue,
		State:     state,
	}
}

// newPracticeRouter mounts the reviewing routes the way the server does, with a
// queue size the tests can recognise in the call the stub records.
func newPracticeRouter(reviews Practice, queueSize int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := NewPracticeHandler(reviews, queueSize, testHandlerLogger())
	router := gin.New()
	router.GET("/api/v1/practice/queue", handler.Queue)
	router.POST("/api/v1/practice/reviews", handler.Submit)
	router.GET("/api/v1/practice/reviews/:requestID", handler.Read)
	router.GET("/api/v1/practice/stats", handler.Stats)
	return router
}

// TestPracticeHandlerQueueAsksForTheDeploymentsSize is the rule that a client
// does not set its own dose: the number comes from the handler that was built,
// and a size in the URL is not read at all.
func TestPracticeHandlerQueueAsksForTheDeploymentsSize(t *testing.T) {
	stub := &practiceStub{}
	recorder := call(t, newPracticeRouter(stub, 7), http.MethodGet,
		"/api/v1/practice/queue?size=99&limit=99", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if len(stub.sizes) != 1 || stub.sizes[0] != 7 {
		t.Errorf("the queue was asked for %v questions, want the deployment's 7", stub.sizes)
	}
}

func TestPracticeHandlerQueueShowsEachQuestionWithItsReason(t *testing.T) {
	due := practice.ReviewState{DueTime: handlerPracticeTime, Version: 4}
	stub := &practiceStub{items: []practice.QueueItem{
		sampleQueueItem(practice.ReasonDue, &due),
		sampleQueueItem(practice.ReasonNew, nil),
	}}
	recorder := call(t, newPracticeRouter(stub, 20), http.MethodGet, "/api/v1/practice/queue", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data queueView `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if len(envelope.Data.Queue) != 2 {
		t.Fatalf("the queue reads %+v", envelope.Data.Queue)
	}
	if envelope.Data.Queue[0].Reason != "due" || !envelope.Data.Queue[0].IsMistake {
		t.Errorf("the first item reads %+v", envelope.Data.Queue[0])
	}
	if envelope.Data.Queue[0].State == nil || envelope.Data.Queue[0].State.Version != 4 {
		t.Errorf("the first item reads %+v, want the state it carries a version in", envelope.Data.Queue[0])
	}
	if envelope.Data.Queue[1].Reason != "new" || envelope.Data.Queue[1].IsMistake {
		t.Errorf("the second item reads %+v", envelope.Data.Queue[1])
	}
	// The question face is the same one the bank shows, which is what makes the
	// card drawable without a second request.
	if envelope.Data.Queue[0].Question.Text != "什么是闭包" ||
		envelope.Data.Queue[0].Question.QuestionType != "short_answer" {
		t.Errorf("the question face reads %+v", envelope.Data.Queue[0].Question)
	}
}

// TestPracticeHandlerQueueLeavesANewQuestionsStateAbsent is the three-state half
// of the queue: a question nothing has been recorded for has no version to send
// back, and "absent" has to survive the trip as absent rather than as a state
// holding zeroes — a client that echoed zeroes would be sending a version
// nothing ever gave it.
func TestPracticeHandlerQueueLeavesANewQuestionsStateAbsent(t *testing.T) {
	stub := &practiceStub{items: []practice.QueueItem{sampleQueueItem(practice.ReasonNew, nil)}}
	recorder := call(t, newPracticeRouter(stub, 20), http.MethodGet, "/api/v1/practice/queue", "")
	if !containsJSON(recorder.Body.String(), `"state":null`) {
		t.Errorf("a question nobody has reviewed reads %s", recorder.Body.String())
	}
}

func TestPracticeHandlerQueueShowsAnEmptyQueueAsAnEmptyList(t *testing.T) {
	recorder := call(t, newPracticeRouter(&practiceStub{}, 20),
		http.MethodGet, "/api/v1/practice/queue", "")
	if !containsJSON(recorder.Body.String(), `"queue":[]`) {
		t.Errorf("nothing to practise reads %s", recorder.Body.String())
	}
}

// TestPracticeHandlerSubmitHandsTheSubmissionOver is the reason the body is
// decoded straight into the domain's shape: every field the client sent has to
// arrive, including the two that are pointers, because a version that arrived as
// nil would silently turn the version guard into a read-then-write.
func TestPracticeHandlerSubmitHandsTheSubmissionOver(t *testing.T) {
	stub := &practiceStub{result: sampleReviewResult(false)}
	recorder := call(t, newPracticeRouter(stub, 20), http.MethodPost, "/api/v1/practice/reviews",
		`{"request_id":"req_1","question_id":"qn_1","user_answer":"它捕获了自由变量",
		  "marker":"", "rating":4, "version":3}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if len(stub.submitted) != 1 {
		t.Fatalf("the review was submitted %d times, want once", len(stub.submitted))
	}
	rating := 4.0
	version := int64(3)
	want := practice.Review{
		RequestID: "req_1", QuestionID: "qn_1",
		UserAnswer: "它捕获了自由变量", Rating: &rating, Version: &version,
	}
	if got := stub.submitted[0]; !reflect.DeepEqual(got, want) {
		t.Errorf("the domain was handed %+v, want %+v", got, want)
	}
}

// TestPracticeHandlerSubmitAnswersTheSameForARetry pins that the status is a
// fact about the request and `replayed` is a fact about the record. A retry
// answered with a different status would be telling a client it did something it
// did not, and the two submissions are literally the same review.
func TestPracticeHandlerSubmitAnswersTheSameForARetry(t *testing.T) {
	bodies := map[bool]string{
		false: `{"request_id":"req_1","question_id":"qn_1","user_answer":"答","rating":4}`,
		true:  `{"request_id":"req_1","question_id":"qn_1","user_answer":"答","rating":4}`,
	}
	for replayed, body := range bodies {
		stub := &practiceStub{result: sampleReviewResult(replayed)}
		recorder := call(t, newPracticeRouter(stub, 20), http.MethodPost, "/api/v1/practice/reviews", body)
		if recorder.Code != http.StatusOK {
			t.Fatalf("replayed=%v gave status %d, want 200; body=%s",
				replayed, recorder.Code, recorder.Body.String())
		}
		var envelope struct {
			Data struct {
				Replayed bool `json:"replayed"`
			} `json:"data"`
		}
		decodeBody(t, recorder, &envelope)
		if envelope.Data.Replayed != replayed {
			t.Errorf("a submission answered replayed=%v reads %v", replayed, envelope.Data.Replayed)
		}
	}
}

func TestPracticeHandlerSubmitAnswersTheStoredResultWhole(t *testing.T) {
	recorder := call(t, newPracticeRouter(&practiceStub{result: sampleReviewResult(false)}, 20),
		http.MethodPost, "/api/v1/practice/reviews",
		`{"request_id":"req_1","question_id":"qn_1","user_answer":"答"}`)
	var envelope struct {
		Data practice.ReviewResult `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	want := sampleReviewResult(false)
	if !reflect.DeepEqual(envelope.Data, want) {
		t.Errorf("the response reads %+v, want %+v", envelope.Data, want)
	}
}

func TestPracticeHandlerSubmitRefusesABodyThatIsNotASubmission(t *testing.T) {
	stub := &practiceStub{}
	for _, body := range []string{"", " ", "{", "[]"} {
		recorder := call(t, newPracticeRouter(stub, 20), http.MethodPost,
			"/api/v1/practice/reviews", body)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("body %q gave status %d, want 400; body=%s",
				body, recorder.Code, recorder.Body.String())
		}
		if !containsJSON(recorder.Body.String(), `"invalid_request"`) {
			t.Errorf("body %q reads %s", body, recorder.Body.String())
		}
	}
	if len(stub.submitted) != 0 {
		t.Error("a body that is not a submission was acted on anyway")
	}
}

// TestPracticeHandlerReadReturnsTheStoredBody is what makes the idempotency rule
// readable from outside: the record of a submission is the same value a retry of
// it is answered with, so a client can tell whether its retry was recorded
// without having to send it twice.
func TestPracticeHandlerReadReturnsTheStoredBody(t *testing.T) {
	stub := &practiceStub{result: sampleReviewResult(false)}
	recorder := call(t, newPracticeRouter(stub, 20), http.MethodGet, "/api/v1/practice/reviews/req_1", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if len(stub.readIDs) != 1 || stub.readIDs[0] != "req_1" {
		t.Errorf("the record was read for %v, want req_1", stub.readIDs)
	}
	var envelope struct {
		Data practice.ReviewResult `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if want := sampleReviewResult(false); !reflect.DeepEqual(envelope.Data, want) {
		t.Errorf("the record reads %+v, want %+v", envelope.Data, want)
	}
}

func TestPracticeHandlerReadReportsAReviewThatIsNotThere(t *testing.T) {
	stub := &practiceStub{err: practice.ErrNotFound}
	recorder := call(t, newPracticeRouter(stub, 20), http.MethodGet, "/api/v1/practice/reviews/req_none", "")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", recorder.Code, recorder.Body.String())
	}
}

// TestPracticeHandlerStatsReadsTheWindow covers the two spellings of an instant
// and the bucket, and the fact that all three travel to the domain unexamined:
// widening a window to whole buckets is the domain's decision, and a handler
// that rounded here would round differently from the series it is answered with.
func TestPracticeHandlerStatsReadsTheWindow(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  practice.StatsQuery
	}{
		{name: "nothing named", query: "", want: practice.StatsQuery{}},
		{
			name:  "timestamps",
			query: "?from=2026-10-06T00:00:00Z&to=2026-10-13T00:00:00Z&bucket=day",
			want: practice.StatsQuery{
				From: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC),
				To:   time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC), Bucket: practice.BucketDay,
			},
		},
		{
			name:  "bare dates",
			query: "?from=2026-09-01&to=2026-10-01&bucket=week",
			want: practice.StatsQuery{
				From: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
				To:   time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Bucket: practice.BucketWeek,
			},
		},
		{
			name:  "one end only",
			query: "?from=2026-10-06",
			want:  practice.StatsQuery{From: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &practiceStub{}
			recorder := call(t, newPracticeRouter(stub, 20), http.MethodGet,
				"/api/v1/practice/stats"+test.query, "")
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
			}
			if len(stub.statsAsked) != 1 {
				t.Fatalf("the history was asked for %d times, want once", len(stub.statsAsked))
			}
			got := stub.statsAsked[0]
			if !got.From.Equal(test.want.From) || !got.To.Equal(test.want.To) ||
				got.Bucket != test.want.Bucket {
				t.Errorf("the window reads %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestPracticeHandlerStatsRefusesAnInstantItCannotRead(t *testing.T) {
	stub := &practiceStub{}
	for _, query := range []string{"?from=yesterday", "?to=13/10/2026", "?from=2026-13-45"} {
		recorder := call(t, newPracticeRouter(stub, 20), http.MethodGet, "/api/v1/practice/stats"+query, "")
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("query %q gave status %d, want 400; body=%s",
				query, recorder.Code, recorder.Body.String())
		}
		if !containsJSON(recorder.Body.String(), `"invalid_query"`) {
			t.Errorf("query %q reads %s", query, recorder.Body.String())
		}
	}
	if len(stub.statsAsked) != 0 {
		t.Error("a window that was refused reached the domain anyway")
	}
}

// TestPracticeHandlerStatsReportsTheWindowItCovered pins that the answer carries
// the window back. A window is widened to whole buckets, so the one that was
// asked for is not the one that was served, and a chart that trusted its own
// request would draw a half day as a whole one.
func TestPracticeHandlerStatsReportsTheWindowItCovered(t *testing.T) {
	stub := &practiceStub{stats: practice.Stats{
		From:   time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
		To:     time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC),
		Bucket: practice.BucketDay,
		Series: []practice.BucketStat{
			{Label: "2026-10-05", Reviews: 3, Correct: 2, Wrong: 1, Added: 1, Accuracy: 2.0 / 3.0},
		},
		Sources:    []practice.Distribution{{Name: "knowledge_base", Count: 2}, {Name: "", Count: 1}},
		Categories: []practice.Distribution{{Name: "数学", Count: 3}},
	}}
	recorder := call(t, newPracticeRouter(stub, 20), http.MethodGet,
		"/api/v1/practice/stats?from=2026-10-06&to=2026-10-12", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data statsView `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if want := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC); !envelope.Data.From.Equal(want) {
		t.Errorf("the window starts at %v, want the bucket the request fell in, %v", envelope.Data.From, want)
	}
	if envelope.Data.Bucket != "day" {
		t.Errorf("the bucket reads %q", envelope.Data.Bucket)
	}
	if len(envelope.Data.Series) != 1 || envelope.Data.Series[0].Label != "2026-10-05" {
		t.Fatalf("the series reads %+v", envelope.Data.Series)
	}
	if got := envelope.Data.Series[0]; got.Reviews != 3 || got.Correct != 2 ||
		got.Wrong != 1 || got.Added != 1 {
		t.Errorf("the point reads %+v", got)
	}
	// The empty source name is a value and not a hole: it is every question that
	// was not written by a conversation.
	if len(envelope.Data.Sources) != 2 || envelope.Data.Sources[1].Name != "" {
		t.Errorf("the sources read %+v", envelope.Data.Sources)
	}
	if len(envelope.Data.Categories) != 1 || envelope.Data.Categories[0].Count != 3 {
		t.Errorf("the labels read %+v", envelope.Data.Categories)
	}
}

func TestPracticeHandlerStatsShowsAnEmptyHistoryAsEmptyLists(t *testing.T) {
	stub := &practiceStub{stats: practice.Stats{Bucket: practice.BucketDay}}
	recorder := call(t, newPracticeRouter(stub, 20), http.MethodGet, "/api/v1/practice/stats", "")
	body := recorder.Body.String()
	for _, want := range []string{`"series":[]`, `"sources":[]`, `"categories":[]`} {
		if !containsJSON(body, want) {
			t.Errorf("a window with nothing in it is missing %s: %s", want, body)
		}
	}
}

// TestPracticeHandlerMapsItsRefusals is the table this file exists for: one row
// per way the domain can say no, and the status and code a client acts on.
func TestPracticeHandlerMapsItsRefusals(t *testing.T) {
	state := practice.ReviewState{DueTime: handlerPracticeTime, Streak: 1, Version: 4}
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name: "a version that moved, with the state it moved to",
			err:  &practice.StaleReview{State: state},
			// 409 rather than 412 or 428: the submission is refused, and the
			// state rides along so that deciding again costs one round trip.
			wantStatus: http.StatusConflict, wantCode: "stale_review",
		},
		{
			name: "a version that moved, without the state",
			err:  practice.ErrStaleReview,
			// The same answer as the row above, minus the state: one code and
			// one sentence, so a client reads a conflict in one branch.
			wantStatus: http.StatusConflict, wantCode: "stale_review",
		},
		{
			name: "a request id already spent on something else",
			err:  fmt.Errorf("%w: req_1 was used for a different submission", practice.ErrConflictingRequestID),
			// Not retryable by reading anything: the client has to find the id
			// it meant.
			wantStatus: http.StatusConflict, wantCode: "conflicting_request_id",
		},
		{
			name:       "a submission that is malformed",
			err:        fmt.Errorf("%w: the request id is blank", practice.ErrInvalidRequest),
			wantStatus: http.StatusBadRequest, wantCode: "invalid_request",
		},
		{
			name:       "a window that cannot be drawn",
			err:        fmt.Errorf("%w: 900 buckets is more than a chart", practice.ErrInvalidStats),
			wantStatus: http.StatusBadRequest, wantCode: "invalid_query",
		},
		{
			name:       "a question or a review that is not there",
			err:        practice.ErrNotFound,
			wantStatus: http.StatusNotFound, wantCode: "not_found",
		},
		{
			name: "a question that cannot be judged",
			err:  fmt.Errorf("%w: a choice question has no correct answer", practice.ErrUnusableQuestion),
			// 422: the request is well formed and the question is the problem,
			// and sending it again with the same body changes nothing.
			wantStatus: http.StatusUnprocessableEntity, wantCode: "question_not_judgeable",
		},
		{
			name: "a judge that is not there",
			err:  fmt.Errorf("%w: the provider did not answer", practice.ErrJudgeUnavailable),
			// 503: the one retryable failure here. The answer may be perfectly
			// good and the provider was simply not answering (§7.3 第 6 条).
			wantStatus: http.StatusServiceUnavailable, wantCode: "judge_unavailable",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &practiceStub{err: test.err}
			recorder := call(t, newPracticeRouter(stub, 20), http.MethodPost,
				"/api/v1/practice/reviews",
				`{"request_id":"req_1","question_id":"qn_1","user_answer":"答"}`)

			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s",
					recorder.Code, test.wantStatus, recorder.Body.String())
			}
			var envelope struct {
				Error struct {
					Code    string          `json:"code"`
					Message string          `json:"message"`
					Details json.RawMessage `json:"details"`
				} `json:"error"`
			}
			decodeBody(t, recorder, &envelope)
			if envelope.Error.Code != test.wantCode {
				t.Errorf("error code = %q, want %q", envelope.Error.Code, test.wantCode)
			}
			if envelope.Error.Message == "" {
				t.Error("the refusal says nothing")
			}
			// Nothing a client is handed may be the sentinel's own sentence:
			// "practice: x" is the package's name for the failure, and a client
			// that read it would be reading a word about our source tree.
			if containsJSON(envelope.Error.Message, "practice: ") {
				t.Errorf("the refusal quotes the sentinel: %q", envelope.Error.Message)
			}
		})
	}
}

// TestPracticeHandlerTellsTheTwoVersionConflictsApart is the half the table
// above cannot state: the conflict that carries the state is the same status and
// the same code as the one that does not, and the difference is a detail a
// client reads when it is there.
func TestPracticeHandlerTellsTheTwoVersionConflictsApart(t *testing.T) {
	state := practice.ReviewState{DueTime: handlerPracticeTime, Streak: 2, Version: 9}

	t.Run("with the state", func(t *testing.T) {
		stub := &practiceStub{err: &practice.StaleReview{State: state}}
		recorder := call(t, newPracticeRouter(stub, 20), http.MethodPost,
			"/api/v1/practice/reviews",
			`{"request_id":"req_1","question_id":"qn_1","user_answer":"答"}`)
		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Details struct {
					State practice.ReviewState `json:"state"`
				} `json:"details"`
			} `json:"error"`
		}
		decodeBody(t, recorder, &envelope)
		if envelope.Error.Details.State.Version != 9 {
			t.Errorf("the refusal carries %+v, want the state the client is behind",
				envelope.Error.Details.State)
		}
	})

	t.Run("without it", func(t *testing.T) {
		stub := &practiceStub{err: practice.ErrStaleReview}
		recorder := call(t, newPracticeRouter(stub, 20), http.MethodPost,
			"/api/v1/practice/reviews",
			`{"request_id":"req_1","question_id":"qn_1","user_answer":"答"}`)
		var envelope struct {
			Error struct {
				Code    string          `json:"code"`
				Details json.RawMessage `json:"details"`
			} `json:"error"`
		}
		decodeBody(t, recorder, &envelope)
		if len(envelope.Error.Details) != 0 {
			t.Errorf("a conflict with no state carries %s", envelope.Error.Details)
		}
	})
}

// TestPracticeHandlerDoesNotReportStorageToTheClient is the one behaviour a
// failure has to have: the sentence that says why is in the log, and what the
// client is handed is a status and a code.
func TestPracticeHandlerDoesNotReportStorageToTheClient(t *testing.T) {
	stub := &practiceStub{err: errors.New("private database details")}
	recorder := call(t, newPracticeRouter(stub, 20), http.MethodGet, "/api/v1/practice/queue", "")
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	if containsJSON(recorder.Body.String(), "private database details") {
		t.Fatalf("the response exposed a storage error: %s", recorder.Body.String())
	}
}
