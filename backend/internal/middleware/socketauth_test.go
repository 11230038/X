package middleware

// The websocket gate: a route that takes two kinds of credential, and the rules
// about which one it reads first and what a refusal is called.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"backend/internal/auth"
	"github.com/gin-gonic/gin"
)

const middlewareTicket = "valid-ticket"

type ticketStub struct {
	user     auth.User
	err      error
	callWith string
	calls    int
}

func (s *ticketStub) Redeem(_ context.Context, ticket string) (auth.User, error) {
	s.calls++
	s.callWith = ticket
	return s.user, s.err
}

// newSocketRouter mounts RequireSocketAuth with a handler that echoes whatever
// account reached the context, so a test sees both the refusal and the
// pass-through.
func newSocketRouter(tokens TokenVerifier, tickets TicketVerifier) *gin.Engine {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
	router := gin.New()
	router.Use(RequestID())
	router.GET("/ws", RequireSocketAuth(tokens, tickets, logger), func(c *gin.Context) {
		user, ok := CurrentUser(c)
		if !ok {
			c.String(http.StatusInternalServerError, "no user in context")
			return
		}
		c.JSON(http.StatusOK, gin.H{"username": user.Username, "user_id": user.UserID, "role": user.Role})
	})
	return router
}

// callSocket sends the query string and the Authorization header a test wants,
// either of which may be empty.
func callSocket(t *testing.T, router *gin.Engine, query, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	target := "/ws"
	if query != "" {
		target += "?" + query
	}
	request := httptest.NewRequest(http.MethodGet, target, nil)
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func decodeSocketUser(t *testing.T, recorder *httptest.ResponseRecorder) auth.User {
	t.Helper()
	var body struct {
		Username string `json:"username"`
		UserID   int64  `json:"user_id"`
		Role     string `json:"role"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
	return auth.User{UserID: body.UserID, Username: body.Username, Role: body.Role}
}

func TestRequireSocketAuthAcceptsATicket(t *testing.T) {
	account := auth.User{UserID: 3, Username: "alice", Role: "user"}
	tickets := &ticketStub{user: account}
	recorder := callSocket(t, newSocketRouter(nil, tickets), "ticket="+middlewareTicket, "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if tickets.callWith != middlewareTicket {
		t.Errorf("Redeem received %q, want %q", tickets.callWith, middlewareTicket)
	}
	// The role reaches the handler, because the ticket was resolved into a whole
	// account rather than into an id.
	if got := decodeSocketUser(t, recorder); got != account {
		t.Errorf("the handler saw %+v, want %+v", got, account)
	}
}

// TestRequireSocketAuthStillAcceptsABearerToken is what keeps cmd/turnprobe
// working. The ticket is for browsers; the clients that can set a header should
// not have to fetch one.
func TestRequireSocketAuthStillAcceptsABearerToken(t *testing.T) {
	account := auth.User{UserID: 3, Username: "alice", Role: "user"}
	tokens := &verifierStub{user: account}
	tickets := &ticketStub{}
	recorder := callSocket(t, newSocketRouter(tokens, tickets), "", "Bearer "+middlewareToken)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if tokens.callWith != middlewareToken {
		t.Errorf("Authenticate received %q, want %q", tokens.callWith, middlewareToken)
	}
	if tickets.calls != 0 {
		t.Errorf("the ticket verifier was consulted %d time(s) for a bearer request", tickets.calls)
	}
}

// TestRequireSocketAuthReadsTheTicketFirst pins the precedence. A real browser
// sends one or the other, so this only decides what happens if a client ever
// sends both — and the answer is that a bad ticket must not be covered up by a
// token that still works, or a broken handshake would look like a working one.
func TestRequireSocketAuthReadsTheTicketFirst(t *testing.T) {
	tokens := &verifierStub{user: auth.User{UserID: 3, Username: "alice", Role: "user"}}
	tickets := &ticketStub{err: auth.ErrInvalidTicket}
	recorder := callSocket(t, newSocketRouter(tokens, tickets),
		"ticket=stale", "Bearer "+middlewareToken)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
	}
	if code := decodeErrorCode(t, recorder); code != "invalid_ticket" {
		t.Errorf("code = %q, want invalid_ticket", code)
	}
	if tokens.calls != 0 {
		t.Errorf("the token verifier was consulted %d time(s) while a ticket was present", tokens.calls)
	}
}

func TestRequireSocketAuthRefusals(t *testing.T) {
	tests := []struct {
		name    string
		tickets TicketVerifier
		query   string
		status  int
		code    string
	}{
		{
			name:    "an expired or forged ticket",
			tickets: &ticketStub{err: auth.ErrInvalidTicket},
			query:   "ticket=stale",
			status:  http.StatusUnauthorized,
			code:    "invalid_ticket",
		},
		{
			// The ticket was fine; the account it named was revoked after it was
			// issued. The client has to sign in again, not fetch another ticket
			// with a token that no longer works either.
			name:    "a ticket whose account was revoked",
			tickets: &ticketStub{err: auth.ErrInvalidToken},
			query:   "ticket=stale",
			status:  http.StatusUnauthorized,
			code:    "invalid_ticket",
		},
		{
			name:    "a ticket for a disabled account",
			tickets: &ticketStub{err: auth.ErrAccountDisabled},
			query:   "ticket=stale",
			status:  http.StatusForbidden,
			code:    "account_disabled",
		},
		{
			name:    "a storage failure is not a refusal",
			tickets: &ticketStub{err: errors.New(middlewareInternal)},
			query:   "ticket=stale",
			status:  http.StatusInternalServerError,
			code:    "authentication_failed",
		},
		{
			name:   "neither credential",
			query:  "",
			status: http.StatusUnauthorized,
			code:   "authorization_required",
		},
		{
			name:   "a ticket parameter with nothing in it",
			query:  "ticket=",
			status: http.StatusUnauthorized,
			code:   "authorization_required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := &verifierStub{}
			recorder := callSocket(t, newSocketRouter(tokens, tt.tickets), tt.query, "")
			if recorder.Code != tt.status {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tt.status, recorder.Body.String())
			}
			if code := decodeErrorCode(t, recorder); code != tt.code {
				t.Errorf("code = %q, want %q", code, tt.code)
			}
		})
	}
}

// TestRequireSocketAuthFailsLoudlyWithoutAVerifier covers the wiring mistake
// rather than a caller's: a route mounted before the ticket verifier is handed
// over must say so, not refuse everybody as though their tickets were bad.
func TestRequireSocketAuthFailsLoudlyWithoutAVerifier(t *testing.T) {
	recorder := callSocket(t, newSocketRouter(nil, nil), "ticket="+middlewareTicket, "")
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	if code := decodeErrorCode(t, recorder); code != "authentication_failed" {
		t.Errorf("code = %q, want authentication_failed", code)
	}
}
