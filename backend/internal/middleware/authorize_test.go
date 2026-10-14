package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"backend/internal/auth"
	"backend/internal/authz"
	"github.com/gin-gonic/gin"
)

// authorizerStub answers with whatever the case set, and records what it was
// asked, because half of these cases are about the question rather than the
// answer: the surface in the item, and the subject read off the request.
type authorizerStub struct {
	decision authz.Decision
	err      error
	calls    int
	actor    authz.Caller
	action   authz.Action
	subject  int64
}

func (s *authorizerStub) Decide(
	_ context.Context, actor authz.Caller, action authz.Action, subject int64,
) (authz.Decision, error) {
	s.calls++
	s.actor, s.action, s.subject = actor, action, subject
	return s.decision, s.err
}

// newAuthorizedRouter mounts the two middlewares in the order the router does
// and a handler behind them that reports the decision it was allowed under.
func newAuthorizedRouter(
	verifier TokenVerifier, authorizer Authorizer, requirement Requirement,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
	router := gin.New()
	router.Use(RequestID())
	router.GET("/protected",
		RequireAuth(verifier, logger),
		Authorize(authorizer, requirement, logger),
		func(c *gin.Context) {
			decision, ok := AuthorizedDecision(c)
			if !ok {
				c.String(http.StatusInternalServerError, "no decision in context")
				return
			}
			c.JSON(http.StatusOK, gin.H{"allowed": decision.Allowed, "reason": decision.Reason})
		})
	return router
}

func getAuthorized(t *testing.T, router *gin.Engine, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestAnAccountThePolicyRefusesIsAnsweredForbidden(t *testing.T) {
	// 403 and not 401 (§2.16). The person is logged in and the answer is about
	// what they may do, which is a different thing to do something about than
	// logging in again — and the two are deliberately not folded together.
	authorizer := &authorizerStub{decision: authz.Decision{
		Layer: authz.LayerPreset, Reason: authz.ReasonBaselineClosed,
	}}
	router := newAuthorizedRouter(&verifierStub{user: auth.User{UserID: 7, Role: authz.RoleUser}},
		authorizer, Surface(authz.SurfaceReading))

	recorder := getAuthorized(t, router, "Bearer "+middlewareToken)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", recorder.Code, recorder.Body.String())
	}
	if code := decodeErrorCode(t, recorder); code != "permission_denied" {
		t.Errorf("error code = %q, want permission_denied", code)
	}
	// The layer and the reason are in the body because they are what the screen
	// says instead of "no": which of the four layers refused, and why.
	for _, want := range []string{`"surface":"reading"`, `"layer":"preset"`, `"reason":"baseline_closed"`} {
		if !strings.Contains(recorder.Body.String(), want) {
			t.Errorf("body = %s, want it to carry %s", recorder.Body.String(), want)
		}
	}
}

