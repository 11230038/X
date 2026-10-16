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
			writeCredentialError(c, logger, err, "invalid_token", "bearer token is invalid")
			return
		}
		c.Set(currentUserKey, user)
		c.Next()
	}
}

// writeCredentialError answers a credential — a bearer token or a websocket
// ticket — that was presented and refused.
//
// The two refusals that mean the same thing on either route are answered here;
// what the credential itself is called is the caller's, because the same
// underlying failure reads differently depending on what the client sent:
// "pass a valid token" and "fetch a new ticket" are different instructions.
//
// A failure that is not a refusal at all — the signer being unreachable, a
// repository error — is not a statement about the credential, so it is not
// answered with 401 and the client is not told to go and replace something that
// was never the problem.
func writeCredentialError(c *gin.Context, logger *slog.Logger, err error, invalidCode, invalidMessage string) {
	switch {
	case errors.Is(err, auth.ErrAccountDisabled):
		httpx.WriteError(c, http.StatusForbidden, "account_disabled", "account is disabled")
	case errors.Is(err, auth.ErrInvalidTicket), errors.Is(err, auth.ErrInvalidToken):
		httpx.WriteError(c, http.StatusUnauthorized, invalidCode, invalidMessage)
	default:
		writeAuthFailure(c, logger, err)
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
