package server

// Which *group* each route sits in.
//
// The status of a refusal only says the authorizer was consulted; the surface on
// it says which group the mount ended up in, and that is the thing a mis-edited
// line in the router changes — plausibly and silently, by registering a route on
// a wider group than the one it belongs to. These cases are what makes that a
// failing assertion.
//
// Which routes exist at all is router_routes_test.go.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"backend/internal/authz"
)

// TestRouterRefusesAnAccountThePolicyDenies pins the 403/401 boundary on a real
// mounted route (§2.16).
//
// The two are deliberately not collapsed, and the only place that can be shown
// is here: the middleware answering them is written per route, so a route that
// mounted the authorizer without the token guard would answer 403 to a stranger
// — telling somebody who is not logged in what they may not do — and a route that
// mounted neither would serve them.
func TestRouterRefusesAnAccountThePolicyDenies(t *testing.T) {
	router := newTestRouterWithAuthorizer(t, readyChecker{}, nil, routerAuthorizerStub{deny: true})
	const path = "/api/v1/library/files/lib_1"

	t.Run("with a token it is forbidden and says why", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer "+routerAcceptedToken)
		router.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", recorder.Code, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), `"code":"permission_denied"`) {
			t.Fatalf("body = %s", recorder.Body.String())
		}
		// Which layer said no, and why, are in the body because they are what a
		// screen has to say instead of "no".
		for _, want := range []string{`"surface":"library"`, `"layer":"preset"`, `"reason":"baseline_closed"`} {
			if !strings.Contains(recorder.Body.String(), want) {
				t.Errorf("body = %s, want it to carry %s", recorder.Body.String(), want)
			}
		}
	})

	t.Run("without a token it is unauthenticated instead", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))

		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), `"code":"authorization_required"`) {
			t.Fatalf("body = %s", recorder.Body.String())
		}
	})

	t.Run("registering is not behind the policy", func(t *testing.T) {
		// A policy that refuses everything must not lock a person out of the two
		// routes that make an account. They are mounted outside both middlewares,
		// and this is the case that would catch one of them moving inside.
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register",
			strings.NewReader(`{"username":"alice","password":"correct-horse"}`))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
		}
	})
}

