package authz

// The policy's cases, and none of them need a database — which is the reason
// stage 5 exists on its own (§6). What is being pinned here is the shape of the
// model rather than a table of answers: a baseline a grant can add to, a grant
// that can subtract from it, a floor neither can reach, and the fact that
// "clear the grants" is not a way out of any of it.

import (
	"errors"
	"testing"
)

// learnerBaseline is the floor plus one configured capability and one
// configured surface, which is the shape a real deployment has: the floor
// narrower than the baseline, so that there is something an administrator may
// take away.
func learnerBaseline() Baseline {
	return NewBaseline(DefaultFloor(), []string{"deep_question"}, []string{"reading"})
}

func learnerActor(grants ...Grant) Actor {
	return Actor{
		Caller: Caller{UserID: 7, Role: RoleUser},
		Preset: PresetLearner,
		Grants: grants,
	}
}

func standardActor(grants ...Grant) Actor {
	return Actor{
		Caller: Caller{UserID: 7, Role: RoleUser},
		Preset: PresetStandard,
		Grants: grants,
	}
}

func TestStandardStartsFromEverything(t *testing.T) {
	// §2.2: 基线(standard) = 全集. Every kind, and the whole of it — there is no
	// enumeration to fall outside of, so "may this account reach X" is answered
	// yes for anything the build decides about.
	for _, kind := range Kinds {
		item := Item{Kind: kind, ID: "anything"}
		if decision := Reach(PresetStandard, learnerBaseline(), nil, item); !decision.Allowed {
			t.Errorf("%s/%s: standard denied, want allowed (%s)", kind, item.ID, decision.Reason)
		}
	}
}

func TestLearnerStartsFromTheBaselineAndNothingElse(t *testing.T) {
	// §2.2: 基线(learner) ⊇ 地板, and the baseline is a closed set. A learner
	// reaches what it names and nothing outside it — the closedness is what
	// makes tools and exec unreachable without anybody having to remember to
	// deny them.
	baseline := learnerBaseline()
	for _, item := range baseline.Items() {
		if !baseline.Holds(item) {
			t.Errorf("%s/%s was listed and then not held", item.Kind, item.ID)
		}
		if decision := Reach(PresetLearner, baseline, nil, item); !decision.Allowed {
			t.Errorf("%s/%s is in the baseline and was denied: %s", item.Kind, item.ID, decision.Reason)
		}
	}
	denied := Reach(PresetLearner, baseline, nil, Item{Kind: KindCapability, ID: "roadmap"})
	if denied.Allowed {
		t.Errorf("a capability outside the baseline was allowed")
	}
	if denied.Layer != LayerPreset || denied.Reason != ReasonBaselineClosed {
		t.Errorf("the denial was %s/%s, want %s/%s",
			denied.Layer, denied.Reason, LayerPreset, ReasonBaselineClosed)
	}
}

func TestTheFloorIsASubsetOfTheBaseline(t *testing.T) {
	// The two sets are not the same question. Every floor item is reachable and
	// every floor item is in the baseline — that is the "⊇" of §2.2 — and the
	// baseline has more in it than the floor does, which is what leaves an
	// administrator something to narrow.
	baseline := learnerBaseline()
	for _, item := range DefaultFloor().Items() {
		if !baseline.Holds(item) {
			t.Errorf("floor item %s/%s is not in the baseline", item.Kind, item.ID)
		}
		if !baseline.Floor.Holds(item) {
			t.Errorf("floor item %s/%s is not held by the floor", item.Kind, item.ID)
		}
	}
	widened := len(baseline.Capabilities) + len(baseline.Surfaces)
	floor := len(DefaultFloor().Capabilities) + len(DefaultFloor().Surfaces)
	if widened <= floor {
		t.Errorf("the baseline has %d items and the floor %d: configuration added nothing", widened, floor)
	}
}

