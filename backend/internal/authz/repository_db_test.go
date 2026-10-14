package authz

// The storage half of the governance tables, checked against a real seekdb.
//
// Everything this file pins is a claim about the schema rather than about Go:
// the unique key that makes a decision about a kind and a decision about one of
// its members two rows, the empty string that has to behave as a value, the two
// CHECK constraints that refuse what no query would catch, and the cascade that
// makes revoking a link take its permissions with it. A double can restate any
// of those and then agree with itself; only the database can refuse.
//
// One database per package, so nothing here calls t.Parallel.

import (
	"context"
	"errors"
	"testing"
	"time"

	"backend/internal/data"
	"backend/internal/data/models"
	"backend/internal/data/testdb"
)

// atSecond is a whole second on a fixed day. DATETIME keeps whole seconds, so a
// fixture that lets the clock pick one compares against a rounded value.
func atSecond(second int) time.Time {
	return time.Date(2026, 10, 1, 12, 0, second, 0, time.UTC)
}

const (
	adminUser  int64 = 1
	parentUser int64 = 2
	childUser  int64 = 3
	otherUser  int64 = 4
)

func newDBStore(t *testing.T) (*GORMStore, *data.Resource) {
	t.Helper()
	resource := testdb.Open(t, "authz")
	// Children before parents: the permissions and links point at users, and
	// the grants do too.
	testdb.Reset(t, resource, "guardian_permissions", "guardian_links", "user_grants", "users")
	return NewGORMStore(resource.GORM()), resource
}

// seedAccounts writes the accounts the foreign keys point at. The credentials
// are placeholders: this package never reads them, and a real deployment's are
// hashes that no test may hold.
func seedAccounts(t *testing.T, resource *data.Resource) {
	t.Helper()
	accounts := []models.User{
		{UserID: adminUser, Username: "admin", PasswordHash: "x", Role: RoleAdmin, Preset: PresetStandard},
		{UserID: parentUser, Username: "parent", PasswordHash: "x", Role: RoleUser, Preset: PresetStandard},
		{UserID: childUser, Username: "child", PasswordHash: "x", Role: RoleUser, Preset: PresetLearner},
		{UserID: otherUser, Username: "other", PasswordHash: "x", Role: RoleUser, Preset: PresetStandard},
	}
	for _, account := range accounts {
		if err := resource.GORM().Create(&account).Error; err != nil {
			t.Fatalf("seed the account %s: %v", account.Username, err)
		}
	}
}

func newDBService(t *testing.T) (*Service, *GORMStore, *data.Resource) {
	t.Helper()
	store, resource := newDBStore(t)
	seedAccounts(t, resource)
	service, err := NewService(store, New(NewBaseline(DefaultFloor(), []string{"deep_question"}, nil)))
	if err != nil {
		t.Fatalf("build the service: %v", err)
	}
	return service, store, resource
}

