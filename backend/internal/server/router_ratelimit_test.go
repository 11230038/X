package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"backend/internal/limit"
	"backend/internal/middleware"
)

// routerLimiterStub is the limiter as the cases in this file need it: it writes
// down which class of work it was asked about, and answers what the case told
// it to. Nothing here is about how a bucket refills — internal/limit is where
// the arithmetic is tested — so the stub is what makes "is this route measured"
// and "does a refusal reach the client" answerable without one.
// The zero value is also what the shared builder in router_test.go hands in as
// the permissive limiter most cases want: a case about which surface a route
// sits in is not also a case about how fast somebody may ask.
type routerLimiterStub struct {
	refuse bool

	mu       sync.Mutex
	asked    []limit.Action
	accounts []int64
}

var _ middleware.Limiter = (*routerLimiterStub)(nil)

func (s *routerLimiterStub) Allow(account int64, action limit.Action) (limit.Verdict, error) {
	s.mu.Lock()
	s.asked = append(s.asked, action)
	s.accounts = append(s.accounts, account)
	s.mu.Unlock()
	if s.refuse {
		return limit.Verdict{Allowed: false, RetryAfter: 3 * time.Second}, nil
	}
	return limit.Verdict{Allowed: true}, nil
}

// askedAbout returns what the limiter was consulted about since the last time
// it was called, which is how a case asks a question about one request rather
// than about the run so far.
func (s *routerLimiterStub) askedAbout() []limit.Action {
	s.mu.Lock()
	defer s.mu.Unlock()
	actions := append([]limit.Action(nil), s.asked...)
	s.asked = nil
	return actions
}

// TestRouterMountsTheLimiterOnTheFourClassesAndNowhereElse checks §2.10's rule
// — limits are mounted on named classes of route, not globally — over the
// router's own route table rather than over a list kept beside it: every route
// the router registers is requested once, and the classes it consulted the
// limiter about are compared with the four this build names. A route added later
// by somebody who has not read that sentence is therefore a route this case
// reports as unlimited, instead of one that quietly joins a list nobody
// maintains.
//
// It also pins how many classes are *one* route: a route mounted with the
// limiter twice would count the same request against two buckets, and this is
// the only place that would be visible.
func TestRouterMountsTheLimiterOnTheFourClassesAndNowhereElse(t *testing.T) {
	limiter := &routerLimiterStub{}
	router := newTestRouterWithGates(t, readyChecker{}, nil, routerAuthorizerStub{}, limiter)

	// What leaves this process: a call to the chat provider, a call to the
	// embedding provider, bytes written to disk, and a keyword sent to a public
	// video site. The socket is the model class because every conversational
	// turn's calls happen inside it, and nothing between the upgrade and the
	// provider can count them.
	want := map[string]limit.Action{
		"GET /api/v1/ws":                                              limit.Model,
		"POST /api/v1/library/files":                                  limit.Upload,
		"POST /api/v1/knowledge-bases/:kbID/search":                   limit.Retrieval,
		"POST /api/v1/practice/reviews":                               limit.Model,
		"POST /api/v1/reading/materials/:materialID/extensions/:name": limit.Model,
		"POST /api/v1/books/:bookID/outline":                          limit.Model,
		"POST /api/v1/books/:bookID/chapters/:ordinal/generate":       limit.Model,
		"GET /api/v1/video/search":                                    limit.Search,
	}

	got := map[string]limit.Action{}
	for _, route := range router.Routes() {
		name := route.Method + " " + route.Path
		limiter.askedAbout()
		request := httptest.NewRequest(route.Method, route.Path, nil)
		request.Header.Set("Authorization", "Bearer "+routerAcceptedToken)
		router.ServeHTTP(httptest.NewRecorder(), request)

		// The route's own answer is not this case's business: what a stubbed
		// handler does is a different file's, and what a nil one does is a
		// panic the recovery middleware already turns into a 500. What is being
		// read here is which bucket the request was counted against, and that
		// survives every one of those answers.
		asked := limiter.askedAbout()
		switch len(asked) {
		case 0:
		case 1:
			got[name] = asked[0]
		default:
			t.Errorf("%s was counted against %d buckets, want 1", name, len(asked))
			got[name] = asked[0]
		}
	}

	for name, action := range want {
		switch gotAction, limited := got[name]; {
		case !limited:
			t.Errorf("%s is not limited, want %s", name, action)
		case gotAction != action:
			t.Errorf("%s is limited as %s, want %s", name, gotAction, action)
		}
	}
	for name, action := range got {
		if _, expected := want[name]; !expected {
			t.Errorf("%s is limited as %s, want it not to be limited at all", name, action)
		}
	}
}

