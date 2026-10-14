package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"backend/internal/practice"
)

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
