package handler

// The cases for GET /me/permissions. What this route is for is a client drawing
// a refusal before it gets one, so what these pin is that the wire carries every
// part of the answer a screen needs: which account, which preset, every item the
// policy has an answer about — with the layer and the reason that explain it —
// and the relationships the caller holds as guardian.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"backend/internal/authz"
	"backend/internal/middleware"
	"github.com/gin-gonic/gin"
)

// permissionsStub is the authorization service as this layer reaches it.
type permissionsStub struct {
	self  authz.Self
	err   error
	calls int
	actor authz.Caller
}

func (s *permissionsStub) Self(_ context.Context, caller authz.Caller) (authz.Self, error) {
	s.calls++
	s.actor = caller
	return s.self, s.err
}

func newPermissionsRouter(stub *permissionsStub) *gin.Engine {
	gin.SetMode(gin.TestMode)
	logger := testHandlerLogger()
	handler := NewPermissionsHandler(stub, logger)
	guarded := middleware.RequireAuth(handlerVerifierStub{}, logger)
	router := gin.New()
	router.Use(middleware.RequestID())
	router.GET("/api/v1/me/permissions", guarded, handler.Get)
	return router
}

func TestPermissionsReportsEveryItemWithItsReason(t *testing.T) {
	stub := &permissionsStub{self: authz.Self{
		Preset: authz.PresetLearner,
		Items: []authz.Decision{
			{Item: authz.Item{Kind: authz.KindCapability, ID: "chat"},
				Allowed: true, Layer: authz.LayerFloor, Reason: authz.ReasonFloorProtected},
			{Item: authz.Item{Kind: authz.KindSurface, ID: authz.SurfaceReading},
				Allowed: false, Layer: authz.LayerPreset, Reason: authz.ReasonBaselineClosed},
		},
		Guardian: []authz.Link{{
			ID: "lnk_1", LearnerID: 42, Permissions: []authz.Permission{authz.PermViewReports},
			CreatedAt: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC),
		}},
	}}
	recorder := callAs(t, newPermissionsRouter(stub), http.MethodGet, "/api/v1/me/permissions", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}

	var body struct {
		Data struct {
			UserID int64  `json:"user_id"`
			Role   string `json:"role"`
			Preset string `json:"preset"`
			Items  []struct {
				Kind    string `json:"kind"`
				ID      string `json:"id"`
				Allowed bool   `json:"allowed"`
				Layer   string `json:"layer"`
				Reason  string `json:"reason"`
			} `json:"items"`
			Guardian []struct {
				LinkID      string   `json:"link_id"`
				LearnerID   int64    `json:"learner_id"`
				Permissions []string `json:"permissions"`
			} `json:"guardian"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &body)

	data := body.Data
	// The account comes from the token rather than from the request, which is
	// what makes this route one no client can ask about somebody else.
	if data.UserID != handlerAccountID {
		t.Errorf("user_id = %d, want %d", data.UserID, handlerAccountID)
	}
	if data.Preset != authz.PresetLearner {
		t.Errorf("preset = %q, want learner", data.Preset)
	}
	// The role is reported so a client can draw the administrative door. It is
	// read off the token, not decided here.
	if data.Role != "user" {
		t.Errorf("role = %q, want the account's own", data.Role)
	}
	if len(data.Items) != 2 {
		t.Fatalf("items = %+v, want two", data.Items)
	}
	// The reason travels with the boolean: a screen that drew "false" alone
	// could not say whether the floor kept it or a guardian closed it.
	if first := data.Items[0]; first.Kind != "capability" || first.ID != "chat" ||
		!first.Allowed || first.Layer != "floor" || first.Reason != "floor_protected" {
		t.Errorf("the floor's item came back as %+v", first)
	}
	if second := data.Items[1]; second.Allowed || second.Reason != "baseline_closed" {
		t.Errorf("the closed surface came back as %+v", second)
	}
	if len(data.Guardian) != 1 || data.Guardian[0].LearnerID != 42 ||
		len(data.Guardian[0].Permissions) != 1 || data.Guardian[0].Permissions[0] != "view_reports" {
		t.Errorf("guardian = %+v, want the one relationship with its permission", data.Guardian)
	}
}

func TestPermissionsAnswersWithEmptyListsRatherThanNull(t *testing.T) {
	// An account with no grants and no learners is answered with arrays, not
	// nulls: a client that had to special-case the empty case would be the one
	// place the shape of this response is two shapes.
	stub := &permissionsStub{self: authz.Self{Preset: authz.PresetStandard}}
	recorder := callAs(t, newPermissionsRouter(stub), http.MethodGet, "/api/v1/me/permissions", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	decodeBody(t, recorder, &body)
	for _, field := range []string{"items", "guardian"} {
		if got := string(body.Data[field]); got != "[]" {
			t.Errorf("%s = %s, want []", field, got)
		}
	}
}

func TestPermissionsAsksAboutTheAccountOnTheToken(t *testing.T) {
	stub := &permissionsStub{self: authz.Self{Preset: authz.PresetStandard}}
	callAs(t, newPermissionsRouter(stub), http.MethodGet, "/api/v1/me/permissions", "")

	if stub.calls != 1 {
		t.Fatalf("the service was asked %d times, want 1", stub.calls)
	}
	if stub.actor.UserID != handlerAccountID {
		t.Errorf("asked about %d, want %d", stub.actor.UserID, handlerAccountID)
	}
}

func TestPermissionsNeedsAToken(t *testing.T) {
	stub := &permissionsStub{self: authz.Self{Preset: authz.PresetStandard}}
	recorder := call(t, newPermissionsRouter(stub), http.MethodGet, "/api/v1/me/permissions", "")
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.calls != 0 {
		t.Errorf("the service was asked %d times without a token", stub.calls)
	}
}

func TestPermissionsSaysNothingAboutAFailure(t *testing.T) {
	stub := &permissionsStub{err: errors.New("grant store is unreachable")}
	recorder := callAs(t, newPermissionsRouter(stub), http.MethodGet, "/api/v1/me/permissions", "")
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeBody(t, recorder, &envelope)
	if envelope.Error.Code != "permissions_unavailable" {
		t.Errorf("error code = %q, want permissions_unavailable", envelope.Error.Code)
	}
	// The same rule as every other 500 in this layer: what failed internally is
	// logged and not sent, because the message would be read in places it should
	// not reach.
	if strings.Contains(recorder.Body.String(), "grant store is unreachable") {
		t.Error("the response exposed the internal failure")
	}
}
