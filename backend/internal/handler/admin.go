package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"backend/internal/audit"
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

// AccountSessions is the third thing governance decides about an account, beside
// the preset it carries and the grants written on top of that: whether what it
// holds still counts, and whether it is locked out (§2.3, §2.4).
//
// The two are separate from the preset change rather than folded into it because
// they are separate acts with separate reasons. An administrator resetting a
// preset is deciding what an account may reach; one revoking its sessions is
// saying the person holding them now is not the account's owner; and one
// unlocking it is deciding the run of failed sign-ins may stop counting. One
// route per act keeps one audit entry per act: folded together, an entry about a
// preset would carry the news that somebody's sessions were ended with it.
type AccountSessions interface {
	RevokeTokens(ctx context.Context, userID int64) error
	Unlock(ctx context.Context, userID int64) error
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
	sessions AccountSessions
	// audit is where the two sign-in routes below leave their entry. It is
	// required rather than optional, and unlike the preset change that is not a
	// matter of what this handler has been handed: ending somebody's sessions is
	// the governance act an audit most needs to be able to answer, and a build
	// that would perform it unrecorded should refuse instead.
	audit  AuditWriter
	logger *slog.Logger
}

// NewAdminHandler constructs the governance handler.
func NewAdminHandler(
	accounts AccountAdmin, grants GrantAdmin, sessions AccountSessions,
	writer AuditWriter, logger *slog.Logger,
) *AdminHandler {
	return &AdminHandler{accounts: accounts, grants: grants, sessions: sessions, audit: writer, logger: logger}
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

// RevokeSessions takes back every token one account holds.
//
// It is what an administrator has instead of that account's own sign-out, and
// the reason a sign-out and this are one act rather than two: nothing in this
// build tells two of an account's tokens apart, so "the sessions of account 42"
// is the narrowest thing either of them can name (§2.4). What it does not do is
// stop the account signing in again — that is the lock's business, and the two
// are separate columns and separate routes for that reason.
func (h *AdminHandler) RevokeSessions(c *gin.Context) {
	h.writeSignInState(c, "revoke_sessions_failed", audit.ActionSessionsRevoked,
		func(ctx context.Context, sessions AccountSessions, userID int64) error {
			return sessions.RevokeTokens(ctx, userID)
		})
}

// Unlock clears the run of failed sign-ins that locked an account, so its owner
// can sign in before the lock would have run out on its own.
//
// This is the way out of the mechanism's own cost (§2.3): the count is kept per
// username, so anybody who knows a username can spend its owner's next few
// minutes outside. The threshold and the window make that expensive; this makes
// it recoverable, and it is the reason the cost was accepted.
func (h *AdminHandler) Unlock(c *gin.Context) {
	h.writeSignInState(c, "unlock_failed", audit.ActionUnlocked,
		func(ctx context.Context, sessions AccountSessions, userID int64) error {
			return sessions.Unlock(ctx, userID)
		})
}

// writeSignInState is the two routes above. Both name an account in their path,
// both change something about whether it can sign in, both answer 204 with no
// body, and both leave one entry naming the administrator as the actor and the
// account as the subject — which is the pair the subject column exists for.
//
// Nothing here asks the account whether it exists. Both changes are statements
// about a state rather than creations: "account 42's tokens do not count" and
// "account 42 is not locked" are true of an account that was never there, and
// the caller has got what it asked for either way. A 404 would say a state was
// not established when it was.
//
// The entry is written after the change, and a failure to write it is reported
// rather than logged and swallowed. Serving a 204 over a change that cannot be
// found afterwards is the one answer this table exists to prevent; and because
// both writes are idempotent — revoking twice and unlocking twice change nothing
// the second time — the 500 is one a client may simply repeat.
//
// The change arrives as a closure over the interface rather than as a method
// value taken at the call site, and that is not indirection for its own sake:
// `h.sessions` may be nil — nothing has been wired — and taking a method value
// from a nil interface panics where it is taken, before the check below can
// answer a 500. The closure is what keeps the check first.
func (h *AdminHandler) writeSignInState(
	c *gin.Context, code string, action audit.Action,
	change func(context.Context, AccountSessions, int64) error,
) {
	if h == nil || h.sessions == nil || h.audit == nil {
		httpx.WriteError(c, http.StatusInternalServerError, "admin_unavailable", "governance is not configured")
		return
	}
	userID, ok := h.userID(c)
	if !ok {
		return
	}
	actor, ok := callerAccount(c)
	if !ok {
		return
	}
	if err := change(c.Request.Context(), h.sessions, userID); err != nil {
		h.fail(c, code, err)
		return
	}
	err := h.audit.Record(c.Request.Context(), audit.Event{
		Actor:  audit.Actor{ID: actor.UserID, Name: actor.Username, Role: actor.Role},
		Action: action,
		Target: audit.Ref{Kind: audit.TargetAccount, ID: strconv.FormatInt(userID, 10)},
		// The actor and the subject are two accounts here, which is what this
		// column is for: the account an entry is about is not the account that
		// acted, and asking "what has been done to this account" has to be one
		// query rather than a scan of every administrator's actions.
		SubjectID: userID,
		Result:    audit.ResultOK,
		RequestID: httpx.RequestID(c),
	})
	if err != nil {
		if h.logger != nil {
			h.logger.Error("recording a governance write failed",
				"request_id", httpx.RequestID(c), "action", string(action), "error", err)
		}
		httpx.WriteError(c, http.StatusInternalServerError, "audit_unavailable",
			"the change was made but could not be recorded")
		return
	}
	c.Status(http.StatusNoContent)
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
