package handler

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"backend/internal/capability"
	"backend/internal/tool"
	"github.com/gin-gonic/gin"
)

type manifestsStub struct {
	manifests []capability.Manifest
}

func (s manifestsStub) Manifests() []capability.Manifest { return s.manifests }

// toolStub is a tool as the catalogue sees it: a name and one sentence.
type toolStub struct {
	name        string
	description string
}

func (s toolStub) Definition() tool.Definition {
	return tool.Definition{Name: s.name, Description: s.description}
}

func (s toolStub) Invoke(context.Context, tool.Call) (tool.Result, error) {
	return tool.Result{}, nil
}

type toolCatalogueStub struct {
	tools []toolStub
}

func (s toolCatalogueStub) Lookup(name string) (tool.Tool, bool) {
	for _, candidate := range s.tools {
		if candidate.name == name {
			return candidate, true
		}
	}
	return nil, false
}

func newCapabilityRouter(manifests Manifests, tools ToolCatalogue) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/capabilities", NewCapabilityHandler(manifests, tools).List)
	return router
}

func TestCapabilityHandlerListsWhatThisBuildCanRun(t *testing.T) {
	router := newCapabilityRouter(manifestsStub{manifests: []capability.Manifest{{
		Name:        "chat",
		Description: "与模型对话",
		Stages:      []string{"exploring", "responding"},
	}}}, toolCatalogueStub{})

	recorder := call(t, router, http.MethodGet, "/api/v1/capabilities", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data capabilityListResponse `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if len(envelope.Data.Capabilities) != 1 {
		t.Fatalf("capabilities = %+v, want the one capability", envelope.Data.Capabilities)
	}
	listed := envelope.Data.Capabilities[0]
	if listed.Name != "chat" || len(listed.Stages) != 2 || listed.Stages[0] != "exploring" {
		t.Errorf("capability = %+v, want the manifest as it was declared", listed)
	}
	// The order stages are walked in is part of the manifest, so it survives
	// the round trip rather than being sorted into something else.
	if listed.Stages[1] != "responding" {
		t.Errorf("stages = %v, want them in the order the capability walks them", listed.Stages)
	}
}

// A tool mounted by two capabilities is still one row, and its description is
// the tool's own — the catalogue must not be able to describe a tool in words
// the model was never given.
func TestCapabilityHandlerDescribesEachToolOnce(t *testing.T) {
	router := newCapabilityRouter(
		manifestsStub{manifests: []capability.Manifest{
			{
				Name:        "deepsolve",
				Description: "分步解决",
				Stages:      []string{"responding"},
				Tools:       []string{"solve_plan", "solve_finish"},
			},
			{
				Name:        "reading",
				Description: "沉浸阅读",
				Stages:      []string{"responding"},
				Tools:       []string{"solve_plan"},
			},
		}},
		toolCatalogueStub{tools: []toolStub{
			{name: "solve_plan", description: "把接下来打算怎么解决的步骤写下来。"},
			{name: "solve_finish", description: "计划结束时调用，然后给出最终答案。"},
		}},
	)

	recorder := call(t, router, http.MethodGet, "/api/v1/capabilities", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data capabilityListResponse `json:"data"`
	}
	decodeBody(t, recorder, &envelope)

	tools := envelope.Data.Tools
	if len(tools) != 2 {
		t.Fatalf("tools = %+v, want one row per tool, not one per mention", tools)
	}
	// Sorted by name, so a client can render the list without ordering it.
	if tools[0].Name != "solve_finish" || tools[1].Name != "solve_plan" {
		t.Errorf("tools = %+v, want them in name order", tools)
	}
	if tools[1].Description != "把接下来打算怎么解决的步骤写下来。" {
		t.Errorf("solve_plan description = %q, want the tool's own", tools[1].Description)
	}
}

func TestCapabilityHandlerWithNothingRegisteredAnswersEmpty(t *testing.T) {
	router := newCapabilityRouter(manifestsStub{}, toolCatalogueStub{})

	recorder := call(t, router, http.MethodGet, "/api/v1/capabilities", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	// An empty list rather than null: a client rendering the catalogue should
	// not have to tell the two apart.
	if body := recorder.Body.String(); !strings.Contains(body, `"capabilities":[]`) {
		t.Errorf("body = %s, want an empty list", body)
	}
}

func TestCapabilityHandlerWithoutARegistryFailsRatherThanPanics(t *testing.T) {
	var handler *CapabilityHandler
	recorder := call(t, func() *gin.Engine {
		gin.SetMode(gin.TestMode)
		router := gin.New()
		router.GET("/api/v1/capabilities", handler.List)
		return router
	}(), http.MethodGet, "/api/v1/capabilities", "")

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
}

func containsJSON(body, want string) bool {
	for i := 0; i+len(want) <= len(body); i++ {
		if body[i:i+len(want)] == want {
			return true
		}
	}
	return false
}