func TestAnAccountThePolicyAllowsIsLetThrough(t *testing.T) {
	authorizer := &authorizerStub{decision: authz.Decision{Allowed: true, Layer: authz.LayerPreset}}
	router := newAuthorizedRouter(&verifierStub{user: auth.User{UserID: 7, Role: authz.RoleUser}},
		authorizer, Surface(authz.SurfaceReading))

	recorder := getAuthorized(t, router, "Bearer "+middlewareToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	// The decision the handler reads is the one that was acted on, which is the
	// reason it is published at all: a handler that asks a second time could get
	// a different answer, and the audit would record the one nobody enforced.
	if !strings.Contains(recorder.Body.String(), `"allowed":true`) {
		t.Errorf("body = %s, want the decision in it", recorder.Body.String())
	}
}

func TestTheQuestionCarriesTheAccountAndTheSurface(t *testing.T) {
	authorizer := &authorizerStub{decision: authz.Decision{Allowed: true}}
	requirement := Requirement{
		Surface:    authz.SurfaceMastery,
		Permission: authz.PermViewReports,
		Subject:    func(*gin.Context) int64 { return 42 },
	}
	router := newAuthorizedRouter(&verifierStub{user: auth.User{UserID: 7, Role: authz.RoleAdmin}},
		authorizer, requirement)

	if recorder := getAuthorized(t, router, "Bearer "+middlewareToken); recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if authorizer.calls != 1 {
		t.Fatalf("the policy was asked %d times, want 1", authorizer.calls)
	}
	// The role travels with the id, because RequireAuth has already reloaded the
	// account on this request and asking the database for it a second time would
	// let the two answers disagree.
	if authorizer.actor != (authz.Caller{UserID: 7, Role: authz.RoleAdmin}) {
		t.Errorf("actor = %+v, want the account from the token", authorizer.actor)
	}
	want := authz.Action{
		Item:       authz.Item{Kind: authz.KindSurface, ID: authz.SurfaceMastery},
		Permission: authz.PermViewReports,
	}
	if authorizer.action != want {
		t.Errorf("action = %+v, want %+v", authorizer.action, want)
	}
	if authorizer.subject != 42 {
		t.Errorf("subject = %d, want 42", authorizer.subject)
	}
}

func TestARouteAboutTheCallersOwnThingsNamesNoSubject(t *testing.T) {
	authorizer := &authorizerStub{decision: authz.Decision{Allowed: true}}
	router := newAuthorizedRouter(&verifierStub{user: auth.User{UserID: 7}},
		authorizer, Surface(authz.SurfaceChat))

	getAuthorized(t, router, "Bearer "+middlewareToken)
	// Zero is "the caller's own", which the policy reads as needing no
	// permission — the same value Service.Target answers for one's own id, so a
	// route that named no subject and one that named the caller agree.
	if authorizer.subject != 0 {
		t.Errorf("subject = %d, want 0 for a route about the caller", authorizer.subject)
	}
	if authorizer.action.Permission != "" {
		t.Errorf("permission = %q, want none", authorizer.action.Permission)
	}
}

func TestWithoutAnAccountTheAnswerIsUnauthenticated(t *testing.T) {
	// A route mounted without RequireAuth, which is a wiring mistake. It is
	// refused rather than allowed: the one failure mode this middleware must not
	// have is the one that turns a check off.
	authorizer := &authorizerStub{decision: authz.Decision{Allowed: true}}
	router := newAuthorizedRouter(&verifierStub{}, authorizer, Surface(authz.SurfaceChat))

	// No token at all: RequireAuth answers first, and it is a 401.
	recorder := getAuthorized(t, router, "")
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
	}
	if authorizer.calls != 0 {
		t.Fatalf("the policy was asked %d times about a request with no account", authorizer.calls)
	}
	if code := decodeErrorCode(t, recorder); code != "authorization_required" {
		t.Errorf("error code = %q, want authorization_required", code)
	}
}

func TestAnAccountOfZeroIsNotAnAccount(t *testing.T) {
	// RequireAuth accepted the token and what it resolved to names no account.
	// Authorize sits directly behind RequireAuth here, which is the only way to
	// reach it, and the answer has to be a refusal: an id of zero would be a
	// caller the policy reads as "the account with nothing".
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
	authorizer := &authorizerStub{decision: authz.Decision{Allowed: true}}
	router := gin.New()
	router.Use(RequestID())
	router.GET("/protected", func(c *gin.Context) {
		c.Set(currentUserKey, auth.User{})
	}, Authorize(authorizer, Surface(authz.SurfaceChat), logger),
		func(c *gin.Context) { c.String(http.StatusOK, "reached the handler") })

	recorder := getAuthorized(t, router, "")
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
	}
	if authorizer.calls != 0 {
		t.Errorf("the policy was asked %d times about an account of zero", authorizer.calls)
	}
}

func TestAFailureToDecideIsNotAPermissionToProceed(t *testing.T) {
	cases := []struct {
		name       string
		authorizer Authorizer
	}{
		{"no authorizer", nil},
		{"the policy failed", &authorizerStub{err: errors.New("connection pool exhausted")}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			router := newAuthorizedRouter(&verifierStub{user: auth.User{UserID: 7}},
				testCase.authorizer, Surface(authz.SurfaceChat))
			recorder := getAuthorized(t, router, "Bearer "+middlewareToken)
			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
			}
			if code := decodeErrorCode(t, recorder); code != "authorization_unavailable" {
				t.Errorf("error code = %q, want authorization_unavailable", code)
			}
			// The same rule as RequireAuth's: an internal failure says nothing
			// about itself, because the message would be read in places it
			// should not reach.
			if strings.Contains(recorder.Body.String(), "connection pool exhausted") {
				t.Error("the response exposed the internal failure")
			}
		})
	}
}
