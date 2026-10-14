package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"backend/internal/practice"
	"backend/internal/usage"
)

// handlerQuotaResets is the moment the quota refusals in these cases come back
// at — the end of the day the refusal is about. It is written out rather than
// computed, because a case that derived it would be asserting the code's own
// arithmetic back at it.
var handlerQuotaResets = time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)

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
		{
			name: "a call the account's quota refused",
			// The refusal arrives inside the judging failure because that is what
			// was being attempted, and the answer is the refusal: a retry offered
			// for `judge_unavailable` is a retry that will be refused the same way
			// until the period ends (§2.9).
			err: fmt.Errorf("%w: %w", practice.ErrJudgeUnavailable, &usage.Exceeded{
				Item: usage.ItemDailyCalls, Limit: 2, Used: 2, ResetsAt: handlerQuotaResets,
			}),
			wantStatus: http.StatusTooManyRequests, wantCode: "quota_exceeded",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &practiceStub{err: test.err}
			recorder := callAs(t, newPracticeRouter(stub, 20), http.MethodPost,
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

// TestTheQuotaRefusalCarriesWhichItemAndWhenItComesBack: §7 asks a refusal to
// name the item that ran out and the moment it recovers, and the sentence above
// cannot — a client that drew a countdown from prose would be parsing English.
// The fields are the answer, and they are the same four the socket's error event
// carries, which is what keeps one refusal one thing across two transports.
func TestTheQuotaRefusalCarriesWhichItemAndWhenItComesBack(t *testing.T) {
	stub := &practiceStub{err: fmt.Errorf("%w: %w", practice.ErrJudgeUnavailable, &usage.Exceeded{
		Item: usage.ItemDailyCalls, Limit: 2, Used: 3, ResetsAt: handlerQuotaResets,
	})}
	recorder := callAs(t, newPracticeRouter(stub, 20), http.MethodPost,
		"/api/v1/practice/reviews",
		`{"request_id":"req_1","question_id":"qn_1","user_answer":"答"}`)

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				Item     string    `json:"item"`
				Limit    int64     `json:"limit"`
				Used     int64     `json:"used"`
				ResetsAt time.Time `json:"resets_at"`
			} `json:"details"`
		} `json:"error"`
	}
	decodeBody(t, recorder, &envelope)
	if envelope.Error.Code != "quota_exceeded" {
		t.Errorf("error code = %q, want quota_exceeded", envelope.Error.Code)
	}
	details := envelope.Error.Details
	if details.Item != usage.ItemDailyCalls || details.Limit != 2 || details.Used != 3 {
		t.Errorf("details = %+v, want the item and the two numbers behind the refusal", details)
	}
	if !details.ResetsAt.Equal(handlerQuotaResets) {
		t.Errorf("resets_at = %v, want %v", details.ResetsAt, handlerQuotaResets)
	}
	// Retry-After belongs to the other refusal. This wait is the end of a day or
	// a month — something to explain to a user rather than to sleep through — and
	// a client that honoured a header automatically would wait in silence.
	if header := recorder.Header().Get("Retry-After"); header != "" {
		t.Errorf("Retry-After = %q, want none on a refusal about a period", header)
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
		recorder := callAs(t, newPracticeRouter(stub, 20), http.MethodPost,
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
		recorder := callAs(t, newPracticeRouter(stub, 20), http.MethodPost,
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
	recorder := callAs(t, newPracticeRouter(stub, 20), http.MethodGet, "/api/v1/practice/queue", "")
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	if containsJSON(recorder.Body.String(), "private database details") {
		t.Fatalf("the response exposed a storage error: %s", recorder.Body.String())
	}
}
