package handler

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"backend/internal/authz"
	"backend/internal/dashboard"
	"backend/internal/httpx"
	"github.com/gin-gonic/gin"
)

// Dashboard is the "continue studying" view consumed by the HTTP handler.
type Dashboard interface {
	Read(context.Context, authz.Caller) (dashboard.View, error)
}

// DashboardHandler handles the one dashboard endpoint.
type DashboardHandler struct {
	view   Dashboard
	logger *slog.Logger
}

// NewDashboardHandler constructs the dashboard HTTP handler.
func NewDashboardHandler(view Dashboard, logger *slog.Logger) *DashboardHandler {
	return &DashboardHandler{view: view, logger: logger}
}

type dashboardEntryView struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Context string `json:"context,omitempty"`
	// UpdatedAt is when the thing last moved, and it is absent for a thing whose
	// domain has no such time — an entry that has never been touched.
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

type dashboardNextView struct {
	Kind             string   `json:"kind"`
	PathID           string   `json:"path_id"`
	KnowledgePointID string   `json:"knowledge_point_id,omitempty"`
	Title            string   `json:"title"`
	Reason           string   `json:"reason"`
	EvidenceIDs      []string `json:"evidence_ids"`
}

// dashboardView is the whole page.
//
// Every list is present and empty rather than null for a person with nothing on
// it: an empty history is an answer, and this is the same rule the path list
// follows (§2.12). The client draws four lists and reads each one the same way
// whether or not there is anything in it.
type dashboardView struct {
	Sessions []dashboardEntryView `json:"sessions"`
	Paths    []dashboardEntryView `json:"paths"`
	Reviews  []dashboardEntryView `json:"reviews"`
	Next     []dashboardNextView  `json:"next"`
}

// Get returns the dashboard.
//
// It is a GET that reads several domains and writes none, so it is safe to call
// whenever a page opens. A failure is a 500 and not a partial page: the one
// thing this view must not do is report somebody's history as shorter than it is
// (§2.12 第 2 条), and a category silently missing would do exactly that.
func (h *DashboardHandler) Get(c *gin.Context) {
	if h == nil || h.view == nil {
		httpx.WriteError(c, http.StatusInternalServerError, "dashboard_unavailable", "the dashboard is not configured")
		return
	}
	actor, ok := caller(c)
	if !ok {
		return
	}
	view, err := h.view.Read(c.Request.Context(), actor)
	if err != nil {
		if h.logger != nil {
			h.logger.Error("dashboard request failed", "error", err, "request_id", httpx.RequestID(c))
		}
		httpx.WriteError(c, http.StatusInternalServerError, "read_dashboard_failed", "the dashboard could not be read")
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, dashboardView{
		Sessions: toDashboardEntries(view.Sessions),
		Paths:    toDashboardEntries(view.Paths),
		Reviews:  toDashboardEntries(view.Reviews),
		Next:     toDashboardNext(view.Next),
	})
}

func toDashboardEntries(entries []dashboard.Entry) []dashboardEntryView {
	listed := make([]dashboardEntryView, 0, len(entries))
	for _, entry := range entries {
		view := dashboardEntryView{ID: entry.ID, Title: entry.Title, Context: entry.Context}
		// A zero time is "never touched" rather than a moment in 1970, and it is
		// left out so that a client sorting by this field does not have to know
		// which of its values means nothing.
		if !entry.UpdatedAt.IsZero() {
			at := entry.UpdatedAt
			view.UpdatedAt = &at
		}
		listed = append(listed, view)
	}
	return listed
}

func toDashboardNext(next []dashboard.Recommendation) []dashboardNextView {
	listed := make([]dashboardNextView, 0, len(next))
	for _, recommendation := range next {
		ids := make([]string, 0, len(recommendation.EvidenceIDs))
		ids = append(ids, recommendation.EvidenceIDs...)
		listed = append(listed, dashboardNextView{
			Kind:             string(recommendation.Kind),
			PathID:           recommendation.PathID,
			KnowledgePointID: recommendation.KnowledgePointID,
			Title:            recommendation.Title,
			Reason:           recommendation.Reason,
			EvidenceIDs:      ids,
		})
	}
	return listed
}
