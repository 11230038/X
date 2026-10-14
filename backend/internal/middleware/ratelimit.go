package middleware

// The third question after "who are you" and "may you": "how fast" (§2.10).
//
// It is mounted per route rather than globally, and that is the design's
// sentence rather than an economy: a global limiter would let one account
// refreshing a page crowd out another account downloading a file, which is a
// limit felt as the product being broken. Only the three classes of route that
// cost something outside this process are mounted with it — see router.go,
// where each one says why it is one of the three.
//
// The account comes from the token, so the bucket is per account and not per
// connection: a client that opens ten sockets is one account here, and an
// account behind a shared address is not mistaken for a crowd.

import (
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"

	"backend/internal/httpx"
	"backend/internal/limit"
	"github.com/gin-gonic/gin"
)

// Limiter answers whether an account may do one more action of a class.
//
// It is declared on the consumer's side, like Authorizer, so that this package
// does not have to know how a bucket is built or when it refills.
type Limiter interface {
	Allow(account int64, action limit.Action) (limit.Verdict, error)
}

// RateLimit refuses a request that came too soon after the last one.
//
// A refusal is 429 and not 403: nothing about the account is wrong, and the
// same request a moment later is expected to work. What makes it actionable is
// `retry_after`, in seconds — and the same number goes in the standard
// `Retry-After` header, because a client that already honours that header
// should not have to be taught this API's body to do the obvious thing.
//
// The two refusals a person will meet are deliberately different shapes, and
// both exist for the client to draw differently (§2.10's last 判据):
// `rate_limited` is a countdown of seconds, and §2.10's other half —
// `quota_exceeded` — is a period, answered where the calls already written down
// are read.
func RateLimit(limiter Limiter, action limit.Action, logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := CurrentUser(c)
		if !ok || user.UserID <= 0 {
			// RequireAuth runs before this on every route that mounts it, so
			// reaching here means the route was mounted wrong. Answered as
			// unauthenticated rather than allowed, for the same reason
			// AuthorizeWith does: a wiring mistake must not be the one thing
			// that turns a check off.
			httpx.WriteError(c, http.StatusUnauthorized, "authentication_failed", "this route needs an account")
			return
		}
		if limiter == nil {
			writeRateLimitFailure(c, logger, errors.New("rate limiter is not configured"))
			return
		}
		verdict, err := limiter.Allow(user.UserID, action)
		if err != nil {
			writeRateLimitFailure(c, logger, err)
			return
		}
		if !verdict.Allowed {
			seconds := int(math.Ceil(verdict.RetryAfter.Seconds()))
			if seconds < 1 {
				// A bucket that says "in less than a second" and a client that
				// waits it out must not be told to try again in zero seconds,
				// which reads as "now" and would be refused again.
				seconds = 1
			}
			c.Header("Retry-After", strconv.Itoa(seconds))
			httpx.WriteErrorDetails(c, http.StatusTooManyRequests, "rate_limited",
				"too many requests of this kind, try again in a moment", gin.H{
					"action":      string(action),
					"retry_after": seconds,
				})
			return
		}
		c.Next()
	}
}

// writeRateLimitFailure answers a failure that is the process's rather than the
// request's.
//
// It is the shape writeAuthorizationFailure has, and for the same two reasons:
// the account did nothing wrong and must not read this as its own limit, and
// the underlying message is logged rather than sent. Here it also covers the
// one mistake that would otherwise be silent — a route mounted with an action
// this deployment has no rate for, which arrives as ErrUnknownAction and is a
// fact about the wiring, not about the request.
func writeRateLimitFailure(c *gin.Context, logger *slog.Logger, err error) {
	if logger != nil {
		logger.Error("rate limiting failed", "request_id", httpx.RequestID(c), "error", err)
	}
	httpx.WriteError(c, http.StatusInternalServerError, "rate_limit_unavailable",
		"rate limiting is unavailable")
}
