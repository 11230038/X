// The send side of a call: the credential it carries, whether it is made at all,
// and what the caller is told when it does not go through. The other half — what
// a response comes back as — is in client_test.go.
package embed

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientNeverRepeatsTheAPIKey(t *testing.T) {
	cases := []struct {
		name       string
		statusCode int
		body       string
	}{
		{name: "provider echoes the key", statusCode: http.StatusUnauthorized,
			body: `{"error":{"message":"invalid api key ` + testKey + `","code":"invalid_api_key"}}`},
		{name: "body is not the documented shape", statusCode: http.StatusBadGateway,
			body: `gateway said no (key ` + testKey + `)`},
		{name: "empty body", statusCode: http.StatusUnauthorized, body: ""},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(testCase.statusCode)
				_, _ = io.WriteString(w, testCase.body)
			}))
			defer fake.Close()

			_, err := newTestClient(t, fake.URL, 16).Embed(context.Background(), []string{"a"})
			if err == nil {
				t.Fatal("Embed() error = nil, want an error")
			}
			if strings.Contains(err.Error(), testKey) {
				t.Fatalf("error exposed the API key: %v", err)
			}
			var httpErr *HTTPError
			if !errors.As(err, &httpErr) {
				t.Fatalf("error = %v, want an HTTPError", err)
			}
			if httpErr.StatusCode != testCase.statusCode {
				t.Errorf("status = %d, want %d", httpErr.StatusCode, testCase.statusCode)
			}
		})
	}
}

func TestClientSendsTheAPIKeyAsABearerToken(t *testing.T) {
	var got string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		got = req.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"data":[{"index":0,"embedding":[1,1,1,1]}]}`)
	}))
	defer fake.Close()

	if _, err := newTestClient(t, fake.URL, 16).Embed(context.Background(), []string{"a"}); err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if got != "Bearer "+testKey {
		t.Fatalf("Authorization = %q", got)
	}
}

// A local provider that needs no key is not sent an empty bearer token, which
// is a different request from no header at all.
func TestClientOmitsTheAuthorizationHeaderWithoutAKey(t *testing.T) {
	var present bool
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, present = req.Header["Authorization"]
		_, _ = io.WriteString(w, `{"data":[{"index":0,"embedding":[1,1,1,1]}]}`)
	}))
	defer fake.Close()

	client, err := NewClient(Options{
		BaseURL: fake.URL, Model: testModel, Dimension: testDimension,
		BatchSize: 16, Timeout: 5 * time.Second, Dial: testDial(t),
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := client.Embed(context.Background(), []string{"a"}); err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if present {
		t.Fatal("request carried an Authorization header built from an empty key")
	}
}

func TestClientEmbedDoesNotCallTheProviderForNoInput(t *testing.T) {
	recorder := &recorder{}
	fake := server(t, recorder)
	defer fake.Close()

	result, err := newTestClient(t, fake.URL, 16).Embed(context.Background(), nil)
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if len(result.Vectors) != 0 {
		t.Fatalf("got %d vectors for no input", len(result.Vectors))
	}
	if recorder.count() != 0 {
		t.Fatalf("made %d requests for no input, want none", recorder.count())
	}
}

func TestClientEmbedReportsCancellation(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		<-req.Context().Done()
	}))
	defer fake.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := newTestClient(t, fake.URL, 16).Embed(ctx, []string{"a"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Embed() error = %v, want context.Canceled", err)
	}
}

func TestClientEmbedReportsATimeout(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = io.WriteString(w, `{"data":[{"index":0,"embedding":[1,1,1,1]}]}`)
	}))
	defer fake.Close()

	client, err := NewClient(Options{
		BaseURL: fake.URL, Model: testModel, Dimension: testDimension,
		BatchSize: 16, Timeout: 20 * time.Millisecond, Dial: testDial(t),
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := client.Embed(context.Background(), []string{"a"}); err == nil {
		t.Fatal("Embed() error = nil, want a timeout")
	}
}

func TestNewClientRejectsAnUnusableShape(t *testing.T) {
	cases := []struct {
		name  string
		mutad func(*Options)
	}{
		{name: "no base URL", mutad: func(o *Options) { o.BaseURL = "" }},
		{name: "no model", mutad: func(o *Options) { o.Model = "  " }},
		{name: "no dimension", mutad: func(o *Options) { o.Dimension = 0 }},
		{name: "no batch size", mutad: func(o *Options) { o.BatchSize = 0 }},
		{name: "no timeout", mutad: func(o *Options) { o.Timeout = 0 }},
		{name: "no dialer", mutad: func(o *Options) { o.Dial = nil }},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Every field is filled and one is taken away: a case that passed
			// because something else was missing would prove nothing about the
			// field it names.
			opts := Options{
				BaseURL: "http://127.0.0.1:1", Model: testModel, Dimension: testDimension,
				BatchSize: 16, Timeout: time.Second, Dial: testDial(t),
			}
			testCase.mutad(&opts)

			if _, err := NewClient(opts); err == nil {
				t.Fatal("NewClient() error = nil, want an error")
			}
		})
	}
}

func TestClientTrimsATrailingSlashFromTheBaseURL(t *testing.T) {
	recorder := &recorder{}
	fake := server(t, recorder)
	defer fake.Close()

	// The path check inside the server fails the test if the client requested
	// //embeddings.
	if _, err := newTestClient(t, fake.URL+"/", 16).Embed(context.Background(), []string{"a"}); err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
}