func TestConfigurationOnlyAddsToTheBaseline(t *testing.T) {
	// The baseline is the floor's union with configuration, never an override.
	// A deployment that set AUTHZ_LEARNER_CAPABILITIES='' would otherwise have
	// emptied the policy by leaving a variable blank (§2.2).
	wide := NewBaseline(DefaultFloor(), []string{"deep_question"}, []string{"reading"})
	for _, item := range []Item{
		{Kind: KindCapability, ID: "chat"},
		{Kind: KindCapability, ID: "deep_question"},
		{Kind: KindSurface, ID: "chat"},
		{Kind: KindSurface, ID: "reading"},
	} {
		if !wide.Holds(item) {
			t.Errorf("%s/%s left the baseline when configuration was applied", item.Kind, item.ID)
		}
	}
	blank := NewBaseline(DefaultFloor(), nil, nil)
	if !blank.Holds(Item{Kind: KindCapability, ID: "chat"}) {
		t.Errorf("configuration with nothing in it took the floor out of the baseline")
	}
}

func TestAGrantOpensSomethingOutsideTheBaseline(t *testing.T) {
	// §2.2's second consequence: grants are an increment on a baseline, and the
	// increment can go up.
	opened := Grant{Item: Item{Kind: KindCapability, ID: "roadmap"}, Allowed: true}
	decision := Reach(PresetLearner, learnerBaseline(), []Grant{opened},
		Item{Kind: KindCapability, ID: "roadmap"})
	if !decision.Allowed {
		t.Fatalf("a grant that turns something on did not turn it on: %s", decision.Reason)
	}
	if decision.Layer != LayerGrant || decision.Reason != ReasonGrantAllows {
		t.Errorf("the allowance was %s/%s, want %s/%s",
			decision.Layer, decision.Reason, LayerGrant, ReasonGrantAllows)
	}
}

func TestAGrantClosesSomethingInTheBaseline(t *testing.T) {
	// And the increment can go down. This is what makes the baseline a
	// description of a baseline rather than one of two competing sets: the item
	// closed here is in the baseline and not in the floor, so there is a way to
	// narrow a learner without touching the floor.
	closed := Grant{Item: Item{Kind: KindCapability, ID: "deep_question"}, Allowed: false}
	decision := Reach(PresetLearner, learnerBaseline(), []Grant{closed},
		Item{Kind: KindCapability, ID: "deep_question"})
	if decision.Allowed {
		t.Fatalf("a grant that turns something off did not turn it off")
	}
	if decision.Layer != LayerGrant || decision.Reason != ReasonGrantDenies {
		t.Errorf("the denial was %s/%s, want %s/%s",
			decision.Layer, decision.Reason, LayerGrant, ReasonGrantDenies)
	}
}

func TestASpecificGrantBeatsAWholeKindOne(t *testing.T) {
	// Both rows can exist: the unique key is (user, kind, item_id) and the empty
	// item_id is a value rather than a NULL. If the general row won, "turn this
	// one thing back on" would be impossible to express.
	grants := []Grant{
		{Item: Whole(KindCapability), Allowed: false},
		{Item: Item{Kind: KindCapability, ID: "chat"}, Allowed: true},
	}
	if decision := Reach(PresetLearner, learnerBaseline(), grants, Item{Kind: KindCapability, ID: "chat"}); !decision.Allowed {
		t.Errorf("the specific grant lost to the whole-kind one: %s", decision.Reason)
	}
	if decision := Reach(PresetLearner, learnerBaseline(), grants, Item{Kind: KindCapability, ID: "roadmap"}); decision.Allowed {
		t.Errorf("the whole-kind grant stopped applying to everything else")
	}
}

func TestAnEmptyItemIDIsNotTheSameDecisionAsANamedOne(t *testing.T) {
	// The same distinction from the other side: a decision about one item says
	// nothing about its kind, and a decision about a kind is not a decision
	// about any particular one of its members. The storage half of this — that
	// both rows are storable at once — is a real-database case in stage 6,
	// because only the unique index can prove it.
	one := []Grant{{Item: Item{Kind: KindSurface, ID: "reading"}, Allowed: true}}
	if decision := Reach(PresetLearner, learnerBaseline(), one, Item{Kind: KindSurface, ID: "notebook"}); decision.Allowed {
		t.Errorf("a grant for one surface opened another: %s", decision.Reason)
	}
}

