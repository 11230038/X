package handler

// The two session routes that hang off the authenticated face: POST
// /auth/logout and PUT /me/password.
//
// They are separate from the credential cases because they are the pair that
// answers with nothing. Register and login hand back a session; these two end one
// and re-key one, and what a client can check about them is a status and a
// subsequent refusal rather than a body.

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"backend/internal/auth"
	"github.com/gin-gonic/gin"
)

func TestAuthHandlerLogoutRevokesTheAccountInTheToken(t *testing.T) {
	stub := &authStub{}
	router := newAuthHandlerRouter(stub)

	recorder := logout(t, router, true)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", recorder.Code, recorder.Body.String())
	}
	// The body is the whole of what a client is told: the tokens it holds are
	// gone and nothing replaces them, so there is nothing to hand back.
	if recorder.Body.Len() != 0 {
		t.Errorf("body = %q, want it empty", recorder.Body.String())
	}
	// The id is the token's, not the request's: a body cannot name whose tokens
	// stop counting, because there is no body.
	if stub.revokeCalls != 1 || stub.revokedUser != handlerAccountID {
		t.Errorf("revoked %d account(s), last = %d, want one call for %d",
			stub.revokeCalls, stub.revokedUser, handlerAccountID)
	}
}

func TestAuthHandlerLogoutWithoutATokenRevokesNothing(t *testing.T) {
	stub := &authStub{}
	recorder := logout(t, newAuthHandlerRouter(stub), false)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.revokeCalls != 0 {
		t.Fatalf("an unauthenticated request reached the service %d time(s)", stub.revokeCalls)
	}
}

func TestAuthHandlerMapsLogoutFailure(t *testing.T) {
	stub := &authStub{err: errors.New(handlerInternal)}
	recorder := logout(t, newAuthHandlerRouter(stub), true)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"code":"logout_failed"`) {
		t.Fatalf("body = %s, want code logout_failed", recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), handlerInternal) {
		t.Fatal("response leaked the internal error")
	}
}

// logout posts to the route the way a client does: nothing but the token.
func logout(t *testing.T, router *gin.Engine, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	if authenticated {
		request.Header.Set("Authorization", "Bearer "+handlerToken)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestAuthHandlerChangesThePasswordOfTheAccountOnTheToken(t *testing.T) {
	stub := &authStub{}
	router := newAuthHandlerRouter(stub)

	recorder := changePassword(t, router, `{"old_password":"`+handlerPassword+`","new_password":"a-much-longer-one"}`, true)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", recorder.Code, recorder.Body.String())
	}
	// Nothing comes back, and that is the whole of §2.2: every token this account
	// held has just stopped counting, this request's included, so there is no
	// session to hand over. A body carrying a replacement token is exactly what
	// this version decided not to issue.
	if recorder.Body.Len() != 0 {
		t.Errorf("body = %q, want it empty", recorder.Body.String())
	}
	call := stub.passwordCall
	if stub.passwordCalls != 1 || call.userID != handlerAccountID {
		t.Errorf("reached the service %d time(s) for account %d, want once for %d",
			stub.passwordCalls, call.userID, handlerAccountID)
	}
	// The two passwords are in the fields they arrived in: a handler that passed
	// them the other way round would set the account's password to the string an
	// attacker sent and hand the real one back to them.
	if call.old != handlerPassword || call.new != "a-much-longer-one" {
		t.Errorf("passed old=%q new=%q, want the old one then the new one", call.old, call.new)
	}
	if strings.Contains(recorder.Body.String(), handlerPassword) {
		t.Error("the response echoed the password")
	}
}

func TestAuthHandlerChangePasswordWithoutATokenChangesNothing(t *testing.T) {
	stub := &authStub{}
	recorder := changePassword(t, newAuthHandlerRouter(stub),
		`{"old_password":"`+handlerPassword+`","new_password":"a-much-longer-one"}`, false)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.passwordCalls != 0 {
		t.Fatal("an unauthenticated request reached the service")
	}
}

func TestAuthHandlerRejectsAMalformedPasswordChange(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantCode string
	}{
		{name: "malformed json", body: `{"old_password":`, wantCode: "invalid_request"},
		// An unknown field is refused rather than ignored, exactly as it is on
		// registration and sign-in: a client that sent `password` instead of
		// `new_password` would otherwise have its request read as a change to the
		// empty password.
		{name: "unknown field", body: `{"old_password":"a","password":"b"}`, wantCode: "invalid_request"},
		{name: "two documents", body: `{"old_password":"a","new_password":"b"}{"old_password":"c"}`, wantCode: "invalid_request"},
		{name: "empty body", body: ``, wantCode: "invalid_request"},
		{name: "oversized body", body: `{"old_password":"` + strings.Repeat("a", 8192) + `"}`, wantCode: "request_too_large"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &authStub{}
			recorder := changePassword(t, newAuthHandlerRouter(stub), tt.body, true)

			if !strings.Contains(recorder.Body.String(), `"code":"`+tt.wantCode+`"`) {
				t.Fatalf("body = %s, want code %s", recorder.Body.String(), tt.wantCode)
			}
			if stub.passwordCalls != 0 {
				t.Fatal("a malformed request reached the service")
			}
		})
	}
}

func TestAuthHandlerMapsPasswordErrors(t *testing.T) {
	tests := []struct {
		err        error
		wantStatus int
		wantCode   string
	}{
		// The token is good — this is the account it names — so the failure is
		// not an authentication one. A 401 here would have a client drop a
		// credential that still works.
		{err: auth.ErrWrongPassword, wantStatus: 403, wantCode: "invalid_credentials"},
		// The same code registration answers with, because it is the same rule:
		// a new password is judged by the policy every password is judged by.
		{err: auth.ErrInvalidPassword, wantStatus: 400, wantCode: "invalid_password"},
		{err: errors.New(handlerInternal), wantStatus: 500, wantCode: "password_change_failed"},
	}

	for _, tt := range tests {
		t.Run(tt.wantCode, func(t *testing.T) {
			router := newAuthHandlerRouter(&authStub{err: tt.err})
			recorder := changePassword(t, router,
				`{"old_password":"`+handlerPassword+`","new_password":"a-much-longer-one"}`, true)

			if recorder.Code != tt.wantStatus || !strings.Contains(recorder.Body.String(), `"code":"`+tt.wantCode+`"`) {
				t.Fatalf("status/body = %d %s, want %d %s",
					recorder.Code, recorder.Body.String(), tt.wantStatus, tt.wantCode)
			}
			if strings.Contains(recorder.Body.String(), handlerInternal) {
				t.Fatal("response leaked the internal error")
			}
		})
	}
}

// changePassword puts to the route the way a client does: a body with the two
// passwords, and the token when the case is about an authenticated one.
func changePassword(t *testing.T, router *gin.Engine, body string, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/me/password", strings.NewReader(body))
	request.Header.Set("Content-Type", handlerBodyAccept)
	if authenticated {
		request.Header.Set("Authorization", "Bearer "+handlerToken)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}
