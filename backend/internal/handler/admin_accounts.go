package handler

// The account-lifecycle routes: the four things a super administrator may do
// that an ordinary administrator may not.
//
// They live in their own file because they are a different question from the
// rest of governance. The routes in admin.go decide what an existing account may
// reach; these decide whether an account exists at all, what its password is,
// and whether it may sign in. The gate in front of them is likewise not the
// administrative surface — that surface lets every administrator in — but the
// deployment's allowlist, checked by middleware.RequireSuperAdmin before any of
// these handlers runs.
//
// The four share three answers: a missing account is a 404, a target the
// allowlist protects is a 403, and every change is recorded after it is made.
// Sharing them here is what keeps the fourth from quietly disagreeing with the
// third.

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"backend/internal/audit"
	"backend/internal/auth"
	"backend/internal/authz"
	"backend/internal/httpx"
	"github.com/gin-gonic/gin"
)

// AccountLifecycle is the account half only a super administrator reaches: the
// four ways an account comes into being, goes away, or has its credential or
// sign-in state changed.
//
// It is separate from AccountAdmin and AccountSessions even though auth.Service
// satisfies all three, because the interfaces are the routes' vocabulary rather
// than the implementation's shape: a handler that could delete any account it
// was handed would be a handler whose reach is whatever it was wired with.
type AccountLifecycle interface {
	CreateAccount(context.Context, auth.CreateAccountInput) (auth.User, error)
	DeleteAccount(context.Context, int64) error
	ResetPassword(context.Context, int64, string) error
	SetDisabled(context.Context, int64, bool) error
}

// superAdminOf answers from the deployment's allowlist. A nil handler or a nil
// predicate is nobody, which is what the tests of the older governance routes
// pass: they predate the allowlist and do not exercise it.
func (h *AdminHandler) superAdminOf(userID int64) bool {
	return h != nil && h.superAdmin != nil && h.superAdmin(userID)
}

// createUserRequest is the body of a provisioning.
//
// Role and preset are required by the service rather than defaulted here: an
// administrator creating an account is deciding what it is, and a request that
// forgot half of that decision should be told so rather than handed the most
// common answer.
type createUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
	Preset   string `json:"preset"`
}

// CreateUser provisions an account. It answers 201 with the account and no
// token: the administrator is creating somebody else's credential, not signing
// that person in.
func (h *AdminHandler) CreateUser(c *gin.Context) {
	if !h.lifecycleReady(c) {
		return
	}
	var request createUserRequest
	if !readJSON(c, maxAdminRequestBytes, &request) {
		return
	}
	user, err := h.lifecycle.CreateAccount(c.Request.Context(), auth.CreateAccountInput{
		Username: request.Username,
		Password: request.Password,
		Role:     request.Role,
		Preset:   request.Preset,
	})
	if err != nil {
		h.failLifecycle(c, "create_user_failed", err)
		return
	}
	if !h.writeAccountAudit(c, audit.ActionAccountProvisioned, user.UserID) {
		return
	}
	httpx.WriteSuccess(c, http.StatusCreated, gin.H{"user": accountView{
		UserID: user.UserID, Username: user.Username, Role: user.Role,
		Preset: request.Preset, PresetOf: authz.NormalizePreset(request.Preset),
		Disabled: false, SuperAdmin: h.superAdminOf(user.UserID),
	}})
}

// DeleteUser removes an account and everything the schema cascades from it.
//
// The audit entry is written after the row is gone and survives it, because the
// audit carries no foreign key to the account it names. The bytes of files the
// account uploaded stay on disk: the cascade removes their rows, and this
// operation does not reach into the storage root.
func (h *AdminHandler) DeleteUser(c *gin.Context) {
	if !h.lifecycleReady(c) {
		return
	}
	userID, ok := h.userID(c)
	if !ok || !h.protectedTarget(c, userID) {
		return
	}
	if err := h.lifecycle.DeleteAccount(c.Request.Context(), userID); err != nil {
		h.failLifecycle(c, "delete_user_failed", err)
		return
	}
	if !h.writeAccountAudit(c, audit.ActionAccountDeleted, userID) {
		return
	}
	c.Status(http.StatusNoContent)
}

// resetPasswordRequest is the body of a password replacement.
type resetPasswordRequest struct {
	Password string `json:"password"`
}

// ResetPassword replaces another account's password without knowing the old
// one. The service records the moment in the same write, so every token the
// account held stops counting as this returns.
func (h *AdminHandler) ResetPassword(c *gin.Context) {
	if !h.lifecycleReady(c) {
		return
	}
	userID, ok := h.userID(c)
	if !ok || !h.protectedTarget(c, userID) {
		return
	}
	var request resetPasswordRequest
	if !readJSON(c, maxAdminRequestBytes, &request) {
		return
	}
	if err := h.lifecycle.ResetPassword(c.Request.Context(), userID, request.Password); err != nil {
		h.failLifecycle(c, "reset_password_failed", err)
		return
	}
	if !h.writeAccountAudit(c, audit.ActionPasswordReset, userID) {
		return
	}
	c.Status(http.StatusNoContent)
}

// disabledRequest is the body of a sign-in flag change. The pointer is what
// lets an absent value be refused rather than read as false.
type disabledRequest struct {
	Disabled *bool `json:"disabled"`
}