func countRows(t *testing.T, resource *data.Resource, table string) int64 {
	t.Helper()
	var count int64
	if err := resource.GORM().Table(table).Count(&count).Error; err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func TestDBAnAccountPresetIsReadBack(t *testing.T) {
	store, resource := newDBStore(t)
	seedAccounts(t, resource)
	ctx := context.Background()

	preset, err := store.Preset(ctx, childUser)
	if err != nil {
		t.Fatalf("read the preset: %v", err)
	}
	if NormalizePreset(preset) != PresetLearner {
		t.Errorf("preset = %q, want a learner", preset)
	}
	// An account that is not there is a distinct answer, because the column and
	// the row both read as "nothing" otherwise.
	if _, err := store.Preset(ctx, 999); !errors.Is(err, ErrUnknownUser) {
		t.Errorf("Preset(999) = %v, want %v", err, ErrUnknownUser)
	}
}

func TestDBADecisionAboutAKindAndOneAboutItsMemberAreTwoRows(t *testing.T) {
	// The whole reason item_id is an empty string rather than NULL. Both rows
	// have to be storable at once — otherwise "the decision about every
	// capability" and "the decision about chat" would be one row that has to
	// pick — and the unique key has to tell them apart, which it only can if
	// the empty string is a value.
	store, resource := newDBStore(t)
	seedAccounts(t, resource)
	ctx := context.Background()

	whole := Assignment{UserID: childUser, Item: Whole(KindCapability), Allowed: false, GrantedBy: adminUser}
	member := Assignment{UserID: childUser, Item: Item{Kind: KindCapability, ID: "chat"}, Allowed: true, GrantedBy: adminUser}
	if err := store.SaveAssignment(ctx, whole); err != nil {
		t.Fatalf("save the whole-kind decision: %v", err)
	}
	if err := store.SaveAssignment(ctx, member); err != nil {
		t.Fatalf("save the decision about one capability: %v", err)
	}
	if rows := countRows(t, resource, "user_grants"); rows != 2 {
		t.Fatalf("user_grants has %d rows, want 2 — the empty item_id collapsed with a named one", rows)
	}
	assignments, err := store.Assignments(ctx, childUser)
	if err != nil {
		t.Fatalf("list the decisions: %v", err)
	}
	if len(assignments) != 2 {
		t.Fatalf("read back %d decisions, want 2", len(assignments))
	}
	// And the two decide differently, which is the point of storing both.
	grants := Grants(assignments)
	baseline := NewBaseline(DefaultFloor(), []string{"deep_question"}, nil)
	if decision := Reach(PresetLearner, baseline, grants, Item{Kind: KindCapability, ID: "chat"}); !decision.Allowed {
		t.Errorf("the specific grant lost to the whole-kind one: %s", decision.Reason)
	}
	if decision := Reach(PresetLearner, baseline, grants, Item{Kind: KindCapability, ID: "deep_question"}); decision.Allowed {
		t.Errorf("the whole-kind denial did not apply: %s", decision.Reason)
	}
}

func TestDBTheSameItemTwiceIsOneRow(t *testing.T) {
	store, resource := newDBStore(t)
	seedAccounts(t, resource)
	ctx := context.Background()
	item := Item{Kind: KindCapability, ID: "roadmap"}

	if err := store.SaveAssignment(ctx, Assignment{UserID: childUser, Item: item, Allowed: true, GrantedBy: adminUser}); err != nil {
		t.Fatalf("save the decision: %v", err)
	}
	if err := store.SaveAssignment(ctx, Assignment{UserID: childUser, Item: item, Allowed: false, GrantedBy: adminUser}); err != nil {
		t.Fatalf("save the decision again: %v", err)
	}
	if rows := countRows(t, resource, "user_grants"); rows != 1 {
		t.Fatalf("user_grants has %d rows, want 1 — the unique key did not hold", rows)
	}
	assignments, err := store.Assignments(ctx, childUser)
	if err != nil {
		t.Fatalf("list the decisions: %v", err)
	}
	if len(assignments) != 1 || assignments[0].Allowed {
		t.Fatalf("the second write replaced nothing: %+v", assignments)
	}
	// The same item for another account is a different row, which is what the
	// key's first column is for.
	if err := store.SaveAssignment(ctx, Assignment{UserID: parentUser, Item: item, Allowed: true, GrantedBy: adminUser}); err != nil {
		t.Fatalf("save the decision for another account: %v", err)
	}
	if rows := countRows(t, resource, "user_grants"); rows != 2 {
		t.Fatalf("user_grants has %d rows, want 2", rows)
	}
}

func TestDBTheKindVocabularyIsClosedByTheConstraint(t *testing.T) {
	store, resource := newDBStore(t)
	seedAccounts(t, resource)
	ctx := context.Background()

	err := store.SaveAssignment(ctx, Assignment{
		UserID: childUser, Item: Item{Kind: "telepathy", ID: "x"}, Allowed: true, GrantedBy: adminUser,
	})
	if err == nil {
		t.Fatalf("a kind outside the CHECK was written")
	}
	// The Go-side check refuses it first, so reaching the constraint at all
	// means a writer bypassed the service — which is exactly the writer the
	// CHECK exists for.
	if rows := countRows(t, resource, "user_grants"); rows != 0 {
		t.Errorf("user_grants has %d rows after a refused write", rows)
	}
}

func TestDBASelfLinkIsRefusedByTheConstraint(t *testing.T) {
	// The service refuses this as well, and both are wanted: the service gives
	// a sentence, the constraint is what makes the sentence true rather than
	// remembered. This writes around the service on purpose.
	store, resource := newDBStore(t)
	seedAccounts(t, resource)
	err := store.SaveLink(context.Background(), Link{
		ID: "lnk_self", GuardianID: parentUser, LearnerID: parentUser,
		Permissions: []Permission{PermViewReports}, CreatedAt: atSecond(1),
	})
	if err == nil {
		t.Fatalf("a self-link was written")
	}
	if rows := countRows(t, resource, "guardian_links"); rows != 0 {
		t.Errorf("guardian_links has %d rows after a refused write", rows)
	}
}

func TestDBAnInventedPermissionIsRefusedByTheConstraint(t *testing.T) {
	store, resource := newDBStore(t)
	seedAccounts(t, resource)
	err := store.SaveLink(context.Background(), Link{
		ID: "lnk_invented", GuardianID: parentUser, LearnerID: childUser,
		Permissions: []Permission{"see_everything"}, CreatedAt: atSecond(1),
	})
	if err == nil {
		t.Fatalf("a permission outside AUTH-05's four was written")
	}
	// And writing the link is all-or-nothing: the permission's failure must not
	// leave the relationship behind, or a link with no permissions would sit
	// there permitting nothing.
	if rows := countRows(t, resource, "guardian_links"); rows != 0 {
		t.Errorf("guardian_links has %d rows after a refused write", rows)
	}
}

func TestDBASecondLinkForTheSamePairIsRefused(t *testing.T) {
	service, _, _ := newDBService(t)
	ctx := context.Background()
	if _, err := service.Link(ctx, admin(), parentUser, childUser, []Permission{PermViewReports}); err != nil {
		t.Fatalf("create the relationship: %v", err)
	}
	_, err := service.Link(ctx, admin(), parentUser, childUser, []Permission{PermAssignMaterials})
	if !errors.Is(err, ErrLinkExists) {
		t.Fatalf("the second relationship = %v, want %v (the 1062 mapping is what this checks)", err, ErrLinkExists)
	}
}

func TestDBRevokingTakesThePermissionsWithIt(t *testing.T) {
	// A revoked link is a deleted row, so the permissions have to go with it
	// through the cascade. If they did not, a new link with a recycled id would
	// inherit a permission nobody granted it.
	service, store, resource := newDBService(t)
	ctx := context.Background()
	link, err := service.Link(ctx, admin(), parentUser, childUser,
		[]Permission{PermViewReports, PermAssignMaterials})
	if err != nil {
		t.Fatalf("create the relationship: %v", err)
	}
	if rows := countRows(t, resource, "guardian_permissions"); rows != 2 {
		t.Fatalf("guardian_permissions has %d rows, want 2", rows)
	}
	if err := service.Revoke(ctx, link.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if rows := countRows(t, resource, "guardian_permissions"); rows != 0 {
		t.Errorf("guardian_permissions has %d rows after the link was revoked", rows)
	}
	if _, err := store.LinkByID(ctx, link.ID); !errors.Is(err, ErrLinkNotFound) {
		t.Errorf("LinkByID after revoking = %v, want %v", err, ErrLinkNotFound)
	}
}

func TestDBALinkComesBackWithItsPermissionsOrNotAtAll(t *testing.T) {
	service, store, _ := newDBService(t)
	ctx := context.Background()
	link, err := service.Link(ctx, admin(), parentUser, childUser, []Permission{PermViewReports})
	if err != nil {
		t.Fatalf("create the relationship: %v", err)
	}
	byPair, err := store.Link(ctx, parentUser, childUser)
	if err != nil {
		t.Fatalf("read by pair: %v", err)
	}
	byID, err := store.LinkByID(ctx, link.ID)
	if err != nil {
		t.Fatalf("read by id: %v", err)
	}
	for name, read := range map[string]Link{"by pair": byPair, "by id": byID} {
		if !read.Holds(PermViewReports) || len(read.Permissions) != 1 {
			t.Errorf("%s: permissions = %v, want exactly view_reports", name, read.Permissions)
		}
		if read.GuardianID != parentUser || read.LearnerID != childUser {
			t.Errorf("%s: pair = (%d, %d), want (%d, %d)",
				name, read.GuardianID, read.LearnerID, parentUser, childUser)
		}
	}
	// A pair that has no relationship is not a link with nothing in it.
	if _, err := store.Link(ctx, childUser, parentUser); !errors.Is(err, ErrLinkNotFound) {
		t.Errorf("reading the reverse pair = %v, want %v", err, ErrLinkNotFound)
	}
}

func TestDBGrantingTwiceLeavesOnePermission(t *testing.T) {
	service, _, resource := newDBService(t)
	ctx := context.Background()
	link, err := service.Link(ctx, admin(), parentUser, childUser, []Permission{PermViewReports})
	if err != nil {
		t.Fatalf("create the relationship: %v", err)
	}
	if err := service.GrantPermission(ctx, admin(), link.ID, PermViewReports); err != nil {
		t.Fatalf("grant the same permission again: %v", err)
	}
	if rows := countRows(t, resource, "guardian_permissions"); rows != 1 {
		t.Fatalf("guardian_permissions has %d rows, want 1", rows)
	}
}

func TestDBAssignRefusesWhatThePolicyRefuses(t *testing.T) {
	// The rule is checked against the store's own contents, so the state this
	// is refused from has to be the state the database actually holds.
	service, store, resource := newDBService(t)
	ctx := context.Background()
	if err := service.Assign(ctx, admin(), childUser, Item{Kind: KindCapability, ID: "chat"}, false); err != nil {
		t.Fatalf("close a baseline item: %v", err)
	}
	assignments, err := store.Assignments(ctx, childUser)
	if err != nil {
		t.Fatalf("list the decisions: %v", err)
	}
	grants := Grants(assignments)
	baseline := NewBaseline(DefaultFloor(), []string{"deep_question"}, nil)
	floorItem := Item{Kind: KindCapability, ID: "chat"}
	if decision := Reach(PresetLearner, baseline, grants, floorItem); !decision.Allowed {
		t.Errorf("the floor was closed by a grant that reached the database: %s", decision.Reason)
	}
	baselineItem := Item{Kind: KindCapability, ID: "deep_question"}
	if decision := Reach(PresetLearner, baseline, grants, baselineItem); decision.Allowed {
		t.Errorf("a baseline item survived a denial that reached the database: %s", decision.Reason)
	}
	// And the refusal for clearing the policy leaves nothing behind.
	if err := service.Assign(ctx, admin(), childUser, Whole(KindLearningPolicy), false); !errors.Is(err, ErrPolicyWouldBeEmpty) {
		t.Fatalf("clearing the policy = %v, want %v", err, ErrPolicyWouldBeEmpty)
	}
	if rows := countRows(t, resource, "user_grants"); rows != 1 {
		t.Errorf("user_grants has %d rows, want the one written before the refusal", rows)
	}
}

func TestDBDeletingAnAccountTakesItsLinksWithIt(t *testing.T) {
	// CASCADE on both ends of a link. The permission table is a child of the
	// link, so the row it hangs off going is what has to be enough.
	service, store, resource := newDBService(t)
	ctx := context.Background()
	if _, err := service.Link(ctx, admin(), parentUser, childUser, []Permission{PermViewReports}); err != nil {
		t.Fatalf("create the relationship: %v", err)
	}
	if err := resource.GORM().Delete(&models.User{}, "user_id = ?", childUser).Error; err != nil {
		t.Fatalf("delete the watched account: %v", err)
	}
	if rows := countRows(t, resource, "guardian_links"); rows != 0 {
		t.Errorf("guardian_links has %d rows after the watched account was deleted", rows)
	}
	if _, err := store.Link(ctx, parentUser, childUser); !errors.Is(err, ErrLinkNotFound) {
		t.Errorf("the relationship outlived the account it was about: %v", err)
	}
}

func TestDBAnAuditOfWhoGrantedWhatSurvivesTheGrantor(t *testing.T) {
	// granted_by is SET NULL rather than CASCADE: deleting the administrator who
	// wrote a decision must not take the decision away, and RESTRICT would make
	// such an administrator undeletable.
	service, store, resource := newDBService(t)
	ctx := context.Background()
	if err := service.Assign(ctx, admin(), childUser, Item{Kind: KindCapability, ID: "roadmap"}, true); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if err := resource.GORM().Delete(&models.User{}, "user_id = ?", adminUser).Error; err != nil {
		t.Fatalf("delete the administrator: %v", err)
	}
	assignments, err := store.Assignments(ctx, childUser)
	if err != nil {
		t.Fatalf("list the decisions: %v", err)
	}
	if len(assignments) != 1 {
		t.Fatalf("the decision went with the account that wrote it: %+v", assignments)
	}
	if assignments[0].GrantedBy != 0 {
		t.Errorf("granted_by = %d, want 0 (NULL) after the grantor was deleted", assignments[0].GrantedBy)
	}
}

func TestDBWritingADecisionForAnAccountThatIsNotThereIsRefused(t *testing.T) {
	store, resource := newDBStore(t)
	seedAccounts(t, resource)
	err := store.SaveAssignment(context.Background(), Assignment{
		UserID: 999, Item: Item{Kind: KindCapability, ID: "chat"}, Allowed: true, GrantedBy: adminUser,
	})
	if err == nil {
		t.Fatalf("a decision was written for an account that does not exist")
	}
	if rows := countRows(t, resource, "user_grants"); rows != 0 {
		t.Errorf("user_grants has %d rows after a refused write", rows)
	}
}