// TestRouterRegistersGovernanceUnderTheAdministrativeSurface pins the routes
// that make the authorization model reachable at all.
//
// They matter out of proportion to their number: a preset can be moved and a
// grant or a relationship written *only* here, so a missing mount would leave
// the four layers correct and unusable, with the failure showing up as an
// administrator who cannot govern rather than as an error anywhere.
//
// The surface in the refusal is what these assert on, and not the status: the
// status only says the authorizer was consulted, while `"surface":"admin"` says
// *which group* the route is in — which is the thing a mis-edited line in the
// router would change, plausibly and silently, by registering one of these on
// `authenticated` or under another surface.
func TestRouterRegistersGovernanceUnderTheAdministrativeSurface(t *testing.T) {
	denied := newTestRouterWithAuthorizer(t, readyChecker{}, nil, routerAuthorizerStub{deny: true})

	cases := []struct {
		method  string
		path    string
		body    string
		surface string
	}{
		{http.MethodGet, "/api/v1/admin/users", "", authz.SurfaceAdmin},
		{http.MethodPatch, "/api/v1/admin/users/42", `{"preset":"learner"}`, authz.SurfaceAdmin},
		// The two writes about whether an account can sign in at all, as against
		// what it may reach once it has. They are here rather than on the
		// unfaced group with the account's own sign-out because they act on
		// somebody else's account, and that is the whole difference between them.
		{http.MethodDelete, "/api/v1/admin/users/42/sessions", "", authz.SurfaceAdmin},
		{http.MethodDelete, "/api/v1/admin/users/42/lock", "", authz.SurfaceAdmin},
		{http.MethodGet, "/api/v1/admin/users/42/grants", "", authz.SurfaceAdmin},
		{http.MethodPost, "/api/v1/admin/users/42/grants", `{"kind":"surface","id":"reading","allowed":true}`, authz.SurfaceAdmin},
		{http.MethodDelete, "/api/v1/admin/users/42/grants", `{"kind":"surface","id":"reading"}`, authz.SurfaceAdmin},
		{http.MethodGet, "/api/v1/admin/guardians", "", authz.SurfaceAdmin},
		{http.MethodPost, "/api/v1/admin/guardians", `{"guardian_id":7,"learner_id":42}`, authz.SurfaceAdmin},
		{http.MethodDelete, "/api/v1/admin/guardians", `{"link_id":"lnk_1"}`, authz.SurfaceAdmin},
		{http.MethodGet, "/api/v1/admin/guardians/lnk_1/permissions", "", authz.SurfaceAdmin},
		{http.MethodPost, "/api/v1/admin/guardians/lnk_1/permissions", `{"permission":"view_reports"}`, authz.SurfaceAdmin},
		{http.MethodDelete, "/api/v1/admin/guardians/lnk_1/permissions", `{"permission":"view_reports"}`, authz.SurfaceAdmin},
		// A guardian's own view is a different surface from the administrator's,
		// because the two are different accounts: the standard baseline reaches
		// both, and a learner reaches neither.
		{http.MethodGet, "/api/v1/guardian/learners", "", authz.SurfaceGuardian},
		// The audit is the governance record read back, and it is the one route
		// here that answers about the whole instance rather than about one
		// account: what it is mounted under is therefore the whole of who may
		// read it.
		{http.MethodGet, "/api/v1/audit", "", authz.SurfaceAdmin},
		// The one route that answers with another account's material. It is in
		// the guardian area and it also carries a permission, and the surface in
		// the refusal is what says it was mounted as part of this group rather
		// than as its own thing.
		{http.MethodGet, "/api/v1/guardian/learners/42/reports", "", authz.SurfaceGuardian},
	}
	for _, testCase := range cases {
		t.Run(testCase.method+" "+testCase.path, func(t *testing.T) {
			t.Run("without a token it is unauthenticated", func(t *testing.T) {
				recorder := httptest.NewRecorder()
				router := newTestRouterWithAuthorizer(t, readyChecker{}, nil, routerAuthorizerStub{})
				router.ServeHTTP(recorder, routerRequest(testCase.method, testCase.path, testCase.body, false))
				if recorder.Code != http.StatusUnauthorized {
					t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
				}
			})

			t.Run("a denial names the surface it is in", func(t *testing.T) {
				recorder := httptest.NewRecorder()
				denied.ServeHTTP(recorder, routerRequest(testCase.method, testCase.path, testCase.body, true))
				if recorder.Code != http.StatusForbidden {
					t.Fatalf("status = %d, want 403; body=%s", recorder.Code, recorder.Body.String())
				}
				if want := `"surface":"` + testCase.surface + `"`; !strings.Contains(recorder.Body.String(), want) {
					t.Errorf("body = %s, want it to carry %s", recorder.Body.String(), want)
				}
			})

			t.Run("an allowed account reaches it", func(t *testing.T) {
				recorder := httptest.NewRecorder()
				router := newTestRouterWithAuthorizer(t, readyChecker{}, nil, routerAuthorizerStub{})
				router.ServeHTTP(recorder, routerRequest(testCase.method, testCase.path, testCase.body, true))
				// 204 joins the two here because the two sign-in writes above say
				// everything they have to say in the status: what changed is a
				// state that now holds, and a body would be a client's copy of
				// something only the account can be told by signing in again.
				switch recorder.Code {
				case http.StatusOK, http.StatusCreated, http.StatusNoContent:
				default:
					t.Fatalf("status = %d, want 200, 201 or 204; body=%s", recorder.Code, recorder.Body.String())
				}
			})
		})
	}
}

