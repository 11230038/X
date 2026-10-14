package authz

// The four layers, combined in one place (§2.1).
//
// Everything above this file's Decide answers a narrower question: whether an
// account is the administrator, whether a row belongs to it, whether a guardian
// link exists. This file is where those answers become one decision, and it is
// deliberately the only file that has them all — a handler that wrote its own
// `if role == admin` would be making a second, private decision that nothing
// else could see or audit.
//
// The vocabulary and the two sets an account is measured against — the floor and
// the baseline — are here. What an account may reach is
// policy_reach.go's, and what an administrator may write down is
// policy_grants.go's. All of it is pure, so all of it is testable without a
// database — which is the point of doing policy before repository (§6, stage 5).

import (
	"errors"
)

// Floor is the part of a learner's baseline that no grant may take away.
//
// It is a constant in this file rather than rows in a table on purpose (§2.2):
// a floor kept in data can be deleted by one mistyped statement, and its
// absence is not an error — it reads as "no restriction", which is the reading
// that lets a learner out of the sandbox quietly.
type Floor struct {
	Capabilities []string
	Surfaces     []string
}

// DefaultFloor is the floor every instance runs with: chat, and nothing else.
//
// It is not configurable, and that is the whole of its value. Configuration
// widens the baseline above it (see Baseline), which is a superset — so there
// is no value of any variable that removes this from a learner's reach.
func DefaultFloor() Floor {
	return Floor{Capabilities: []string{"chat"}, Surfaces: []string{SurfaceChat}}
}

// Items lists the floor as items — what a learner reaches even when an
// administrator has closed everything closable.
func (f Floor) Items() []Item {
	items := make([]Item, 0, len(f.Capabilities)+len(f.Surfaces))
	for _, name := range f.Capabilities {
		items = append(items, Item{Kind: KindCapability, ID: name})
	}
	for _, name := range f.Surfaces {
		items = append(items, Item{Kind: KindSurface, ID: name})
	}
	return items
}

// Holds reports whether an item is one the floor protects.
//
// A whole-kind item is never held: for capability and surface, "everything of
// this kind" is exactly what a closed baseline denies, so there is no kind
// whose every member the floor speaks for.
func (f Floor) Holds(item Item) bool {
	if item.ID == "" {
		return false
	}
	switch item.Kind {
	case KindCapability:
		return contains(f.Capabilities, item.ID)
	case KindSurface:
		return contains(f.Surfaces, item.ID)
	default:
		return false
	}
}

// Baseline is what a learner may reach: the floor, plus whatever configuration
// names on top of it.
//
// The two are kept apart because they answer two different questions. The
// baseline is what an administrator may narrow — a grant may close anything in
// it that the floor does not hold — and the floor is what they may not
// narrow. Collapsing them into one set would make "narrow a learner's policy"
// either impossible or unbounded (§2.2).
type Baseline struct {
	// Floor is the subset no grant may close.
	Floor Floor
	// Capabilities and Surfaces are the floor plus what AUTHZ_LEARNER_* named.
	Capabilities []string
	Surfaces     []string
}

// NewBaseline builds a learner's baseline: the floor, plus what configuration
// named.
//
// It is a union rather than an override, and that is the direction AUTH-03's
// "must not be emptied" needs: a deployment that set AUTHZ_LEARNER_CAPABILITIES
// to something, or to nothing, cannot take the floor away by doing so. The
// floor is a lower bound of the baseline and not a default of it.
func NewBaseline(floor Floor, capabilities, surfaces []string) Baseline {
	return Baseline{
		Floor:        floor,
		Capabilities: union(floor.Capabilities, capabilities),
		Surfaces:     union(floor.Surfaces, surfaces),
	}
}

// Holds reports whether the baseline reaches an item at all.
func (b Baseline) Holds(item Item) bool {
	if item.ID == "" {
		return false
	}
	switch item.Kind {
	case KindCapability:
		return contains(b.Capabilities, item.ID)
	case KindSurface:
		return contains(b.Surfaces, item.ID)
	default:
		return false
	}
}

