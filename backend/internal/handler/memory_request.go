package handler

// What a body may send to the memory routes, and how a query string is read.
//
// The bodies are here rather than beside the handlers because they are the one
// part of this surface the domain does not define: memory.EditRequest and its
// neighbours are what the domain decides on, and these are the shapes a client
// writes. Keeping them together is what makes "the wire says less than the domain
// accepts" a single thing to read — a fact id, for instance, is in the URL here
// and in the request struct only because the domain puts it there.

import (
	"net/http"
	"strings"
	"time"

	"backend/internal/httpx"
	"backend/internal/memory"
	"github.com/gin-gonic/gin"
)

// editFactRequest changes what a fact says.
//
// Both fields are pointers, and that is the whole of the PATCH: a body that sends
// only a statement is not sending a scope, and with plain strings the two would
// be the same request as "set the scope to nothing" — which is a different act,
// and one the domain accepts. Absent means leave it; present-and-empty means set
// it to empty, and the domain is the one that refuses a blank statement.
type editFactRequest struct {
	Statement *string `json:"statement"`
	Scope     *string `json:"scope"`
}

// stateFactRequest is the user writing something about themselves (§2.7).
//
// No surface and no kind: they are constants on this path, which is what makes it
// the fourth path rather than a way around the gate (§2.7). What the user chooses
// is the sentence and, if they want one, the scope it belongs to.
type stateFactRequest struct {
	Statement string `json:"statement"`
	Scope     string `json:"scope"`
}

// dedupeRequest is the merge's only input.
//
// `dry_run` is the entire difference between the two runs — same owner, same
// facts, same fold — so a preview and a commit report the same shape and can be
// compared line by line (§2.14).
type dedupeRequest struct {
	DryRun bool `json:"dry_run"`
}

// cleanupRequest is the account forgetting a batch on purpose.
//
// The cutoff is a string here rather than a time.Time so that a malformed one is
// refused with the route's own message instead of a decode error, and so that an
// absent one arrives as empty: a cleanup with no cutoff is the account going
// away, which is not this route.
type cleanupRequest struct {
	Before  string   `json:"before"`
	Surface string   `json:"surface"`
	Status  []string `json:"status"`
	DryRun  bool     `json:"dry_run"`
}

// timeParam reads an optional RFC 3339 query parameter, and answers the request
// itself when it is not one.
//
// An absent parameter is the zero time, which is what "no bound" is: the domain's
// queries read a zero time as unbounded, so the handler does not have to spell
// the absence out twice.
func timeParam(c *gin.Context, name string) (time.Time, bool) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return time.Time{}, true
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request",
			name+" is not a timestamp, and timestamps here are RFC 3339")
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

// statusParam reads the status filter, which a client names once per status
// (`?status=retracted&status=superseded`).
//
// The words travel as they arrived and are refused by the domain, which is where
// the three of them are defined. An empty value is passed through for the same
// reason: `?status=` is a filter that cannot be honoured, and silently dropping
// it would answer a narrower question than the caller asked.
func statusParam(c *gin.Context) []memory.Status {
	return statuses(c.QueryArray("status"))
}

// statuses reads a list of status words from wherever they arrived.
func statuses(values []string) []memory.Status {
	read := make([]memory.Status, 0, len(values))
	for _, value := range values {
		read = append(read, memory.Status(strings.TrimSpace(value)))
	}
	return read
}

// cutoff reads a cleanup's cutoff, which is a timestamp in a body rather than a
// query string because a cleanup is a POST with the rest of its filters beside it.
// An empty one is left as the zero time, which the domain refuses.
func cutoff(raw string) (time.Time, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, true
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

// badParam answers a query parameter that could not be read. The message is the
// parameter's own, because the caller is the one who has to fix it.
func badParam(c *gin.Context, err error) {
	httpx.WriteError(c, http.StatusBadRequest, "invalid_request", err.Error())
}
