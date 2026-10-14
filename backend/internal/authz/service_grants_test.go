package authz

// What an administrator may hand out one item at a time, and what the account sees
// when it asks what it may reach.
//
// The rule these are about is a property of the whole set of grants rather than
// of a row, which is why every case here arrives at a state by writing twice.

import (
	"context"
	"errors"
	"testing"
)

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
