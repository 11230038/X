package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"backend/internal/authz"
	"backend/internal/httpx"
	"github.com/gin-gonic/gin"
)

// RelationshipAdmin is the relationship half of the governance use case.
//
// It is one interface for the administrative view and the guardian's own view
// because they read the same rows through the same methods and differ only in
// whose they are: an administrator reads every relationship, a guardian reads
// the ones it holds. Splitting the interface would make the second look like a
// different capability when it is the same read with a narrower filter.
type RelationshipAdmin interface {
	AllRelationships(ctx context.Context) ([]authz.Link, error)
	Relationships(ctx context.Context, guardianID int64) ([]authz.Link, error)
	Link(ctx context.Context, caller authz.Caller, guardianID, learnerID int64, permissions []authz.Permission) (authz.Link, error)
	Revoke(ctx context.Context, linkID string) error
	LinkByID(ctx context.Context, linkID string) (authz.Link, error)
	// GrantPermission takes the caller and RevokePermission does not, and the
	// asymmetry is the data's: a granted permission is a row that records who
	// granted it (granted_by), and a revoked one is a row that is gone.
	GrantPermission(ctx context.Context, caller authz.Caller, linkID string, permission authz.Permission) error
	RevokePermission(ctx context.Context, linkID string, permission authz.Permission) error
}

// GuardianHandler serves the relationship routes (§2.16).
//
// The relationship is the only path in this product that reaches another
// account's material, so every route here exists to make "why is this allowed"
// something that can be pointed at: a row that was created, a permission that
// was granted, and — because revoking is deleting — a row that is not there any
// more.
type GuardianHandler struct {
	guardians RelationshipAdmin
	logger    *slog.Logger
}

// NewGuardianHandler constructs the relationship handler.
func NewGuardianHandler(guardians RelationshipAdmin, logger *slog.Logger) *GuardianHandler {
	return &GuardianHandler{guardians: guardians, logger: logger}
}

// linkView is one relationship on the wire.
type linkView struct {
	LinkID      string   `json:"link_id"`
	GuardianID  int64    `json:"guardian_id"`
	LearnerID   int64    `json:"learner_id"`
	Permissions []string `json:"permissions"`
}

func toLinkView(link authz.Link) linkView {
	permissions := make([]string, 0, len(link.Permissions))
	for _, permission := range link.Permissions {
		permissions = append(permissions, string(permission))
	}
	return linkView{
		LinkID: link.ID, GuardianID: link.GuardianID, LearnerID: link.LearnerID,
		Permissions: permissions,
	}
}

func toLinkViews(links []authz.Link) []linkView {
	views := make([]linkView, 0, len(links))
	for _, link := range links {
		views = append(views, toLinkView(link))
	}
	return views
}

