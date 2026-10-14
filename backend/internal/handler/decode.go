package handler

// The one body reader the write endpoints share.
//
// The four handlers that take a body take one the same way: a small JSON object
// that is refused whole when it is malformed or oversized, with a sentence the
// client can act on. What differs between them is only how much of it there can
// be, and that is the argument.

import (
	"encoding/json"
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
