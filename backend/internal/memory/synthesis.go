package memory

// L3, the synthesis: what memory looks like now (§2.6).
//
// The layer is a pure function rather than a table, and §2.6 asks a reviewer to
// take that judgement rather than assume it. A snapshot table would be a second
// source of truth, and this kind of debt is paid on every edit, retraction and
// merge — with "we forgot to refresh it" showing up as a fact the user took back
// that is still in the synthesis, which is the failure MEM-02 names. Computed,
// there is nothing to forget: an edit changes the answer in the same request, and
// a retraction is simply not read.
//
// The cost is that L3 has no history. "What did the synthesis look like last
// month" is not answerable, and §2.6 takes that cost because no requirement asks
// it: MEM-02 audits *actions* rather than views, and every action that moved the
// view is a row `memory_audit` already holds.
//
// Nothing in this file reads a clock, a table or the environment. The moment is a
// parameter, the ordering is total (newestFirst) and the knobs are handed in,
// which is what makes "the same input gives the same output" a property a case
// can check instead of a claim in a comment.
//
// The four sections are the slots the reference implementation names (recent /
// profile / scope / preferences), rendered from what L2 actually holds. One of
// them had to be re-founded rather than translated: L2 stores no `kind`, and its
// absence is the layer's meaning — a kind is part of what an *observation* said,
// and a fact is the claim that survived several of them (types.go). So
// "preferences" cannot mean "the facts whose kind is preference". What it means
// here is the facts the user wrote down themselves, which is the section a reader
// opens to see what they have told the system about themselves, and the only
// facts in memory whose authority is the user rather than the gate.

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"
)

// Synthesis is what memory looks like now: the whole of L3, computed on every
// read.
//
// The four lists are regroupings of one set of facts — the facts in force, less
// whatever the per-surface bound left out — and not four different memories. Two
// of them partition it (Recent and Profile, by when), and the other two cut it a
// second way (Scopes and Preferences, by what the fact says about itself).
type Synthesis struct {
	OwnerID int64
	// ComputedAt is the moment this synthesis describes and WindowDays the window
	// it was computed with. Both travel with the answer because both are inputs a
	// reader would otherwise have to be told by the deployment: "recent" is a
	// question about a window, and an answer that did not carry the one it used
	// would be read as if it covered everything.
	ComputedAt time.Time
	WindowDays int

	// Recent is the facts in force that were admitted inside the window, newest
	// first: what the system has just been told about the account.
	Recent []Fact
	// Profile is what has settled — the facts in force admitted *before* the
	// window, one row per surface. Together with Recent it is a partition of the
	// facts this synthesis is showing, which is what makes the pair readable as
	// "what is new" and "what has been true for a while". It is also where the
	// per-surface bound is reported, because it is the per-surface section — and
	// the bound and this section are about the same facts: a bound that keeps the
	// newest leaves almost entirely settled ones outside it.
	Profile []SurfaceProfile
	// Scopes is the same facts grouped by the scope the admitting observation
	// named.
	Scopes []ScopeGroup
	// Preferences is the facts the user wrote down themselves (§2.7's fourth
	// path) — see the note above on why this section is not keyed on a kind.
	Preferences []Fact
}

// SurfaceProfile is one surface's contribution to the synthesis.
//
// It is the section that answers PRD §11.3's "L3 → contributing surfaces": a
// synthesis is a cross-surface picture, and a picture that did not say which
// surface each part of it came from would be one the reader has to take on trust.
type SurfaceProfile struct {
	Surface Surface
	Facts   []Fact
	// Truncated is how many of this surface's facts the per-surface bound left
	// out, which are the ones it said longest ago. It is reported rather than
	// absorbed because a bound nobody can see makes a bounded synthesis read as
	// the whole picture.
	Truncated int
}

// ScopeGroup is the facts that named one scope.
//
// The empty scope is a group like any other rather than an absence: "this
// belongs nowhere in particular" is a thing a surface can say by naming no
// scope, and a fact with no scope dropping out of the section would turn a
// grouping into a filter.
type ScopeGroup struct {
	Scope string
	Facts []Fact
}

// surfaceOrder is the order the per-surface sections come back in: the order the
// surface constants are declared in, rather than whatever order a map happened to
// iterate in. A synthesis that reshuffled its surfaces between two reads of the
// same facts would look like memory had changed when nothing had.
var surfaceOrder = []Surface{SurfaceMastery, SurfaceReading, SurfacePractice, SurfaceUser}

