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
}

// AuthHandler handles registration, login, and current-account requests.
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
	RequestID string    `json:"request_id"`
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
	c.JSON(http.StatusCreated, newSessionResponse(result, httpx.RequestID(c)))
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
	c.JSON(http.StatusOK, newSessionResponse(result, httpx.RequestID(c)))
}

// Me returns the account established by the bearer token middleware.
func (h *AuthHandler) Me(c *gin.Context) {
	user, ok := middleware.CurrentUser(c)
	if !ok {
		h.writeAuthFailure(c, "authentication_failed", errors.New("route is not behind the auth middleware"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": user, "request_id": httpx.RequestID(c)})
}

// decodeCredentials reads one strict, size-bounded JSON credential object.
func (h *AuthHandler) decodeCredentials(c *gin.Context) (credentialsRequest, bool) {
	responseWriter := http.ResponseWriter(c.Writer)
	if unwrapper, ok := responseWriter.(interface{ Unwrap() http.ResponseWriter }); ok {
		responseWriter = unwrapper.Unwrap()
	}
	c.Request.Body = http.MaxBytesReader(responseWriter, c.Request.Body, maxAuthRequestBytes)

	var request credentialsRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			httpx.WriteError(c, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large")
			return credentialsRequest{}, false
		}
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return credentialsRequest{}, false
	}
	if err := decoder.Decode(new(struct{})); !errors.Is(err, io.EOF) {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "request body is invalid")
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
	default:
		h.writeAuthFailure(c, "login_failed", err)
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

// newSessionResponse builds the register and login response. It carries the
// access token but never the stored password hash.
func newSessionResponse(result auth.AuthResult, requestID string) sessionResponse {
	return sessionResponse{
		User:      result.User,
		Token:     result.Token,
		TokenType: "Bearer",
		ExpiresAt: result.ExpiresAt.UTC(),
		RequestID: requestID,
	}
}