func TestTheLearnerFloorSurvivesEveryGrant(t *testing.T) {
	// This is AUTH-03's "must not be emptied" as a case. An administrator denies
	// the learner the whole of every kind; the floor is still there, because a
	// grant may not reach past it — and every floor item is allowed *because* of
	// the floor, which is the part that says the protection is doing the work
	// rather than the baseline happening to be there.
	// Every kind but learning_policy: denying that one is refused as "clearing
	// the policy" (TestClearTheLearningPolicyIsRefused), which is a different
	// rule and would otherwise be the reason this set came back refused.
	grants := make([]Grant, 0, len(Kinds))
	for _, kind := range Kinds {
		if kind == KindLearningPolicy {
			continue
		}
		grants = append(grants, Grant{Item: Whole(kind), Allowed: false})
	}
	for _, item := range DefaultFloor().Items() {
		decision := Reach(PresetLearner, learnerBaseline(), grants, item)
		if !decision.Allowed {
			t.Errorf("%s/%s was closed by a grant: %s", item.Kind, item.ID, decision.Reason)
		}
		if decision.Reason != ReasonFloorProtected {
			t.Errorf("%s/%s allowed for %s, want the floor to be the reason", item.Kind, item.ID, decision.Reason)
		}
	}
	// And a baseline item that is not in the floor did get closed, so the grants
	// above were not simply ignored.
	closed := Reach(PresetLearner, learnerBaseline(), grants, Item{Kind: KindCapability, ID: "deep_question"})
	if closed.Allowed {
		t.Errorf("a baseline item outside the floor survived a whole-kind denial: %s", closed.Reason)
	}
	if err := CheckGrants(PresetLearner, learnerBaseline(), grants); err != nil {
		t.Errorf("a set of grants that leaves the floor intact was refused: %v", err)
	}
}

func TestClearTheLearningPolicyIsRefused(t *testing.T) {
	// AUTH-03's "must not be emptied", as the one row that means it. It is the
	// kind learning_policy exists for: a whole-kind denial of it is an
	// administrator saying "this learner may reach nothing", and the refusal is
	// a sentence rather than a learner who quietly got everything.
	cleared := Grant{Item: Whole(KindLearningPolicy), Allowed: false}
	err := CheckGrants(PresetLearner, learnerBaseline(), []Grant{cleared})
	if !errors.Is(err, ErrPolicyWouldBeEmpty) {
		t.Errorf("clearing the learning policy = %v, want %v", err, ErrPolicyWouldBeEmpty)
	}
	// Granting it, or denying one named thing, is not clearing it.
	if err := CheckGrants(PresetLearner, learnerBaseline(), []Grant{{Item: Whole(KindLearningPolicy), Allowed: true}}); err != nil {
		t.Errorf("turning the learning policy on was refused: %v", err)
	}
}

func TestAnEmptyBaselineIsRefused(t *testing.T) {
	// The state §2.2 warns about is unreachable through configuration, so the
	// only way to arrive at it is for a caller to build one directly. It is
	// refused rather than read as "no restriction", which is what an empty
	// policy reads as if nobody refuses it.
	if err := Validate(Baseline{}); !errors.Is(err, ErrPolicyWouldBeEmpty) {
		t.Errorf("Validate(empty baseline) = %v, want %v", err, ErrPolicyWouldBeEmpty)
	}
	if err := Validate(learnerBaseline()); err != nil {
		t.Errorf("Validate(learner baseline) = %v, want nil", err)
	}
}

