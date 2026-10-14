package middleware

// The step after "are you logged in": "may you do this" (§2.1).
//
// RequireAuth answers who is asking. This answers what they may reach, and it
// is a middleware rather than a call inside each handler for the reason every
// cross-cutting rule ends up in one here: a check written per handler is a
// check omitted per handler, and the omission does not fail — it serves the
// request.
//
// Nothing in this file compares a role. It asks an Authorizer, which is the one
// place the four layers are combined, because a second private reading of "is
// this the administrator" is a decision nothing can audit.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"backend/internal/authz"
	"backend/internal/httpx"
	"github.com/gin-gonic/gin"
)

// decisionKey is where an allowed request's decision is published.
const decisionKey = "authz_decision"

// Authorizer answers what an account may do. It is satisfied by the authz
// service; it is declared here, on the consumer's side, so that this package
// does not have to know how the four layers are stored.
type Authorizer interface {
	Decide(ctx context.Context, caller authz.Caller, action authz.Action, subject int64) (authz.Decision, error)
}

// Requirement is what one route needs of the account making the request.
//
// Surface is required and Permission is not, because they answer two different
// questions: a surface narrows what a learner may reach at all, and a
// permission is what lets an account reach *somebody else's* material. A route
// that is always about the caller's own things has no permission to name, and
// one that is about another account always has.
type Requirement struct {
	// Surface is the area of the product the route belongs to.
	Surface string
	// Permission is the guardian permission the route exercises when it is
	// asked about somebody else's material. Empty for a route that is only ever
	// about the caller's own.
	Permission authz.Permission
	// Subject reads the account the request is about. A nil Subject means the
	// caller's own, which is what most routes are.
	Subject func(*gin.Context) int64
}

// Surface is the common case: a route about the caller's own material, in one
// area of the product.
func Surface(name string) Requirement { return Requirement{Surface: name} }

// Authorize refuses a request the caller may not make.
//
// A refusal is 403 rather than 401, and the two are deliberately not collapsed
// (§2.16): "you are not logged in" and "you are logged in and may not" are
// different things for the person to do something about, and one status would
// make which one it is a guess.
func Authorize(authorizer Authorizer, requirement Requirement, logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := CurrentUser(c)
		if !ok || user.UserID <= 0 {
			// RequireAuth runs before this on every route that mounts it, so
			// reaching here means the route was mounted wrong. It is answered
			// as unauthenticated rather than allowed: a wiring mistake must not
			// be the one thing that turns a check off.
			httpx.WriteError(c, http.StatusUnauthorized, "authentication_failed", "this route needs an account")
			return
		}
		if authorizer == nil {
			writeAuthorizationFailure(c, logger, errors.New("authorizer is not configured"))
			return
		}
		caller := authz.Caller{UserID: user.UserID, Role: user.Role}
		subject := int64(0)
		if requirement.Subject != nil {
			subject = requirement.Subject(c)
		}
		decision, err := authorizer.Decide(c.Request.Context(), caller,
			authz.Action{
				Item:       authz.Item{Kind: authz.KindSurface, ID: requirement.Surface},
				Permission: requirement.Permission,
			}, subject)
		if err != nil {
			writeAuthorizationFailure(c, logger, err)
			return
		}
		if !decision.Allowed {
			// The reason is in the body because the two refusals a person can
			// act on are different: a surface they do not have, and a
			// relationship they do not hold. The layer is in it as well so that
			// the screen can say which of the four said no.
			httpx.WriteErrorDetails(c, http.StatusForbidden, "permission_denied",
				"this account may not do that", gin.H{
					"surface": requirement.Surface,
					"layer":   string(decision.Layer),
					"reason":  decision.Reason,
				})
			return
		}
		c.Set(decisionKey, decision)
		c.Next()
	}
}

// writeAuthorizationFailure answers a failure that is the process's rather than
// the request's.
//
// It is not writeAuthFailure above, and the difference is the code: "we could
// not reach the policy" is not "we could not tell who you are", and a client
// that retried the first by signing in again would be doing the one thing that
// cannot help. Both are 500s and both say nothing about themselves — the
// underlying message is logged and never sent.
func writeAuthorizationFailure(c *gin.Context, logger *slog.Logger, err error) {
	if logger != nil {
		logger.Error("authorization failed", "request_id", httpx.RequestID(c), "error", err)
	}
	httpx.WriteError(c, http.StatusInternalServerError, "authorization_unavailable",
		"authorization is unavailable")
}

// AuthorizedDecision returns the decision Authorize made for this request.
//
// It is here so that a handler which has to explain *why* something is allowed
// — the audit does, and so does anything that reports a learner's effective
// permissions — reads the answer that was acted on rather than asking a second
// time and possibly getting a different one.
func AuthorizedDecision(c *gin.Context) (authz.Decision, bool) {
	value, exists := c.Get(decisionKey)
	if !exists {
		return authz.Decision{}, false
	}
	decision, ok := value.(authz.Decision)
	return decision, ok
}
