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

// Refusal is one request the policy said no to.
//
// It carries what an audit entry needs and nothing else: who asked, in which
// name, what they were reaching for, and which layer said no. There is no
// subject here — a refusal is about the person who was refused (§7.1) — and no
// body of any kind, which is the same discipline the audit's own columns keep
// (§2.6).
type Refusal struct {
	Caller authz.Caller
	// Name is the account's name at the moment of the request. It is carried
	// rather than looked up by whoever writes the entry down, because it is
	// already in hand: RequireAuth reloads the account on every request, so the
	// name cost nothing to get here and would cost a read on the refusal path
	// there — at the moment an instance with a broken policy is busiest.
	Name     string
	Decision authz.Decision
	Surface  string
	// RequestID is the id the access log line for this request carries. It is
	// what makes an audit entry and a log line the same request rather than two
	// records that happen to be about the same account.
	RequestID string
}

// Recorder writes a refusal down. It is satisfied by the audit service.
//
// The refusal is recorded from here rather than from each handler because this
// is the one place a refusal is *produced*: a handler cannot be asked to record
// a decision it never saw, so a check written per handler would be a record
// omitted per handler — and an omitted record does not fail, it makes the audit
// say nothing happened.
type Recorder interface {
	RecordRefusal(ctx context.Context, refusal Refusal) error
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
	return AuthorizeWith(authorizer, requirement, nil, logger)
}

// AuthorizeWith is Authorize with somewhere to write a refusal down.
//
// A nil recorder is not an error and not a silent downgrade: it is what the
// tests of every other route in this repository mount, where the question is
// whether a request is refused rather than whether the refusal was recorded.
// The application always passes one, and the one place that is visible is
// main.go, next to the audit service it comes from.
func AuthorizeWith(
	authorizer Authorizer, requirement Requirement, recorder Recorder, logger *slog.Logger,
) gin.HandlerFunc {
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
			recordRefusal(c, recorder, logger, Refusal{
				Caller: caller, Name: user.Username, Decision: decision,
				Surface: requirement.Surface, RequestID: httpx.RequestID(c),
			})
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

// recordRefusal writes a refusal down, and the answer does not depend on whether
// it worked.
//
// The request is refused either way. A failure to record it is logged and the
// refusal stands, and the direction matters: a version that answered 200 because
// the audit was unreachable would be a version where the audit's availability is
// what decides whether somebody may read somebody else's material.
func recordRefusal(c *gin.Context, recorder Recorder, logger *slog.Logger, refusal Refusal) {
	if recorder == nil {
		return
	}
	if err := recorder.RecordRefusal(c.Request.Context(), refusal); err != nil && logger != nil {
		logger.Error("recording a refusal failed",
			"request_id", httpx.RequestID(c), "surface", refusal.Surface, "error", err)
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
