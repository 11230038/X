package handler

// GET /api/v1/guardian/learners/{userID}/reports — one learner's progress,
// read by the guardian who holds the relationship (AC-11).
//
// The answer is the learner's own dashboard, and it is deliberately the same
// view rather than a second one shaped for guardians. There is one question —
// what has this account been doing — and two shapes for it would be two answers
// that can disagree, with the one a parent reads being the one nobody looks at.
// What makes this route different is not the shape of the answer but who may
// ask and what asking leaves behind.
//
// The permission is checked before this runs: the route is mounted with
// `view_reports` on the guardian surface, so an account with no relationship,
// or with one that does not carry the permission, is refused in the middleware
// and recorded there as a refusal. What is left for this handler is the
// successful read — which is the one the audit is most needed for, because a
// guardian looking at a learner's material is the only way one account sees
// another's without an administrator deciding each time.

import (
	"log/slog"
	"net/http"
	"strconv"

	"backend/internal/audit"
	"backend/internal/auth"
	"backend/internal/authz"
	"backend/internal/httpx"
	"backend/internal/middleware"
	"github.com/gin-gonic/gin"
)

// ReportsHandler serves one learner's report to a guardian.
type ReportsHandler struct {
	view   Dashboard
	audit  AuditWriter
	logger *slog.Logger
}

// NewReportsHandler constructs the reports handler.
func NewReportsHandler(view Dashboard, writer AuditWriter, logger *slog.Logger) *ReportsHandler {
	return &ReportsHandler{view: view, audit: writer, logger: logger}
}

// Get returns one learner's report.
func (h *ReportsHandler) Get(c *gin.Context) {
	if h == nil || h.view == nil {
		httpx.WriteError(c, http.StatusInternalServerError, "reports_unavailable", "reports are not configured")
		return
	}
	guardian, ok := callerAccount(c)
	if !ok {
		return
	}
	learnerID, ok := h.learnerID(c)
	if !ok {
		return
	}
	// The report is read as the learner, not as the guardian: every domain below
	// answers about the account it is asked about, and passing the guardian's
	// own caller would return the guardian's dashboard with the learner's id in
	// the path — an answer that is wrong in the one way a reader cannot tell.
	view, err := h.view.Read(c.Request.Context(), authz.Caller{UserID: learnerID})
	if err != nil {
		if h.logger != nil {
			h.logger.Error("reading a learner's reports failed",
				"request_id", httpx.RequestID(c), "learner_id", learnerID, "error", err)
		}
		httpx.WriteError(c, http.StatusInternalServerError, "read_reports_failed",
			"the report could not be read")
		return
	}
	// The read has happened and nothing has been sent yet, which is the moment
	// this record has to be written in. A guardian who cannot be recorded has
	// not read the report: the alternative — serve it and log the failure — is
	// an instance where the audit's availability decides whether one account
	// reads another's material unrecorded, and that is the one thing this route
	// exists to be able to answer afterwards.
	if err := h.record(c, guardian, learnerID); err != nil {
		if h.logger != nil {
			h.logger.Error("recording a guardian's report read failed",
				"request_id", httpx.RequestID(c), "learner_id", learnerID, "error", err)
		}
		httpx.WriteError(c, http.StatusInternalServerError, "audit_unavailable",
			"the read could not be recorded, so it was not served")
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, dashboardView{
		Sessions: toDashboardEntries(view.Sessions),
		Paths:    toDashboardEntries(view.Paths),
		Reviews:  toDashboardEntries(view.Reviews),
		Next:     toDashboardNext(view.Next),
	})
}

// learnerID reads the account whose report was asked for.
func (h *ReportsHandler) learnerID(c *gin.Context) (int64, bool) {
	learnerID, err := strconv.ParseInt(c.Param("userID"), 10, 64)
	if err != nil || learnerID <= 0 {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "the learner id is not an account")
		return 0, false
	}
	return learnerID, true
}

// record writes the entry that says who read whose report and under which
// permission.
//
// The layer and the reason are the decision that allowed the request, read from
// the middleware rather than asked for a second time: an audit that re-derived
// the answer could record a different one from the answer that was acted on,
// and the entry would then be about a policy that was never applied.
func (h *ReportsHandler) record(c *gin.Context, guardian auth.User, learnerID int64) error {
	if h.audit == nil {
		return audit.ErrNotConfigured
	}
	detail := audit.Detail{}
	if decision, ok := middleware.AuthorizedDecision(c); ok {
		detail.Layer = string(decision.Layer)
		detail.Reason = decision.Reason
	}
	return h.audit.Record(c.Request.Context(), audit.Event{
		Actor:  audit.Actor{ID: guardian.UserID, Name: guardian.Username, Role: guardian.Role},
		Action: audit.ActionReportsViewed,
		Target: audit.Ref{Kind: audit.TargetAccount, ID: strconv.FormatInt(learnerID, 10)},
		// The subject is the account the entry is about, and it is the only
		// column that answers "who has been looking at this learner" — which is
		// the question a guardian route is eventually asked about itself.
		SubjectID: learnerID,
		Result:    audit.ResultOK,
		Detail:    detail,
		RequestID: httpx.RequestID(c),
	})
}
