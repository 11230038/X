package authz

// What an administrator may write down, and the two lists that say what no grant
// may touch.
//
// The rule and its enforcement are kept in separate places on purpose: the
// never-list and the administrative list are declared beside the vocabulary in
// policy.go, and the two functions at the bottom of this file are the only code
// that reads them.

import (
	"fmt"
)

// CheckGrants refuses a set of grants an administrator may not write.
//
// Three refusals, and they are three different rules rather than one:
//
//   - a grant that clears the learning policy — AUTH-03's "must not be emptied",
//     written down as the one row that means it. It is refused rather than
//     filtered out because an administrator who tried it believes something
//     about the learner that the answer has to correct.
//   - a grant that hands a learner something NeverForLearners says no learner
//     may be handed, however the administrator asks for it.
//   - a kind outside 00016's CHECK, which means the caller invented vocabulary.
//
// It answers about the whole set rather than one row because the question "is
// this policy still a policy" is not a property of a row: it is the state the
// set arrives at, and it is arrived at one row at a time.
func CheckGrants(preset string, baseline Baseline, grants []Grant) error {
	preset = NormalizePreset(preset)
	for _, grant := range grants {
		switch {
		case !KnownKind(grant.Item.Kind):
			return ErrUnknownItem
		case grant.Allowed && isAdministrative(grant.Item):
			// Writing it is refused rather than ignored: an administrator who
			// tried to hand out a governance route believes something about the
			// account that the answer has to correct, and the row would have
			// looked written while deciding nothing.
			return fmt.Errorf("%w: %s", ErrItemNotGrantable, grant.Item.ID)
		case preset != PresetLearner:
			continue
		case grant.Allowed && neverForLearners(grant.Item):
			return ErrNeverForLearners
		case !grant.Allowed && grant.Item.Kind == KindLearningPolicy:
			return ErrPolicyWouldBeEmpty
		}
	}
	if preset != PresetLearner {
		return nil
	}
	// And the state itself, which the two rules above should already have made
	// unreachable. Checking it anyway is what keeps the invariant true if a
	// third rule is ever added: an administrator who can describe a learner
	// that may reach nothing has described something the code has no meaning
	// for, and the refusal is a sentence rather than a learner who quietly got
	// everything.
	for _, item := range baseline.Items() {
		if decision := Reach(preset, baseline, grants, item); decision.Allowed {
			return nil
		}
	}
	return ErrPolicyWouldBeEmpty
}

// isAdministrative reports whether an item is one no preset opens, and which
// only the role layer answers.
//
// It is written as a scan of the list rather than as a comparison against the
// one name, so that a second administrative area is added where it is declared
// rather than where it is enforced.
func isAdministrative(item Item) bool {
	for _, reserved := range Administrative {
		if reserved.Kind != item.Kind {
			continue
		}
		if reserved.ID == "" || reserved.ID == item.ID {
			return true
		}
	}
	return false
}

// neverForLearners reports whether an item is under a rule no grant may lift.
// A whole-kind rule covers every item of the kind, which is why an item naming
// a specific tool is refused as firmly as the kind itself.
func neverForLearners(item Item) bool {
	for _, forbidden := range NeverForLearners {
		if forbidden.Kind != item.Kind {
			continue
		}
		if forbidden.ID == "" || forbidden.ID == item.ID {
			return true
		}
	}
	return false
}

// mostSpecific is the grant that decides about an item: the one naming it if
// there is one, and otherwise the one naming its whole kind.
//
// Specific beats general because both rows can exist side by side — the unique
// key is (user_id, item_kind, item_id) and the empty item_id is a value, not a
// NULL — and reading the general one when a specific one is there would make
// "turn this one thing back on" impossible to express.
func mostSpecific(grants []Grant, item Item) (Grant, bool) {
	var kindWide Grant
	var haveKindWide bool
	for _, grant := range grants {
		if grant.Item.Kind != item.Kind {
			continue
		}
		if grant.Item.ID == item.ID {
			return grant, true
		}
		if grant.Item.ID == "" {
			kindWide, haveKindWide = grant, true
		}
	}
	return kindWide, haveKindWide
}
