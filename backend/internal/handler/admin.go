package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"backend/internal/authz"
	"backend/internal/httpx"
	"github.com/gin-gonic/gin"
)

// AccountAdmin is the account half of the governance use case: who the accounts
// are, and which preset each carries.
type AccountAdmin interface {
	Accounts(ctx context.Context) ([]authz.Account, error)
	SetPreset(ctx context.Context, caller authz.Caller, userID int64, preset string) error
}

// GrantAdmin is the per-item half: the grants written on top of a preset.
type GrantAdmin interface {
	Assignments(ctx context.Context, userID int64) ([]authz.Assignment, error)
	Assign(ctx context.Context, caller authz.Caller, userID int64, item authz.Item, allowed bool) error
	Withdraw(ctx context.Context, userID int64, item authz.Item) error
}

// AdminHandler serves the governance routes (§2.16).
//
// It is what makes the four-layer model reachable at all: without it a preset
// can only be set by hand in the database and a grant can only be written by a
// migration, so the model would be correct and unusable. Every route here is
// mounted behind the administrative surface, and none of them compares a role —
// who may come in is Decide's answer, given by the middleware.
type AdminHandler struct {
	accounts AccountAdmin
	grants   GrantAdmin
	logger   *slog.Logger
}

// NewAdminHandler constructs the governance handler.
func NewAdminHandler(accounts AccountAdmin, grants GrantAdmin, logger *slog.Logger) *AdminHandler {
	return &AdminHandler{accounts: accounts, grants: grants, logger: logger}
}

// accountView is one row of the account list.
//
// The preset travels together with the name the policy reads it as
// (preset_of), because the stored value and the meaning differ for exactly the
// accounts that predate the column: a client that drew the raw string would
// show those accounts as having no preset when they in fact have the whole
// product.
type accountView struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Preset   string `json:"preset"`
	PresetOf string `json:"preset_of"`
	Disabled bool   `json:"disabled"`
}

// grantView is one grant: which item, and which way it was decided.
type grantView struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Allowed bool   `json:"allowed"`
}

// ListUsers returns every account with the two identity layers it carries.
func (h *AdminHandler) ListUsers(c *gin.Context) {
	if h == nil || h.accounts == nil {
		httpx.WriteError(c, http.StatusInternalServerError, "admin_unavailable", "governance is not configured")
		return
	}
	accounts, err := h.accounts.Accounts(c.Request.Context())
	if err != nil {
		h.fail(c, "list_accounts_failed", err)
		return
	}
	listed := make([]accountView, 0, len(accounts))
	for _, account := range accounts {
		listed = append(listed, accountView{
			UserID: account.UserID, Username: account.Username, Role: account.Role,
			Preset: account.Preset, PresetOf: account.PresetOf(), Disabled: account.Disabled,
		})
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"users": listed})
}

// presetRequest is the body of a preset change.
type presetRequest struct {
	Preset string `json:"preset"`
}

// UpdateUser changes one account's preset.
//
// The refusal this route can answer with that matters is `policy_would_be_empty`
// (§2.2): a preset that would leave the account reaching nothing is not a
// stricter account, it is one whose meaning the build has no definition for.
func (h *AdminHandler) UpdateUser(c *gin.Context) {
	if h == nil || h.accounts == nil {
		httpx.WriteError(c, http.StatusInternalServerError, "admin_unavailable", "governance is not configured")
		return
	}
	userID, ok := h.userID(c)
	if !ok {
		return
	}
	var request presetRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "the request body is not a preset change")
		return
	}
	actor, ok := caller(c)
	if !ok {
		return
	}
	if err := h.accounts.SetPreset(c.Request.Context(), actor, userID, request.Preset); err != nil {
		h.failPreset(c, err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"user_id": userID, "preset": request.Preset})
}

// ListGrants returns the decisions recorded about one account.
//
// It answers with the grants alone and not with the effective reach: the
// effective reach is the account's own picture, it is what /me/permissions is
// for, and an administrator reading it here would be reading a second
// implementation of the same question.
func (h *AdminHandler) ListGrants(c *gin.Context) {
	if h == nil || h.grants == nil {
		httpx.WriteError(c, http.StatusInternalServerError, "admin_unavailable", "governance is not configured")
		return
	}
	userID, ok := h.userID(c)
	if !ok {
		return
	}
	assignments, err := h.grants.Assignments(c.Request.Context(), userID)
	if err != nil {
		h.fail(c, "list_grants_failed", err)
		return
	}
	granted := make([]grantView, 0, len(assignments))
	for _, assignment := range assignments {
		granted = append(granted, grantView{
			Kind: string(assignment.Item.Kind), ID: assignment.Item.ID, Allowed: assignment.Allowed,
		})
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"user_id": userID, "grants": granted})
}

