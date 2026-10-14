package handler

// The memory layer over HTTP (§2.14): the twelve routes that read and change what
// the system has admitted about an account about itself.
//
// Three things about this file are decisions rather than shape.
//
// Every route is owner-scoped, and the owner is the bearer token's account. There
// is no `owner_id` in any body or query string here, which is what makes "you
// cannot read somebody else's memory" (MEM-04) a property of the routes rather
// than a check each handler has to remember to make.
//
// Nothing here writes an observation. §2.14 has no route that hands a surface's
// observation in, and that is the fourth path's safety rather than an omission
// (§2.7): the surface and the kind a fact is admitted under are what the gate
// keys on, so a route able to choose them would be a route able to skip the gate.
// The one thing a user may write is their own statement, which is `POST
// /memory/facts` — where the two are constants and the statement is the only
// input.
//
// There is no `DELETE /memory/facts/:factID`, deliberately. MEM-02's three verbs
// are edit, retract and cleanup: a retraction leaves the row and is reversible,
// and the deletion is the wide, explicit, previewed batch of cleanup.go. A
// deletion of one row named in a URL would be an action nobody can describe
// afterwards.
//
// What the domain decides is not re-decided here. The vocabularies — a surface, a
// kind, a status, an action — travel as the strings they arrived as and are
// refused by the domain, which is where those words live; a handler that kept its
// own copy of a closed vocabulary would be a second definition of it, free to
// drift.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"backend/internal/httpx"
	"backend/internal/memory"
	"backend/internal/middleware"
	"github.com/gin-gonic/gin"
)

// Memory is the memory layer's use case as HTTP reaches it.
type Memory interface {
	// The reads: one account's observations, its facts, one fact with the
	// evidence chain it was admitted on, and the trail of what was done to any
	// of it.
	Traces(ctx context.Context, query memory.TraceQuery) ([]memory.Trace, error)
	Facts(ctx context.Context, query memory.FactQuery) ([]memory.Fact, error)
	Fact(ctx context.Context, ownerID int64, factID string) (memory.FactWithEvidence, error)
	Audits(ctx context.Context, query memory.AuditQuery) ([]memory.Audit, error)
	// The three verbs and the user's own way in (§2.7, §2.14): edit, state,
	// retract, restore.
	Edit(ctx context.Context, request memory.EditRequest) (memory.FactWithEvidence, error)
	State(ctx context.Context, request memory.StateRequest) (memory.FactWithEvidence, error)
	Retract(ctx context.Context, request memory.FactRequest) (memory.Fact, error)
	Restore(ctx context.Context, request memory.FactRequest) (memory.Fact, error)
	// The two batches, each with a preview.
	Dedupe(ctx context.Context, request memory.DedupeRequest) (memory.DedupeReport, error)
	Cleanup(ctx context.Context, query memory.CleanupQuery) (memory.CleanupReport, error)
	// The synthesis, computed from L2 on every read, and the account being handed
	// everything this layer holds about it.
	Synthesis(ctx context.Context, ownerID int64) (memory.Synthesis, error)
	Export(ctx context.Context, request memory.ExportRequest) (memory.Export, error)
}

// MemoryHandler handles the memory layer.
type MemoryHandler struct {
	memory Memory
	logger *slog.Logger
}

// NewMemoryHandler constructs the memory HTTP handler.
func NewMemoryHandler(memory Memory, logger *slog.Logger) *MemoryHandler {
	return &MemoryHandler{memory: memory, logger: logger}
}

