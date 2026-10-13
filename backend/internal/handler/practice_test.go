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
