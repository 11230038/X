package handler

// GET /api/v1/usage — what the model calls cost (§2.7, §2.8, §17.1).
//
// It is a report about the caller and it is narrowed to the caller: the account
// comes from the token, and there is no query parameter that names somebody
// else's. What an administrator sees on top of their own calls is the rows that
// belong to nobody, which are the rows written before V5 and which somebody has
// to be able to see (internal/data.OwnerScope) — not another account's.
//
// The wire shape is where AC-13 is checkable by eye. There is no base URL, no
// prompt and no response in the view below, and there is no column behind one:
// the requirement is "numeric usage with no endpoint", and the way to keep it is
// for the answer's shape to have nowhere to put an endpoint rather than for
// somebody to remember to strip one.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"backend/internal/authz"
	"backend/internal/httpx"
	"backend/internal/usage"
	"github.com/gin-gonic/gin"
)

// UsageReader is the usage report as this layer reaches it. It is a read and
// nothing else: nothing outside internal/usage writes a row through the wire,
// and the recorder is not reachable from here.
type UsageReader interface {
	Read(ctx context.Context, query usage.Query) (usage.Report, error)
}

// UsageHandler serves the usage report.
type UsageHandler struct {
	reader UsageReader
	logger *slog.Logger
}

// NewUsageHandler constructs the usage handler.
func NewUsageHandler(reader UsageReader, logger *slog.Logger) *UsageHandler {
	return &UsageHandler{reader: reader, logger: logger}
}

// usageTotalsView is one set of numbers: the calls that came back, split by how
// they came back, and the columns summed over them.
//
// Every field is a sum of a column and none of them is read out of usage_json
// (§2.8). The two timings are sums rather than averages on purpose: an average
// carries a denominator nobody looking at the number can see, and whoever wants
// one has `calls` and a division.
type usageTotalsView struct {
	Calls            int64 `json:"calls"`
	Ok               int64 `json:"ok"`
	Error            int64 `json:"error"`
	Cancelled        int64 `json:"cancelled"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	DurationMs       int64 `json:"duration_ms"`
	TTFTMs           int64 `json:"ttft_ms"`
}

// usageGroupView is one bucket of a broken-down report.
type usageGroupView struct {
	Key        string          `json:"key"`
	Finished   usageTotalsView `json:"finished"`
	Unfinished int64           `json:"unfinished"`
}

// usageReportView is the whole answer.
//
// `unfinished` is the count of calls that did not come back (§2.7), and it sits
// beside the totals rather than inside them: those calls are not in the numbers
// — they produced none — and they are not dirty data either, so a client draws
// them as the state they are. `groups` is present and empty rather than null
// when the window had nothing in it, so that a client reads one shape.
type usageReportView struct {
	Since      string           `json:"since,omitempty"`
	Until      string           `json:"until,omitempty"`
	GroupBy    string           `json:"group_by,omitempty"`
	Finished   usageTotalsView  `json:"finished"`
	Unfinished int64            `json:"unfinished"`
	Groups     []usageGroupView `json:"groups"`
}

// Get answers one report over the caller's own calls.
func (h *UsageHandler) Get(c *gin.Context) {
	if h == nil || h.reader == nil {
		httpx.WriteError(c, http.StatusInternalServerError, "usage_unavailable", "the usage report is not configured")
		return
	}
	actor, ok := caller(c)
	if !ok {
		return
	}
	query, ok := h.query(c, actor)
	if !ok {
		return
	}
	report, err := h.reader.Read(c.Request.Context(), query)
	if err != nil {
		h.fail(c, err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, usageReportView{
		Since:      rendered(report.Since),
		Until:      rendered(report.Until),
		GroupBy:    string(report.GroupBy),
		Finished:   totalsView(report.Finished),
		Unfinished: report.Unfinished,
		Groups:     groupViews(report.Groups),
	})
}

// query reads the narrowing from the query string and refuses what it cannot
// read.
//
// Every unreadable value is refused rather than ignored, for the same reason the
// audit's filter does it: a report that quietly dropped a parameter would be a
// report over a broader window than the one that was asked for, and broader is
// the direction that looks like an answer.
func (h *UsageHandler) query(c *gin.Context, actor authz.Caller) (usage.Query, bool) {
	query := usage.Query{
		OwnerID:   actor.UserID,
		Admin:     actor.IsAdmin(),
		Model:     strings.TrimSpace(c.Query("model")),
		Provider:  strings.TrimSpace(c.Query("provider")),
		Source:    strings.TrimSpace(c.Query("source")),
		SessionID: strings.TrimSpace(c.Query("session_id")),
	}
	if raw := strings.TrimSpace(c.Query("group_by")); raw != "" {
		breakdown := usage.GroupBy(raw)
		if !usage.KnownGroupBy(breakdown) {
			httpx.WriteError(c, http.StatusBadRequest, "invalid_request",
				"group_by is not a breakdown this build serves")
			return usage.Query{}, false
		}
		query.GroupBy = breakdown
	}
	for _, field := range []struct {
		name string
		into *time.Time
	}{
		{"since", &query.Since},
		{"until", &query.Until},
	} {
		raw := strings.TrimSpace(c.Query(field.name))
		if raw == "" {
			continue
		}
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			httpx.WriteError(c, http.StatusBadRequest, "invalid_request", field.name+" is not a timestamp")
			return usage.Query{}, false
		}
		*field.into = value.UTC()
	}
	return query, true
}

// fail answers a report that did not happen.
func (h *UsageHandler) fail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, usage.ErrUnknownGroupBy):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "group_by is not a breakdown this build serves")
	case errors.Is(err, usage.ErrEmptyWindow):
		// Refused rather than served as an empty report: a report of nothing
		// reads as "you spent nothing", which is an answer to a question nobody
		// asked.
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "the window has to end after it starts")
	case errors.Is(err, usage.ErrNoAccount):
		httpx.WriteError(c, http.StatusUnauthorized, "authentication_failed", "this route needs an account")
	default:
		if h.logger != nil {
			h.logger.Error("usage read failed", "request_id", httpx.RequestID(c), "error", err)
		}
		httpx.WriteError(c, http.StatusInternalServerError, "usage_read_failed", "the usage report could not be read")
	}
}

// totalsView lays one set of totals out on the wire.
func totalsView(totals usage.Totals) usageTotalsView {
	return usageTotalsView{
		Calls:            totals.Calls,
		Ok:               totals.Ok,
		Error:            totals.Error,
		Cancelled:        totals.Cancelled,
		PromptTokens:     totals.PromptTokens,
		CompletionTokens: totals.CompletionTokens,
		TotalTokens:      totals.TotalTokens,
		DurationMs:       totals.DurationMs,
		TTFTMs:           totals.TTFTMs,
	}
}

func groupViews(groups []usage.Group) []usageGroupView {
	listed := make([]usageGroupView, 0, len(groups))
	for _, group := range groups {
		listed = append(listed, usageGroupView{
			Key:        group.Key,
			Finished:   totalsView(group.Finished),
			Unfinished: group.Unfinished,
		})
	}
	return listed
}

// rendered writes a window bound as RFC 3339 in UTC, and writes nothing at all
// for an end the caller did not give. The column's own format has no zone, so a
// window read from two places would otherwise be two different moments and
// which one is right is not something a reader can work out afterwards.
func rendered(at *time.Time) string {
	if at == nil {
		return ""
	}
	return at.UTC().Format(time.RFC3339)
}
