package handler

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"backend/internal/httpx"
	"github.com/gin-gonic/gin"
)

// ReadinessChecker reports whether an external dependency can accept work.
type ReadinessChecker interface {
	Ping(context.Context) error
}

// SystemHandler contains handlers for service-level endpoints.
type SystemHandler struct {
	readinessChecker ReadinessChecker
	readinessTimeout time.Duration
	logger           *slog.Logger
}

// NewSystemHandler constructs the system endpoint handler.
func NewSystemHandler(checker ReadinessChecker, timeout time.Duration, logger *slog.Logger) *SystemHandler {
	return &SystemHandler{
		readinessChecker: checker,
		readinessTimeout: timeout,
		logger:           logger,
	}
}

// Root returns a small, backwards-compatible welcome response.
func (h *SystemHandler) Root(c *gin.Context) {
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"message": "Gin server is running"})
}

// Health returns the legacy health response.
func (h *SystemHandler) Health(c *gin.Context) {
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"status": "ok"})
}

// Liveness reports whether the process is running.
func (h *SystemHandler) Liveness(c *gin.Context) {
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"status": "ok"})
}

// Readiness reports whether PostgreSQL can accept work.
func (h *SystemHandler) Readiness(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), h.readinessTimeout)
	defer cancel()

	if err := h.readinessChecker.Ping(ctx); err != nil {
		h.logger.Warn("readiness check failed", "request_id", httpx.RequestID(c), "error", err)
		httpx.WriteError(c, http.StatusServiceUnavailable, "not_ready", "service is not ready")
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"status": "ready"})
}

// NotFound returns the common 404 response.
func (h *SystemHandler) NotFound(c *gin.Context) {
	httpx.WriteNotFound(c)
}

// MethodNotAllowed returns the common 405 response.
func (h *SystemHandler) MethodNotAllowed(c *gin.Context) {
	httpx.WriteMethodNotAllowed(c)
}
