package middleware

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"backend/internal/auth"
	"backend/internal/limit"
	"github.com/gin-gonic/gin"
)

// limiterStub answers with what the case set and writes down what it was asked,
// because half of these cases are about the question: which account, and which
// class of work.
type limiterStub struct {
	verdict limit.Verdict
	err     error
	calls   int
	account int64
	action  limit.Action
}

func (s *limiterStub) Allow(account int64, action limit.Action) (limit.Verdict, error) {
	s.calls++
	s.account, s.action = account, action
	return s.verdict, s.err
}

// newLimitedRouter mounts the three middlewares in the order the router does —
// who is asking, what they may reach, how fast — with a handler behind them
// that says it ran, so that a request which never reached the handler is
// visible in the body rather than inferred from the status.
func newLimitedRouter(verifier TokenVerifier, limiter Limiter, action limit.Action) *gin.Engine {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
	router := gin.New()
	router.Use(RequestID())
	router.GET("/expensive",
		RequireAuth(verifier, logger),
		RateLimit(limiter, action, logger),
		func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ran": true}) })
	return router
}

func limitedGet(t *testing.T, router *gin.Engine, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/expensive", nil)
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestARequestOverTheLimitIsAnsweredTooManyRequests(t *testing.T) {
	// 429 and not 403: nothing about the account is wrong, and the same request
	// a moment later is expected to work (§2.10). It is also a different shape
	// from `quota_exceeded`, which is a period rather than a countdown, and the
	// client draws the two differently.
	limiter := &limiterStub{verdict: limit.Verdict{Allowed: false, RetryAfter: 3 * time.Second}}
	router := newLimitedRouter(&verifierStub{user: auth.User{UserID: 7}}, limiter, limit.Model)

	recorder := limitedGet(t, router, "Bearer "+middlewareToken)
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body=%s", recorder.Code, recorder.Body.String())
	}
	if code := decodeErrorCode(t, recorder); code != "rate_limited" {
		t.Errorf("error code = %q, want rate_limited", code)
	}
	// The countdown is in the body for the screen to draw and in the header for
	// a client that already honours it without being taught this API.
	for _, want := range []string{`"retry_after":3`, `"action":"model"`} {
		if !strings.Contains(recorder.Body.String(), want) {
			t.Errorf("body = %s, want it to carry %s", recorder.Body.String(), want)
		}
	}
	if got := recorder.Header().Get("Retry-After"); got != "3" {
		t.Errorf("Retry-After = %q, want 3", got)
	}
	if strings.Contains(recorder.Body.String(), `"ran":true`) {
		t.Error("the handler ran on a request that was refused")
	}
}

// TestTheWaitIsNeverReportedAsZero: a bucket whose next token arrives in a
// fraction of a second reports that fraction, and a client told "retry in 0
// seconds" would retry at once and be refused again. The floor of one second is
// what the header and the body both carry.
func TestTheWaitIsNeverReportedAsZero(t *testing.T) {
	limiter := &limiterStub{verdict: limit.Verdict{Allowed: false, RetryAfter: 200 * time.Millisecond}}
	router := newLimitedRouter(&verifierStub{user: auth.User{UserID: 7}}, limiter, limit.Upload)

	recorder := limitedGet(t, router, "Bearer "+middlewareToken)
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"retry_after":1`) {
		t.Errorf("body = %s, want a one second wait", recorder.Body.String())
	}
}

func TestARequestWithinTheLimitReachesTheHandler(t *testing.T) {
	limiter := &limiterStub{verdict: limit.Verdict{Allowed: true}}
	router := newLimitedRouter(&verifierStub{user: auth.User{UserID: 7}}, limiter, limit.Model)

	recorder := limitedGet(t, router, "Bearer "+middlewareToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"ran":true`) {
		t.Errorf("body = %s, want the handler's answer", recorder.Body.String())
	}
	// The bucket is per account and per action, so both have to travel: the
	// account comes from the token rather than from the body, which is what
	// makes the limit the account's rather than the request's.
	if limiter.calls != 1 {
		t.Fatalf("the limiter was asked %d times, want 1", limiter.calls)
	}
	if limiter.account != 7 {
		t.Errorf("account = %d, want the account on the token", limiter.account)
	}
	if limiter.action != limit.Model {
		t.Errorf("action = %q, want %q", limiter.action, limit.Model)
	}
}

// TestARouteWithoutALimiterIsNotServed: a router built without one must not
// serve the expensive routes unmeasured. It is answered as a failure of the
// process rather than as a limit, because the account did nothing wrong and
// must not read it as a countdown.
func TestARouteWithoutALimiterIsNotServed(t *testing.T) {
	router := newLimitedRouter(&verifierStub{user: auth.User{UserID: 7}}, nil, limit.Model)

	recorder := limitedGet(t, router, "Bearer "+middlewareToken)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	if code := decodeErrorCode(t, recorder); code != "rate_limit_unavailable" {
		t.Errorf("error code = %q, want rate_limit_unavailable", code)
	}
	if strings.Contains(recorder.Body.String(), "ran") {
		t.Error("the handler ran without a limiter")
	}
	// The reason is logged and never sent, the same discipline the authorizer's
	// failures keep.
	if strings.Contains(recorder.Body.String(), "not configured") {
		t.Errorf("body = %s, want no reason in it", recorder.Body.String())
	}
}

// TestALimiterThatCannotSayIsNotServed is the loud half of the wiring mistake
// this middleware is the only place to catch: a route mounted with an action
// the deployment has no rate for. The failure is the process's, and the answer
// says nothing about the action — which is a name from this repository rather
// than anything a client sent, but the discipline is one rule and not a list of
// exceptions.
func TestALimiterThatCannotSayIsNotServed(t *testing.T) {
	limiter := &limiterStub{err: errors.New(`limit: unknown action: "retrival"`)}
	router := newLimitedRouter(&verifierStub{user: auth.User{UserID: 7}}, limiter, limit.Retrieval)

	recorder := limitedGet(t, router, "Bearer "+middlewareToken)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	if code := decodeErrorCode(t, recorder); code != "rate_limit_unavailable" {
		t.Errorf("error code = %q, want rate_limit_unavailable", code)
	}
	for _, unwanted := range []string{"retrival", "unknown action"} {
		if strings.Contains(recorder.Body.String(), unwanted) {
			t.Errorf("body = %s, want none of %q in it", recorder.Body.String(), unwanted)
		}
	}
}

// TestTheLimitNeedsAnAccountToBeAbout: there is no such thing as a limit on
// nobody, so a route mounted without RequireAuth in front of it is answered as
// unauthenticated rather than measured against account zero — which every
// anonymous request would share, turning a per-account limit into a global one.
func TestTheLimitNeedsAnAccountToBeAbout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
	limiter := &limiterStub{verdict: limit.Verdict{Allowed: true}}
	router := gin.New()
	router.Use(RequestID())
	router.GET("/expensive", RateLimit(limiter, limit.Model, logger),
		func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ran": true}) })

	recorder := limitedGet(t, router, "")
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
	}
	if limiter.calls != 0 {
		t.Errorf("the limiter was asked %d times, want 0", limiter.calls)
	}
}
