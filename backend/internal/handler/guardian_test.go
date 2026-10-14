package handler

// The cases for the relationship routes. A relationship is the one path in this
// product that reaches another account's material, so what these pin is that
// every part of "why is this allowed" survives the wire: who the two accounts
// are, which permissions the link carries, and — for each of AUTH-04's three
// prohibitions — the code that says which one refused.
//
// The rules live in the service. What is asserted here is that a refusal arrives
// as its own sentence rather than as a generic 400, because a client that cannot
// tell "that is not a learner" from "you already have this" has to guess.

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"backend/internal/authz"
	"backend/internal/middleware"
	"github.com/gin-gonic/gin"
)

// guardianStub is the relationship service as this layer reaches it.
type guardianStub struct {
	links     []authz.Link
	link      authz.Link
	linkErr   error
	listCalls int

	allCalls int
	allErr   error

	createdGuardian int64
	createdLearner  int64
	createdPerms    []authz.Permission
	createdBy       authz.Caller
	createCalls     int
	createErr       error

	readLinkID string
	readCalls  int

	revokedID   string
	revokeCalls int
	revokeErr   error

	grantedLinkID string
	grantedPerm   authz.Permission
	grantedBy     authz.Caller
	grantCalls    int
	grantErr      error

	revokedPerm     authz.Permission
	revokePermID    string
	revokePermCalls int
	revokePermErr   error
}

func (s *guardianStub) AllRelationships(context.Context) ([]authz.Link, error) {
	s.allCalls++
	return s.links, s.allErr
}

func (s *guardianStub) Relationships(_ context.Context, guardianID int64) ([]authz.Link, error) {
	s.listCalls++
	s.createdGuardian = guardianID
	return s.links, nil
}

func (s *guardianStub) Link(_ context.Context, caller authz.Caller, guardianID, learnerID int64, permissions []authz.Permission) (authz.Link, error) {
	s.createCalls++
	s.createdBy, s.createdGuardian, s.createdLearner = caller, guardianID, learnerID
	s.createdPerms = permissions
	return s.link, s.createErr
}

func (s *guardianStub) Revoke(_ context.Context, linkID string) error {
	s.revokeCalls++
	s.revokedID = linkID
	return s.revokeErr
}

func (s *guardianStub) LinkByID(_ context.Context, linkID string) (authz.Link, error) {
	s.readCalls++
	s.readLinkID = linkID
	return s.link, s.linkErr
}

func (s *guardianStub) GrantPermission(_ context.Context, caller authz.Caller, linkID string, permission authz.Permission) error {
	s.grantCalls++
	s.grantedBy, s.grantedLinkID, s.grantedPerm = caller, linkID, permission
	return s.grantErr
}

func (s *guardianStub) RevokePermission(_ context.Context, linkID string, permission authz.Permission) error {
	s.revokePermCalls++
	s.revokePermID, s.revokedPerm = linkID, permission
	return s.revokePermErr
}

func newGuardianRouter(stub *guardianStub) *gin.Engine {
	gin.SetMode(gin.TestMode)
	logger := testHandlerLogger()
	handler := NewGuardianHandler(stub, logger)
	guarded := middleware.RequireAuth(handlerVerifierStub{}, logger)
	router := gin.New()
	router.Use(middleware.RequestID())
	router.GET("/api/v1/admin/guardians", guarded, handler.ListRelationships)
	router.POST("/api/v1/admin/guardians", guarded, handler.CreateRelationship)
	router.DELETE("/api/v1/admin/guardians", guarded, handler.RevokeRelationship)
	router.GET("/api/v1/admin/guardians/:linkID/permissions", guarded, handler.ListPermissions)
	router.POST("/api/v1/admin/guardians/:linkID/permissions", guarded, handler.GrantPermission)
	router.DELETE("/api/v1/admin/guardians/:linkID/permissions", guarded, handler.RevokePermission)
	router.GET("/api/v1/guardian/learners", guarded, handler.ListLearners)
	return router
}

// aRelationship is one link with the permission that makes it worth having.
func aRelationship() authz.Link {
	return authz.Link{
		ID: "lnk_1", GuardianID: 7, LearnerID: 42,
		Permissions: []authz.Permission{authz.PermViewReports},
	}
}

// relationshipBody is the answer shape, for the cases that read it back.
type relationshipBody struct {
	Data struct {
		LinkID      string   `json:"link_id"`
		GuardianID  int64    `json:"guardian_id"`
		LearnerID   int64    `json:"learner_id"`
		Permissions []string `json:"permissions"`
	} `json:"data"`
}

