package authz

// What one account may reach: its own two layers, and the one question that is
// about somebody else's material.
//
// Both are exported because they are asked on their own — an administrator
// asking "what can this learner reach" wants Reach, and a guardian's route wants
// CrossAccount — and neither is a special case of the other.

// Decide combines the four layers for one request.
//
// The order is the order of the questions, and it is not arbitrary: role first
// because there is nobody above the administrator to check it; then ownership,
// because an account's own material needs no permission to reach; then the
// guardian link, because a decision about somebody else's material is not a
// decision about one's own restricted by a permission; and only then the
// baseline with the grants on top.
func (d Decider) Decide(actor Actor, action Action, target Target) Decision {
	if actor.IsAdmin() {
		return Decision{Item: action.Item, Allowed: true, Layer: LayerRole, Reason: ReasonAdministrator}
	}
	if target.Subject == 0 || target.Subject == actor.UserID {
		return Reach(actor.Preset, d.baseline, actor.Grants, action.Item)
	}
	// CrossAccount answers a question about a permission rather than about an
	// item, so it returns a decision with none; the item is what was being
	// attempted, which is the caller's to supply and belongs on the answer.
	decision := CrossAccount(target.Link, target.Permissions, action.Permission)
	decision.Item = action.Item
	return decision
}

// Reach answers what an account's own two layers allow about one item: the
// baseline its preset gives it, with its grants applied on top, over the floor
// that no grant may remove.
//
// It is exported because it is the half of the model that has nothing to do
// with anybody else, and an administrator asking "what can this learner reach"
// is asking this question — not the composite one, which would have to be told
// whose material was meant.
func Reach(preset string, baseline Baseline, grants []Grant, item Item) Decision {
	if !KnownKind(item.Kind) {
		return Decision{Item: item, Allowed: false, Layer: LayerPreset, Reason: ReasonUnknownItem}
	}
	preset = NormalizePreset(preset)

	// The administrative area first, and before the grants: it is not a preset's
	// to give and not an administrator's to hand out one item at a time, so
	// nothing below may open it. An administrator never asks this question —
	// Decide answers the role layer first — which is why a refusal here is a
	// refusal to everybody else.
	if isAdministrative(item) {
		return Decision{Item: item, Allowed: false, Layer: LayerRole, Reason: ReasonNotAdministrator}
	}
	// The never-list is checked before the grants so that the refusal names the
	// rule rather than looking like a baseline that happened to be closed, and
	// before the floor so that a floor containing a forbidden item could not
	// open one by accident.
	if preset == PresetLearner && neverForLearners(item) {
		return Decision{Item: item, Allowed: false, Layer: LayerFloor, Reason: ReasonLearnerNever}
	}
	if grant, found := mostSpecific(grants, item); found {
		// A grant may turn something off. It may not turn off the floor: the
		// floor is the part of the baseline that survives an administrator
		// closing everything closable, and a grant that reached past it would
		// make "the policy cannot be emptied" a statement about nothing.
		if !grant.Allowed && preset == PresetLearner && baseline.Floor.Holds(item) {
			return Decision{Item: item, Allowed: true, Layer: LayerFloor, Reason: ReasonFloorProtected}
		}
		if grant.Allowed {
			return Decision{Item: item, Allowed: true, Layer: LayerGrant, Reason: ReasonGrantAllows}
		}
		return Decision{Item: item, Allowed: false, Layer: LayerGrant, Reason: ReasonGrantDenies}
	}
	if preset == PresetLearner {
		if baseline.Holds(item) {
			return Decision{Item: item, Allowed: true, Layer: LayerPreset, Reason: ReasonBaselineAllows}
		}
		return Decision{Item: item, Allowed: false, Layer: LayerPreset, Reason: ReasonBaselineClosed}
	}
	// standard and custom start from everything and are narrowed per item. The
	// learner is the only preset with a closed baseline (§2.2), and a custom
	// account that was meant to be restricted is restricted by its grants.
	return Decision{Item: item, Allowed: true, Layer: LayerPreset, Reason: ReasonBaselineUnrestricted}
}

// CrossAccount answers whether an account may reach another's material.
//
// The two refusals are kept apart because they are two different facts for an
// audit to record: having no relationship at all, and having one that does not
// carry this permission. A single "denied" would lose the distinction that
// makes AUTH-04's third prohibition checkable.
func CrossAccount(link bool, held []Permission, need Permission) Decision {
	if !link {
		return Decision{Allowed: false, Layer: LayerGuardian, Reason: ReasonNoRelationship}
	}
	if need == "" {
		return Decision{Allowed: false, Layer: LayerGuardian, Reason: ReasonPermissionMissing}
	}
	for _, permission := range held {
		if permission == need {
			return Decision{Allowed: true, Layer: LayerGuardian, Reason: ReasonPermissionHeld}
		}
	}
	return Decision{Allowed: false, Layer: LayerGuardian, Reason: ReasonPermissionMissing}
}
