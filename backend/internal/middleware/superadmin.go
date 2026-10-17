package middleware

// The gate after the administrative surface: the account-lifecycle operations
// are for the deployment's super administrators, not for every administrator.
//
// It is a middleware rather than a comparison in each handler for the same
// reason AuthorizeWith is: a check written per handler is a check omitted per
// handler, and the omission does not fail — it serves the request. It is also
// not a fifth layer of Decide: the allowlist is static configuration about who
// may perform four named operations, not a policy about what an account may
// reach, and keeping it out of the policy leaves the four-layer model and its
// two roles exactly as they were.
//
// The refusals are not audited here. A caller who does not reach `admin` at all
// was already refused — and recorded — by AuthorizeWith; what is left for this
// gate is an administrator who is simply not in the allowlist, which is a
// deployment fact rather than a policy decision. The four operations
// themselves each leave an entry in the audit.

import (
	"log/slog"
	"net/http"

	"backend/internal/httpx"
	"github.com/gin-gonic/gin"
)

// RequireSuperAdmin refuses a request from an account outside the deployment's
// allowlist.
//
// RequireAuth and the surface gate run before it on every route that mounts it,
// but the missing-account branch is still answered rather than assumed away: a
// route mounted without them must fail closed, not open.
func RequireSuperAdmin(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := CurrentUser(c)
		if !ok || user.UserID <= 0 {
			httpx.WriteError(c, http.StatusUnauthorized, "authentication_failed",
				"this route needs an account")
			return
		}
		if !user.SuperAdmin {
			if logger != nil {
				logger.Warn("super administrator route refused",
					"request_id", httpx.RequestID(c), "user_id", user.UserID)
			}
			httpx.WriteError(c, http.StatusForbidden, "super_admin_required",
				"this operation needs a super administrator")
			return
		}
		c.Next()
	}
}
