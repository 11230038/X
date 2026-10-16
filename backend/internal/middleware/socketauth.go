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

// TicketVerifier exchanges a websocket ticket for the account it points at.
type TicketVerifier interface {
	Redeem(context.Context, string) (auth.User, error)
}

// RequireSocketAuth is the gate on the websocket route, and it is a separate
// gate because a browser cannot be a bearer client.
//
// `new WebSocket(url, protocols)` takes a subprotocol list, not headers, so a
// browser has nowhere to put an Authorization header on the handshake. It
// therefore trades its access token for a short-lived ticket over an ordinary
// HTTP request and puts the ticket in the connection's URL, which is what this
// accepts.
//
// The bearer path stays for the clients that are not browsers — cmd/turnprobe
// among them — so this reads the ticket first and falls back rather than
// replacing. Tickets are looked at first on purpose: both credentials cannot
// arrive from a real browser, and if a caller ever sends both, the one this
// route exists for is the one whose failure should be visible instead of being
// papered over by a token that happens to still work.
//
// The account published here is the account as it is now, not as the credential
// described it: Redeem reloads it, so a ticket issued before a logout, a
// password change, an administrative revocation or a disable stops working at
// the same moment that account's tokens do.
func RequireSocketAuth(tokens TokenVerifier, tickets TicketVerifier, logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// A ticket in the query string is the browser's way in. Note that this
		// package's AccessLog records the path and not the query, so the ticket
		// does not reach this service's own access log; the hops between the user
		// and here are what its short life is for.
		if ticket := strings.TrimSpace(c.Query("ticket")); ticket != "" {
			if tickets == nil {
				writeAuthFailure(c, logger, errors.New("ticket verifier is not configured"))
				return
			}
			user, err := tickets.Redeem(c.Request.Context(), ticket)
			if err != nil {
				writeCredentialError(c, logger, err, "invalid_ticket", "websocket ticket is invalid")
				return
			}
			c.Set(currentUserKey, user)
			c.Next()
			return
		}

		token, ok := bearerToken(c.GetHeader("Authorization"))
		if !ok {
			// The message names both ways in, because a caller that reached the
			// websocket route without either has to be told what it is missing
			// rather than what it failed to include.
			httpx.WriteError(c, http.StatusUnauthorized, "authorization_required",
				"a bearer token or a websocket ticket is required")
			return
		}
		if tokens == nil {
			writeAuthFailure(c, logger, errors.New("token verifier is not configured"))
			return
		}
		user, err := tokens.Authenticate(c.Request.Context(), token)
		if err != nil {
			writeCredentialError(c, logger, err, "invalid_token", "bearer token is invalid")
			return
		}
		c.Set(currentUserKey, user)
		c.Next()
	}
}
