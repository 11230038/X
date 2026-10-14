package authz

// The rules about writing a governance row, against a store that keeps them in
// a map. None of these cases needs a database: they are about which writes are
// allowed and which are refused with which sentence. The ones that *are* about
// storage — the unique key, the CHECKs, the cascade — are in repository_db_test.go,
// because only the real database can answer them.

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

// fakeStore is a store that remembers. It is deliberately literal: it applies
// the same two facts the real one does (a pair is unique, a link's permissions
// go with it) and nothing else, so that a case which passes here is passing for
// the rule's sake rather than the double's cleverness.
type fakeStore struct {
	presets map[int64]string
	names   map[int64]string
	links   map[string]Link
	grants  map[int64][]Assignment
	nextID  int
	fail    error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		presets: map[int64]string{},
		names:   map[int64]string{},
		links:   map[string]Link{},
		grants:  map[int64][]Assignment{},
	}
}

func (f *fakeStore) failWith(err error) { f.fail = err }

func (f *fakeStore) Preset(_ context.Context, userID int64) (string, error) {
	if f.fail != nil {
		return "", f.fail
	}
	preset, ok := f.presets[userID]
	if !ok {
		return "", ErrUnknownUser
	}
	return preset, nil
}

func (f *fakeStore) SavePreset(_ context.Context, userID int64, preset string) error {
	if f.fail != nil {
		return f.fail
	}
	if _, ok := f.presets[userID]; !ok {
		return ErrUnknownUser
	}
	f.presets[userID] = preset
	return nil
}

func (f *fakeStore) Accounts(_ context.Context) ([]Account, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	ids := make([]int64, 0, len(f.presets))
	for id := range f.presets {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	accounts := make([]Account, 0, len(ids))
	for _, id := range ids {
		accounts = append(accounts, Account{
			UserID:   id,
			Username: f.names[id],
			Role:     RoleUser,
			Preset:   f.presets[id],
		})
	}
	return accounts, nil
}

func (f *fakeStore) Link(_ context.Context, guardianID, learnerID int64) (Link, error) {
	if f.fail != nil {
		return Link{}, f.fail
	}
	for _, link := range f.links {
		if link.GuardianID == guardianID && link.LearnerID == learnerID {
			return link, nil
		}
	}
	return Link{}, ErrLinkNotFound
}

func (f *fakeStore) LinkByID(_ context.Context, linkID string) (Link, error) {
	if f.fail != nil {
		return Link{}, f.fail
	}
	if link, ok := f.links[linkID]; ok {
		return link, nil
	}
	return Link{}, ErrLinkNotFound
}

func (f *fakeStore) Links(_ context.Context, guardianID int64) ([]Link, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	var links []Link
	for _, link := range f.links {
		if link.GuardianID == guardianID {
			links = append(links, link)
		}
	}
	return links, nil
}

func (f *fakeStore) AllLinks(_ context.Context) ([]Link, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	links := make([]Link, 0, len(f.links))
	for _, link := range f.links {
		links = append(links, link)
	}
	slices.SortFunc(links, func(a, b Link) int {
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})
	return links, nil
}

func (f *fakeStore) SaveLink(_ context.Context, link Link) error {
	if f.fail != nil {
		return f.fail
	}
	for _, existing := range f.links {
		if existing.GuardianID == link.GuardianID && existing.LearnerID == link.LearnerID {
			return ErrLinkExists
		}
	}
	f.links[link.ID] = link
	return nil
}

func (f *fakeStore) DeleteLink(_ context.Context, linkID string) error {
	if f.fail != nil {
		return f.fail
	}
	delete(f.links, linkID)
	return nil
}

func (f *fakeStore) SavePermission(_ context.Context, linkID string, permission Permission, grantedBy int64) error {
	if f.fail != nil {
		return f.fail
	}
	link, ok := f.links[linkID]
	if !ok {
		return ErrLinkNotFound
	}
	if link.Holds(permission) {
		return nil
	}
	link.Permissions = append(link.Permissions, permission)
	f.links[linkID] = link
	return nil
}

func (f *fakeStore) DeletePermission(_ context.Context, linkID string, permission Permission) error {
	if f.fail != nil {
		return f.fail
	}
	link, ok := f.links[linkID]
	if !ok {
		return ErrLinkNotFound
	}
	kept := make([]Permission, 0, len(link.Permissions))
	for _, held := range link.Permissions {
		if held != permission {
			kept = append(kept, held)
		}
	}
	link.Permissions = kept
	f.links[linkID] = link
	return nil
}

func (f *fakeStore) Assignments(_ context.Context, userID int64) ([]Assignment, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	return append([]Assignment(nil), f.grants[userID]...), nil
}

func (f *fakeStore) SaveAssignment(_ context.Context, assignment Assignment) error {
	if f.fail != nil {
		return f.fail
	}
	f.grants[assignment.UserID] = replaceAssignment(f.grants[assignment.UserID], assignment)
	return nil
}

func (f *fakeStore) DeleteAssignment(_ context.Context, userID int64, item Item) error {
	if f.fail != nil {
		return f.fail
	}
	kept := make([]Assignment, 0, len(f.grants[userID]))
	for _, assignment := range f.grants[userID] {
		if assignment.Item != item {
			kept = append(kept, assignment)
		}
	}
	f.grants[userID] = kept
	return nil
}

const (
	adminID    int64 = 1
	parentID   int64 = 2
	childID    int64 = 3
	otherID    int64 = 4
	strangerID int64 = 5
)

func newService(t *testing.T, store *fakeStore) *Service {
	t.Helper()
	store.presets[adminID] = PresetStandard
	store.presets[parentID] = PresetStandard
	store.presets[childID] = PresetLearner
	store.presets[otherID] = PresetStandard
	store.presets[strangerID] = PresetLearner
	service, err := NewService(store, New(NewBaseline(DefaultFloor(), nil, nil)))
	if err != nil {
		t.Fatalf("build the service: %v", err)
	}
	service.random = func(buffer []byte) (int, error) {
		store.nextID++
		for index := range buffer {
			buffer[index] = byte(store.nextID + index)
		}
		return len(buffer), nil
	}
	service.now = func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }
	return service
}

