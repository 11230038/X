// What a call to the provider comes back with, and where each vector lands. The
// send-side half — the key, the URL, the failures — is in request_test.go; the
// Vector type's own literal is in vector_test.go.
package embed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testModel = "qwen3.7-text-embedding-flash"
	testKey   = "unit-test-embedding-key"
	// The shipped dimension, narrowed here so that a test vector stays
	// readable. Nothing in the client is tied to 1024.
	testDimension = 4
)

// recorder is a fake /embeddings endpoint that remembers what it was asked.
type recorder struct {
	mu       sync.Mutex
	requests []embeddingRequest
	bodies   []string
}

func (r *recorder) record(t *testing.T, req *http.Request) embeddingRequest {
	t.Helper()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	var decoded embeddingRequest
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode request body %q: %v", body, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, decoded)
	r.bodies = append(r.bodies, string(body))
	return decoded
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

// vectorFor makes an unmistakable vector per text, so that a test can tell
// which input a vector was paired with.
func vectorFor(text string) []float32 {
	vector := make([]float32, testDimension)
	for index := range vector {
		vector[index] = float32(len(text)) + float32(index)/10
	}
	return vector
}

// server answers /embeddings by echoing one vector per input, and records every
// request. Checks the path and method: a client that put the key in the URL, or
// called the wrong endpoint, would otherwise pass.
func server(t *testing.T, recorder *recorder) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost || req.URL.Path != embeddingsPath {
			t.Errorf("request = %s %s, want POST %s", req.Method, req.URL.Path, embeddingsPath)
		}
		if req.URL.RawQuery != "" {
			t.Errorf("request carried a query string: %q", req.URL.RawQuery)
		}
		decoded := recorder.record(t, req)

		type datum struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}
		response := struct {
			Data  []datum `json:"data"`
			Usage struct {
				PromptTokens int `json:"prompt_tokens"`
				TotalTokens  int `json:"total_tokens"`
			} `json:"usage"`
		}{}
		for index, text := range decoded.Input {
			response.Data = append(response.Data, datum{Index: index, Embedding: vectorFor(text)})
		}
		response.Usage.PromptTokens = len(decoded.Input) * 10
		response.Usage.TotalTokens = len(decoded.Input) * 10

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
}

func newTestClient(t *testing.T, baseURL string, batchSize int) *Client {
	t.Helper()
	client, err := NewClient(Options{
		BaseURL:   baseURL,
		APIKey:    testKey,
		Model:     testModel,
		Dimension: testDimension,
		BatchSize: batchSize,
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}

func TestClientEmbedSendsEveryInputInOrder(t *testing.T) {
	recorder := &recorder{}
	fake := server(t, recorder)
	defer fake.Close()

	texts := []string{"甲", "乙乙", "丙丙丙"}
	result, err := newTestClient(t, fake.URL, 16).Embed(context.Background(), texts)
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}

	if len(result.Vectors) != len(texts) {
		t.Fatalf("got %d vectors, want %d", len(result.Vectors), len(texts))
	}
	for index, text := range texts {
		want := Vector(vectorFor(text))
		if result.Vectors[index].String() != want.String() {
			t.Errorf("vector %d = %s, want the one for %q", index, result.Vectors[index], text)
		}
	}
	if recorder.count() != 1 {
		t.Fatalf("made %d requests, want 1", recorder.count())
	}
	if got := recorder.requests[0].Model; got != testModel {
		t.Errorf("model = %q, want %q", got, testModel)
	}
	if got := recorder.requests[0].EncodingFormat; got != encodingFormatFloat {
		t.Errorf("encoding_format = %q, want %q", got, encodingFormatFloat)
	}
	// The width is checked on the response instead, so that the client does not
	// depend on a field every compatible provider may not accept.
	if raw := recorder.bodies[0]; strings.Contains(raw, "dimension") {
		t.Errorf("request carried a dimensions field: %s", raw)
	}
}

func TestClientEmbedSplitsIntoBatches(t *testing.T) {
	recorder := &recorder{}
	fake := server(t, recorder)
	defer fake.Close()

	texts := []string{"a", "bb", "ccc", "dddd", "eeeee", "ffffff", "ggggggg"}
	result, err := newTestClient(t, fake.URL, 3).Embed(context.Background(), texts)
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}

	if recorder.count() != 3 {
		t.Fatalf("made %d requests, want 3", recorder.count())
	}
	sizes := []int{3, 3, 1}
	for index, want := range sizes {
		if got := len(recorder.requests[index].Input); got != want {
			t.Errorf("request %d carried %d inputs, want %d", index, got, want)
		}
	}
	// Order survives the split: the last batch's single vector belongs to the
	// last input, not to the first batch's first input.
	if len(result.Vectors) != len(texts) {
		t.Fatalf("got %d vectors, want %d", len(result.Vectors), len(texts))
	}
	for index, text := range texts {
		if result.Vectors[index].String() != Vector(vectorFor(text)).String() {
			t.Errorf("vector %d does not belong to %q", index, text)
		}
	}
	if result.Usage.PromptTokens != 70 {
		t.Errorf("prompt tokens = %d, want the sum over batches (70)", result.Usage.PromptTokens)
	}
}

