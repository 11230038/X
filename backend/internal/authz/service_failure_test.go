package authz

// A store that is not answering, and a service that was built without one.
//
// Both are cases about a failure being *said* rather than being absorbed: a
// lookup that fails is not a lookup that found nothing.

import (
	"context"
	"errors"
	"testing"
)

func TestStorageFailuresReachTheCaller(t *testing.T) {
	// Every read and write above goes through the store, so every one of them
	// has to carry a storage failure out rather than answering as if it had
	// succeeded — a lookup that fails is not a lookup that found nothing.
	boom := errors.New("the database is not answering")
	store := newFakeStore()
	service := newService(t, store)
	ctx := context.Background()
	caller := Caller{UserID: parentID, Role: RoleUser}
	link, err := service.Link(ctx, admin(), parentID, childID, []Permission{PermViewReports})
	if err != nil {
		t.Fatalf("create the relationship: %v", err)
	}
	store.failWith(boom)

	cases := []struct {
		name string
		call func() error
	}{
		{"Actor", func() error { _, err := service.Actor(ctx, caller); return err }},
		{"Target", func() error { _, err := service.Target(ctx, caller, childID); return err }},
		{"Decide", func() error { _, err := service.Decide(ctx, caller, Action{}, childID); return err }},
		{"Link", func() error { _, err := service.Link(ctx, admin(), parentID, childID, nil); return err }},
		{"GrantPermission", func() error { return service.GrantPermission(ctx, admin(), link.ID, PermViewReports) }},
		{"Relationships", func() error { _, err := service.Relationships(ctx, parentID); return err }},
		{"Assign", func() error { return service.Assign(ctx, admin(), childID, Item{Kind: KindCapability}, true) }},
		{"Withdraw", func() error { return service.Withdraw(ctx, childID, Item{Kind: KindCapability}) }},
		{"Assignments", func() error { _, err := service.Assignments(ctx, childID); return err }},
		{"Effective", func() error { _, err := service.Effective(ctx, childID); return err }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if err := testCase.call(); !errors.Is(err, boom) {
				t.Fatalf("err = %v, want the storage failure", err)
			}
		})
	}
}

func TestBuildingAServiceWithoutAStoreOrAFloorIsRefused(t *testing.T) {
	if _, err := NewService(nil, New(NewBaseline(DefaultFloor(), nil, nil))); err == nil {
		t.Errorf("a service with no store was built")
	}
	if _, err := NewService(newFakeStore(), New(Baseline{})); !errors.Is(err, ErrPolicyWouldBeEmpty) {
		t.Errorf("a service over an empty baseline = %v, want %v", err, ErrPolicyWouldBeEmpty)
	}
}
