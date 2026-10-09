package handler

import (
	"net/http"

	"backend/internal/httpx"
	"github.com/gin-gonic/gin"
)

// SystemHandler contains handlers for service-level endpoints.
type SystemHandler struct{}

// NewSystemHandler constructs the system endpoint handler.
func NewSystemHandler() *SystemHandler {
	return &SystemHandler{}
}

// Root returns a small, backwards-compatible welcome response.
func (h *SystemHandler) Root(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"message": "Gin server is running"})
}

// Health returns the legacy health response.
func (h *SystemHandler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Liveness reports whether the process is running.
func (h *SystemHandler) Liveness(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Readiness reports whether the process can accept traffic. There are no
// external dependencies yet, so a running process is ready.
func (h *SystemHandler) Readiness(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}

// NotFound returns the common 404 response.
func (h *SystemHandler) NotFound(c *gin.Context) {
	httpx.WriteNotFound(c)
}

// MethodNotAllowed returns the common 405 response.
func (h *SystemHandler) MethodNotAllowed(c *gin.Context) {
	httpx.WriteMethodNotAllowed(c)
}
