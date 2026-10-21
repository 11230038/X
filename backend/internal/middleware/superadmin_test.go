package middleware

// The cases for the gate in front of the four account-lifecycle routes.
//
// The gate is deliberately small, and so are these: who is on the allowlist is
// the configuration's answer, and what the four routes do is the handler's.
// What is checked here is that the three states a request can arrive in get
// three different answers — no account, an account that is not listed, an
// account that is — and that the middle one is a refusal rather than a pass.

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"backend/internal/auth"
	"github.com/gin-gonic/gin"
)

func newSuperAdminRouter(user auth.User, present bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
	router := gin.New()
	router.Use(RequestID())
	router.GET("/protected",
		func(c *gin.Context) {
			if present {
				c.Set(currentUserKey, user)
			}
			c.Next()
		},
		RequireSuperAdmin(logger),
		func(c *gin.Context) {
			c.Status(http.StatusNoContent)
		})
	return router
}

func serveSuperAdmin(t *testing.T, router *gin.Engine) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/protected", nil))
	return recorder
}

func refusalCode(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("the response is not the API envelope: %v (%s)", err, recorder.Body.String())
	}
	return envelope.Error.Code
}

func TestRequireSuperAdminLetsAListedAccountThrough(t *testing.T) {
	router := newSuperAdminRouter(auth.User{
		UserID: 7, Username: "alice", Role: "admin", SuperAdmin: true,
	}, true)
	recorder := serveSuperAdmin(t, router)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRequireSuperAdminRefusesAnAdministratorWhoIsNotListed(t *testing.T) {
	router := newSuperAdminRouter(auth.User{
		UserID: 7, Username: "alice", Role: "admin", SuperAdmin: false,
	}, true)
	recorder := serveSuperAdmin(t, router)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", recorder.Code, recorder.Body.String())
	}
	if code := refusalCode(t, recorder); code != "super_admin_required" {
		t.Errorf("code = %q, want super_admin_required", code)
	}
}

// A route mounted without RequireAuth must fail closed. The check is written
// rather than assumed away because the omission that would remove it is exactly
// the kind no case above would notice.
func TestRequireSuperAdminRefusesARequestWithNoAccount(t *testing.T) {
	for _, user := range []auth.User{
		{},                            // nothing set at all
		{UserID: 0, SuperAdmin: true}, // a zero id, which is not an account
	} {
		router := newSuperAdminRouter(user, user != (auth.User{}))
		recorder := serveSuperAdmin(t, router)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
		}
		if code := refusalCode(t, recorder); code != "authentication_failed" {
			t.Errorf("code = %q, want authentication_failed", code)
		}
	}
}

// A nil logger is what a test or a half-built process may pass; refusing must
// not depend on having somewhere to write the refusal down.
func TestRequireSuperAdminWorksWithoutALogger(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestID())
	router.GET("/protected",
		func(c *gin.Context) { c.Set(currentUserKey, auth.User{UserID: 7}); c.Next() },
		RequireSuperAdmin(nil),
		func(c *gin.Context) { c.Status(http.StatusNoContent) })
	recorder := serveSuperAdmin(t, router)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", recorder.Code, recorder.Body.String())
	}
}
