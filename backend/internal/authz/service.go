package authz

// The rules that are not the policy's: who may be linked to whom, and what a
// change to a policy has to survive before it is written down.
//
// The split matters. policy.go answers "may this account reach this item" and
// is pure; this file answers "may this row be written at all" and needs the
// database to do it — a link's two accounts have to be looked up before either
// prohibition can be applied, and "the watched account is a learner" reads a
// column no CHECK constraint can reach across tables for.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

const (
	linkIDPrefix      = "lnk_"
	linkIDRandomBytes = 16
)

// Service owns the governance tables and the rules about writing them.
type Service struct {
	store  Store
	policy Decider
	now    func() time.Time
	random func([]byte) (int, error)
}

// NewService builds the service over a store and a policy.
func NewService(store Store, policy Decider) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("invalid authorization configuration: no store")
	}
	if err := Validate(policy.Baseline()); err != nil {
		return nil, fmt.Errorf("invalid authorization configuration: %w", err)
	}
	return &Service{store: store, policy: policy, now: time.Now, random: rand.Read}, nil
}

// Policy returns the decider this service decides with.
func (s *Service) Policy() Decider { return s.policy }

// Actor loads the two layers that live in the database for one account: the
// preset it was given and the grants written on top of it.
//
// It is what a caller hands to Decide, and it is loaded per request rather than
// cached, because the endpoint that changes an account's grants has to take
// effect on that account's next request rather than on its next restart.
func (s *Service) Actor(ctx context.Context, caller Caller) (Actor, error) {
	if !caller.Valid() {
		return Actor{}, fmt.Errorf("%w: caller names no account", ErrUnknownUser)
	}
	preset, err := s.store.Preset(ctx, caller.UserID)
	if err != nil {
		return Actor{}, err
	}
	assignments, err := s.store.Assignments(ctx, caller.UserID)
	if err != nil {
		return Actor{}, err
	}
	return Actor{Caller: caller, Preset: preset, Grants: Grants(assignments)}, nil
}

// Target loads what a decision about somebody else's material needs: whether a
// link exists and what it carries.
//
// A subject that is the caller itself comes back as an empty Target — no link
// and no permissions — which Decide reads as "this is about the actor's own
// material". That is not a shortcut: an account has no link to itself, and
// asking the store would answer ErrLinkNotFound for the ordinary case of a
// request about one's own things.
func (s *Service) Target(ctx context.Context, caller Caller, subject int64) (Target, error) {
	if subject == 0 || subject == caller.UserID {
		return Target{Subject: caller.UserID}, nil
	}
	link, err := s.store.Link(ctx, caller.UserID, subject)
	if err != nil {
		if errors.Is(err, ErrLinkNotFound) {
			// Not an error: having no relationship is an answer, and the one
			// Decide turns into a refusal with a reason an audit can record.
			return Target{Subject: subject}, nil
		}
		return Target{}, err
	}
	return Target{Subject: subject, Link: true, Permissions: link.Permissions}, nil
}

// Decide answers one question, loading both sides of it.
func (s *Service) Decide(ctx context.Context, caller Caller, action Action, subject int64) (Decision, error) {
	actor, err := s.Actor(ctx, caller)
	if err != nil {
		return Decision{}, err
	}
	target, err := s.Target(ctx, caller, subject)
	if err != nil {
		return Decision{}, err
	}
	return s.policy.Decide(actor, action, target), nil
}

// Link creates a guardian relationship, with the permissions it starts with.
//
// AUTH-04's three prohibitions are applied here, and two of them are here
// because they cannot be anywhere else: no account is its own guardian
// (migration 00016 has a CHECK for that as well, and this is the sentence
// rather than the constraint violation), the watched account must be a learner,
// and a learner must not be a guardian. The last two read users.preset on two
// different accounts, which is a cross-table question.
func (s *Service) Link(ctx context.Context, caller Caller, guardianID, learnerID int64, permissions []Permission) (Link, error) {
	if guardianID == learnerID {
		return Link{}, ErrSelfLink
	}
	if err := s.checkPermissionNames(permissions); err != nil {
		return Link{}, err
	}
	guardianPreset, err := s.store.Preset(ctx, guardianID)
	if err != nil {
		return Link{}, err
	}
	if NormalizePreset(guardianPreset) == PresetLearner {
		return Link{}, fmt.Errorf("%w: %d", ErrGuardianIsLearner, guardianID)
	}
	learnerPreset, err := s.store.Preset(ctx, learnerID)
	if err != nil {
		return Link{}, err
	}
	if NormalizePreset(learnerPreset) != PresetLearner {
		return Link{}, fmt.Errorf("%w: %d", ErrNotALearner, learnerID)
	}
	id, err := s.newLinkID()
	if err != nil {
		return Link{}, err
	}
	link := Link{
		ID: id, GuardianID: guardianID, LearnerID: learnerID,
		CreatedBy: caller.UserID, Permissions: append([]Permission(nil), permissions...),
		CreatedAt: s.now().UTC(),
	}
	if err := s.store.SaveLink(ctx, link); err != nil {
		return Link{}, err
	}
	return link, nil
}