// Traces lists the observations on record, newest first (§2.14).
//
// This is L1, and all of it: the observations that were believed and the ones
// that were not. "The system heard this and does not believe it" is a state the
// user has to be able to see (MEM-03), and a listing that showed only what was
// believed would hide exactly the rows they most want to look at.
func (h *MemoryHandler) Traces(c *gin.Context) {
	ownerID, ok := h.account(c)
	if !ok {
		return
	}
	since, ok := timeParam(c, "since")
	if !ok {
		return
	}
	until, ok := timeParam(c, "until")
	if !ok {
		return
	}
	limit, err := intParam(c, "limit")
	if err != nil {
		badParam(c, err)
		return
	}
	found, err := h.memory.Traces(c.Request.Context(), memory.TraceQuery{
		OwnerID: ownerID,
		Surface: memory.Surface(strings.TrimSpace(c.Query("surface"))),
		Kind:    memory.Kind(strings.TrimSpace(c.Query("kind"))),
		Since:   since,
		Until:   until,
		Limit:   limit,
	})
	if err != nil {
		h.fail(c, "list_traces_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"traces": toTraceViews(found)})
}

// Facts lists what the system has admitted, most recently changed first.
//
// The status filter is the difference between the two questions this route
// answers: `?status=active` is "what do you believe about me", and naming all
// three is "what have you ever had" (§2.14).
func (h *MemoryHandler) Facts(c *gin.Context) {
	ownerID, ok := h.account(c)
	if !ok {
		return
	}
	before, ok := timeParam(c, "before")
	if !ok {
		return
	}
	limit, err := intParam(c, "limit")
	if err != nil {
		badParam(c, err)
		return
	}
	found, err := h.memory.Facts(c.Request.Context(), memory.FactQuery{
		OwnerID:  ownerID,
		Statuses: statusParam(c),
		Surface:  memory.Surface(strings.TrimSpace(c.Query("surface"))),
		Before:   before,
		Limit:    limit,
	})
	if err != nil {
		h.fail(c, "list_facts_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"facts": toFactViews(found)})
}

// Fact returns one fact with the observations it was admitted on.
//
// The two are one answer because they are one question — "why does the system
// think this?" — and a fact whose evidence were a second request away would be a
// fact the interface shows as an assertion (MEM-01).
func (h *MemoryHandler) Fact(c *gin.Context) {
	ownerID, ok := h.account(c)
	if !ok {
		return
	}
	held, err := h.memory.Fact(c.Request.Context(), ownerID, c.Param("factID"))
	if err != nil {
		h.fail(c, "get_fact_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"fact": toFactDetailView(held)})
}

// EditFact changes what a fact says: its wording, the scope it belongs to, or
// both.
//
// It is a PATCH rather than a PUT because a body that sends one field is not
// sending the other: nil is "leave it as it is", and the domain refuses a body
// that changes nothing rather than treating it as an edit that happened to be a
// no-op.
func (h *MemoryHandler) EditFact(c *gin.Context) {
	ownerID, ok := h.account(c)
	if !ok {
		return
	}
	var request editFactRequest
	if !readJSON(c, maxMemoryRequestBytes, &request) {
		return
	}
	edit := memory.EditRequest{OwnerID: ownerID, FactID: c.Param("factID"), Actor: memory.ActorUser}
	if request.Statement != nil {
		// Passed through as it was typed: a statement is prose the person wrote
		// about themselves and is shown back to them, so the handler does not
		// reword it. A blank one is the domain's to refuse.
		edit.Statement = request.Statement
	}
	if request.Scope != nil {
		// The scope is a name the synthesis groups by, and a name is read by its
		// trimmed spelling: a scope of "   " is no scope, not a fourth one.
		scope := strings.TrimSpace(*request.Scope)
		edit.Scope = &scope
	}
	edited, err := h.memory.Edit(c.Request.Context(), edit)
	if err != nil {
		h.fail(c, "edit_fact_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"fact": toFactDetailView(edited)})
}

// StateFact writes a fact the user wrote themselves (§2.7's fourth path).
//
// The answer is 200 and not 201 because stating something the account already
// believes does not create anything: the observation joins the fact in force, and
// what comes back is that fact as it now stands. What the route does always do is
// leave a line in the trail saying the user wrote it (MEM-02).
func (h *MemoryHandler) StateFact(c *gin.Context) {
	ownerID, ok := h.account(c)
	if !ok {
		return
	}
	var request stateFactRequest
	if !readJSON(c, maxMemoryRequestBytes, &request) {
		return
	}
	written, err := h.memory.State(c.Request.Context(), memory.StateRequest{
		OwnerID:   ownerID,
		Statement: request.Statement,
		Scope:     strings.TrimSpace(request.Scope),
		Actor:     memory.ActorUser,
	})
	if err != nil {
		h.fail(c, "state_fact_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"fact": toFactDetailView(written)})
}

// RetractFact takes a fact back (MEM-02). The row stays and nothing is deleted.
func (h *MemoryHandler) RetractFact(c *gin.Context) {
	h.move(c, "retract_fact_failed", h.memory.Retract)
}

// RestoreFact undoes a retraction, which is what makes a retraction a state
// rather than a mark.
func (h *MemoryHandler) RestoreFact(c *gin.Context) {
	h.move(c, "restore_fact_failed", h.memory.Restore)
}

// move runs the two directions of the same verb.
//
// Retract and restore are one action read two ways — the request is the same, and
// so is the answer, which is the fact in its new state — so they are one method
// taking which direction to run rather than two that would drift apart.
func (h *MemoryHandler) move(c *gin.Context, code string, act func(context.Context, memory.FactRequest) (memory.Fact, error)) {
	ownerID, ok := h.account(c)
	if !ok {
		return
	}
	moved, err := act(c.Request.Context(), memory.FactRequest{
		OwnerID: ownerID, FactID: c.Param("factID"), Actor: memory.ActorUser,
	})
	if err != nil {
		h.fail(c, code, err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"fact": toFactView(moved)})
}

// Dedupe merges the facts in force that say the same thing.
//
// The preview is not a formality here: "one fact in force per assertion" is not
// expressible as a constraint in this engine (§0.2), so duplicates are found
// afterwards and folded, and the preview is the only look a caller gets before
// two rows become one (§2.14). The body's `dry_run` is the whole of the
// difference between the two runs, and the two runs read one owner.
func (h *MemoryHandler) Dedupe(c *gin.Context) {
	ownerID, ok := h.account(c)
	if !ok {
		return
	}
	var request dedupeRequest
	if !readJSON(c, maxMemoryRequestBytes, &request) {
		return
	}
	report, err := h.memory.Dedupe(c.Request.Context(), memory.DedupeRequest{
		OwnerID: ownerID, DryRun: request.DryRun, Actor: memory.ActorUser,
	})
	if err != nil {
		h.fail(c, "dedupe_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"dedupe": toDedupeReportView(report)})
}

// Cleanup forgets a batch of facts on purpose: the one place this package removes
// a row.
//
// The cutoff is in the body and is required, because it is what makes "delete"
// into "delete what was admitted before this date" — a cleanup with no cutoff and
// no filter would be the account going away, which is a different act with a
// different route (deleting the account). `dry_run` reports what would go and
// removes nothing: the two runs of a cleanup are not the same act twice, so the
// preview has to be run first (§2.14).
func (h *MemoryHandler) Cleanup(c *gin.Context) {
	ownerID, ok := h.account(c)
	if !ok {
		return
	}
	var request cleanupRequest
	if !readJSON(c, maxMemoryRequestBytes, &request) {
		return
	}
	before, ok := cutoff(request.Before)
	if !ok {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request",
			"before is not a timestamp, and the cutoff of a cleanup is one (RFC 3339)")
		return
	}
	report, err := h.memory.Cleanup(c.Request.Context(), memory.CleanupQuery{
		OwnerID:  ownerID,
		Before:   before,
		Surface:  memory.Surface(strings.TrimSpace(request.Surface)),
		Statuses: statuses(request.Status),
		DryRun:   request.DryRun,
		Actor:    memory.ActorUser,
	})
	if err != nil {
		h.fail(c, "cleanup_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"cleanup": toCleanupReportView(report)})
}

// Synthesis returns what memory looks like now (§2.6, PRD §11.3).
//
// There is nothing to invalidate and no cache to miss: L3 is a function of L2, so
// this read sees every edit, retraction and merge that has already happened — and
// the fact that it sees them is what §2.6 means by there being no second source of
// truth.
func (h *MemoryHandler) Synthesis(c *gin.Context) {
	ownerID, ok := h.account(c)
	if !ok {
		return
	}
	computed, err := h.memory.Synthesis(c.Request.Context(), ownerID)
	if err != nil {
		h.fail(c, "synthesis_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"synthesis": toSynthesisView(computed)})
}

// Audit lists what has been done to this account's memory, newest first
// (MEM-02).
//
// It is the trail rather than the state, and the two are not the same answer: a
// retracted fact looks like any other retracted fact, and only the trail says who
// took it back and when.
func (h *MemoryHandler) Audit(c *gin.Context) {
	ownerID, ok := h.account(c)
	if !ok {
		return
	}
	limit, err := intParam(c, "limit")
	if err != nil {
		badParam(c, err)
		return
	}
	found, err := h.memory.Audits(c.Request.Context(), memory.AuditQuery{
		OwnerID: ownerID,
		Action:  memory.Action(strings.TrimSpace(c.Query("action"))),
		Limit:   limit,
	})
	if err != nil {
		h.fail(c, "list_audit_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"audits": toAuditViews(found)})
}

// Export hands the account everything this layer holds about it (PRD §11.3).
//
// It is a read that is written down: the account's whole memory goes to whoever
// is holding the request, so the trail records that it did (ActionMemoryExported).
// What it carries is the graph — every fact with its evidence, every observation
// including the ones that support nothing, and the trail — and what it must not
// carry is anything with a credential in it, which is a rule of what L2 stores
// rather than of this route: a credential-shaped observation is never admitted
// (§2.7's sensitive kinds), so it cannot be exported as a fact.
func (h *MemoryHandler) Export(c *gin.Context) {
	ownerID, ok := h.account(c)
	if !ok {
		return
	}
	exported, err := h.memory.Export(c.Request.Context(), memory.ExportRequest{
		OwnerID: ownerID, Actor: memory.ActorUser,
	})
	if err != nil {
		h.fail(c, "export_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"export": toExportView(exported)})
}

// account reads the bearer token's account, which owns everything this handler
// touches, and reports whether the request can go on.
//
// It answers the request itself when it cannot. An unconfigured handler is a 500,
// and a route reached with no account is a 401 — which cannot happen while every
// route here is behind RequireAuth (§2.14) and is still answered rather than
// trusted, because a route that arrived without an account is a route whose
// middleware was removed.
func (h *MemoryHandler) account(c *gin.Context) (int64, bool) {
	if h == nil || h.memory == nil {
		httpx.WriteError(c, http.StatusInternalServerError, "memory_unavailable", "the memory layer is not configured")
		return 0, false
	}
	user, ok := middleware.CurrentUser(c)
	if !ok || user.UserID <= 0 {
		if h.logger != nil {
			h.logger.Error("a memory route was reached without an account", "request_id", httpx.RequestID(c))
		}
		httpx.WriteError(c, http.StatusUnauthorized, "authentication_failed", "this route needs an account")
		return 0, false
	}
	return user.UserID, true
}

// fail reports a memory failure, separating what the caller can act on from what
// only an operator can.
//
// A fact the caller's account does not have is a 404 rather than a 403, and that
// is deliberate: another account's fact id is indistinguishable from one that
// does not exist, so a 403 would answer the question "does this id exist" for
// somebody who has no business asking it. The two conflicts are a fact that says
// something already on record (ErrDuplicate) and one that moved underneath a
// write (ErrStaleState) — the caller's answer to the second is to read it again
// and decide, which is why it is not a 500.
func (h *MemoryHandler) fail(c *gin.Context, code string, err error) {
	switch {
	case errors.Is(err, memory.ErrNotFound):
		httpx.WriteError(c, http.StatusNotFound, "not_found", "no such fact")
	case errors.Is(err, memory.ErrInvalidRequest):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", detail(err, memory.ErrInvalidRequest))
	case errors.Is(err, memory.ErrDuplicate):
		httpx.WriteError(c, http.StatusConflict, "conflict", detail(err, memory.ErrDuplicate))
	case errors.Is(err, memory.ErrStaleState):
		httpx.WriteError(c, http.StatusConflict, "stale_state", detail(err, memory.ErrStaleState))
	default:
		if h.logger != nil {
			h.logger.Error("memory request failed", "error", err, "request_id", httpx.RequestID(c))
		}
		httpx.WriteError(c, http.StatusInternalServerError, code, "the memory request failed")
	}
}
