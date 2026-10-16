package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"backend/internal/auth"
	"backend/internal/httpx"
	"backend/internal/middleware"
	"github.com/gin-gonic/gin"
)

// maxAuthRequestBytes bounds credential bodies, which carry two short fields.
const maxAuthRequestBytes int64 = 4 << 10

// Authenticator is the authentication use case consumed by the HTTP handler.
type Authenticator interface {
	Register(context.Context, auth.RegisterInput) (auth.AuthResult, error)
	Login(context.Context, auth.LoginInput) (auth.AuthResult, error)
	// RevokeTokens takes back every token the account holds, including the one
	// that made the request. There is no narrower form: nothing in a token tells
	// two of an account's tokens apart, so "this device" is not a thing this
	// build can be asked to sign out (§2.4).
	RevokeTokens(context.Context, int64) error
	// ChangePassword replaces the account's password, having checked the old one,
	// and takes back every token it holds. It is the one write in this API that
	// asks an account that is already signed in to prove itself again: a token
	// says what an account may reach, not who is holding it (§2.9).
	ChangePassword(context.Context, int64, string, string) error
	// IssueWebSocketTicket mints the short-lived credential a browser trades for
	// a WebSocket connection, and returns the moment it stops working.
	IssueWebSocketTicket(context.Context, int64) (string, time.Time, error)
}

// AuthHandler handles registration, login, sign-out, password changes, and
// current-account requests.
type AuthHandler struct {
	authenticator Authenticator
	logger        *slog.Logger
}

// NewAuthHandler constructs the authentication HTTP handler.
func NewAuthHandler(authenticator Authenticator, logger *slog.Logger) *AuthHandler {
	return &AuthHandler{authenticator: authenticator, logger: logger}
}

type credentialsRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type sessionResponse struct {
	User      auth.User `json:"user"`
	Token     string    `json:"token"`
	TokenType string    `json:"token_type"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Register creates an account and returns its first access token.
func (h *AuthHandler) Register(c *gin.Context) {
	request, ok := h.decodeCredentials(c)
	if !ok {
		return
	}
	if h.authenticator == nil {
		h.writeAuthFailure(c, "registration_failed", errors.New("auth service is not configured"))
		return
	}
	result, err := h.authenticator.Register(c.Request.Context(), auth.RegisterInput{
		Username: request.Username,
		Password: request.Password,
	})
	if err != nil {
		h.writeRegisterError(c, err)
		return
	}
	httpx.WriteSuccess(c, http.StatusCreated, newSessionResponse(result))
}

// Login verifies credentials and returns an access token.
func (h *AuthHandler) Login(c *gin.Context) {
	request, ok := h.decodeCredentials(c)
	if !ok {
		return
	}
	if h.authenticator == nil {
		h.writeAuthFailure(c, "login_failed", errors.New("auth service is not configured"))
		return
	}
	result, err := h.authenticator.Login(c.Request.Context(), auth.LoginInput{
		Username: request.Username,
		Password: request.Password,
	})
	if err != nil {
		h.writeLoginError(c, err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, newSessionResponse(result))
}

// Logout takes back every token the account holds, the one that made this request
// included, and answers with nothing.
//
// Nothing is issued in their place. A replacement token would have to be signed
// after the moment just written, and at whole-second precision that asks the
// revocation and the signing to land in different seconds — a security rule
// resting on a coincidence of two clock reads. Signing in again is what a client
// does instead, and the route says so by having no body to read (§2.2).
func (h *AuthHandler) Logout(c *gin.Context) {
	account, ok := callerAccount(c)
	if !ok {
		return
	}
	if h.authenticator == nil {
		h.writeAuthFailure(c, "logout_failed", errors.New("auth service is not configured"))
		return
	}
	if err := h.authenticator.RevokeTokens(c.Request.Context(), account.UserID); err != nil {
		h.writeAuthFailure(c, "logout_failed", err)
		return
	}
	c.Status(http.StatusNoContent)
}

// socketTicketResponse is the body of a ticket issue. It carries no account
// detail: the caller already has one, and a ticket that described its owner
// would be a second place that description could go stale.
type socketTicketResponse struct {
	Ticket    string    `json:"ticket"`
	ExpiresAt time.Time `json:"expires_at"`
}

// IssueWebSocketTicket mints the credential a browser connects with.
//
// It exists because a browser cannot set an Authorization header on a WebSocket
// handshake — `new WebSocket(url, protocols)` takes a subprotocol list — so the
// browser makes an ordinary HTTP request it *can* put a header on, and carries
// the result in the connection's URL. That is why the ticket is short-lived
// rather than merely secret: a URL is recorded by every hop it passes, and the
// only thing that makes that survivable is the recorded string being worthless
// by the time anyone reads it.
//
// The account is the one the token names and never one from the request, so this
// is not a route for minting somebody else's ticket. It is mounted with no
// surface, like the sign-out beside it: what a policy decides is which areas of
// the product an account may reach, and whether this account may open the
// websocket at all is decided on the websocket route itself, where the chat
// surface is checked. An account restricted out of every area can still be given
// the credential it will be turned away with.
//
// Nothing here is rate-limited. The limiter exists to bound work that leaves the
// process and spends someone else's resource, and its three actions are the
// three such classes this build has; minting a ticket is one HMAC over a few
// bytes and reaches nothing.
func (h *AuthHandler) IssueWebSocketTicket(c *gin.Context) {
	account, ok := callerAccount(c)
	if !ok {
		return
	}
	if h.authenticator == nil {
		h.writeAuthFailure(c, "ticket_issue_failed", errors.New("auth service is not configured"))
		return
	}
	ticket, expires, err := h.authenticator.IssueWebSocketTicket(c.Request.Context(), account.UserID)
	if err != nil {
		h.writeAuthFailure(c, "ticket_issue_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, socketTicketResponse{Ticket: ticket, ExpiresAt: expires.UTC()})
}

// passwordRequest is the body of a password change: the credential being
// replaced, and the one replacing it.
type passwordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// ChangePassword replaces the signed-in account's password.
//
// The account is the one the token names and never one from the request, which
// is what keeps this from being a route for changing somebody else's password:
// an administrator has no route to that at all — there is no such thing as
// resetting a forgotten password in this build, and the absence is deliberate
// (§0.2: it would need a mail channel).
//
// It is mounted with no surface, like the sign-out beside it. What a policy
// decides is which areas of the product an account may reach, and the password
// is what lets the account in: an account restricted out of every area must
// still be able to replace it.
//
// Success is a 204 with no body. Every token this account holds has just been
// taken back, this request's included, so there is no session left to hand back
// and the client signs in again (§2.2).
func (h *AuthHandler) ChangePassword(c *gin.Context) {
	account, ok := callerAccount(c)
	if !ok {
		return
	}
	var request passwordRequest
	if !h.decodeStrict(c, &request) {
		return
	}
	if h.authenticator == nil {
		h.writeAuthFailure(c, "password_change_failed", errors.New("auth service is not configured"))
		return
	}
	err := h.authenticator.ChangePassword(
		c.Request.Context(), account.UserID, request.OldPassword, request.NewPassword,
	)
	if err != nil {
		h.writePasswordError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Me returns the account established by the bearer token middleware.
func (h *AuthHandler) Me(c *gin.Context) {
	user, ok := middleware.CurrentUser(c)
	if !ok {
		h.writeAuthFailure(c, "authentication_failed", errors.New("route is not behind the auth middleware"))
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"user": user})
}

// decodeStrict reads one size-bounded JSON object into target, and refuses
// everything else the way every credential route refuses it.
//
// It is one function for the three of them — register, sign in, change password
// — because the strictness is one policy rather than three. An unknown field, a
// second document in the same body, and a body past the bound are each refused
// here, and a route that grew its own copy of this would be a route where one of
// the three is eventually not refused. The bound is this package's smallest
// because all three bodies carry two short fields.
func (h *AuthHandler) decodeStrict(c *gin.Context, target any) bool {
	responseWriter := http.ResponseWriter(c.Writer)
	if unwrapper, ok := responseWriter.(interface{ Unwrap() http.ResponseWriter }); ok {
		responseWriter = unwrapper.Unwrap()
	}
	c.Request.Body = http.MaxBytesReader(responseWriter, c.Request.Body, maxAuthRequestBytes)

	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			httpx.WriteError(c, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large")
			return false
		}
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return false
	}
	if err := decoder.Decode(new(struct{})); !errors.Is(err, io.EOF) {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return false
	}
	return true
}

// decodeCredentials reads one strict, size-bounded JSON credential object.
func (h *AuthHandler) decodeCredentials(c *gin.Context) (credentialsRequest, bool) {
	var request credentialsRequest
	if !h.decodeStrict(c, &request) {
		return credentialsRequest{}, false
	}
	return request, true
}

func (h *AuthHandler) writeRegisterError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, auth.ErrInvalidUsername):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_username", "username is invalid")
	case errors.Is(err, auth.ErrInvalidPassword):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_password", "password is invalid")
	case errors.Is(err, auth.ErrUsernameTaken):
		httpx.WriteError(c, http.StatusConflict, "username_taken", "username is already taken")
	default:
		h.writeAuthFailure(c, "registration_failed", err)
	}
}

func (h *AuthHandler) writeLoginError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		httpx.WriteError(c, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
	case errors.Is(err, auth.ErrAccountDisabled):
		httpx.WriteError(c, http.StatusForbidden, "account_disabled", "account is disabled")
	case errors.Is(err, auth.ErrAccountLocked):
		// A third answer, and a different one from the two above: this account is
		// not disabled and no administrator has to do anything. It stops being
		// locked on its own, so the client is being told to wait rather than to
		// ask somebody — which is why the code is not account_disabled.
		httpx.WriteError(c, http.StatusForbidden, "account_locked", "account is locked after too many failed sign-ins")
	default:
		h.writeAuthFailure(c, "login_failed", err)
	}
}

// writePasswordError answers a password change that did not happen.
func (h *AuthHandler) writePasswordError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, auth.ErrWrongPassword):
		// 403 and not 401: the token is valid and this is the account it names,
		// so nothing is missing from what the client already presented. What
		// failed is the second, deliberate proof this one route asks for — and a
		// 401 would tell a client its token was bad, which is the one thing it
		// was not, and would have it drop a credential that still works.
		httpx.WriteError(c, http.StatusForbidden, "invalid_credentials", "the current password is not this account's")
	case errors.Is(err, auth.ErrInvalidPassword):
		// The same code registration answers with, for the same rule: a new
		// password is judged by the policy every password is judged by, or the
		// policy would only ever apply to accounts that never changed one.
		httpx.WriteError(c, http.StatusBadRequest, "invalid_password", "the new password is invalid")
	case errors.Is(err, auth.ErrUserNotFound):
		// The account came from the token and the middleware reloaded it on this
		// request, so the only way here is the account having been removed in the
		// moment between the two reads.
		httpx.WriteError(c, http.StatusUnauthorized, "invalid_token", "the account this token names no longer exists")
	default:
		h.writeAuthFailure(c, "password_change_failed", err)
	}
}

// writeAuthFailure logs the real cause and returns a generic 500, so internal
// details never reach the client.
func (h *AuthHandler) writeAuthFailure(c *gin.Context, code string, err error) {
	if h.logger != nil {
		h.logger.Error("authentication request failed", "request_id", httpx.RequestID(c), "error", err)
	}
	httpx.WriteError(c, http.StatusInternalServerError, code, "authentication is unavailable")
}

// newSessionResponse builds the register and login payload. It carries the
// access token but never the stored password hash.
func newSessionResponse(result auth.AuthResult) sessionResponse {
	return sessionResponse{
		User:      result.User,
		Token:     result.Token,
		TokenType: "Bearer",
		ExpiresAt: result.ExpiresAt.UTC(),
	}
}