// Items lists the baseline as items, floor first.
func (b Baseline) Items() []Item {
	items := make([]Item, 0, len(b.Capabilities)+len(b.Surfaces))
	for _, name := range b.Capabilities {
		items = append(items, Item{Kind: KindCapability, ID: name})
	}
	for _, name := range b.Surfaces {
		items = append(items, Item{Kind: KindSurface, ID: name})
	}
	return items
}

// Empty reports whether the baseline has nothing in it.
func (b Baseline) Empty() bool {
	return len(b.Capabilities) == 0 && len(b.Surfaces) == 0
}

// NeverForLearners is AUTH-03's first sentence as a constant: tools, MCP, CLI
// apps and exec are not reachable by a learner. No grant opens one, and the
// refusal says which rule refused rather than silently doing nothing.
//
// The closed baseline already denies these — a learner reaches only what the
// floor names — so this list is not what makes them unreachable. It is what
// makes an administrator's attempt to open one *refused* instead of merely
// ineffective, which is the difference between a rule and a coincidence.
var NeverForLearners = []Item{
	Whole(KindTool),
	Whole(KindExec),
	{Kind: KindSurface, ID: "mcp"},
	{Kind: KindSurface, ID: "cli"},
}

// Administrative is the part of the product no preset opens: the governance
// routes. It is reached through the role layer and nothing else.
//
// Without this the administrative area would be open to every ordinary account,
// because the standard baseline is the whole product (§2.2) and the surface
// layer alone cannot tell "the product" from "the product's controls". It is
// checked before the grants for the same reason the never-list is: a grant that
// named this surface would otherwise be a way to promote an account without
// saying so, and a refusal at the point of writing it is a sentence rather than
// a row that quietly does nothing.
var Administrative = []Item{{Kind: KindSurface, ID: SurfaceAdmin}}

// Errors the policy refuses with. They are sentinels because a caller maps them
// onto a status, a response code and an audit reason, and three callers reading
// three different strings is how those three drift apart.
var (
	// ErrPolicyWouldBeEmpty is what an administrative change is refused with
	// when it would leave the account with nothing at all (AUTH-03's "must not
	// be emptied", §2.2).
	ErrPolicyWouldBeEmpty = errors.New("authz: the change would leave the policy empty")
	// ErrUnknownItem is what a decision about a kind this build does not decide
	// is refused with. It is an error rather than a denial because a kind that
	// is not in 00016's CHECK cannot have been written down, so reaching here
	// means a caller invented one.
	ErrUnknownItem = errors.New("authz: not a kind this build decides about")
	// ErrNeverForLearners is what a grant that tries to open one of
	// NeverForLearners is refused with.
	ErrNeverForLearners = errors.New("authz: a learner may not be given this")
	// ErrItemNotGrantable is what a grant that tries to open an item no preset
	// opens is refused with — the administrative area, which is the role
	// layer's. It is a separate sentence from ErrNeverForLearners because the
	// two rules are about different accounts: one is about a learner, and this
	// one is about everybody who is not the administrator.
	ErrItemNotGrantable = errors.New("authz: this item cannot be given by a grant")
)

// The reason codes a Decision carries. They are codes rather than sentences
// because they end up in audit_events.detail_json, and a sentence written by
// the layer that decided cannot be counted by the layer that reads it back.
const (
	ReasonAdministrator        = "administrator"
	ReasonOwnMaterial          = "own_material"
	ReasonBaselineUnrestricted = "baseline_unrestricted"
	ReasonBaselineAllows       = "baseline_allows"
	ReasonBaselineClosed       = "baseline_closed"
	ReasonGrantAllows          = "grant_allows"
	ReasonGrantDenies          = "grant_denies"
	ReasonFloorProtected       = "floor_protected"
	ReasonLearnerNever         = "learner_never"
	ReasonNoRelationship       = "no_relationship"
	ReasonPermissionMissing    = "permission_missing"
	ReasonPermissionHeld       = "permission_held"
	// ReasonUnknownItem is a denial, not an error, because the question was
	// still answered: a kind this build does not decide about is one nothing
	// may reach. The error ErrUnknownItem is for the write path, where the same
	// kind means a caller is inventing vocabulary.
	ReasonUnknownItem = "unknown_item"
	// ReasonNotAdministrator is what an administrative item is refused with
	// when the account is not the instance's administrator. It is its own code
	// rather than "baseline_closed" because the two are different sentences: a
	// closed baseline is a policy somebody set, and this is a door no preset
	// opens.
	ReasonNotAdministrator = "not_administrator"
)

