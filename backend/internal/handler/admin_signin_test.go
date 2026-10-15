package handler

// The two sign-in writes: DELETE .../sessions and DELETE .../lock (§2.9).
//
// What the handler owns here is what it owns everywhere in the admin cases — the
// path the account id travels down, the shape of the answer, and the entry left
// behind. Whether taking an account's tokens back actually ends its sessions is
// storage's question and it is asked in internal/auth, against a real database;
// a case here could only agree with the stub.
//
// They are a file of their own because they are one subject stated twice: two
// routes that differ in the verb they call and agree in everything a client can
// see, which is what signInWriteCase is for.

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"backend/internal/audit"
)

// signInWriteCase is one of the two routes, so that the cases below state their
// claims once rather than twice.
type signInWriteCase struct {
	name    string
	path    string
	action  audit.Action
	calls   func(*adminStub) int
	subject func(*adminStub) int64
}

func signInWrites() []signInWriteCase {
	return []signInWriteCase{
		{
			name: "revoking the sessions", path: "/api/v1/admin/users/42/sessions",
			action: audit.ActionSessionsRevoked,
			calls:  func(s *adminStub) int { return s.revokeCalls },
			subject: func(s *adminStub) int64 {
				return s.revokedUser
			},
		},
		{
			name: "clearing the lock", path: "/api/v1/admin/users/42/lock",
			action: audit.ActionUnlocked,
			calls:  func(s *adminStub) int { return s.unlockCalls },
			subject: func(s *adminStub) int64 {
				return s.unlockedUser
			},
		},
	}
}

func TestAdminSignInWritesNameTheAccountAndRecordIt(t *testing.T) {
	for _, tt := range signInWrites() {
		t.Run(tt.name, func(t *testing.T) {
			stub := &adminStub{}
			ledger := &adminAuditStub{}
			recorder := callAs(t, newAdminRouterWithAudit(stub, ledger), http.MethodDelete, tt.path, "")

			if recorder.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204; body=%s", recorder.Code, recorder.Body.String())
			}
			// Nothing comes back: the change is a state that now holds, and there
			// is nothing about it a client could draw.
			if recorder.Body.Len() != 0 {
				t.Errorf("body = %q, want it empty", recorder.Body.String())
			}
			// The account is the path's and never the token's: the administrator
			// acting and the account acted on are two different people, and the
			// stub would not notice if the handler passed the wrong one.
			if tt.calls(stub) != 1 || tt.subject(stub) != 42 {
				t.Errorf("reached the service %d time(s) for account %d, want once for 42",
					tt.calls(stub), tt.subject(stub))
			}
			if len(ledger.entries) != 1 {
				t.Fatalf("entries = %d, want 1", len(ledger.entries))
			}
			entry := ledger.entries[0]
			if entry.Action != tt.action {
				t.Errorf("action = %q, want %q", entry.Action, tt.action)
			}
			if entry.Target.Kind != audit.TargetAccount || entry.Target.ID != "42" {
				t.Errorf("target = %s/%s, want account/42", entry.Target.Kind, entry.Target.ID)
			}
			// The subject is the account the entry is about, and it is here
			// because the actor is somebody else: it is the only column that
			// answers "what has been done to account 42".
			if entry.SubjectID != 42 {
				t.Errorf("subject = %d, want 42", entry.SubjectID)
			}
			if entry.Actor.ID != handlerAccountID {
				t.Errorf("actor = %d, want the account on the token %d", entry.Actor.ID, handlerAccountID)
			}
			if entry.Result != audit.ResultOK {
				t.Errorf("result = %q, want %q", entry.Result, audit.ResultOK)
			}
		})
	}
}

func TestAdminSignInWritesRefuseAnUnreadableAccount(t *testing.T) {
	for _, tt := range signInWrites() {
		for _, path := range []string{"/0", "/not-an-account"} {
			t.Run(tt.name+" "+path, func(t *testing.T) {
				stub := &adminStub{}
				ledger := &adminAuditStub{}
				full := strings.Replace(tt.path, "/42", path, 1)
				recorder := callAs(t, newAdminRouterWithAudit(stub, ledger), http.MethodDelete, full, "")

				if recorder.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
				}
				// A refused request reaches neither the change nor the ledger: an
				// entry about account zero would be an entry about nobody.
				if tt.calls(stub) != 0 || ledger.calls != 0 {
					t.Error("an unreadable account id reached the service or the audit")
				}
			})
		}
	}
}

func TestAdminSignInWritesRefuseToChangeWhatTheyCannotRecord(t *testing.T) {
	for _, tt := range signInWrites() {
		t.Run(tt.name, func(t *testing.T) {
			stub := &adminStub{}
			ledger := &adminAuditStub{err: errors.New("audit_events is unreachable at 10.0.0.4")}
			recorder := callAs(t, newAdminRouterWithAudit(stub, ledger), http.MethodDelete, tt.path, "")

			// A 500 and not a 204: the change may well have happened — the stub
			// says it reached the service — and the one answer this table exists
			// to prevent is a governance act that cannot be found afterwards.
			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
			}
			if code := decodeRefusal(t, recorder).Error.Code; code != "audit_unavailable" {
				t.Errorf("code = %q, want audit_unavailable", code)
			}
			if strings.Contains(recorder.Body.String(), "10.0.0.4") {
				t.Error("the response exposed the internal failure")
			}
		})
	}
}

func TestAdminSignInWritesWithoutALedgerAreNotServed(t *testing.T) {
	for _, tt := range signInWrites() {
		t.Run(tt.name, func(t *testing.T) {
			stub := &adminStub{}
			// A router built without an audit writer still refuses rather than
			// performing the change and leaving nothing behind.
			recorder := callAs(t, newAdminRouterWithAudit(stub, nil), http.MethodDelete, tt.path, "")

			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
			}
			if code := decodeRefusal(t, recorder).Error.Code; code != "admin_unavailable" {
				t.Errorf("code = %q, want admin_unavailable", code)
			}
			if tt.calls(stub) != 0 {
				t.Error("a change was made with nothing to record it in")
			}
		})
	}
}

func TestAdminSignInWritesReportAFailedChange(t *testing.T) {
	for _, tt := range signInWrites() {
		t.Run(tt.name, func(t *testing.T) {
			stub := &adminStub{sessionsErr: errors.New("users is unreachable at 10.0.0.4")}
			ledger := &adminAuditStub{}
			recorder := callAs(t, newAdminRouterWithAudit(stub, ledger), http.MethodDelete, tt.path, "")

			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
			}
			// A change that did not happen is not an entry: an audit of what was
			// done that includes what was not done is worse than no audit.
			if ledger.calls != 0 {
				t.Error("a change that failed was recorded")
			}
			if strings.Contains(recorder.Body.String(), "10.0.0.4") {
				t.Error("the response exposed the internal failure")
			}
		})
	}
}
