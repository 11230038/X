package turn

import (
	"errors"
	"net/http"
	"testing"

	"backend/internal/llm"
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
		})
	}
}