// Decision is the answer to one question, and which layer gave it.
//
// Layer is carried rather than only the boolean because the two are read by
// different consumers: a handler reads Allowed, an audit entry records Layer
// and Reason. An answer that said only "no" would leave the audit asking the
// same question a second time to find out why.
//
// Item is carried for the reason a single answer does not need it and a list of
// them does: Effective and Self return decisions in a batch, and a batch whose
// entries do not say what they are about can only be read positionally against
// the baseline — which stopped being the whole set the moment a grant was
// allowed to open something the baseline never mentioned (§2.2).
type Decision struct {
	Item    Item
	Allowed bool
	Layer   Layer
	Reason  string
}

// Layer names which of the four layers decided.
type Layer string

const (
	// LayerRole is the account's role: are you the instance's administrator.
	LayerRole Layer = "role"
	// LayerSelf is ownership: is this yours.
	LayerSelf Layer = "self"
	// LayerPreset is the baseline the account's preset gives it.
	LayerPreset Layer = "preset"
	// LayerGrant is the administrator's per-item decision.
	LayerGrant Layer = "grant"
	// LayerFloor is the part of a learner's baseline that may not be taken
	// away (AUTH-03).
	LayerFloor Layer = "floor"
	// LayerGuardian is a link and the permission it holds over another account.
	LayerGuardian Layer = "guardian"
)

// Actor is the account a request is being served for, with everything the
// layers below the role need about it.
type Actor struct {
	Caller
	// Preset is the account's preset as stored, empty and all.
	Preset string
	// Grants are the account's rows of user_grants.
	Grants []Grant
}

// Action is what is being attempted.
type Action struct {
	// Item is what the action reaches.
	Item Item
	// Permission is the guardian permission this action exercises, when the
	// action is about somebody else's material. It is empty for an action about
	// one's own, and a cross-account action with an empty permission is denied:
	// there is no such thing as a guardian permission that was not named.
	Permission Permission
}

// Target is what an action is about.
type Target struct {
	// Subject is the account whose material the action concerns. Zero and the
	// actor's own id both mean "this is about the actor's own material".
	Subject int64
	// Link reports whether a guardian link from the actor to Subject exists.
	Link bool
	// Permissions are the permissions that link holds. A link that holds none
	// is a link that permits nothing, which is what makes the row's existence
	// the permission (§2.3).
	Permissions []Permission
}

// Decider is the learner's baseline in hand. It is a value rather than a
// package-level function so that the baseline is handed over once, where the
// process is assembled, instead of being read from a global that configuration
// happens to have set by the time somebody asks.
type Decider struct {
	baseline Baseline
}

// New returns a decider over a learner's baseline.
func New(baseline Baseline) Decider { return Decider{baseline: baseline} }

// Baseline returns the baseline this decider was built with.
func (d Decider) Baseline() Baseline { return d.baseline }

// Validate refuses a baseline a learner could not be given. It is called where
// the baseline is assembled, so that a misconfigured instance fails to start
// rather than starting with a learner whose policy means something nobody
// intended (§2.2).
//
// It cannot fire today — NewBaseline unions in a non-empty floor, so every
// baseline has something in it — and it is written anyway, because the way this
// becomes reachable is somebody adding a "start the baseline from configuration
// only" path, and a check that is already there is one that path has to get
// past.
func Validate(baseline Baseline) error {
	if baseline.Empty() {
		return ErrPolicyWouldBeEmpty
	}
	return nil
}
