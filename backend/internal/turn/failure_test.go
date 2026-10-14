package turn

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"backend/internal/llm"
	"backend/internal/usage"
)

func TestAProviderFailureIsRetriedOnlyIfItSaysSo(t *testing.T) {
	// A client reads Retryable to decide whether to offer a retry, so it has to
	// be right: a rejected key fails the same way every time, and a provider
	// that is briefly overloaded does not.
	tests := []struct {
		name          string
		failure       error
		wantRetryable bool
	}{
		{
			name:          "a rejected request is final",
			failure:       &llm.HTTPError{StatusCode: http.StatusUnauthorized, Code: "invalid_api_key", Message: "Authentication Fails"},
			wantRetryable: false,
		},
		{
			name:          "a provider that is away is worth asking again",
			failure:       &llm.HTTPError{StatusCode: http.StatusBadGateway},
			wantRetryable: true,
		},
		{
			name:          "a failure nobody classified keeps the default",
			failure:       errors.New("something nobody classified"),
			wantRetryable: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			described := describe(test.failure)
			if described.Code != CodeInternalError {
				t.Errorf("Code = %q, want %q", described.Code, CodeInternalError)
			}
			if described.Retryable != test.wantRetryable {
				t.Errorf("Retryable = %v, want %v", described.Retryable, test.wantRetryable)
			}
			// None of these is a refusal, and a failure that carried one would
			// tell the client about a limit nobody reached.
			if described.Quota != nil {
				t.Errorf("Quota = %+v, want none", described.Quota)
			}
		})
	}
}

// TestAQuotaRefusalKeepsItsItemAndItsMoment: the refusal arrives wrapped in
// whatever carried it — here the judging failure practice wraps it in — and it
// has to survive that. What the client is told depends on it: the turn's code
// names the refusal, and the item and the moment are the two facts §7 asks a
// refusal to name, which no other part of the failure has room for.
func TestAQuotaRefusalKeepsItsItemAndItsMoment(t *testing.T) {
	resets := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	refusal := &usage.Exceeded{Item: usage.ItemDailyCalls, Limit: 2, Used: 2, ResetsAt: resets}

	described := describe(fmt.Errorf("judge: %w: %w", errors.New("unavailable"), refusal))

	if described.Code != CodeQuotaExceeded {
		t.Errorf("Code = %q, want %q", described.Code, CodeQuotaExceeded)
	}
	if !described.Retryable {
		t.Error("Retryable = false, want true: the period ends and the call may be made again")
	}
	if described.Quota != refusal {
		t.Errorf("Quota = %+v, want the refusal that came in", described.Quota)
	}
	// The operator reads the reason and the client does not: the message is
	// there, and it is not the error's own text.
	if described.Error == "" || described.Error == clientMessages[CodeQuotaExceeded] {
		t.Errorf("Error = %q, want the reason rather than the message", described.Error)
	}
}
