package server

// Which routes exist at which path and method, behind the token.
//
// A client was told a shape and this is the only place that checks the shape was
// mounted. The group each route sits in — the thing only a refusal's surface
// witnesses — is router_surfaces_test.go, and the builder both files share is
// router_stubs_test.go.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRouterRegistersTheKnowledgeBaseAndReadRoutes pins the two things this
// file is the only place for: that each route exists at the path and method a
// client was told, and that it is behind the token.
//
// Reading a file is reading the material a turn was grounded on, and the
// knowledge bases beside it are the collections that material lives in, so
// neither can be public the way an upload still is.
func TestRouterRegistersTheKnowledgeBaseAndReadRoutes(t *testing.T) {
	router := newTestRouter(t, readyChecker{})

	routes := []struct{ name, method, path string }{
		{"file", http.MethodGet, "/api/v1/library/files/lib_1"},
		{"file text", http.MethodGet, "/api/v1/library/files/lib_1/text"},
		{"create base", http.MethodPost, "/api/v1/knowledge-bases"},
		{"list bases", http.MethodGet, "/api/v1/knowledge-bases"},
		{"get base", http.MethodGet, "/api/v1/knowledge-bases/kb_1"},
		{"delete base", http.MethodDelete, "/api/v1/knowledge-bases/kb_1"},
		{"add document", http.MethodPost, "/api/v1/knowledge-bases/kb_1/documents"},
		{"document text", http.MethodGet, "/api/v1/knowledge-bases/kb_1/documents/doc_1/text"},
		{"remove document", http.MethodDelete, "/api/v1/knowledge-bases/kb_1/documents/doc_1"},
		{"rebuild", http.MethodPost, "/api/v1/knowledge-bases/kb_1/rebuild"},
		{"search", http.MethodPost, "/api/v1/knowledge-bases/kb_1/search"},
		{"set default", http.MethodPost, "/api/v1/knowledge-bases/kb_1/default"},
		{"video search", http.MethodGet, "/api/v1/video/search?q=algebra"},
	}

	for _, route := range routes {
		t.Run(route.name+" without a token", func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(route.method, route.path, nil))
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), `"code":"authorization_required"`) {
				t.Fatalf("body = %s", recorder.Body.String())
			}
		})

		t.Run(route.name+" with a token", func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(route.method, route.path, nil)
			request.Header.Set("Authorization", "Bearer "+routerAcceptedToken)
			router.ServeHTTP(recorder, request)
			// Anything but these two means the route is there and a handler
			// answered it: a wrong path is 404 and a wrong method is 405.
			if recorder.Code == http.StatusNotFound || recorder.Code == http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

// TestRouterRegistersTheDirectChatRoute pins the one-shot route: one question
// in, one answer out, behind the token like every other model call.
//
// The body is what pins the other half — a request that reaches this handler
// answers with the direct chat stub's own sentence, which a route mounted
// against a neighboring handler could not produce.
func TestRouterRegistersTheDirectChatRoute(t *testing.T) {
	router := newTestRouter(t, readyChecker{})
	const path = "/api/v1/chat/complete"

	t.Run("without a token", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, nil))
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("with a token", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, path,
			strings.NewReader(`{"question":"1+2 等于几"}`))
		request.Header.Set("Authorization", "Bearer "+routerAcceptedToken)
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), `"answer":"直接回答"`) {
			t.Fatalf("body = %s", recorder.Body.String())
		}
	})
}

// TestRouterRegistersTheBookRoutes pins the ten routes a book is read and
// written through.
//
// The two generation routes take no request body, so the bodyless requests the
// loop sends are exactly what a client sends. The two writes that do take one
// answer 400 with no body, which is still the evidence this case wants: the
// route exists, a handler answered it, and it is behind the token.
func TestRouterRegistersTheBookRoutes(t *testing.T) {
	router := newTestRouter(t, readyChecker{})

	routes := []struct{ name, method, path string }{
		{"list books", http.MethodGet, "/api/v1/books"},
		{"create book", http.MethodPost, "/api/v1/books"},
		{"get book", http.MethodGet, "/api/v1/books/bk_1"},
		{"delete book", http.MethodDelete, "/api/v1/books/bk_1"},
		{"generate outline", http.MethodPost, "/api/v1/books/bk_1/outline"},
		{"save outline", http.MethodPut, "/api/v1/books/bk_1/outline"},
		{"generate chapter", http.MethodPost, "/api/v1/books/bk_1/chapters/1/generate"},
		{"read chapter", http.MethodGet, "/api/v1/books/bk_1/chapters/1"},
		{"save progress", http.MethodPut, "/api/v1/books/bk_1/progress"},
		{"export book", http.MethodGet, "/api/v1/books/bk_1/export"},
	}

	for _, route := range routes {
		t.Run(route.name+" without a token", func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(route.method, route.path, nil))
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
			}
		})

		t.Run(route.name+" with a token", func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(route.method, route.path, nil)
			request.Header.Set("Authorization", "Bearer "+routerAcceptedToken)
			router.ServeHTTP(recorder, request)
			if recorder.Code == http.StatusNotFound || recorder.Code == http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestRouterReadsAFileForWhoeverHoldsTheToken(t *testing.T) {
	router := newTestRouter(t, readyChecker{})

	for _, test := range []struct{ path, want string }{
		{"/api/v1/library/files/lib_1", `"filename":"手册.pdf"`},
		{"/api/v1/library/files/lib_1/text", `"text":"保修期是两年。"`},
	} {
		t.Run(test.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request.Header.Set("Authorization", "Bearer "+routerAcceptedToken)
			router.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), test.want) {
				t.Fatalf("body = %s, want %s", recorder.Body.String(), test.want)
			}
		})
	}
}

// TestRouterRegistersThePermissionsRoute pins the one route an account can reach
// with nothing else.
//
// It is mounted without a surface requirement on purpose: an account restricted
// out of every area must still be able to ask what it may reach, otherwise the
// screen that explains the refusal is itself refused.
func TestRouterRegistersThePermissionsRoute(t *testing.T) {
	router := newTestRouterWithAuthorizer(t, readyChecker{}, nil, routerAuthorizerStub{deny: true})
	const path = "/api/v1/me/permissions"

	t.Run("without a token", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("a denial elsewhere does not close it", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer "+routerAcceptedToken)
		router.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), `"preset":"standard"`) {
			t.Fatalf("body = %s", recorder.Body.String())
		}
	})
}

// TestRouterRegistersTheWebSocketTicketRoute pins the route a browser needs
// before it can open a socket at all.
//
// Like the permissions route above, it is mounted with no surface on purpose,
// and the denial sub-case is what says so: what a policy decides is which areas
// of the product an account may reach, and this reaches none of them — it hands
// back a credential, and the surface it will be spent on is checked where it is
// spent. An account restricted out of every area can still be handed the ticket
// it will be turned away with, which is a refusal that names the right reason.
func TestRouterRegistersTheWebSocketTicketRoute(t *testing.T) {
	router := newTestRouterWithAuthorizer(t, readyChecker{}, nil, routerAuthorizerStub{deny: true})
	const path = "/api/v1/ws-ticket"

	t.Run("without a token", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, nil))
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("a denial elsewhere does not close it", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, path, nil)
		request.Header.Set("Authorization", "Bearer "+routerAcceptedToken)
		router.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), `"ticket":"`+routerWsTicket+`"`) {
			t.Fatalf("body = %s", recorder.Body.String())
		}
	})
}
