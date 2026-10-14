package authz

// The two writes an administrator makes, and the two lists they read.
//
// They are separated from service_test.go because they answer a different
// question: that file is about a relationship's two accounts, and this one is
// about an account's own two layers. What is asserted here is which changes are
// *refused* — a preset this build does not write, an account that is not there,
// and above all a preset move that would leave an account reaching nothing —
// because a refusal written as a sentence is what keeps a policy from being
// emptied one plausible request at a time.

import (
	"context"
	"errors"
	"testing"
)

func TestSetPresetMovesTheBaselineAndLeavesTheGrantsWhereTheyAre(t *testing.T) {
	store := newFakeStore()
	service := newService(t, store)
	store.grants[parentID] = []Assignment{
		{UserID: parentID, Item: Item{Kind: KindSurface, ID: SurfaceReading}, Allowed: true},
	}

	if err := service.SetPreset(context.Background(), admin(), parentID, PresetLearner); err != nil {
		t.Fatalf("SetPreset: %v", err)
	}
	if store.presets[parentID] != PresetLearner {
		t.Errorf("preset = %q, want learner", store.presets[parentID])
	}
	// The grants are not touched by a preset move, and that is why the question
	// is asked about a *state*: what has to survive is the combination the
	// account arrives at, not the two writes taken one at a time.
	if len(store.grants[parentID]) != 1 || store.grants[parentID][0].Item.ID != SurfaceReading {
		t.Errorf("the preset move rewrote the grants: %+v", store.grants[parentID])
	}
}

func TestSetPresetRefusesAPresetThisBuildDoesNotWrite(t *testing.T) {
	// NormalizePreset answers a *read* and turns anything it does not recognise
	// into standard, so it cannot be the guard on a write: accepting "Learner"
	// here would store a string the policy then reads as the whole product.
	for _, preset := range []string{"", "Learner", "superuser", "admin"} {
		store := newFakeStore()
		service := newService(t, store)
		err := service.SetPreset(context.Background(), admin(), parentID, preset)
		if !errors.Is(err, ErrUnknownPreset) {
			t.Errorf("SetPreset(%q) = %v, want ErrUnknownPreset", preset, err)
		}
		if store.presets[parentID] != PresetStandard {
			t.Errorf("SetPreset(%q) wrote %q anyway", preset, store.presets[parentID])
		}
	}
}

func TestSetPresetRefusesToLeaveALearnerReachingNothing(t *testing.T) {
	// A learner whose whole floor is closed reaches nothing, and this build has
	// no meaning for that account. The refusal is the whole of AUTH-03's "must
	// not be emptied": the alternative is a preset move that quietly produces an
	// account nobody can describe.
	store := newFakeStore()
	service := newService(t, store)
	// The floor is what survives every grant, so it is the one thing to close.
	store.grants[childID] = []Assignment{
		{UserID: childID, Item: Item{Kind: KindCapability, ID: "chat"}, Allowed: false},
		{UserID: childID, Item: Item{Kind: KindSurface, ID: SurfaceChat}, Allowed: false},
	}

	err := service.SetPreset(context.Background(), admin(), childID, PresetCustom)
	if !errors.Is(err, ErrPolicyWouldBeEmpty) {
		t.Fatalf("SetPreset = %v, want ErrPolicyWouldBeEmpty", err)
	}
	if store.presets[childID] != PresetLearner {
		t.Errorf("preset = %q, want it left at learner", store.presets[childID])
	}
}

func TestSetPresetRefusesAnAccountThatIsNotThere(t *testing.T) {
	store := newFakeStore()
	service := newService(t, store)
	if err := service.SetPreset(context.Background(), admin(), 999, PresetLearner); !errors.Is(err, ErrUnknownUser) {
		t.Fatalf("SetPreset = %v, want ErrUnknownUser", err)
	}
}

func TestSetPresetRefusesACallerThatNamesNoAccount(t *testing.T) {
	// The caller is checked for validity and not for authority — authority is
	// Decide's answer, given by the route — but a caller that names nobody means
	// the request did not come through the token guard, and writing the row
	// anyway would leave a preset move with no record of who made it.
	store := newFakeStore()
	service := newService(t, store)
	if err := service.SetPreset(context.Background(), Caller{}, parentID, PresetLearner); !errors.Is(err, ErrUnknownUser) {
		t.Fatalf("SetPreset = %v, want ErrUnknownUser", err)
	}
	if store.presets[parentID] != PresetStandard {
		t.Error("a caller-less request moved a preset")
	}
}

func TestAccountsAndAllRelationshipsAreTheStoresAnswersUnfiltered(t *testing.T) {
	// Neither of these filters by the caller, and that is deliberate: what makes
	// them administrative is the route in front of them, and a second filter
	// here would be a second answer to the same question. The case pins the
	// absence, so that adding one has to be a decision rather than a slip.
	store := newFakeStore()
	service := newService(t, store)
	if _, err := service.Link(context.Background(), admin(), parentID, childID,
		[]Permission{PermViewReports}); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if _, err := service.Link(context.Background(), admin(), otherID, strangerID,
		[]Permission{PermAssignMaterials}); err != nil {
		t.Fatalf("Link: %v", err)
	}

	accounts, err := service.Accounts(context.Background())
	if err != nil {
		t.Fatalf("Accounts: %v", err)
	}
	if len(accounts) != 5 {
		t.Errorf("accounts = %d, want every account the store holds", len(accounts))
	}
	links, err := service.AllRelationships(context.Background())
	if err != nil {
		t.Fatalf("AllRelationships: %v", err)
	}
	if len(links) != 2 {
		t.Errorf("links = %d, want every relationship there is", len(links))
	}
}

func TestAccountsCarryThePresetAsStoredAndAsRead(t *testing.T) {
	// The two differ for exactly the accounts that predate the column, and a
	// client drawing the raw string would show them as having no preset at all.
	store := newFakeStore()
	store.presets[strangerID] = ""
	service := newService(t, store)

	accounts, err := service.Accounts(context.Background())
	if err != nil {
		t.Fatalf("Accounts: %v", err)
	}
	for _, account := range accounts {
		if account.UserID != strangerID {
			continue
		}
		if account.Preset != "" {
			t.Errorf("the stored preset was rewritten to %q", account.Preset)
		}
		if got := account.PresetOf(); got != PresetStandard {
			t.Errorf("PresetOf = %q, want standard", got)
		}
		return
	}
	t.Fatal("the account was not in the list")
}
