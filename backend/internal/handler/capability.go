package handler

import (
	"net/http"

	"backend/internal/capability"
	"backend/internal/httpx"
	"github.com/gin-gonic/gin"
)

// Manifests is the read-only view of what this build can run (CFG-03).
type Manifests interface {
	Manifests() []capability.Manifest
}

// CapabilityHandler answers what capabilities exist.
//
// This version has no consumer for it: it exists so that the contract a client
// will read is settled before there is a client, rather than being invented at
// the same time as the screen that shows it.
type CapabilityHandler struct {
	manifests Manifests
}

// NewCapabilityHandler constructs the capability HTTP handler.
func NewCapabilityHandler(manifests Manifests) *CapabilityHandler {
	return &CapabilityHandler{manifests: manifests}
}

// manifestView is a capability as a client reads it. It is the manifest's own
// fields, minus anything that is this process's business.
type manifestView struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Stages      []string `json:"stages"`
	Tools       []string `json:"tools"`
}

type capabilityListResponse struct {
	Capabilities []manifestView `json:"capabilities"`
}

// List returns every registered capability.
func (h *CapabilityHandler) List(c *gin.Context) {
	if h == nil || h.manifests == nil {
		httpx.WriteError(c, http.StatusInternalServerError, "capabilities_unavailable", "the capability registry is not configured")
		return
	}
	manifests := h.manifests.Manifests()
	listed := make([]manifestView, 0, len(manifests))
	for _, manifest := range manifests {
		listed = append(listed, manifestView{
			Name:        manifest.Name,
			Description: manifest.Description,
			Stages:      manifest.Stages,
			Tools:       manifest.Tools,
		})
	}
	httpx.WriteSuccess(c, http.StatusOK, capabilityListResponse{Capabilities: listed})
}
