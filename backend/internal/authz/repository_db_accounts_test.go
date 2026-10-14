package authz

// The two reads and the one write an administrator's screen is built from,
// checked against a real seekdb.
//
// They are in their own file because what they have to prove is narrow and
// specific: that a preset write lands in users.preset and nowhere else, that the
// account list reads the whole table in an order rather than a page of it, and
// that "every relationship" is a query the database actually accepts — the last
// one because the natural spelling of "no filter" is an empty condition, and an
// empty condition is not "everything" to the query builder, it is a syntax
// error that only the call passing nothing would ever hit.
//
// One database per package, so nothing here calls t.Parallel.

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"backend/internal/data/models"
)

func TestDBSavePresetWritesTheColumnAndNothingElse(t *testing.T) {
	store, resource := newDBStore(t)
	seedAccounts(t, resource)
	ctx := context.Background()

	if err := store.SavePreset(ctx, childUser, PresetStandard); err != nil {
		t.Fatalf("write the preset: %v", err)
	}
	preset, err := store.Preset(ctx, childUser)
	if err != nil {
		t.Fatalf("read the preset back: %v", err)
	}
	if preset != PresetStandard {
		t.Errorf("preset = %q, want standard", preset)
	}
	// The write is about one column of one row. The two layers beside it —
	// role and the credential — are what make the account an account, and a
	// preset move that touched either would be a governance route that can
	// change who somebody is.
	var row models.User
	if err := resource.GORM().First(&row, "user_id = ?", childUser).Error; err != nil {
		t.Fatalf("read the account: %v", err)
	}
	if row.Role != RoleUser || row.Username != "child" || row.PasswordHash != "x" {
		t.Errorf("the preset write touched another column: %+v", row)
	}
	// And only that row.
	var others int64
	if err := resource.GORM().Model(&models.User{}).
		Where("preset <> ?", PresetStandard).Count(&others).Error; err != nil {
		t.Fatalf("count the accounts left alone: %v", err)
	}
	if others != 0 {
		t.Errorf("%d other account(s) moved with it", others)
	}
}

func TestDBSavePresetToTheValueAlreadyThereIsNotAFailure(t *testing.T) {
	// The update reports zero rows both for "no such account" and for "already
	// this value", so the two are told apart by asking — and asking only in the
	// zero-row case. A version that read the row first would be the same answer
	// for one more round trip; a version that did neither would report a
	// present account as missing the moment an administrator re-sent a change
	// that was already applied.
	store, resource := newDBStore(t)
	seedAccounts(t, resource)
	ctx := context.Background()

	if err := store.SavePreset(ctx, childUser, PresetLearner); err != nil {
		t.Fatalf("re-writing the preset it already had: %v", err)
	}
	if _, err := store.Preset(ctx, childUser); err != nil {
		t.Fatalf("read it back: %v", err)
	}
}

func TestDBSavePresetForAnAccountThatIsNotThereIsRefused(t *testing.T) {
	store, resource := newDBStore(t)
	seedAccounts(t, resource)

	err := store.SavePreset(context.Background(), 999, PresetLearner)
	if !errors.Is(err, ErrUnknownUser) {
		t.Fatalf("SavePreset(999) = %v, want %v", err, ErrUnknownUser)
	}
}

func TestDBAccountsIsEveryAccountWithThePresetAsStored(t *testing.T) {
	store, resource := newDBStore(t)
	seedAccounts(t, resource)
	// An account written before the column existed. It is read back as the
	// empty string — the read does not invent a value — and it is the caller's
	// job (PresetOf) to say what that means.
	if err := resource.GORM().Model(&models.User{}).
		Where("user_id = ?", otherUser).Update("preset", "").Error; err != nil {
		t.Fatalf("blank the preset: %v", err)
	}
	// A disabled account, so that the flag is read rather than assumed.
	if err := resource.GORM().Model(&models.User{}).
		Where("user_id = ?", adminUser).Update("disable", true).Error; err != nil {
		t.Fatalf("disable the account: %v", err)
	}

	accounts, err := store.Accounts(context.Background())
	if err != nil {
		t.Fatalf("list the accounts: %v", err)
	}
	if len(accounts) != 4 {
		t.Fatalf("accounts = %d, want all four", len(accounts))
	}
	// Ordered by id, so two lists a page apart are the same list.
	for index, account := range accounts {
		if want := int64(index + 1); account.UserID != want {
			t.Errorf("account %d is %d, want the list ordered by id", index, account.UserID)
		}
	}
	if accounts[0].Role != RoleAdmin || !accounts[0].Disabled {
		t.Errorf("the administrator came back as %+v", accounts[0])
	}
	if accounts[otherUser-1].Preset != "" || accounts[otherUser-1].PresetOf() != PresetStandard {
		t.Errorf("the pre-column account reads as preset=%q preset_of=%q, want \"\" and standard",
			accounts[otherUser-1].Preset, accounts[otherUser-1].PresetOf())
	}
	if accounts[adminUser-1].Username != "admin" {
		t.Errorf("username = %q, want admin", accounts[adminUser-1].Username)
	}
}

func TestDBAllLinksIsEveryRelationshipRatherThanASyntaxError(t *testing.T) {
	store, resource := newDBStore(t)
	seedAccounts(t, resource)
	ctx := context.Background()

	first, err := makeLink(store, ctx, adminUser, parentUser, childUser, PermViewReports)
	if err != nil {
		t.Fatalf("create the first relationship: %v", err)
	}
	second, err := makeLink(store, ctx, adminUser, otherUser, childUser, PermAssignMaterials)
	if err != nil {
		t.Fatalf("create the second relationship: %v", err)
	}

	links, err := store.AllLinks(ctx)
	if err != nil {
		t.Fatalf("list every relationship: %v", err)
	}
	if len(links) != 2 {
		t.Fatalf("links = %d, want 2", len(links))
	}
	// Each comes back with its own permissions and not the other's, which is
	// the difference between "the list is right" and "the list is the same
	// shape as a one-row answer".
	byID := map[string][]Permission{}
	for _, link := range links {
		byID[link.ID] = link.Permissions
	}
	if got := byID[first]; len(got) != 1 || got[0] != PermViewReports {
		t.Errorf("the first relationship came back as %v", got)
	}
	if got := byID[second]; len(got) != 1 || got[0] != PermAssignMaterials {
		t.Errorf("the second relationship came back as %v", got)
	}
	// And the narrow read is still narrow: one guardian's list is one row.
	only, err := store.Links(ctx, parentUser)
	if err != nil {
		t.Fatalf("list one guardian's relationships: %v", err)
	}
	if len(only) != 1 || only[0].ID != first {
		t.Errorf("a guardian's own list returned %d relationship(s)", len(only))
	}
}

// makeLink writes one relationship with one permission, the way the service
// does, and returns its id.
func makeLink(
	store *GORMStore, ctx context.Context, creator, guardian, learner int64, permission Permission,
) (string, error) {
	link := Link{
		ID: linkIDFor(guardian, learner), GuardianID: guardian, LearnerID: learner,
		CreatedBy: creator, Permissions: []Permission{permission}, CreatedAt: atSecond(0),
	}
	if err := store.SaveLink(ctx, link); err != nil {
		return "", err
	}
	return link.ID, nil
}

// linkIDFor is a stable id per pair, so that a failure names the relationship it
// is about rather than a random one.
func linkIDFor(guardian, learner int64) string {
	return fmt.Sprintf("lnk_%d_%d", guardian, learner)
}
