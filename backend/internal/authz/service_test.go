package authz

// The rules about writing a governance row, against a store that keeps them in a
// map: who may be a guardian, what a link without a permission is worth, and
// what revoking does to the very next question.
//
// What the grants do is service_grants_test.go's, and what happens when the
// store itself fails is service_failure_test.go's.

import (
	"context"
	"errors"
	"testing"
)

func TestALinkNeedsAlearnerAndAGuardianWhoIsNotOne(t *testing.T) {
	cases := []struct {
		name     string
		guardian int64
		learner  int64
		want     error
	}{
		{"an account as its own guardian", parentID, parentID, ErrSelfLink},
		{"a standard account being watched", parentID, otherID, ErrNotALearner},
		{"a learner as the guardian", strangerID, childID, ErrGuardianIsLearner},
		{"an account that does not exist", parentID, 999, ErrUnknownUser},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			store := newFakeStore()
			service := newService(t, store)
			_, err := service.Link(context.Background(), admin(), testCase.guardian, testCase.learner,
				[]Permission{PermViewReports})
			if !errors.Is(err, testCase.want) {
				t.Fatalf("err = %v, want %v", err, testCase.want)
			}
			if len(store.links) != 0 {
				t.Errorf("a refused relationship was written anyway")
			}
		})
	}
}

func TestTheSameRelationshipTwiceIsRefused(t *testing.T) {
	store := newFakeStore()
	service := newService(t, store)
	if _, err := service.Link(context.Background(), admin(), parentID, childID,
		[]Permission{PermViewReports}); err != nil {
		t.Fatalf("create the relationship: %v", err)
	}
	_, err := service.Link(context.Background(), admin(), parentID, childID, []Permission{PermAssignMaterials})
	if !errors.Is(err, ErrLinkExists) {
		t.Fatalf("err = %v, want %v", err, ErrLinkExists)
	}
}

func TestAGuardianSeesOnlyTheirOwnLearners(t *testing.T) {
	store := newFakeStore()
	service := newService(t, store)
	ctx := context.Background()
	if _, err := service.Link(ctx, admin(), parentID, childID, []Permission{PermViewReports}); err != nil {
		t.Fatalf("create the relationship: %v", err)
	}
	// The other account asks about the same learner. It holds no relationship
	// to that learner, so the answer is "no relationship" — not "somebody
	// else's relationship, which you may not use".
	target, err := service.Target(ctx, Caller{UserID: otherID, Role: RoleUser}, childID)
	if err != nil {
		t.Fatalf("load the target: %v", err)
	}
	if target.Link || len(target.Permissions) != 0 {
		t.Fatalf("another account inherited a relationship: %+v", target)
	}
}

func TestRevokingIsImmediate(t *testing.T) {
	store := newFakeStore()
	service := newService(t, store)
	ctx := context.Background()
	parent := Caller{UserID: parentID, Role: RoleUser}
	action := Action{Item: Whole(KindLearningPolicy), Permission: PermViewReports}

	link, err := service.Link(ctx, admin(), parentID, childID, []Permission{PermViewReports})
	if err != nil {
		t.Fatalf("create the relationship: %v", err)
	}
	before, err := service.Decide(ctx, parent, action, childID)
	if err != nil {
		t.Fatalf("decide before revoking: %v", err)
	}
	if !before.Allowed {
		t.Fatalf("a relationship carrying view_reports was refused: %s", before.Reason)
	}
	if err := service.Revoke(ctx, link.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	after, err := service.Decide(ctx, parent, action, childID)
	if err != nil {
		t.Fatalf("decide after revoking: %v", err)
	}
	if after.Allowed {
		t.Fatalf("a revoked relationship still allowed the read")
	}
	if after.Reason != ReasonNoRelationship {
		t.Errorf("reason = %s, want %s", after.Reason, ReasonNoRelationship)
	}
}

func TestALinkWithoutThePermissionIsNotEnough(t *testing.T) {
	store := newFakeStore()
	service := newService(t, store)
	ctx := context.Background()
	parent := Caller{UserID: parentID, Role: RoleUser}
	if _, err := service.Link(ctx, admin(), parentID, childID, []Permission{PermAssignMaterials}); err != nil {
		t.Fatalf("create the relationship: %v", err)
	}
	decision, err := service.Decide(ctx, parent,
		Action{Item: Whole(KindLearningPolicy), Permission: PermViewReports}, childID)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if decision.Allowed {
		t.Fatalf("a link without view_reports allowed a report read")
	}
	if decision.Reason != ReasonPermissionMissing {
		t.Errorf("reason = %s, want %s", decision.Reason, ReasonPermissionMissing)
	}
}

func TestTakingBackAPermissionTheLinkDoesNotHoldIsRefused(t *testing.T) {
	store := newFakeStore()
	service := newService(t, store)
	ctx := context.Background()
	link, err := service.Link(ctx, admin(), parentID, childID, []Permission{PermViewReports})
	if err != nil {
		t.Fatalf("create the relationship: %v", err)
	}
	if err := service.RevokePermission(ctx, link.ID, PermManageRestrictions); !errors.Is(err, ErrPermissionNotHeld) {
		t.Errorf("err = %v, want %v", err, ErrPermissionNotHeld)
	}
	if err := service.RevokePermission(ctx, link.ID, PermViewReports); err != nil {
		t.Fatalf("revoke the permission the link holds: %v", err)
	}
	after, err := service.store.LinkByID(ctx, link.ID)
	if err != nil {
		t.Fatalf("read the relationship back: %v", err)
	}
	if after.Holds(PermViewReports) {
		t.Errorf("the permission is still held after being revoked")
	}
}

func TestOnlyTheFourPermissionsExist(t *testing.T) {
	store := newFakeStore()
	service := newService(t, store)
	ctx := context.Background()
	_, err := service.Link(ctx, admin(), parentID, childID, []Permission{"see_everything"})
	if !errors.Is(err, ErrUnknownPermission) {
		t.Fatalf("err = %v, want %v", err, ErrUnknownPermission)
	}
	if err := service.GrantPermission(ctx, admin(), "lnk_whatever", "see_everything"); !errors.Is(err, ErrUnknownPermission) {
		t.Errorf("granting an invented permission = %v, want %v", err, ErrUnknownPermission)
	}
}

func TestGrantingTheSamePermissionTwiceIsNotAFailure(t *testing.T) {
	// A repeated request is what a retry looks like, and a retry that fails is
	// a caller that will report a problem where there is none.
	store := newFakeStore()
	service := newService(t, store)
	ctx := context.Background()
	link, err := service.Link(ctx, admin(), parentID, childID, []Permission{PermViewReports})
	if err != nil {
		t.Fatalf("create the relationship: %v", err)
	}
	if err := service.GrantPermission(ctx, admin(), link.ID, PermViewReports); err != nil {
		t.Fatalf("granting a permission the link already holds: %v", err)
	}
	after, err := service.store.LinkByID(ctx, link.ID)
	if err != nil {
		t.Fatalf("read the relationship back: %v", err)
	}
	if len(after.Permissions) != 1 {
		t.Errorf("the link holds %v, want one copy of the permission", after.Permissions)
	}
}
