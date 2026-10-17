// Package video searches a public video site and answers what came back in the
// small shape this service serves.
//
// Only Bilibili is implemented, and only search: the site's official player is
// embedded by the client, so this package never downloads media, never reads a
// subtitle track and never stores what it found. The upstream API is public but
// undocumented and unsigned requests are refused by risk control, which is why
// the client signs each search (wbi.go) instead of calling the bare endpoint.
package video

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"backend/internal/outbound"
)

const (
	// maxResponseBodyBytes bounds one upstream response. A page of twenty search
	// results is a few hundred kilobytes; this ceiling is far above that and far
	// below anything that could exhaust memory.
	maxResponseBodyBytes = 8 << 20

	// MaxQueryRunes is the longest search the handler and the client accept. It
	// is a bound rather than a target: a keyword is a word or a sentence, and a
	// request that carries a chapter is a request this endpoint cannot serve.
	MaxQueryRunes = 100

	// MaxPage is the last page either side will ask for. The upstream exposes
	// more, but results that far in are no longer answers to the keyword, and
	// walking them is how an account turns a search box into a crawler.
	MaxPage = 50
)

// ErrInvalidQuery is the empty-or-too-long query. The handler checks the same
// two bounds before calling, because a malformed query is the client's to fix
// and deserves a 400 rather than a failed upstream call; this copy is what keeps
// the domain from accepting something it cannot search.
var ErrInvalidQuery = errors.New("video: invalid query")

// Options is what the client needs to exist. Every value comes from
// configuration; none of them comes from a request.
type Options struct {
	// BaseURL is a prefix, not an endpoint: the client appends its own paths.
	BaseURL string
	// Timeout bounds one call, including reading the body. It is the client's
	// whole deadline rather than a header deadline, because a slow body is as
	// much of a hang as a slow header.
	Timeout time.Duration
	// CacheTTL is how long one keyword's page is reused before the upstream is
	// asked again.
	CacheTTL time.Duration
	// Dial is the outbound boundary every connection passes through (§2.6).
	Dial outbound.DialFunc
}

// Validate refuses a configuration that would fail at the first search.
func (o Options) Validate() error {
	if strings.TrimSpace(o.BaseURL) == "" {
		return fmt.Errorf("video: base URL is required")
	}
	if o.Timeout <= 0 {
		return fmt.Errorf("video: timeout must be positive")
	}
	if o.CacheTTL <= 0 {
		return fmt.Errorf("video: cache TTL must be positive")
	}
	if o.Dial == nil {
		return fmt.Errorf("video: a dial boundary is required")
	}
	return nil
}

// Query is one search: the words the person typed and which page of the answer
// they are on. Page is one-based; the client treats a zero as the first page.
type Query struct {
	Text string
	Page int
}

// Result is one video, normalised. The URLs are built here rather than by the
// client so that a browser is never handed an address assembled out of anything
// but the upstream's video id.
type Result struct {
	VideoID         string
	Title           string
	Author          string
	CoverURL        string
	DurationSeconds int64
	PlayCount       int64
	// PublishedAt is the upstream's publication instant, in Unix seconds.
	PublishedAt int64
	WatchURL    string
	EmbedURL    string
}

// SearchResult is one page of results plus whether another page exists.
type SearchResult struct {
	Query   string
	Page    int
	HasMore bool
	Items   []Result
}

// HTTPError is a failed answer from the upstream: a non-2xx status or a body
// whose envelope says the call did not succeed. Its message is kept for the log
// and is not sent to a client — what a caller can act on is "the search is
// unavailable", not the upstream's wording.
type HTTPError struct {
	StatusCode int
	Code       int
	Message    string
}

func (e *HTTPError) Error() string {
	switch {
	case e.Message == "":
		return fmt.Sprintf("video: upstream returned status %d", e.StatusCode)
	case e.Code != 0:
		return fmt.Sprintf("video: upstream returned status %d (code %d): %s", e.StatusCode, e.Code, e.Message)
	default:
		return fmt.Sprintf("video: upstream returned status %d: %s", e.StatusCode, e.Message)
	}
}
