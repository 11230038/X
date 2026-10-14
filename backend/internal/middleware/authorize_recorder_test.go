package middleware

// The cases where a refusal is written down.
//
// They are separate from authorize_test.go because they are about a different
// question: that file is about whether a request is refused, and this one is
// about what the instance knows afterwards. The two are worth keeping apart
// because the second can be silently wrong while the first is perfectly right —
// a refusal that is never recorded looks exactly like a refusal that is.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"backend/internal/auth"
	"backend/internal/authz"
	"github.com/gin-gonic/gin"
)

// recorderStub keeps what it was handed. It is satisfied by the audit service
// through the adapter in main.go; here it is only the shape.
type recorderStub struct {
	refusals []Refusal
	err      error
}

func (s *recorderStub) RecordRefusal(_ context.Context, refusal Refusal) error {
	s.refusals = append(s.refusals, refusal)
	return s.err
}

// newRecordingRouter is newAuthorizedRouter with somewhere to write refusals
// down, and it is a different helper rather than an extra argument because the
// routes without a recorder are the ones every other case in the package uses.
func newRecordingRouter(
	verifier TokenVerifier, authorizer Authorizer, requirement Requirement,
	recorder Recorder, log *strings.Builder,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(log, nil))
	router := gin.New()
	router.Use(RequestID())
	router.GET("/protected",
		RequireAuth(verifier, logger),
		AuthorizeWith(authorizer, requirement, recorder, logger),
		func(c *gin.Context) { c.String(http.StatusOK, "through") })
	return router
}

func aNamedAccount() *verifierStub {
	return &verifierStub{user: auth.User{UserID: 7, Username: "wei", Role: authz.RoleUser}}
}

func aRefusingAuthorizer() *authorizerStub {
	return &authorizerStub{decision: authz.Decision{
		Layer: authz.LayerPreset, Reason: authz.ReasonBaselineClosed,
	}}
}

func TestARefusalIsHandedToTheRecorder(t *testing.T) {
	recorder := &recorderStub{}
	router := newRecordingRouter(aNamedAccount(), aRefusingAuthorizer(),
		Surface(authz.SurfaceReading), recorder, &strings.Builder{})

	if got := getAuthorized(t, router, "Bearer "+middlewareToken); got.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", got.Code)
	}
	if len(recorder.refusals) != 1 {
		t.Fatalf("the recorder was handed %d refusals, want one", len(recorder.refusals))
	}
	refusal := recorder.refusals[0]
	if refusal.Caller.UserID != 7 {
		t.Errorf("Caller = %+v, want the account on the token", refusal.Caller)
	}
	if refusal.Name != "wei" {
		t.Errorf("Name = %q, want the name RequireAuth had already loaded", refusal.Name)
	}
	if refusal.Surface != authz.SurfaceReading {
		t.Errorf("Surface = %q, want the area the route is in", refusal.Surface)
	}
	if refusal.Decision.Layer != authz.LayerPreset || refusal.Decision.Reason != authz.ReasonBaselineClosed {
		t.Errorf("Decision = %+v, want the layer and the reason that refused it", refusal.Decision)
	}
}

// The refusal and the log line for it are the same request, and the id is the
// only thing that says so.
func TestARefusalCarriesTheRequestId(t *testing.T) {
	recorder := &recorderStub{}
	router := newRecordingRouter(aNamedAccount(), aRefusingAuthorizer(),
		Surface(authz.SurfaceReading), recorder, &strings.Builder{})

	getAuthorized(t, router, "Bearer "+middlewareToken)

	if len(recorder.refusals) != 1 {
		t.Fatalf("the recorder was handed %d refusals, want one", len(recorder.refusals))
	}
	if recorder.refusals[0].RequestID == "" {
		t.Error("RequestID is empty: a refusal that cannot be joined to its log line")
	}
}

func TestAnAllowedRequestIsNotRecorded(t *testing.T) {
	recorder := &recorderStub{}
	authorizer := &authorizerStub{decision: authz.Decision{Allowed: true, Layer: authz.LayerSelf}}
	router := newRecordingRouter(aNamedAccount(), authorizer,
		Surface(authz.SurfaceReading), recorder, &strings.Builder{})

	if got := getAuthorized(t, router, "Bearer "+middlewareToken); got.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", got.Code)
	}
	if len(recorder.refusals) != 0 {
		t.Errorf("the recorder was handed %d refusals for a request that was allowed",
			len(recorder.refusals))
	}
}

// A recording that failed does not change the answer. The direction matters: a
// version that let the request through because the audit was unreachable would
// be a version where the audit's availability decides who may read whose
// material.
func TestAFailureToRecordDoesNotLetTheRequestThrough(t *testing.T) {
	var log strings.Builder
	recorder := &recorderStub{err: errors.New("the audit is not reachable")}
	router := newRecordingRouter(aNamedAccount(), aRefusingAuthorizer(),
		Surface(authz.SurfaceReading), recorder, &log)

	got := getAuthorized(t, router, "Bearer "+middlewareToken)
	if got.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 even though the record failed; body=%s",
			got.Code, got.Body.String())
	}
	if code := decodeErrorCode(t, got); code != "permission_denied" {
		t.Errorf("error code = %q, want the same refusal as any other", code)
	}
	// It is not swallowed either: an instance whose audit is unreachable has to
	// say so somewhere, and the log is the only place left.
	if !strings.Contains(log.String(), "recording a refusal failed") {
		t.Errorf("nothing was logged about the failure: %s", log.String())
	}
}

// A policy that could not answer is not a refusal. Nobody was told no — the
// question was not answered — and an entry filed as `denied` would say a person
// was refused something when the refusal never happened.
func TestAFailureToDecideIsNotRecordedAsARefusal(t *testing.T) {
	recorder := &recorderStub{}
	authorizer := &authorizerStub{err: errors.New("the policy is unreachable")}
	router := newRecordingRouter(aNamedAccount(), authorizer,
		Surface(authz.SurfaceReading), recorder, &strings.Builder{})

	if got := getAuthorized(t, router, "Bearer "+middlewareToken); got.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", got.Code)
	}
	if len(recorder.refusals) != 0 {
		t.Errorf("a failure to decide was recorded as %d refusals", len(recorder.refusals))
	}
}

// The routes that mount Authorize rather than AuthorizeWith still refuse. They
// are the ones every other test in this package uses, and an optional recorder
// that quietly turned the check off would be the worst of the available
// mistakes.
func TestARouteWithoutARecorderStillRefuses(t *testing.T) {
	router := newAuthorizedRouter(aNamedAccount(), aRefusingAuthorizer(), Surface(authz.SurfaceReading))

	if got := getAuthorized(t, router, "Bearer "+middlewareToken); got.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", got.Code)
	}
}
