// Package embed turns text into vectors through an OpenAI-compatible
// /embeddings endpoint.
//
// It is a second provider client rather than part of internal/llm because it is
// a second provider: the chat model serves no embedding endpoint at all, so the
// two can share nothing but the wire style they happen to have in common.
package embed

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"backend/internal/outbound"
)

// Vector is one embedding, as the provider returned it.
type Vector []float32

// String renders the vector in the textual form seekdb's vector column takes,
// which is how it reaches the database: the arithmetic happens there, and
// nothing in this process ever reads a vector back out.
func (v Vector) String() string {
	var builder strings.Builder
	builder.WriteByte('[')
	for index, value := range v {
		if index > 0 {
			builder.WriteByte(',')
		}
		// 'f' rather than 'g': 'g' switches to exponent notation for small
		// magnitudes, and an exponent is a token the database's parser is not
		// documented to take.
		builder.WriteString(strconv.FormatFloat(float64(value), 'f', -1, 32))
	}
	builder.WriteByte(']')
	return builder.String()
}

// Usage is the provider's own accounting, summed over the requests one call
// took. It is read for cost and diagnosis only: nothing decides anything by it.
type Usage struct {
	PromptTokens int
	TotalTokens  int
}

// Result is what one call to Embed returns.
type Result struct {
	// Vectors has one entry per input text, in the order the texts were given.
	Vectors []Vector
	Usage   Usage
}

// The two ways a response can disagree with the request. Both are errors rather
// than adjustments: padding a short vector, truncating a long one, or pairing a
// vector with the wrong text would all corrupt the index silently, and a
// corrupted index answers questions with the wrong evidence.
var (
	// ErrDimensionMismatch reports a vector whose width is not the width the
	// column holds.
	ErrDimensionMismatch = errors.New("embed: provider returned a vector of the wrong width")
	// ErrVectorCount reports a response that does not describe every input.
	ErrVectorCount = errors.New("embed: provider did not return one vector per input")
)

// Options configures a Client.
type Options struct {
	// BaseURL is a prefix, not the endpoint: the client appends /embeddings,
	// the way the chat client appends /chat/completions.
	BaseURL string
	// APIKey is sent as a bearer token when it is not empty, and never appears
	// in an error message, a log line, or a returned value.
	APIKey string
	Model  string
	// Dimension is the width every returned vector must have.
	Dimension int
	// BatchSize is how many texts one request carries. The client splits longer
	// input itself, so callers pass everything they have.
	BatchSize int
	// Timeout bounds how long a request may take to start responding. The
	// caller's context bounds everything else.
	Timeout time.Duration
	// Dial is where this client's requests are allowed to go, produced by
	// internal/outbound from a policy. It is required, and Validate says so: the
	// embedding provider is somewhere on the internet and a client that could be
	// built without an answer to "which addresses may this reach" is the mistake
	// §2.5 is about.
	Dial outbound.DialFunc
}

// Validate checks that a client could work with these options. It deliberately
// does not re-check what configuration already checked: it exists so that a
// client built directly — by a test, or by a future caller — cannot silently
// end up with a zero dimension that rejects every vector as wrong.
func (o Options) Validate() error {
	for _, field := range []struct{ key, value string }{
		{"base URL", o.BaseURL},
		{"model", o.Model},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("embed: %s must not be empty", field.key)
		}
	}
	if o.Dimension <= 0 {
		return fmt.Errorf("embed: dimension must be a positive integer")
	}
	if o.BatchSize <= 0 {
		return fmt.Errorf("embed: batch size must be a positive integer")
	}
	if o.Timeout <= 0 {
		return fmt.Errorf("embed: timeout must be a positive duration")
	}
	if o.Dial == nil {
		return fmt.Errorf("embed: a dialer is required, so that the client cannot reach an address no policy allowed")
	}
	return nil
}

// Embedder is the seam the index worker goes through, so that the worker's own
// tests can drive it without a provider.
type Embedder interface {
	// Embed returns one vector per text, in the order the texts were given. An
	// empty input is not a request: it returns an empty result without calling
	// the provider. A cancelled ctx surfaces as ctx.Err().
	Embed(ctx context.Context, texts []string) (Result, error)
}
