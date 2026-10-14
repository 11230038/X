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

// PermissionsReader is the use case behind GET /me/permissions: one account's
// own picture of what it may reach.
type PermissionsReader interface {
	Self(ctx context.Context, caller authz.Caller) (authz.Self, error)
}

// PermissionsHandler answers what the caller may reach.
//
// It exists because a screen has to be able to draw a refusal before the refusal
// happens (§2.16): the difference between "you are not logged in" and "you are
// logged in and may not" is a difference a person acts on differently, and a
// client that could only learn which by trying would have to make a request it
// knows will fail in order to find out whether to show a door.
//
// Every answer comes from the policy's own function, so this route reports what
// the middleware enforces rather than a second reading of the same rules.
type PermissionsHandler struct {
	permissions PermissionsReader
	logger      *slog.Logger
}

// NewPermissionsHandler constructs the handler.
func NewPermissionsHandler(permissions PermissionsReader, logger *slog.Logger) *PermissionsHandler {
	return &PermissionsHandler{permissions: permissions, logger: logger}
}

// permissionItem is one item and the policy's answer about it.
//
// The layer and the reason travel with the boolean because they are what the
// screen needs to explain the answer: "the floor keeps this" and "your guardian
// closed this" are the same `allowed: false` and two different sentences.
type permissionItem struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Allowed bool   `json:"allowed"`
	Layer   string `json:"layer"`
	Reason  string `json:"reason"`
}

// guardianLinkView is one relationship the caller holds as guardian.
//
// It is the caller's own links — the ones it is the guardian in — which is why
// this route needs no permission check to report them: a link is only ever read
// from the side that created it.
type guardianLinkView struct {
	LinkID      string   `json:"link_id"`
	LearnerID   int64    `json:"learner_id"`
	Permissions []string `json:"permissions"`
}

type permissionsResponse struct {
	UserID int64 `json:"user_id"`
	// Role is reported so the client can draw the administrative door without
	// asking a second route; it is not a decision, and nothing in the request
	// path reads it from here.
	Role     string             `json:"role"`
	Preset   string             `json:"preset"`
	Items    []permissionItem   `json:"items"`
	Guardian []guardianLinkView `json:"guardian"`
}

// Get answers the caller's own reach (AUTH-03, §2.16).
//
// It is mounted without a surface requirement on purpose: an account restricted
// out of everything must still be able to ask why, and a route that refused it
// would leave the one screen that explains the refusal unreachable.
func (h *PermissionsHandler) Get(c *gin.Context) {
	actor, ok := caller(c)
	if !ok {
		return
	}
	if h.permissions == nil {
		h.writePermissionsFailure(c, errors.New("authorization service is not configured"))
		return
	}
	self, err := h.permissions.Self(c.Request.Context(), actor)
	if err != nil {
		h.writePermissionsFailure(c, err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, permissionsResponse{
		UserID:   actor.UserID,
		Role:     actor.Role,
		Preset:   self.Preset,
		Items:    permissionItems(self.Items),
		Guardian: guardianLinks(self.Guardian),
	})
}

// permissionItems renders the decisions. Both lists are built non-nil so that an
// account with no grants, and one with no learners, are answered with an empty
// array rather than a null the client has to special-case.
func permissionItems(decisions []authz.Decision) []permissionItem {
	items := make([]permissionItem, 0, len(decisions))
	for _, decision := range decisions {
		items = append(items, permissionItem{
			Kind:    string(decision.Item.Kind),
			ID:      decision.Item.ID,
			Allowed: decision.Allowed,
			Layer:   string(decision.Layer),
			Reason:  decision.Reason,
		})
	}
	return items
}

func guardianLinks(links []authz.Link) []guardianLinkView {
	views := make([]guardianLinkView, 0, len(links))
	for _, link := range links {
		permissions := make([]string, 0, len(link.Permissions))
		for _, permission := range link.Permissions {
			permissions = append(permissions, string(permission))
		}
		views = append(views, guardianLinkView{
			LinkID: link.ID, LearnerID: link.LearnerID, Permissions: permissions,
		})
	}
	return views
}

func (h *PermissionsHandler) writePermissionsFailure(c *gin.Context, err error) {
	if h.logger != nil {
		h.logger.Error("reading permissions failed",
			"request_id", httpx.RequestID(c), "error", err)
	}
	httpx.WriteError(c, http.StatusInternalServerError, "permissions_unavailable",
		"authorization is unavailable")
}