func TestGuardianListsTheLearnersTheCallerWatches(t *testing.T) {
	stub := &guardianStub{links: []authz.Link{aRelationship()}}
	recorder := callAs(t, newGuardianRouter(stub), http.MethodGet, "/api/v1/guardian/learners", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	// The list is the caller's own, and the caller is the token's: there is no
	// id in the path or the query, so the only way this can be somebody else's
	// list is if the handler invented one.
	if stub.createdGuardian != handlerAccountID {
		t.Errorf("listed relationships for %d, want %d", stub.createdGuardian, handlerAccountID)
	}
	if stub.listCalls != 1 {
		t.Errorf("read %d times, want once", stub.listCalls)
	}
}

func TestGuardianCarriesThePermissionsWithTheRelationship(t *testing.T) {
	stub := &guardianStub{links: []authz.Link{aRelationship()}}
	recorder := callAs(t, newGuardianRouter(stub), http.MethodGet, "/api/v1/admin/guardians", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Data struct {
			Guardians []struct {
				LinkID      string   `json:"link_id"`
				GuardianID  int64    `json:"guardian_id"`
				LearnerID   int64    `json:"learner_id"`
				Permissions []string `json:"permissions"`
			} `json:"guardians"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &body)
	if len(body.Data.Guardians) != 1 {
		t.Fatalf("guardians = %d, want 1", len(body.Data.Guardians))
	}
	link := body.Data.Guardians[0]
	if link.LinkID != "lnk_1" || link.GuardianID != 7 || link.LearnerID != 42 {
		t.Errorf("the relationship lost a column on the wire: %+v", link)
	}
	// A link's permissions are not decoration: a link with none permits nothing,
	// and a list that dropped them would show a relationship that looks like it
	// allows nothing next to one that looks the same and allows reports.
	if len(link.Permissions) != 1 || link.Permissions[0] != string(authz.PermViewReports) {
		t.Errorf("permissions = %v, want [view_reports]", link.Permissions)
	}
}

func TestGuardianCreatesARelationshipWithItsPermissions(t *testing.T) {
	stub := &guardianStub{link: aRelationship()}
	recorder := callAs(t, newGuardianRouter(stub), http.MethodPost, "/api/v1/admin/guardians",
		`{"guardian_id":7,"learner_id":42,"permissions":["view_reports","assign_materials"]}`)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.createdGuardian != 7 || stub.createdLearner != 42 {
		t.Errorf("created %d -> %d, want 7 -> 42", stub.createdGuardian, stub.createdLearner)
	}
	if stub.createdBy.UserID != handlerAccountID {
		t.Errorf("created by %d, want %d", stub.createdBy.UserID, handlerAccountID)
	}
	if len(stub.createdPerms) != 2 || stub.createdPerms[0] != authz.PermViewReports {
		t.Errorf("permissions = %v, want the two that were sent", stub.createdPerms)
	}
	var body relationshipBody
	decodeBody(t, recorder, &body)
	if body.Data.LinkID != "lnk_1" {
		t.Errorf("link_id = %q, want the one the service returned", body.Data.LinkID)
	}
}

func TestGuardianAnswersEachProhibitionWithItsOwnCode(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"an account as its own guardian", authz.ErrSelfLink,
			http.StatusBadRequest, "guardian_self_link"},
		// A relationship that is already there is not a malformed request: the
		// request was well formed and the state refused it, which is the
		// difference between "ask again differently" and "this is already true".
		{"a relationship that already exists", authz.ErrLinkExists,
			http.StatusConflict, "guardian_link_exists"},
		{"a watched account that is not a learner", authz.ErrNotALearner,
			http.StatusBadRequest, "guardian_not_learner"},
		{"a guardian that is a learner", authz.ErrGuardianIsLearner,
			http.StatusBadRequest, "guardian_not_learner"},
		{"an account that is not there", authz.ErrUnknownUser,
			http.StatusNotFound, "not_found"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			stub := &guardianStub{createErr: testCase.err}
			recorder := callAs(t, newGuardianRouter(stub), http.MethodPost, "/api/v1/admin/guardians",
				`{"guardian_id":7,"learner_id":42}`)
			if recorder.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s",
					recorder.Code, testCase.wantStatus, recorder.Body.String())
			}
			if code := decodeRefusal(t, recorder).Error.Code; code != testCase.wantCode {
				t.Errorf("code = %q, want %q", code, testCase.wantCode)
			}
		})
	}
}

func TestGuardianRefusesAPermissionThisBuildDoesNotHold(t *testing.T) {
	stub := &guardianStub{}
	recorder := callAs(t, newGuardianRouter(stub), http.MethodPost, "/api/v1/admin/guardians",
		`{"guardian_id":7,"learner_id":42,"permissions":["view_grades"]}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.createCalls != 0 {
		t.Error("a permission this build has never heard of reached the service")
	}
}

func TestGuardianRefusesARelationshipWithNoAccountsInIt(t *testing.T) {
	stub := &guardianStub{}
	recorder := callAs(t, newGuardianRouter(stub), http.MethodPost, "/api/v1/admin/guardians",
		`{"guardian_id":7}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.createCalls != 0 {
		t.Error("a relationship with one end reached the service")
	}
}

func TestGuardianRevokesARelationshipByName(t *testing.T) {
	stub := &guardianStub{}
	recorder := callAs(t, newGuardianRouter(stub), http.MethodDelete, "/api/v1/admin/guardians",
		`{"link_id":"lnk_1"}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.revokedID != "lnk_1" || stub.revokeCalls != 1 {
		t.Errorf("revoked %q in %d calls, want lnk_1 in one", stub.revokedID, stub.revokeCalls)
	}
}

func TestGuardianRefusesARevocationWithNoRelationshipNamed(t *testing.T) {
	stub := &guardianStub{}
	recorder := callAs(t, newGuardianRouter(stub), http.MethodDelete, "/api/v1/admin/guardians", `{}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.revokeCalls != 0 {
		t.Error("a revocation naming nothing reached the service")
	}
}

func TestGuardianReadsThePermissionsOnOneRelationship(t *testing.T) {
	stub := &guardianStub{link: aRelationship()}
	recorder := callAs(t, newGuardianRouter(stub), http.MethodGet,
		"/api/v1/admin/guardians/lnk_1/permissions", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.readLinkID != "lnk_1" {
		t.Errorf("read %q, want lnk_1", stub.readLinkID)
	}
	var body struct {
		Data struct {
			LinkID      string   `json:"link_id"`
			Permissions []string `json:"permissions"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &body)
	if body.Data.LinkID != "lnk_1" || len(body.Data.Permissions) != 1 {
		t.Errorf("read back %+v, want the link's one permission", body.Data)
	}
}

func TestGuardianGrantsAndRevokesOnePermission(t *testing.T) {
	granting := &guardianStub{}
	recorder := callAs(t, newGuardianRouter(granting), http.MethodPost,
		"/api/v1/admin/guardians/lnk_1/permissions", `{"permission":"view_reports"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("grant: status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if granting.grantedLinkID != "lnk_1" || granting.grantedPerm != authz.PermViewReports {
		t.Errorf("granted %q on %q", granting.grantedPerm, granting.grantedLinkID)
	}
	// Granting is a row that records who granted it, so this is the one half of
	// the pair that needs the caller.
	if granting.grantedBy.UserID != handlerAccountID {
		t.Errorf("granted by %d, want %d", granting.grantedBy.UserID, handlerAccountID)
	}

	revoking := &guardianStub{}
	recorder = callAs(t, newGuardianRouter(revoking), http.MethodDelete,
		"/api/v1/admin/guardians/lnk_1/permissions", `{"permission":"view_reports"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("revoke: status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if revoking.revokePermID != "lnk_1" || revoking.revokedPerm != authz.PermViewReports {
		t.Errorf("revoked %q on %q", revoking.revokedPerm, revoking.revokePermID)
	}
}

func TestGuardianRefusesToRevokeAPermissionTheLinkDoesNotCarry(t *testing.T) {
	// Taking back something the relationship does not carry means the caller
	// believes something about it that the answer has to correct — and if it
	// were a silent success, the same belief applied to a different permission
	// would be a way to lose one that is held.
	stub := &guardianStub{revokePermErr: authz.ErrPermissionNotHeld}
	recorder := callAs(t, newGuardianRouter(stub), http.MethodDelete,
		"/api/v1/admin/guardians/lnk_1/permissions", `{"permission":"assign_materials"}`)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", recorder.Code, recorder.Body.String())
	}
	if code := decodeRefusal(t, recorder).Error.Code; code != "not_found" {
		t.Errorf("code = %q, want not_found", code)
	}
}

func TestGuardianNeedsAToken(t *testing.T) {
	for _, path := range []string{
		"/api/v1/guardian/learners",
		"/api/v1/admin/guardians",
	} {
		stub := &guardianStub{}
		recorder := call(t, newGuardianRouter(stub), http.MethodGet, path, "")
		if recorder.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", path, recorder.Code)
		}
		if stub.listCalls != 0 || stub.allCalls != 0 {
			t.Errorf("%s: an account-less request reached the service", path)
		}
	}
}

func TestGuardianSaysNothingAboutAFailure(t *testing.T) {
	stub := &guardianStub{allErr: errors.New("guardian_links is missing at 10.0.0.4")}
	recorder := callAs(t, newGuardianRouter(stub), http.MethodGet, "/api/v1/admin/guardians", "")

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	if code := decodeRefusal(t, recorder).Error.Code; code != "list_relationships_failed" {
		t.Errorf("code = %q, want list_relationships_failed", code)
	}
}
