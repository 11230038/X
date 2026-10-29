package handler

import (
	"net/http"
	"sort"

	"backend/internal/capability"
	"backend/internal/httpx"
	"backend/internal/tool"
	"github.com/gin-gonic/gin"
)

// Manifests is the read-only view of what this build can run (CFG-03).
type Manifests interface {
	Manifests() []capability.Manifest
}

// ToolCatalogue is the tool plane as the catalogue reads it.
//
// A manifest declares which tools it mounts by name; what a tool does is the
// tool's own business (`tool.Definition`). Asking the registry rather than
// repeating the sentence in the manifest keeps one description per tool: the
// same one the model is handed, so the page cannot describe a tool the model
// would not recognise.
type ToolCatalogue interface {
	Lookup(name string) (tool.Tool, bool)
}

// CapabilityHandler answers what capabilities exist.
//
// This version has no consumer for it: it exists so that the contract a client
// will read is settled before there is a client, rather than being invented at
// the same time as the screen that shows it.
type CapabilityHandler struct {
	manifests Manifests
	tools     ToolCatalogue
}

// NewCapabilityHandler constructs the capability HTTP handler.
func NewCapabilityHandler(manifests Manifests, tools ToolCatalogue) *CapabilityHandler {
	return &CapabilityHandler{manifests: manifests, tools: tools}
}

// manifestView is a capability as a client reads it. It is the manifest's own
// fields, minus anything that is this process's business.
type manifestView struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Stages      []string `json:"stages"`
	Tools       []string `json:"tools"`
}

// toolView is one tool as a client reads it. The description is the one the
// model sees when the tool is offered, so a catalogue and a turn agree about
// what the tool is for.
type toolView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type capabilityListResponse struct {
	Capabilities []manifestView `json:"capabilities"`
	// Tools are the tools some capability mounts, sorted by name. It is a flat
	// list rather than a field on each capability because a tool can be mounted
	// by more than one capability and has exactly one description.
	Tools []toolView `json:"tools"`
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
	httpx.WriteSuccess(c, http.StatusOK, capabilityListResponse{
		Capabilities: listed,
		Tools:        h.toolViews(manifests),
	})
}

// toolViews collects every tool the manifests mount, once, in name order, with
// the description the tool itself reports.
//
// A declared name that the registry does not know keeps its row with a blank
// description rather than disappearing: registration already refuses a manifest
// naming a tool nothing can resolve (`checkDrift`), so a blank here means the
// process was built with a registry this handler was not told about — worth
// seeing on the page, not worth failing the whole listing over.
func (h *CapabilityHandler) toolViews(manifests []capability.Manifest) []toolView {
	seen := make(map[string]bool)
	names := make([]string, 0)
	for _, manifest := range manifests {
		for _, name := range manifest.Tools {
			if seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)

	tools := make([]toolView, 0, len(names))
	for _, name := range names {
		view := toolView{Name: name}
		if h.tools != nil {
			if found, ok := h.tools.Lookup(name); ok {
				view.Description = found.Definition().Description
			}
		}
		tools = append(tools, view)
	}
	return tools
}
