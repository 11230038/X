package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"backend/internal/auth"
	"backend/internal/httpx"
	"github.com/gin-gonic/gin"
)

const currentUserKey = "auth_user"

// TokenVerifier resolves a bearer token into the account that owns it.
type TokenVerifier interface {
	Authenticate(context.Context, string) (auth.User, error)
}

// RequireAuth rejects requests without a usable bearer token and publishes the
// authenticated account for downstream handlers. The account is reloaded on
// every request, so revoking or disabling it takes effect immediately.
func RequireAuth(verifier TokenVerifier, logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := bearerToken(c.GetHeader("Authorization"))
		if !ok {
			httpx.WriteError(c, http.StatusUnauthorized, "authorization_required", "bearer token is required")
			return
		}
		if verifier == nil {
			writeAuthFailure(c, logger, errors.New("token verifier is not configured"))
			return
		}
		user, err := verifier.Authenticate(c.Request.Context(), token)
		if err != nil {
			switch {
			case errors.Is(err, auth.ErrAccountDisabled):
				httpx.WriteError(c, http.StatusForbidden, "account_disabled", "account is disabled")
			case errors.Is(err, auth.ErrInvalidToken):
				httpx.WriteError(c, http.StatusUnauthorized, "invalid_token", "bearer token is invalid")
			default:
				writeAuthFailure(c, logger, err)
			}
			return
		}
		c.Set(currentUserKey, user)
		c.Next()
	}
}

// CurrentUser returns the account established by RequireAuth.
func CurrentUser(c *gin.Context) (auth.User, bool) {
	value, exists := c.Get(currentUserKey)
	if !exists {
		return auth.User{}, false
	}
	user, ok := value.(auth.User)
	return user, ok
}

// bearerToken extracts the token from an Authorization header. Header values are
// never logged.
func bearerToken(header string) (string, bool) {
	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	if token == "" || strings.ContainsAny(token, " \t") {
		return "", false
	}
	return token, true
}

func writeAuthFailure(c *gin.Context, logger *slog.Logger, err error) {
	if logger != nil {
		logger.Error("token authentication failed", "request_id", httpx.RequestID(c), "error", err)
	}
	httpx.WriteError(c, http.StatusInternalServerError, "authentication_failed", "authentication is unavailable")
}