func TestClientEmbedBelievesTheIndexOverTheArrivalOrder(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// The documented field says which input each vector belongs to, so a
		// provider that returns them out of order is still understood.
		_, _ = fmt.Fprintf(w, `{"data":[
			{"index":1,"embedding":[1,1,1,1]},
			{"index":0,"embedding":[0,0,0,0]},
			{"index":2,"embedding":[2,2,2,2]}]}`)
	}))
	defer fake.Close()

	result, err := newTestClient(t, fake.URL, 16).Embed(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	for index, want := range []string{"[0,0,0,0]", "[1,1,1,1]", "[2,2,2,2]"} {
		if got := result.Vectors[index].String(); got != want {
			t.Errorf("vector %d = %s, want %s", index, got, want)
		}
	}
}

func TestClientEmbedFallsBackToTheArrivalOrder(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// No index at all. Absent means "the order is the order", which is a
		// different statement from index 0.
		_, _ = fmt.Fprintf(w, `{"data":[
			{"embedding":[0,0,0,0]},
			{"embedding":[1,1,1,1]}]}`)
	}))
	defer fake.Close()

	result, err := newTestClient(t, fake.URL, 16).Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if got := result.Vectors[1].String(); got != "[1,1,1,1]" {
		t.Errorf("vector 1 = %s, want the second datum", got)
	}
}

func TestClientEmbedRejectsAWrongWidth(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Three numbers where the column holds four.
		_, _ = fmt.Fprintf(w, `{"data":[{"index":0,"embedding":[1,2,3]},{"index":1,"embedding":[4,5,6]}]}`)
	}))
	defer fake.Close()

	_, err := newTestClient(t, fake.URL, 16).Embed(context.Background(), []string{"a", "b"})
	if !errors.Is(err, ErrDimensionMismatch) {
		t.Fatalf("Embed() error = %v, want ErrDimensionMismatch", err)
	}
	// The message has to say which of the two numbers is which, or the operator
	// changing EMBEDDING_DIMENSION has nothing to go on.
	if !strings.Contains(err.Error(), "expected 4") || !strings.Contains(err.Error(), "got 3") {
		t.Fatalf("error = %v, want it to name both widths", err)
	}
}

func TestClientEmbedRejectsResponsesThatDoNotMatchTheInput(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "fewer vectors than inputs", body: `{"data":[{"index":0,"embedding":[0,0,0,0]}]}`},
		{name: "more vectors than inputs", body: `{"data":[
			{"index":0,"embedding":[0,0,0,0]},
			{"index":1,"embedding":[1,1,1,1]},
			{"index":2,"embedding":[2,2,2,2]}]}`},
		{name: "an index past the end", body: `{"data":[
			{"index":0,"embedding":[0,0,0,0]},
			{"index":7,"embedding":[1,1,1,1]}]}`},
		{name: "two vectors for one position", body: `{"data":[
			{"index":0,"embedding":[0,0,0,0]},
			{"index":0,"embedding":[1,1,1,1]}]}`},
		{name: "a negative index", body: `{"data":[
			{"index":-1,"embedding":[0,0,0,0]},
			{"index":1,"embedding":[1,1,1,1]}]}`},
		{name: "no data at all", body: `{"data":[]}`},
		{name: "not JSON", body: `hello`},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, testCase.body)
			}))
			defer fake.Close()

			_, err := newTestClient(t, fake.URL, 16).Embed(context.Background(), []string{"a", "b"})
			if err == nil {
				t.Fatal("Embed() error = nil, want an error")
			}
			if strings.Contains(err.Error(), testKey) {
				t.Fatal("error exposed the API key")
			}
		})
	}
}

// A failed batch discards the batches that already succeeded. Storing half a
// document's vectors would leave a document that is half retrievable, with
// nothing recording which half.
func TestClientEmbedReturnsNothingWhenALaterBatchFails(t *testing.T) {
	var calls int
	var mu sync.Mutex
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		calls++
		attempt := calls
		mu.Unlock()

		if attempt == 2 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":{"message":"upstream is unwell","code":"internal"}}`)
			return
		}
		var decoded embeddingRequest
		_ = json.NewDecoder(req.Body).Decode(&decoded)
		var data []map[string]any
		for index := range decoded.Input {
			data = append(data, map[string]any{"index": index, "embedding": []float32{1, 1, 1, 1}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer fake.Close()

	result, err := newTestClient(t, fake.URL, 2).Embed(context.Background(), []string{"a", "b", "c", "d"})
	if err == nil {
		t.Fatal("Embed() error = nil, want the failed batch to fail the call")
	}
	if len(result.Vectors) != 0 {
		t.Fatalf("got %d vectors from a failed call, want none", len(result.Vectors))
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || !httpErr.Retryable() {
		t.Fatalf("error = %v, want a retryable HTTPError", err)
	}
}