// SetDisabled turns an account's ability to sign in off or back on.
//
// Disabling also takes back the tokens the account already holds, in the same
// storage write; enabling does not bring them back, because they were taken
// back and this operation cannot mint them again. Both directions are recorded
// under their own action: "who shut this off" and "who let it back in" are
// different questions an auditor asks by name.
func (h *AdminHandler) SetDisabled(c *gin.Context) {
	if !h.lifecycleReady(c) {
		return
	}
	userID, ok := h.userID(c)
	if !ok || !h.protectedTarget(c, userID) {
		return
	}
	var request disabledRequest
	if !readJSON(c, maxAdminRequestBytes, &request) {
		return
	}
	if request.Disabled == nil {
		// An absent boolean is not "false": the caller has not said which way,
		// and defaulting it would turn a malformed request into an enabling.
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "disabled is required")
		return
	}
	if err := h.lifecycle.SetDisabled(c.Request.Context(), userID, *request.Disabled); err != nil {
		h.failLifecycle(c, "set_user_status_failed", err)
		return
	}
	action := audit.ActionAccountEnabled
	if *request.Disabled {
		action = audit.ActionAccountDisabled
	}
	if !h.writeAccountAudit(c, action, userID) {
		return
	}
	c.Status(http.StatusNoContent)
}

// lifecycleReady answers whether the four routes can be served at all. A build
// wired without the lifecycle service refuses rather than dereferences it; a
// nil audit beside it is refused for the same reason it is on the two sign-in
// routes: an account change that cannot be recorded is one this build declines.
func (h *AdminHandler) lifecycleReady(c *gin.Context) bool {
	if h == nil || h.lifecycle == nil || h.audit == nil {
		httpx.WriteError(c, http.StatusInternalServerError, "admin_unavailable", "governance is not configured")
		return false
	}
	return true
}

// protectedTarget refuses a change to an account the allowlist protects, and to
// the caller's own account.
//
// The two are not the same rule — the caller need not be a super administrator
// for the self case — but they share one answer because they share one reason:
// these operations are what makes an account unreachable to its owner, and an
// administrator who can reach through them to the allowlist or to themselves can
// end the only account that could undo it. Refusing the self case also stops the
// accident in which an administrator removes their own access and has to be
// recovered from outside the product.
func (h *AdminHandler) protectedTarget(c *gin.Context, userID int64) bool {
	actor, ok := callerAccount(c)
	if !ok {
		return false
	}
	if userID == actor.UserID || h.superAdminOf(userID) {
		httpx.WriteError(c, http.StatusForbidden, "protected_account",
			"a super administrator account cannot be changed here")
		return false
	}
	return true
}

// writeAccountAudit leaves the entry one of the four changes deserves.
//
// It is written after the change and its failure is answered as a 500 rather
// than logged and swallowed, which is the same bargain writeSignInState makes:
// the change has happened, and serving a 2xx over a change nobody can find
// afterwards is the answer this table exists to prevent. Three of the four are
// idempotent enough to repeat; a repeated provisioning will meet a taken
// username, which is the honest report that the first attempt did land.
func (h *AdminHandler) writeAccountAudit(c *gin.Context, action audit.Action, userID int64) bool {
	actor, ok := callerAccount(c)
	if !ok {
		return false
	}
	err := h.audit.Record(c.Request.Context(), audit.Event{
		Actor:  audit.Actor{ID: actor.UserID, Name: actor.Username, Role: actor.Role},
		Action: action,
		Target: audit.Ref{Kind: audit.TargetAccount, ID: strconv.FormatInt(userID, 10)},
		// The account the entry is about is not the account that acted, which
		// is exactly the pair this column exists for.
		SubjectID: userID,
		Result:    audit.ResultOK,
		RequestID: httpx.RequestID(c),
	})
	if err != nil {
		if h.logger != nil {
			h.logger.Error("recording an account change failed",
				"request_id", httpx.RequestID(c), "action", string(action), "error", err)
		}
		httpx.WriteError(c, http.StatusInternalServerError, "audit_unavailable",
			"the change was made but could not be recorded")
		return false
	}
	return true
}

// failLifecycle answers a lifecycle write that did not happen.
//
// The two validation refusals are 400s rather than 403s for the reason
// failGrant gives: the caller was allowed to make this request — it is a super
// administrator — and what is wrong is the value in it.
func (h *AdminHandler) failLifecycle(c *gin.Context, code string, err error) {
	switch {
	case errors.Is(err, auth.ErrInvalidUsername):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_username", "the username is not acceptable")
	case errors.Is(err, auth.ErrInvalidPassword):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_password", "the password is outside the accepted length")
	case errors.Is(err, auth.ErrUsernameTaken):
		httpx.WriteError(c, http.StatusConflict, "username_taken", "that username is already in use")
	case errors.Is(err, auth.ErrUnknownRole):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "not a role this build writes")
	case errors.Is(err, authz.ErrUnknownPreset):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "not a preset this build writes")
	case errors.Is(err, auth.ErrUserNotFound):
		httpx.WriteError(c, http.StatusNotFound, "not_found", "no such account")
	default:
		h.fail(c, code, err)
	}
}