func admin() Caller { return Caller{UserID: adminID, Role: RoleAdmin} }

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

func TestAssignRefusesToClearALearnersPolicy(t *testing.T) {
	// The rule is a property of the whole set, which is why the write path
	// reads the existing grants, applies the change to them, and checks the
	// result. A write that checked only the row being written would let a
	// second call arrive at a policy the first was refused for.
	store := newFakeStore()
	service := newService(t, store)
	ctx := context.Background()
	clearing := Whole(KindLearningPolicy)
	if err := service.Assign(ctx, admin(), childID, clearing, false); !errors.Is(err, ErrPolicyWouldBeEmpty) {
		t.Fatalf("clearing a learner's policy = %v, want %v", err, ErrPolicyWouldBeEmpty)
	}
	if err := service.Assign(ctx, admin(), childID, clearing, true); err != nil {
		t.Fatalf("turning the learning policy on: %v", err)
	}
	// And a grant that hands a learner a tool is refused here as well.
	if err := service.Assign(ctx, admin(), childID, Whole(KindTool), true); !errors.Is(err, ErrNeverForLearners) {
		t.Errorf("handing a learner a tool = %v, want %v", err, ErrNeverForLearners)
	}
	// A standard account is not subject to either rule.
	if err := service.Assign(ctx, admin(), parentID, Whole(KindTool), true); err != nil {
		t.Errorf("handing a standard account a tool: %v", err)
	}
}

func TestAssignmentsSurviveTheRoundTrip(t *testing.T) {
	store := newFakeStore()
	service := newService(t, store)
	ctx := context.Background()
	item := Item{Kind: KindCapability, ID: "roadmap"}
	if err := service.Assign(ctx, admin(), childID, item, true); err != nil {
		t.Fatalf("assign: %v", err)
	}
	actor, err := service.Actor(ctx, Caller{UserID: childID, Role: RoleUser})
	if err != nil {
		t.Fatalf("load the actor: %v", err)
	}
	if actor.Preset != PresetLearner {
		t.Errorf("preset = %q, want %q", actor.Preset, PresetLearner)
	}
	decision := Reach(actor.Preset, NewBaseline(DefaultFloor(), nil, nil), actor.Grants, item)
	if !decision.Allowed {
		t.Errorf("the grant did not reach the actor: %s", decision.Reason)
	}
	if err := service.Withdraw(ctx, childID, item); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	after, err := service.Assignments(ctx, childID)
	if err != nil {
		t.Fatalf("list assignments: %v", err)
	}
	if len(after) != 0 {
		t.Errorf("the grant survived being withdrawn: %+v", after)
	}
}

func TestWithdrawingReturnsAnItemToItsBaseline(t *testing.T) {
	// AUTH-03's "must not be emptied" seen from the other end: removing the
	// last grant cannot empty a policy, because the baseline is not a row.
	store := newFakeStore()
	service := newService(t, store)
	ctx := context.Background()
	item := Item{Kind: KindCapability, ID: "chat"}
	if err := service.Assign(ctx, admin(), childID, item, false); err != nil {
		t.Fatalf("close the floor item: %v", err)
	}
	if err := service.Withdraw(ctx, childID, item); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	actor, err := service.Actor(ctx, Caller{UserID: childID, Role: RoleUser})
	if err != nil {
		t.Fatalf("load the actor: %v", err)
	}
	decision := Reach(actor.Preset, NewBaseline(DefaultFloor(), nil, nil), actor.Grants, item)
	if !decision.Allowed {
		t.Errorf("the floor item did not come back: %s", decision.Reason)
	}
}

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

func TestEffectiveIsComputedByTheSameFunctionThatAnswers(t *testing.T) {
	store := newFakeStore()
	service := newService(t, store)
	ctx := context.Background()
	if err := service.Assign(ctx, admin(), childID, Item{Kind: KindCapability, ID: "roadmap"}, true); err != nil {
		t.Fatalf("assign: %v", err)
	}
	decisions, err := service.Effective(ctx, childID)
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	byItem := map[Item]Decision{}
	for _, decision := range decisions {
		byItem[decision.Item] = decision
	}
	if decision := byItem[Item{Kind: KindCapability, ID: "chat"}]; !decision.Allowed {
		t.Errorf("the floor was reported as unreachable: %s", decision.Reason)
	}
	// The granted capability is not in the baseline, and this is the case that
	// says the list has to be the baseline plus what the grants name: an answer
	// that only listed the baseline would pin roadmap at index len(baseline) or
	// not at all, and the screen would draw a learner unable to reach the thing
	// the grant just opened.
	if decision := byItem[Item{Kind: KindCapability, ID: "roadmap"}]; !decision.Allowed {
		t.Errorf("a granted capability was reported as unreachable: %s", decision.Reason)
	}
	if len(decisions) != len(service.policy.Baseline().Items())+1 {
		t.Errorf("Effective reported %d items, want the baseline plus the one grant", len(decisions))
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