// ListLearners returns the relationships the caller holds as guardian.
//
// "Learners" is the PRD's word for the accounts on the other side of these
// rows, and it is the same list as any other: a link is only ever read from the
// side that holds it, so this route needs no relationship permission of its
// own. What it may not do is read somebody else's — that is
// `/guardian/learners/{userID}/reports`, and there the permission is the point.
func (h *GuardianHandler) ListLearners(c *gin.Context) {
	if h == nil || h.guardians == nil {
		httpx.WriteError(c, http.StatusInternalServerError, "guardians_unavailable", "relationships are not configured")
		return
	}
	actor, ok := caller(c)
	if !ok {
		return
	}
	links, err := h.guardians.Relationships(c.Request.Context(), actor.UserID)
	if err != nil {
		h.fail(c, "list_learners_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"learners": toLinkViews(links)})
}

// ListRelationships returns every relationship in the instance.
func (h *GuardianHandler) ListRelationships(c *gin.Context) {
	if h == nil || h.guardians == nil {
		httpx.WriteError(c, http.StatusInternalServerError, "guardians_unavailable", "relationships are not configured")
		return
	}
	links, err := h.guardians.AllRelationships(c.Request.Context())
	if err != nil {
		h.fail(c, "list_relationships_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"guardians": toLinkViews(links)})
}

// CreateRelationship builds a relationship, with the permissions it starts with.
//
// AUTH-04's three prohibitions are answered here, each with its own code: an
// account may not be its own guardian, the watched account must be a learner,
// and a learner may not be a guardian. They are three codes rather than one
// refusal because they are three different mistakes in the request.
func (h *GuardianHandler) CreateRelationship(c *gin.Context) {
	if h == nil || h.guardians == nil {
		httpx.WriteError(c, http.StatusInternalServerError, "guardians_unavailable", "relationships are not configured")
		return
	}
	var request struct {
		GuardianID  int64    `json:"guardian_id"`
		LearnerID   int64    `json:"learner_id"`
		Permissions []string `json:"permissions"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "the request body is not a relationship")
		return
	}
	if request.GuardianID <= 0 || request.LearnerID <= 0 {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "both accounts are required")
		return
	}
	permissions := make([]authz.Permission, 0, len(request.Permissions))
	for _, name := range request.Permissions {
		permission := authz.Permission(name)
		if !authz.KnownPermission(permission) {
			httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "not a permission this build recognises")
			return
		}
		permissions = append(permissions, permission)
	}
	actor, ok := caller(c)
	if !ok {
		return
	}
	link, err := h.guardians.Link(c.Request.Context(), actor, request.GuardianID, request.LearnerID, permissions)
	if err != nil {
		h.failRelationship(c, err)
		return
	}
	httpx.WriteSuccess(c, http.StatusCreated, gin.H{"guardian": toLinkView(link)})
}

// RevokeRelationship removes a relationship and, with it, everything it allowed.
//
// The id arrives in the body because the table in §2.16 gives this path
// GET/POST/DELETE and nothing under it: there is no `/admin/guardians/{id}`, so
// the only thing that can name a relationship is the body. It is the same shape
// the grants' delete has.
func (h *GuardianHandler) RevokeRelationship(c *gin.Context) {
	if h == nil || h.guardians == nil {
		httpx.WriteError(c, http.StatusInternalServerError, "guardians_unavailable", "relationships are not configured")
		return
	}
	var request struct {
		LinkID string `json:"link_id"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "the request body is not a relationship")
		return
	}
	if request.LinkID == "" {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "link_id is required")
		return
	}
	if err := h.guardians.Revoke(c.Request.Context(), request.LinkID); err != nil {
		h.fail(c, "revoke_relationship_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"link_id": request.LinkID, "revoked": true})
}

// ListPermissions returns what one relationship carries.
func (h *GuardianHandler) ListPermissions(c *gin.Context) {
	link, ok := h.link(c)
	if !ok {
		return
	}
	permissions := make([]string, 0, len(link.Permissions))
	for _, permission := range link.Permissions {
		permissions = append(permissions, string(permission))
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{
		"link_id": link.ID, "learner_id": link.LearnerID, "permissions": permissions,
	})
}

// GrantPermission gives one relationship one permission (AUTH-05).
func (h *GuardianHandler) GrantPermission(c *gin.Context) {
	h.writePermission(c, true)
}

// RevokePermission takes one permission back.
func (h *GuardianHandler) RevokePermission(c *gin.Context) {
	h.writePermission(c, false)
}

// writePermission is the two routes above. Granting twice is not an error and
// revoking a permission the link does not carry is: the first is what a retry
// looks like, and the second means the caller believes something about the
// relationship that the answer has to correct.
func (h *GuardianHandler) writePermission(c *gin.Context, granting bool) {
	if h == nil || h.guardians == nil {
		httpx.WriteError(c, http.StatusInternalServerError, "guardians_unavailable", "relationships are not configured")
		return
	}
	var request struct {
		Permission string `json:"permission"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "the request body is not a permission")
		return
	}
	permission := authz.Permission(request.Permission)
	if !authz.KnownPermission(permission) {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "not a permission this build recognises")
		return
	}
	actor, ok := caller(c)
	if !ok {
		return
	}
	linkID := c.Param("linkID")
	var err error
	if granting {
		err = h.guardians.GrantPermission(c.Request.Context(), actor, linkID, permission)
	} else {
		err = h.guardians.RevokePermission(c.Request.Context(), linkID, permission)
	}
	if err != nil {
		h.failRelationship(c, err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{
		"link_id": linkID, "permission": string(permission), "granted": granting,
	})
}

// link reads the relationship a request names, or answers why it cannot.
func (h *GuardianHandler) link(c *gin.Context) (authz.Link, bool) {
	if h == nil || h.guardians == nil {
		httpx.WriteError(c, http.StatusInternalServerError, "guardians_unavailable", "relationships are not configured")
		return authz.Link{}, false
	}
	linkID := c.Param("linkID")
	if linkID == "" {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "a relationship id is required")
		return authz.Link{}, false
	}
	link, err := h.guardians.LinkByID(c.Request.Context(), linkID)
	if err != nil {
		h.failRelationship(c, err)
		return authz.Link{}, false
	}
	return link, true
}

// failRelationship answers a relationship write that did not happen.
func (h *GuardianHandler) failRelationship(c *gin.Context, err error) {
	switch {
	case errors.Is(err, authz.ErrSelfLink):
		httpx.WriteError(c, http.StatusBadRequest, "guardian_self_link", "an account may not be its own guardian")
	case errors.Is(err, authz.ErrLinkExists):
		// 409 rather than 400: the request was well formed and the state is
		// what refused it, which is the difference between "ask again
		// differently" and "this is already true".
		httpx.WriteError(c, http.StatusConflict, "guardian_link_exists", "that relationship already exists")
	case errors.Is(err, authz.ErrNotALearner):
		httpx.WriteError(c, http.StatusBadRequest, "guardian_not_learner", "the watched account is not a learner")
	case errors.Is(err, authz.ErrGuardianIsLearner):
		httpx.WriteError(c, http.StatusBadRequest, "guardian_not_learner", "a learner may not be a guardian")
	case errors.Is(err, authz.ErrUnknownPermission):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "not a permission this build recognises")
	case errors.Is(err, authz.ErrPermissionNotHeld):
		httpx.WriteError(c, http.StatusNotFound, "not_found", "that relationship does not carry that permission")
	case errors.Is(err, authz.ErrLinkNotFound):
		httpx.WriteError(c, http.StatusNotFound, "not_found", "no such relationship")
	case errors.Is(err, authz.ErrUnknownUser):
		httpx.WriteError(c, http.StatusNotFound, "not_found", "no such account")
	default:
		h.fail(c, "guardian_request_failed", err)
	}
}

func (h *GuardianHandler) fail(c *gin.Context, code string, err error) {
	if h.logger != nil {
		h.logger.Error("relationship request failed", "request_id", httpx.RequestID(c), "error", err)
	}
	httpx.WriteError(c, http.StatusInternalServerError, code, "the relationship request could not be served")
}