// SetGrant turns one item on or off for one account.
//
// It is one route and not two because the row carries the boolean: "open this"
// and "close this" are the same write with a different value, and two routes
// would be two places where the set of grants is re-checked and one of them
// eventually not.
func (h *AdminHandler) SetGrant(c *gin.Context) {
	h.writeGrant(c, true)
}

// WithdrawGrant removes the decision recorded for one item, which returns that
// item to the account's baseline.
//
// The item arrives in the body rather than in the path, because an item is two
// values — a kind and an id — and half of them have an empty id that means
// "every item of this kind". A path segment cannot carry the empty one.
func (h *AdminHandler) WithdrawGrant(c *gin.Context) {
	h.writeGrant(c, false)
}

// writeGrant is the two routes above: opening (or closing) with a boolean, and
// withdrawing by removing the row. They differ only in which call they make,
// and keeping them one function is what stops the two from disagreeing about
// what a well-formed item looks like.
func (h *AdminHandler) writeGrant(c *gin.Context, recording bool) {
	if h == nil || h.grants == nil {
		httpx.WriteError(c, http.StatusInternalServerError, "admin_unavailable", "governance is not configured")
		return
	}
	userID, ok := h.userID(c)
	if !ok {
		return
	}
	var request struct {
		Kind    string `json:"kind"`
		ID      string `json:"id"`
		Allowed *bool  `json:"allowed"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "the request body is not a grant")
		return
	}
	item := authz.Item{Kind: authz.Kind(request.Kind), ID: request.ID}
	if !authz.KnownKind(item.Kind) {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "not a kind this build decides about")
		return
	}
	if !recording {
		if err := h.grants.Withdraw(c.Request.Context(), userID, item); err != nil {
			h.failGrant(c, err)
			return
		}
		httpx.WriteSuccess(c, http.StatusOK, gin.H{"user_id": userID, "withdrawn": toItemView(item)})
		return
	}
	if request.Allowed == nil {
		// An absent boolean is not "false": the caller has not said which way,
		// and defaulting it would turn a malformed request into a revocation.
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "allowed is required")
		return
	}
	actor, ok := caller(c)
	if !ok {
		return
	}
	if err := h.grants.Assign(c.Request.Context(), actor, userID, item, *request.Allowed); err != nil {
		h.failGrant(c, err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{
		"user_id": userID, "grant": grantView{
			Kind: string(item.Kind), ID: item.ID, Allowed: *request.Allowed,
		},
	})
}

// itemView is an item on the wire, in the two fields it has.
type itemView struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

func toItemView(item authz.Item) itemView {
	return itemView{Kind: string(item.Kind), ID: item.ID}
}

// userID reads the account a governance route is about.
func (h *AdminHandler) userID(c *gin.Context) (int64, bool) {
	userID, err := strconv.ParseInt(c.Param("userID"), 10, 64)
	if err != nil || userID <= 0 {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "the account id is not an account")
		return 0, false
	}
	return userID, true
}

func (h *AdminHandler) failPreset(c *gin.Context, err error) {
	switch {
	case errors.Is(err, authz.ErrPolicyWouldBeEmpty):
		httpx.WriteError(c, http.StatusBadRequest, "policy_would_be_empty",
			"that change would leave the account with nothing")
	case errors.Is(err, authz.ErrUnknownPreset):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "not a preset this build writes")
	case errors.Is(err, authz.ErrUnknownUser):
		httpx.WriteError(c, http.StatusNotFound, "not_found", "no such account")
	default:
		h.fail(c, "update_user_failed", err)
	}
}

// failGrant answers a grant write that did not happen.
//
// Both policy refusals are 400s rather than 403s: the caller was allowed to
// make this request — it is the administrator — and what is wrong is the change
// it asked for. A 403 would say the administrator may not govern, which is the
// one sentence this route must never say.
func (h *AdminHandler) failGrant(c *gin.Context, err error) {
	switch {
	case errors.Is(err, authz.ErrPolicyWouldBeEmpty):
		httpx.WriteError(c, http.StatusBadRequest, "policy_would_be_empty",
			"that change would leave the account with nothing")
	case errors.Is(err, authz.ErrNeverForLearners):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "a learner may not be given that")
	case errors.Is(err, authz.ErrItemNotGrantable):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "that item cannot be given by a grant")
	case errors.Is(err, authz.ErrUnknownItem):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "not a kind this build decides about")
	case errors.Is(err, authz.ErrUnknownUser):
		httpx.WriteError(c, http.StatusNotFound, "not_found", "no such account")
	default:
		h.fail(c, "write_grant_failed", err)
	}
}

func (h *AdminHandler) fail(c *gin.Context, code string, err error) {
	if h.logger != nil {
		h.logger.Error("governance request failed", "request_id", httpx.RequestID(c), "error", err)
	}
	httpx.WriteError(c, http.StatusInternalServerError, code, "the governance request could not be served")
}