// TestARefusedRequestIsAnsweredAtTheRoute: a refusal has to survive the trip
// through the router's own chain, and the handler behind it must not run. The
// body the stub would have answered with is what makes the second half of that
// visible — a 429 whose handler had already done the work would be a limit that
// costs what it was meant to save.
func TestARefusedRequestIsAnsweredAtTheRoute(t *testing.T) {
	limiter := &routerLimiterStub{refuse: true}
	router := newTestRouterWithGates(t, readyChecker{}, nil, routerAuthorizerStub{}, limiter)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/practice/reviews", nil)
	request.Header.Set("Authorization", "Bearer "+routerAcceptedToken)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"code":"rate_limited"`) {
		t.Errorf("body = %s, want rate_limited", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"retry_after":3`) {
		t.Errorf("body = %s, want the countdown in it", recorder.Body.String())
	}
	if got := recorder.Header().Get("Retry-After"); got != "3" {
		t.Errorf("Retry-After = %q, want 3", got)
	}
	if strings.Contains(recorder.Body.String(), "req_1") {
		t.Error("the review was judged on a request that was refused")
	}
	if accounts := limiter.accounts; len(accounts) != 1 || accounts[0] != 1 {
		t.Errorf("accounts = %v, want the one account on the token", accounts)
	}
}

// TestARouterWithoutALimiterStillAnswersEverythingElse: the absence is a
// failure of the routes that are measured and of nothing else. A router that
// refused every request without a limiter would make the mistake look like an
// outage, and one that served the measured routes anyway is the shape §2.10
// exists to prevent — so the two are pinned together, in one case, against the
// same router.
func TestARouterWithoutALimiterStillAnswersEverythingElse(t *testing.T) {
	router := newTestRouterWithGates(t, readyChecker{}, nil, routerAuthorizerStub{}, nil)

	limited := httptest.NewRequest(http.MethodPost, "/api/v1/practice/reviews", nil)
	limited.Header.Set("Authorization", "Bearer "+routerAcceptedToken)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, limited)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"code":"rate_limit_unavailable"`) {
		t.Errorf("body = %s, want rate_limit_unavailable", recorder.Body.String())
	}

	unlimited := httptest.NewRequest(http.MethodGet, "/api/v1/usage", nil)
	unlimited.Header.Set("Authorization", "Bearer "+routerAcceptedToken)
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, unlimited)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
}

// TestTheLimiterIsMountedBehindTheToken: the bucket is the account's, so the
// route has to know the account before it can measure anything. This is the
// order the router mounts them in, and it is asserted here because it is the
// one thing about the mount that a client could otherwise not tell: an
// unauthenticated request to a limited route is answered as unauthenticated
// rather than as an anonymous account sharing a bucket with everybody.
func TestTheLimiterIsMountedBehindTheToken(t *testing.T) {
	limiter := &routerLimiterStub{refuse: true}
	router := newTestRouterWithGates(t, readyChecker{}, nil, routerAuthorizerStub{}, limiter)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/library/files", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
	}
	if asked := limiter.askedAbout(); len(asked) != 0 {
		t.Errorf("the limiter was asked about %v before the account was known", asked)
	}
}
