package handler

import (
	"net/http"

	"backend/internal/auth"
	"backend/internal/authz"
	"backend/internal/httpx"
	"backend/internal/middleware"
	"github.com/gin-gonic/gin"
)

// caller reads the account a request is being served for.
//
// Every handler that touches owned data starts here, because the alternative —
// each handler asking middleware.CurrentUser itself and deciding what a missing
// account means — is the same decision made a dozen times, and the dozen first
// drafts of it are the ones that return a 500 or, worse, go on with a zero id
// and list a table for account number zero.
//
// The account comes from the token rather than from any part of the request.
// RequireAuth has already reloaded it from storage on this request, so a
// disabled account is refused before this runs and the role read here is current.
func caller(c *gin.Context) (authz.Caller, bool) {
	user, ok := callerAccount(c)
	if !ok {
		return authz.Caller{}, false
	}
	return authz.Caller{UserID: user.UserID, Role: user.Role}, true
}

// callerAccount is caller for the routes that need the name as well.
//
// There is one such route and it is the reason this exists: an audit entry
// records who did it *then*, under the name that account had at the time, and
// the name is the one RequireAuth loaded on this request. A handler that went
// back to storage for it would be reading a name a rename may already have
// changed — which is the one thing the snapshot is for.
func callerAccount(c *gin.Context) (auth.User, bool) {
	user, ok := middleware.CurrentUser(c)
	if !ok || user.UserID <= 0 {
		httpx.WriteError(c, http.StatusUnauthorized, "authentication_failed", "this route needs an account")
		return auth.User{}, false
	}
	return user, true
}
