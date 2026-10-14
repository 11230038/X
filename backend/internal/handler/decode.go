package handler

// The body readers the write endpoints share.
//
// The handlers that take a body take one the same way: a small body that is
// refused whole when it is malformed or oversized, with a sentence the client can
// act on. What differs between them is only how much of it there can be, and that
// is the argument.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"backend/internal/httpx"
	"github.com/gin-gonic/gin"
)

// Body bounds, per shape rather than per handler. A body over its bound is cut
// by the reader and refused as malformed, which is the honest answer: a request
// that large is not one of these requests, and parsing as much of it as fits
// would be acting on half of what was sent.
const (
	// The largest session body carries a title, and the largest question body a
	// filter or a set of marks.
	maxSessionRequestBytes  int64 = 4 << 10
	maxQuestionRequestBytes int64 = 8 << 10
	// The largest knowledge base body carries a query, and this is far more
	// than a query needs.
	maxKBRequestBytes int64 = 8 << 10
	// The largest reading body carries a coordinate and the note written beside
	// one. A note is a sentence or two, so this is generous; what it must not be
	// is unbounded, because a note is stored.
	maxReadingRequestBytes int64 = 8 << 10
	// The largest question import is a file of questions, one to a line, so it is
	// the one body here that is not an object. It is measured in questions rather
	// than in bytes — a question is a few hundred bytes, so this is a file of some
	// hundreds of them — and it is a bound rather than a limit to grow: importing
	// more questions than this is a job for a migration, not for a request that
	// has to finish inside one turn.
	maxQuestionImportBytes int64 = 256 << 10
)

// readJSON reads a JSON body into into and reports whether the caller should go
// on. It answers the request itself when it cannot: a handler that read the
// error and had to remember which response to write is a handler that will
// forget once.
func readJSON(c *gin.Context, limit int64, into any) bool {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, limit))
	if err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "the request body could not be read")
		return false
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "a request body is required")
		return false
	}
	if err := json.Unmarshal(body, into); err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "the request body is not a JSON object")
		return false
	}
	return true
}

// readText reads a body as text, for the one request whose body is a file rather
// than an object (the question import, §2.13).
//
// It bounds the body the way readJSON's limit does not: an oversized body is
// refused as oversized and not cut. The difference matters here because the body
// is a sequence of lines — a reader that cut it at a line boundary would hand the
// import a shorter file and get back a report of a file the client did not send,
// which is the one failure a preview must not have.
func readText(c *gin.Context, limit int64) (string, bool) {
	responseWriter := http.ResponseWriter(c.Writer)
	if unwrapper, ok := responseWriter.(interface{ Unwrap() http.ResponseWriter }); ok {
		responseWriter = unwrapper.Unwrap()
	}
	c.Request.Body = http.MaxBytesReader(responseWriter, c.Request.Body, limit)

	body, err := io.ReadAll(c.Request.Body)
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		httpx.WriteError(c, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large")
		return "", false
	case err != nil:
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "the request body could not be read")
		return "", false
	}
	text := string(body)
	if strings.TrimSpace(text) == "" {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "a request body is required")
		return "", false
	}
	return text, true
}
