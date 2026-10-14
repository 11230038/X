package handler

// GET /api/v1/audit — the governance record, read (§2.16).
//
// It is a read of the instance rather than of the caller, which is why it is
// mounted behind the administrative surface and why nothing in here filters by
// the account on the token. What it may be narrowed by is in the query: the
// three questions asked of an audit are "what did this account do", "what
// happened to this account" and "what happened to this thing".

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"backend/internal/audit"
	"backend/internal/httpx"
	"github.com/gin-gonic/gin"
)

// AuditReader is the audit as this layer reaches it. It is a read and nothing
// else: nothing outside the audit package appends an entry through the wire, and
// the routes here could not do it if they wanted to.
type AuditReader interface {
	List(ctx context.Context, filter audit.Filter) ([]audit.Event, error)
	MaxPageSize() int
}

// AuditWriter is the audit as a handler reaches it: one entry, written. It is
// satisfied by the audit service.
//
// It is a separate interface from AuditReader on purpose. A route that reads the
// audit is not a route that writes to it, and the two are declared apart so that
// neither can be handed to the other's caller by accident — a handler given the
// reader cannot quietly become a writer, and one given the writer cannot read
// the stream.
type AuditWriter interface {
	Record(ctx context.Context, event audit.Event) error
}

// AuditHandler serves the audit stream.
type AuditHandler struct {
	audit  AuditReader
	logger *slog.Logger
}

// NewAuditHandler constructs the audit handler.
func NewAuditHandler(reader AuditReader, logger *slog.Logger) *AuditHandler {
	return &AuditHandler{audit: reader, logger: logger}
}

// auditEventView is one entry on the wire.
//
// The detail is served as it is stored, and that is deliberate: a client that
// wanted a field this shape does not have would be a client asking the audit to
// carry something it deliberately cannot (§2.6).
type auditEventView struct {
	AuditID    int64        `json:"audit_id"`
	ActorID    int64        `json:"actor_id,omitempty"`
	ActorName  string       `json:"actor_name"`
	ActorRole  string       `json:"actor_role"`
	Action     string       `json:"action"`
	TargetKind string       `json:"target_kind"`
	TargetID   string       `json:"target_id"`
	SubjectID  int64        `json:"subject_id,omitempty"`
	Result     string       `json:"result"`
	Detail     audit.Detail `json:"detail,omitempty"`
	RequestID  string       `json:"request_id,omitempty"`
	CreatedAt  string       `json:"created_at"`
}

// List returns the entries a filter selects, newest first.
//
// The time is served as RFC 3339 in UTC rather than as the column's own format,
// because the column's format has no zone: an audit read from two places would
// otherwise be two different moments, and which one is right is not something a
// reader can work out after the fact.
func (h *AuditHandler) List(c *gin.Context) {
	if h == nil || h.audit == nil {
		httpx.WriteError(c, http.StatusInternalServerError, "audit_unavailable", "the audit is not configured")
		return
	}
	filter, ok := h.filter(c)
	if !ok {
		return
	}
	events, err := h.audit.List(c.Request.Context(), filter)
	if err != nil {
		h.fail(c, err)
		return
	}
	listed := make([]auditEventView, 0, len(events))
	for _, event := range events {
		listed = append(listed, auditEventView{
			AuditID: event.ID,
			ActorID: event.Actor.ID, ActorName: event.Actor.Name, ActorRole: event.Actor.Role,
			Action:     string(event.Action),
			TargetKind: string(event.Target.Kind), TargetID: event.Target.ID,
			SubjectID: event.SubjectID,
			Result:    string(event.Result),
			Detail:    event.Detail,
			RequestID: event.RequestID,
			CreatedAt: event.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{
		"events": listed,
		// The cap travels with the answer so that a client can tell a page that
		// is the whole history from a page that is the limit, without asking.
		"max_page_size": h.audit.MaxPageSize(),
	})
}

// filter reads the narrowing from the query and refuses what it cannot read.
//
// Every unreadable value is refused rather than ignored, and that is the whole
// of this function's value: an audit query that quietly dropped a parameter
// would answer a broader question than the one that was asked, and broader is
// the direction that looks like an answer.
func (h *AuditHandler) filter(c *gin.Context) (audit.Filter, bool) {
	var filter audit.Filter
	for _, field := range []struct {
		name string
		into *int64
	}{
		{"actor_id", &filter.ActorID},
		{"subject_id", &filter.SubjectID},
	} {
		raw := c.Query(field.name)
		if raw == "" {
			continue
		}
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			httpx.WriteError(c, http.StatusBadRequest, "invalid_request", field.name+" is not an account id")
			return audit.Filter{}, false
		}
		*field.into = value
	}
	if raw := c.Query("action"); raw != "" {
		action := audit.Action(raw)
		if !audit.KnownAction(action) {
			httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "not an action this build records")
			return audit.Filter{}, false
		}
		filter.Action = action
	}
	if raw := c.Query("target_kind"); raw != "" {
		kind := audit.TargetKind(raw)
		if !audit.KnownTargetKind(kind) {
			httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "not a target this build records")
			return audit.Filter{}, false
		}
		filter.TargetKind = kind
	}
	filter.TargetID = c.Query("target_id")
	for _, field := range []struct {
		name string
		into *time.Time
	}{
		{"since", &filter.Since},
		{"until", &filter.Until},
	} {
		raw := c.Query(field.name)
		if raw == "" {
			continue
		}
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			httpx.WriteError(c, http.StatusBadRequest, "invalid_request", field.name+" is not a timestamp")
			return audit.Filter{}, false
		}
		*field.into = value.UTC()
	}
	if raw := c.Query("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit <= 0 {
			httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "limit is not a page size")
			return audit.Filter{}, false
		}
		filter.Limit = limit
	}
	return filter, true
}

// fail answers an audit read that did not happen.
func (h *AuditHandler) fail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, audit.ErrPageTooLarge):
		// A cap is not a clamp: a page silently shortened to it reads as "this
		// is everything", which is the one wrong answer an audit must not give.
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "that page size is larger than this deployment serves")
	case errors.Is(err, audit.ErrUnknownAction), errors.Is(err, audit.ErrUnknownTargetKind):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "not something this build records")
	default:
		if h.logger != nil {
			h.logger.Error("audit read failed", "request_id", httpx.RequestID(c), "error", err)
		}
		httpx.WriteError(c, http.StatusInternalServerError, "audit_read_failed", "the audit could not be read")
	}
}