func TestAGrantMayNotHandALearnerATool(t *testing.T) {
	// AUTH-03's first sentence. The closed baseline already denies tools and
	// exec; this makes the attempt to open one a refusal with a reason, which is
	// the difference between a rule and a coincidence.
	for _, item := range NeverForLearners {
		grant := Grant{Item: item, Allowed: true}
		decision := Reach(PresetLearner, learnerBaseline(), []Grant{grant}, item)
		if decision.Allowed {
			t.Errorf("%s/%s was opened by a grant", item.Kind, item.ID)
		}
		if decision.Layer != LayerFloor || decision.Reason != ReasonLearnerNever {
			t.Errorf("%s/%s denied for %s/%s, want %s/%s",
				item.Kind, item.ID, decision.Layer, decision.Reason, LayerFloor, ReasonLearnerNever)
		}
		if err := CheckGrants(PresetLearner, learnerBaseline(), []Grant{grant}); !errors.Is(err, ErrNeverForLearners) {
			t.Errorf("CheckGrants(%s/%s) = %v, want %v", item.Kind, item.ID, err, ErrNeverForLearners)
		}
	}
	// A standard account may have a tool; the rule is about learners.
	tool := Grant{Item: Whole(KindTool), Allowed: true}
	if err := CheckGrants(PresetStandard, learnerBaseline(), []Grant{tool}); err != nil {
		t.Errorf("a standard account was refused a tool: %v", err)
	}
}

func TestAnUnnamedPresetReadsAsStandard(t *testing.T) {
	// Every account registered before V5 carries preset = ''. Reading them as
	// restricted would be a tightening nobody asked for, and reading them as
	// standard is what they can actually do today (§2.2).
	if NormalizePreset("") != PresetStandard {
		t.Errorf("NormalizePreset(\"\") = %q, want %q", NormalizePreset(""), PresetStandard)
	}
	decision := Reach("", learnerBaseline(), nil, Item{Kind: KindCapability, ID: "anything"})
	if !decision.Allowed || decision.Reason != ReasonBaselineUnrestricted {
		t.Errorf("a pre-V5 account was not read as standard: %s/%s", decision.Layer, decision.Reason)
	}
}

func TestAnUnknownKindIsDeniedRatherThanGuessedAt(t *testing.T) {
	decision := Reach(PresetStandard, learnerBaseline(), nil, Item{Kind: "telepathy", ID: "x"})
	if decision.Allowed {
		t.Errorf("a kind outside 00016's CHECK was allowed")
	}
	if decision.Reason != ReasonUnknownItem {
		t.Errorf("the denial was %s, want %s", decision.Reason, ReasonUnknownItem)
	}
	err := CheckGrants(PresetStandard, learnerBaseline(), []Grant{{Item: Item{Kind: "telepathy"}}})
	if !errors.Is(err, ErrUnknownItem) {
		t.Errorf("CheckGrants(unknown kind) = %v, want %v", err, ErrUnknownItem)
	}
}

func TestTheFourLayersAreCombinedInOrder(t *testing.T) {
	decider := New(learnerBaseline())

	admin := Actor{Caller: Caller{UserID: 1, Role: RoleAdmin}, Preset: PresetLearner}
	decision := decider.Decide(admin, Action{Item: Whole(KindTool)}, Target{})
	if !decision.Allowed {
		t.Errorf("the administrator was denied: %s", decision.Reason)
	} else if decision.Layer != LayerRole {
		t.Errorf("the administrator's allowance came from %s, want %s", decision.Layer, LayerRole)
	}

	// Own material is answered by the reach an account has over its own things,
	// and the subject naming itself is the same thing as the subject being
	// absent.
	actor := learnerActor()
	own := decider.Decide(actor, Action{Item: Item{Kind: KindCapability, ID: "chat"}}, Target{Subject: actor.UserID})
	if !own.Allowed {
		t.Errorf("a learner was denied its own floor item: %s", own.Reason)
	}
	absent := decider.Decide(actor, Action{Item: Item{Kind: KindCapability, ID: "chat"}}, Target{})
	if !absent.Allowed {
		t.Errorf("a learner was denied its own floor item when the subject was unset: %s", absent.Reason)
	}
	// And a standard account's own material is its own even for a kind a learner
	// would be refused.
	standard := standardActor()
	if got := decider.Decide(standard, Action{Item: Whole(KindTool)}, Target{}); !got.Allowed {
		t.Errorf("a standard account was denied a tool on its own material: %s", got.Reason)
	}
}