// TestRouterKeepsSigningOutAndChangingAPasswordOutsideEveryPolicy pins the two
// routes an account has over its own credential, and the thing about them that
// only a mount can show: neither is behind a surface (§2.9).
//
// The case to answer is a policy that refuses everything — an account whose
// preset reaches nothing, or one whose every item has been withdrawn. If either
// of these were mounted under a surface, such an account could not take its
// tokens back and could not replace the password that lets it in, and the failure
// would look like a client whose sign-out silently 403s. The permissions route
// beside them is mounted unfaced for the same reason; these two are the stronger
// case of it, because what is at stake is not being told why but being able to
// get out.
func TestRouterKeepsSigningOutAndChangingAPasswordOutsideEveryPolicy(t *testing.T) {
	denied := newTestRouterWithAuthorizer(t, readyChecker{}, nil, routerAuthorizerStub{deny: true})

	cases := []struct{ name, method, path, body string }{
		{"signing out", http.MethodPost, "/api/v1/auth/logout", ""},
		{"changing a password", http.MethodPut, "/api/v1/me/password",
			`{"old_password":"correct-horse","new_password":"a-much-longer-one"}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Run("without a token it is unauthenticated", func(t *testing.T) {
				recorder := httptest.NewRecorder()
				router := newTestRouterWithAuthorizer(t, readyChecker{}, nil, routerAuthorizerStub{})
				router.ServeHTTP(recorder,
					routerRequest(testCase.method, testCase.path, testCase.body, false))
				if recorder.Code != http.StatusUnauthorized {
					t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
				}
			})

			t.Run("a policy that refuses everything does not close it", func(t *testing.T) {
				recorder := httptest.NewRecorder()
				denied.ServeHTTP(recorder,
					routerRequest(testCase.method, testCase.path, testCase.body, true))
				if recorder.Code != http.StatusNoContent {
					t.Fatalf("status = %d, want 204; body=%s", recorder.Code, recorder.Body.String())
				}
			})
		})
	}
}

// The three reads a client makes about itself and the deployment it is talking
// to: the usage report (§2.7), and the build and the status (§2.15). They are
// mounted together and nowhere near the administrative surface. The two are
// different questions — the audit answers about the whole instance, these about
// the account asking and the process answering — and the surface in the refusal
// is the only thing that says which group a mount ended up in: the status only
// says the authorizer was consulted.
//
// The usage report is also the mount §7.2's security check turns on. That A
// cannot see a row of B's is decided in the query the handler builds
// (handler/usage_test.go) and in the scope the ledger reads through
// (usage/store_db_test.go); what this case adds is that the route is reachable at
// all, and reachable as the account's own.
func TestRouterRegistersTheSystemReadsUnderTheSystemSurface(t *testing.T) {
	for _, path := range []string{
		"/api/v1/usage",
		"/api/v1/system/version",
		"/api/v1/system/status",
	} {
		t.Run(path, func(t *testing.T) {
			t.Run("without a token it is unauthenticated", func(t *testing.T) {
				recorder := httptest.NewRecorder()
				router := newTestRouterWithAuthorizer(t, readyChecker{}, nil, routerAuthorizerStub{})
				router.ServeHTTP(recorder, routerRequest(http.MethodGet, path, "", false))
				if recorder.Code != http.StatusUnauthorized {
					t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
				}
			})

			t.Run("a denial names the surface it is in", func(t *testing.T) {
				recorder := httptest.NewRecorder()
				denied := newTestRouterWithAuthorizer(t, readyChecker{}, nil, routerAuthorizerStub{deny: true})
				denied.ServeHTTP(recorder, routerRequest(http.MethodGet, path, "", true))
				if recorder.Code != http.StatusForbidden {
					t.Fatalf("status = %d, want 403; body=%s", recorder.Code, recorder.Body.String())
				}
				if want := `"surface":"` + authz.SurfaceSystem + `"`; !strings.Contains(recorder.Body.String(), want) {
					t.Errorf("body = %s, want it to carry %s", recorder.Body.String(), want)
				}
			})

			t.Run("the account reaches it", func(t *testing.T) {
				recorder := httptest.NewRecorder()
				router := newTestRouterWithAuthorizer(t, readyChecker{}, nil, routerAuthorizerStub{})
				router.ServeHTTP(recorder, routerRequest(http.MethodGet, path, "", true))
				if recorder.Code != http.StatusOK {
					t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
				}
			})
		})
	}
}