// Revoke removes a relationship and, with it, everything it allowed.
//
// It is a delete rather than a flag (§2.3), so the effect is immediate and
// there is no state for a later read to forget to check.
func (s *Service) Revoke(ctx context.Context, linkID string) error {
	return s.store.DeleteLink(ctx, linkID)
}

// GrantPermission adds one permission to an existing link (AUTH-05).
func (s *Service) GrantPermission(ctx context.Context, caller Caller, linkID string, permission Permission) error {
	if err := s.checkPermissionNames([]Permission{permission}); err != nil {
		return err
	}
	return s.store.SavePermission(ctx, linkID, permission, caller.UserID)
}

// RevokePermission takes one permission back.
//
// A permission that was not there is ErrPermissionNotHeld rather than a silent
// success, unlike revoking a link twice: taking back something a relationship
// does not carry means the caller believes something about it that the answer
// has to correct, and the same belief applied to a different permission is a
// way to lose one that is held.
func (s *Service) RevokePermission(ctx context.Context, linkID string, permission Permission) error {
	if err := s.checkPermissionNames([]Permission{permission}); err != nil {
		return err
	}
	link, err := s.store.LinkByID(ctx, linkID)
	if err != nil {
		return err
	}
	if !link.Holds(permission) {
		return fmt.Errorf("%w: %s", ErrPermissionNotHeld, permission)
	}
	return s.store.DeletePermission(ctx, linkID, permission)
}

// Relationships lists the relationships an account holds as guardian.
func (s *Service) Relationships(ctx context.Context, guardianID int64) ([]Link, error) {
	return s.store.Links(ctx, guardianID)
}

// Assign records one decision about one item, or refuses to.
//
// The refusal is the interesting half. A grant is a row, but the rule it has to
// satisfy is a property of the whole set (§2.2), so the existing grants are
// read, the change is applied to them, and the result is what is checked —
// which is also why this is the only write path: an update that skipped the
// check would be a way to arrive at a policy nobody allowed.
func (s *Service) Assign(ctx context.Context, caller Caller, userID int64, item Item, allowed bool) error {
	if !KnownKind(item.Kind) {
		return fmt.Errorf("%w: %s", ErrUnknownItem, item.Kind)
	}
	if !caller.Valid() {
		return fmt.Errorf("%w: caller names no account", ErrUnknownUser)
	}
	preset, err := s.store.Preset(ctx, userID)
	if err != nil {
		return err
	}
	existing, err := s.store.Assignments(ctx, userID)
	if err != nil {
		return err
	}
	change := Assignment{
		UserID: userID, Item: item, Allowed: allowed,
		GrantedBy: caller.UserID, CreatedAt: s.now().UTC(),
	}
	candidate := replaceAssignment(existing, change)
	if err := CheckGrants(preset, s.policy.Baseline(), Grants(candidate)); err != nil {
		return err
	}
	return s.store.SaveAssignment(ctx, change)
}

// Withdraw removes the decision recorded for one item, which returns that item
// to whatever the account's baseline says about it.
//
// It is not a way to empty a policy: removing the last grant leaves the
// baseline, and the baseline is not a row anybody can delete (§2.2). That is
// why this path has no check of its own.
func (s *Service) Withdraw(ctx context.Context, userID int64, item Item) error {
	if !KnownKind(item.Kind) {
		return fmt.Errorf("%w: %s", ErrUnknownItem, item.Kind)
	}
	return s.store.DeleteAssignment(ctx, userID, item)
}

// Assignments lists the decisions recorded about one account.
func (s *Service) Assignments(ctx context.Context, userID int64) ([]Assignment, error) {
	return s.store.Assignments(ctx, userID)
}

// Self is an account's own picture of itself: which preset it carries, every
// item it has an answer about, and the relationships it holds as guardian.
//
// The three travel together because they are read together — a screen asking
// "what may I reach" draws the preset, the items and the learners it watches on
// the same frame — and reading them one at a time would let the picture be
// assembled from three different moments (§2.16).
type Self struct {
	Preset   string
	Items    []Decision
	Guardian []Link
}

