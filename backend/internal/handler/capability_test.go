package handler

import (
	"net/http"
	"strings"
	"testing"

	"backend/internal/capability"
	"github.com/gin-gonic/gin"
)

type manifestsStub struct {
	manifests []capability.Manifest
}

func (s manifestsStub) Manifests() []capability.Manifest { return s.manifests }

func newCapabilityRouter(manifests Manifests) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/capabilities", NewCapabilityHandler(manifests).List)
	return router
}

func TestCapabilityHandlerListsWhatThisBuildCanRun(t *testing.T) {
	router := newCapabilityRouter(manifestsStub{manifests: []capability.Manifest{{
		Name:        "chat",
		Description: "与模型对话",
		Stages:      []string{"exploring", "responding"},
	}}})

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

func TestCapabilityHandlerWithNothingRegisteredAnswersEmpty(t *testing.T) {
	router := newCapabilityRouter(manifestsStub{})

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
