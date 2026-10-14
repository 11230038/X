package llm

// What a call says back to the key pool: which key it took, whether it worked,
// and — for the one refusal that is about the credential — that it was rested.
//
// A transport fault and a stream that dies halfway are both here, because both
// are cases about what the pool is *not* told.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
)

// newClientWithKeys points a client at a handler over a chosen key source, so
// that a case about what the client asks for and reports back can watch the
// source rather than the wire.
func newClientWithKeys(t *testing.T, handler http.HandlerFunc, keys KeySource) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, keys, 5*time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}

// A client built without anything to authenticate with is refused where it is
// built. Left until the first call it would send an unauthenticated request and
// surface as a provider error inside somebody's turn.
func TestAClientWithoutAKeySourceIsRefused(t *testing.T) {
	client, err := NewClient("http://127.0.0.1:1", nil, time.Second)
	if !errors.Is(err, ErrNoKeySource) {
		t.Fatalf("NewClient() error = %v, want ErrNoKeySource", err)
	}
	if client != nil {
		t.Error("a client was returned alongside the failure")
	}
}

// A key is taken once per call rather than once per client: a client that kept
// one would keep using a credential the pool has since rested.
func TestEachCallTakesItsOwnKey(t *testing.T) {
	keys := &watchingKeys{}
	client := newClientWithKeys(t, streamHandler(t, sseData(`{"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`)), keys)

	for range 2 {
		if _, err := client.Stream(context.Background(), Request{Model: "deepseek-chat"}, func(Delta) error { return nil }); err != nil {
			t.Fatalf("Stream() error = %v", err)
		}
	}
	if len(keys.handed) != 2 {
		t.Errorf("keys taken = %d, want one per call", len(keys.handed))
	}
	if !slices.Equal(keys.succeeded, []string{testAPIKey, testAPIKey}) {
		t.Errorf("succeeded = %v, want both calls reported as answered", keys.succeeded)
	}
	if len(keys.limited) != 0 {
		t.Errorf("rate limited = %v, want nothing: no call was refused", keys.limited)
	}
}

// A refusal that is about the key reaches the pool, and it is the only refusal
// that does: a provider that is unwell is not this credential's problem.
func TestOnlyARateLimitIsLaidAtTheKeysDoor(t *testing.T) {
	for _, tt := range []struct {
		name        string
		status      int
		wantLimited bool
	}{
		{name: "rate limited", status: http.StatusTooManyRequests, wantLimited: true},
		{name: "the provider is unwell", status: http.StatusInternalServerError},
		{name: "the key is rejected", status: http.StatusUnauthorized},
		{name: "the request is bad", status: http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			keys := &watchingKeys{}
			client := newClientWithKeys(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
			}, keys)

			if _, err := client.Stream(context.Background(), Request{Model: "deepseek-chat"}, func(Delta) error { return nil }); err == nil {
				t.Fatal("Stream() error = nil, want the provider's refusal")
			}
			want := []string{}
			if tt.wantLimited {
				want = []string{testAPIKey}
			}
			if !slices.Equal(keys.limited, want) {
				t.Errorf("rate limited = %v, want %v", keys.limited, want)
			}
			if len(keys.succeeded) != 0 {
				t.Errorf("succeeded = %v, want nothing: the call did not come back", keys.succeeded)
			}
		})
	}
}

// A call that never reached the provider says nothing about the key, so it says
// nothing: resting a credential for a transport fault would take a working key
// out of service for a reason that had nothing to do with it.
func TestACallThatNeverArrivedSaysNothingAboutTheKey(t *testing.T) {
	keys := &watchingKeys{}
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := server.URL
	server.Close()

	client, err := NewClient(address, keys, time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := client.Stream(context.Background(), Request{Model: "deepseek-chat"}, func(Delta) error { return nil }); err == nil {
		t.Fatal("Stream() error = nil, want a transport failure")
	}
	if len(keys.succeeded) != 0 || len(keys.limited) != 0 {
		t.Errorf("the key was reported on: %v / %v", keys.succeeded, keys.limited)
	}
}

// The provider's verdict on a credential is its status line. How the answer it
// then sends behaves is about the answer, so a stream that dies halfway still
// counts as the key having worked.
func TestAStatusLineIsTheVerdictOnTheKey(t *testing.T) {
	keys := &watchingKeys{}
	client := newClientWithKeys(t, streamHandler(t, sseData(`{"choices":[{"index":0,"delta":{"content":"half"},"finish_reason":null}]}`)), keys)

	_, err := client.Stream(context.Background(), Request{Model: "deepseek-chat"}, func(Delta) error { return nil })
	if !errors.Is(err, ErrIncompleteStream) {
		t.Fatalf("Stream() error = %v, want ErrIncompleteStream", err)
	}
	if !slices.Equal(keys.succeeded, []string{testAPIKey}) {
		t.Errorf("succeeded = %v, want the key credited: the provider accepted it", keys.succeeded)
	}
	if len(keys.limited) != 0 {
		t.Errorf("rate limited = %v, want nothing", keys.limited)
	}
}
