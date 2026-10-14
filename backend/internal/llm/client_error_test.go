package llm

// What a provider's refusal becomes: an *HTTPError that carries the status and
// the provider's own code, that says whether it is worth retrying, and that
// never repeats the credential back.
//
// The fixtures these run on are client_test.go's.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStreamReturnsAProviderErrorWithoutTheAPIKey(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		// The body repeats the key back, which is what a gateway that reports
		// what it received does. The error must not carry it further.
		body := `{"error":{"message":"Authentication Fails for key ` + testAPIKey + `","type":"authentication_error","code":"invalid_api_key"}}`
		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("write body: %v", err)
		}
	})

	_, err := client.Stream(context.Background(), Request{Model: "deepseek-chat"}, func(Delta) error { return nil })
	if err == nil {
		t.Fatal("Stream() error = nil, want a provider error")
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("Stream() error = %v, want *HTTPError", err)
	}
	if httpErr.StatusCode != http.StatusUnauthorized || httpErr.Code != "invalid_api_key" {
		t.Errorf("HTTPError = %+v", httpErr)
	}
	if httpErr.Retryable() {
		t.Error("Retryable() = true, want false for a rejected key")
	}
	if strings.Contains(err.Error(), testAPIKey) {
		t.Fatal("provider error exposed the API key")
	}
}

func TestStreamHandlesAnUnrecognisedErrorBody(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		if _, err := io.WriteString(w, "upstream down"); err != nil {
			t.Errorf("write body: %v", err)
		}
	})

	_, err := client.Stream(context.Background(), Request{Model: "deepseek-chat"}, func(Delta) error { return nil })
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("Stream() error = %v, want *HTTPError", err)
	}
	if httpErr.StatusCode != http.StatusBadGateway || !httpErr.Retryable() {
		t.Errorf("HTTPError = %+v, retryable = %v", httpErr, httpErr.Retryable())
	}
	if strings.Contains(err.Error(), testAPIKey) {
		t.Fatal("provider error exposed the API key")
	}
}

func TestHTTPErrorRetryable(t *testing.T) {
	for _, tt := range []struct {
		status int
		want   bool
	}{
		{status: http.StatusBadRequest, want: false},
		{status: http.StatusUnauthorized, want: false},
		{status: http.StatusPaymentRequired, want: false},
		{status: http.StatusTooManyRequests, want: true},
		{status: http.StatusInternalServerError, want: true},
		{status: http.StatusBadGateway, want: true},
	} {
		if got := (&HTTPError{StatusCode: tt.status}).Retryable(); got != tt.want {
			t.Errorf("status %d Retryable() = %v, want %v", tt.status, got, tt.want)
		}
	}
}

func TestStreamStopsWhenTheConsumerFails(t *testing.T) {
	stop := errors.New("consumer stopped")
	body := sseData(
		`{"choices":[{"index":0,"delta":{"content":"a"},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"content":"b"},"finish_reason":null}]}`,
		`[DONE]`,
	)
	client := newTestClient(t, streamHandler(t, body))

	calls := 0
	_, err := client.Stream(context.Background(), Request{Model: "deepseek-chat"}, func(Delta) error {
		calls++
		return stop
	})

	if !errors.Is(err, stop) {
		t.Fatalf("Stream() error = %v, want %v", err, stop)
	}
	if calls != 1 {
		t.Errorf("onDelta called %d times, want 1", calls)
	}
}

func TestStreamReturnsCancellationAsAContextError(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		// Hold the stream open so the answer never finishes.
		<-release
	})
	// Registered after the server's own cleanup so it runs before it: the server
	// will not shut down while a handler is still blocked.
	t.Cleanup(func() { once.Do(func() { close(release) }) })

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := client.Stream(ctx, Request{Model: "deepseek-chat"}, func(Delta) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Stream() error = %v, want context.Canceled", err)
	}
}