// Self reads the caller's own picture.
//
// It takes a Caller and not a user id: the id comes from the token, and a
// version that took one would be a route for reading somebody else's reach, which
// is exactly what the guardian permission check exists to be asked about rather
// than assumed.
func (s *Service) Self(ctx context.Context, caller Caller) (Self, error) {
	if !caller.Valid() {
		return Self{}, fmt.Errorf("%w: caller names no account", ErrUnknownUser)
	}
	preset, err := s.store.Preset(ctx, caller.UserID)
	if err != nil {
		return Self{}, err
	}
	assignments, err := s.store.Assignments(ctx, caller.UserID)
	if err != nil {
		return Self{}, err
	}
	links, err := s.store.Links(ctx, caller.UserID)
	if err != nil {
		return Self{}, err
	}
	return Self{
		Preset:   NormalizePreset(preset),
		Items:    s.reach(preset, assignments),
		Guardian: links,
	}, nil
}

// Effective lists what one account may reach, as the policy answers it.
//
// It is what an administrator is shown when they ask what a learner can do,
// and it is computed by the same function that answers the requests, so that
// the screen and the behaviour cannot drift.
func (s *Service) Effective(ctx context.Context, userID int64) ([]Decision, error) {
	preset, err := s.store.Preset(ctx, userID)
	if err != nil {
		return nil, err
	}
	assignments, err := s.store.Assignments(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.reach(preset, assignments), nil
}

// reach answers the policy for every item the account has an answer about, in a
// fixed order.
//
// The set is the baseline *plus* every item a grant names, and the union is the
// whole point (§2.2): a grant may open something the baseline never mentioned,
// so a screen built from the baseline alone would show a learner unable to
// reach the thing they are at that moment using. Every answer comes from Reach,
// the same function the middleware calls, so what is drawn and what is enforced
// cannot drift apart.
func (s *Service) reach(preset string, assignments []Assignment) []Decision {
	baseline := s.policy.Baseline()
	grants := Grants(assignments)
	items := mergeItems(baseline.Items(), grantedItems(grants))
	decisions := make([]Decision, 0, len(items))
	for _, item := range items {
		decisions = append(decisions, Reach(preset, baseline, grants, item))
	}
	return decisions
}

// grantedItems lists the items grants name.
//
// A grant whose id is empty is skipped, and it is not an oversight: the empty
// id is a statement about every item of its kind, not an item, so listing it
// would put a row on the screen — "capability" with no name — that no request
// can ever be about. The grants arrive ordered by (kind, id) from the store, so
// the items do too.
func grantedItems(grants []Grant) []Item {
	items := make([]Item, 0, len(grants))
	for _, grant := range grants {
		if grant.Item.ID != "" {
			items = append(items, grant.Item)
		}
	}
	return items
}

// mergeItems appends the items of extra that base does not already have,
// keeping base's order first. It is here rather than inlined so that the
// answer's order is one decision: a list that reshuffled between requests would
// be one nobody could compare against the previous screen.
func mergeItems(base, extra []Item) []Item {
	merged := make([]Item, 0, len(base)+len(extra))
	seen := make(map[Item]bool, len(base)+len(extra))
	for _, source := range [][]Item{base, extra} {
		for _, item := range source {
			if seen[item] {
				continue
			}
			seen[item] = true
			merged = append(merged, item)
		}
	}
	return merged
}

func (s *Service) checkPermissionNames(permissions []Permission) error {
	for _, permission := range permissions {
		if !KnownPermission(permission) {
			return fmt.Errorf("%w: %s", ErrUnknownPermission, permission)
		}
	}
	return nil
}

func (s *Service) newLinkID() (string, error) {
	buffer := make([]byte, linkIDRandomBytes)
	if _, err := s.random(buffer); err != nil {
		return "", fmt.Errorf("generate a relationship id: %w", err)
	}
	return linkIDPrefix + hex.EncodeToString(buffer), nil
}

// replaceAssignment applies a change to a set of grants that differ only in
// their Item. It is where "one decision per (user, kind, item)" is expressed
// above the unique key, so that CheckGrants sees the set the change would
// produce rather than the set plus a row.
func replaceAssignment(existing []Assignment, change Assignment) []Assignment {
	replaced := make([]Assignment, 0, len(existing)+1)
	for _, assignment := range existing {
		if assignment.Item == change.Item {
			continue
		}
		replaced = append(replaced, assignment)
	}
	return append(replaced, change)
}