// Synthesize is L3: a pure function of the facts, the moment and the knobs.
//
// The facts are what the caller read; a fact that is not in force is dropped here
// as well as in the read, because this function is also reachable by a caller
// holding all of L2, and "what memory looks like now" cannot contain something the
// account has taken back.
func Synthesize(ownerID int64, facts []Fact, now time.Time, options Options) Synthesis {
	kept, truncated := boundPerSurface(inForce(facts), options.MaxFactsPerSurface)
	// A day is a calendar day and the times in this package are UTC throughout, so
	// a window of thirty days reaches exactly thirty days back.
	cutoff := now.AddDate(0, 0, -options.SynthesisWindowDays)

	synthesis := Synthesis{
		OwnerID: ownerID, ComputedAt: now, WindowDays: options.SynthesisWindowDays,
		Recent: []Fact{}, Profile: []SurfaceProfile{}, Scopes: []ScopeGroup{}, Preferences: []Fact{},
	}
	// The one ordering of this synthesis: boundPerSurface hands back what it kept in
	// newestFirst order, and every section below is a regroup of that one ordered
	// slice, so no section has an order of its own to disagree with.
	settled := make([]Fact, 0, len(kept))
	for _, fact := range kept {
		if fact.CreatedAt.Before(cutoff) {
			settled = append(settled, fact)
			continue
		}
		synthesis.Recent = append(synthesis.Recent, fact)
	}

	for _, surface := range surfaceOrder {
		profile := SurfaceProfile{Surface: surface, Facts: []Fact{}, Truncated: truncated[surface]}
		for _, fact := range settled {
			if fact.Surface == surface {
				profile.Facts = append(profile.Facts, fact)
			}
		}
		if len(profile.Facts) == 0 && profile.Truncated == 0 {
			// A surface with nothing to say is not a row: an empty section reads as
			// a claim about that surface rather than as the absence of one.
			continue
		}
		synthesis.Profile = append(synthesis.Profile, profile)
	}

	byScope := make(map[string][]Fact, len(kept))
	for _, fact := range kept {
		byScope[fact.Scope] = append(byScope[fact.Scope], fact)
	}
	for _, scope := range sortedScopes(byScope) {
		synthesis.Scopes = append(synthesis.Scopes, ScopeGroup{Scope: scope, Facts: byScope[scope]})
	}

	for _, fact := range kept {
		if fact.ConfirmedBy == ConfirmerUser {
			synthesis.Preferences = append(synthesis.Preferences, fact)
		}
	}
	return synthesis
}

// Synthesis reads L2 and computes L3 (§2.6).
//
// One read and one function, with nothing written in between — no snapshot, no
// cache, no memo. That is the whole of the mechanism, and it is why an edit, a
// retraction or a merge changes the answer in the same request: there is nowhere
// else for the old answer to live.
//
// The read is of the facts in force, which is a filter in SQL rather than a
// decision left to the function: a retracted fact is not part of what memory
// looks like now, and a service that read all three statuses to throw two of them
// away would be reading rows it has no use for.
func (s *Service) Synthesis(ctx context.Context, ownerID int64) (Synthesis, error) {
	if ownerID <= 0 {
		return Synthesis{}, fmt.Errorf("%w: the owner is %d", ErrInvalidRequest, ownerID)
	}
	facts, err := s.repository.Facts(ctx, FactQuery{OwnerID: ownerID, Statuses: []Status{StatusActive}})
	if err != nil {
		return Synthesis{}, err
	}
	return Synthesize(ownerID, facts, s.clock(), s.options), nil
}

// inForce keeps the facts that belong in a synthesis at all.
func inForce(facts []Fact) []Fact {
	kept := make([]Fact, 0, len(facts))
	for _, fact := range facts {
		if fact.InForce() {
			kept = append(kept, fact)
		}
	}
	return kept
}

// boundPerSurface applies MaxFactsPerSurface to one surface's contribution.
//
// The bound is applied once, to the whole set, before anything is arranged into
// sections — "how many facts one surface contributes to a synthesis" is a
// sentence about the synthesis rather than about one of its four lists. What a
// surface loses is what it said longest ago, and the count it loses is reported
// in its own Profile row: the fact that the picture is bounded is part of the
// picture.
//
// A bound that is not positive is not a bound and is not applied. Options.Validate
// refuses one, so this is only reachable by a caller building Options by hand —
// and keeping every fact is the answer that cannot quietly lose one.
//
// What comes back is in newestFirst order, and it is the order Synthesize reads
// the whole synthesis off: the bound is where a synthesis is ordered, once.
func boundPerSurface(facts []Fact, max int) ([]Fact, map[Surface]int) {
	truncated := map[Surface]int{}
	if max <= 0 {
		return facts, truncated
	}
	seen := map[Surface]int{}
	kept := make([]Fact, 0, len(facts))
	for _, fact := range newestFirst(facts) {
		seen[fact.Surface]++
		if seen[fact.Surface] > max {
			truncated[fact.Surface]++
			continue
		}
		kept = append(kept, fact)
	}
	return kept, truncated
}

// newestFirst orders facts the way every listing in this package does: most
// recently admitted first, and a tie — two facts admitted in the same second,
// which the DATETIME columns make ordinary — broken by id, descending.
//
// A total order is what makes the synthesis a function rather than a rendering:
// the engine is free to return the rows of one read in any order it likes, and
// two reads of the same facts have to produce the same answer.
func newestFirst(facts []Fact) []Fact {
	ordered := slices.Clone(facts)
	slices.SortFunc(ordered, func(left, right Fact) int {
		return cmp.Or(right.CreatedAt.Compare(left.CreatedAt), cmp.Compare(right.ID, left.ID))
	})
	return ordered
}

// sortedScopes names the scopes in alphabetical order, so that the section's own
// order is a property of the names rather than of the order the rows arrived in.
// The empty scope sorts first, which is where a reader looks for "the things that
// belong nowhere in particular".
func sortedScopes(byScope map[string][]Fact) []string {
	names := make([]string, 0, len(byScope))
	for scope := range byScope {
		names = append(names, scope)
	}
	slices.Sort(names)
	return names
}