func TestAGuardianNeedsBothALinkAndThePermission(t *testing.T) {
	decider := New(learnerBaseline())
	actor := standardActor()
	subject := int64(9)
	action := Action{Item: Whole(KindKB), Permission: PermViewReports}

	cases := []struct {
		name   string
		target Target
		want   string
		allow  bool
	}{
		{
			name:   "no relationship at all",
			target: Target{Subject: subject, Link: false, Permissions: []Permission{PermViewReports}},
			want:   ReasonNoRelationship,
		},
		{
			name:   "a link that does not carry this permission",
			target: Target{Subject: subject, Link: true, Permissions: []Permission{PermAssignMaterials}},
			want:   ReasonPermissionMissing,
		},
		{
			name:   "a link that carries none",
			target: Target{Subject: subject, Link: true},
			want:   ReasonPermissionMissing,
		},
		{
			name:   "a link that carries it",
			target: Target{Subject: subject, Link: true, Permissions: []Permission{PermViewReports}},
			want:   ReasonPermissionHeld,
			allow:  true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			decision := decider.Decide(actor, action, testCase.target)
			if decision.Allowed != testCase.allow {
				t.Fatalf("allowed = %v, want %v (%s)", decision.Allowed, testCase.allow, decision.Reason)
			}
			if decision.Reason != testCase.want {
				t.Errorf("reason = %s, want %s", decision.Reason, testCase.want)
			}
			if decision.Layer != LayerGuardian {
				t.Errorf("layer = %s, want %s", decision.Layer, LayerGuardian)
			}
		})
	}
}

func TestAnActionAboutSomebodyElseMustNameAPermission(t *testing.T) {
	// There is no such thing as a guardian permission that was not named: an
	// action against another account with an empty permission is refused even
	// when the link carries all four.
	decision := CrossAccount(true, Permissions, "")
	if decision.Allowed {
		t.Errorf("an unnamed permission was allowed")
	}
	if decision.Reason != ReasonPermissionMissing {
		t.Errorf("reason = %s, want %s", decision.Reason, ReasonPermissionMissing)
	}
}

func TestTheVocabularyMatchesTheCheckConstraint(t *testing.T) {
	// The seven kinds and the four permissions are the DDL's closed sets
	// (migration 00016). They are repeated here because Go cannot read a CHECK,
	// so the two lists are asserted against each other where a reviewer will see
	// the pair side by side.
	if len(Kinds) != 7 {
		t.Errorf("len(Kinds) = %d, want 7", len(Kinds))
	}
	if len(Permissions) != 4 {
		t.Errorf("len(Permissions) = %d, want 4 (AUTH-05)", len(Permissions))
	}
	for _, kind := range Kinds {
		if !KnownKind(kind) {
			t.Errorf("%s is listed but not known", kind)
		}
	}
	for _, permission := range Permissions {
		if !KnownPermission(permission) {
			t.Errorf("%s is listed but not known", permission)
		}
	}
	if KnownKind("") {
		t.Errorf("the empty kind was accepted")
	}
	if KnownPermission("") {
		t.Errorf("the empty permission was accepted")
	}
	// The floor is not one of the things a learner may be given or refused: it
	// is a constant, and a caller that could name it as an item could hand it to
	// somebody else's policy.
	for _, item := range DefaultFloor().Items() {
		if item.Kind != KindCapability && item.Kind != KindSurface {
			t.Errorf("the floor holds %s/%s, which is neither a capability nor a surface", item.Kind, item.ID)
		}
	}
}
