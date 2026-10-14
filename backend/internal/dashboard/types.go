// Package dashboard owns the "continue studying" view: one read that fans in
// over the domains and answers where to pick up (COURSE-03).
//
// It is a package rather than a handler because the aggregation is a rule and
// not a response shape — which categories there are, what an entry carries, and
// what makes a recommendation one that may be handed out — and rules belong
// where they can be read on their own. It is also the only package in this build
// that depends on several domains and is depended on by none (§1).
//
// Three things it deliberately does not do:
//
//   - it keeps no table and no cache (§2.12). What it returns is a handful of
//     indexed reads at this build's data scale, and a snapshot table would
//     raise the question of when to refresh it in exchange for a speed nobody
//     has measured;
//   - it writes nothing. Every method of every domain it holds is a read, which
//     is what makes the dashboard safe to call on a page load;
//   - it does not decide the limit. How many entries a category carries is a
//     deployment's number (§5, DASHBOARD_RECENT_LIMIT) and arrives at
//     construction.
package dashboard

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrInvalidEntry is a row the dashboard would not put on the page.
var ErrInvalidEntry = errors.New("dashboard: the entry is not one to show")

// Entry is one thing the dashboard lists: an id in the domain it came from, a
// title to draw, a line of context, and when it last moved.
//
// The id is the domain's own and is not namespaced, because the category it sits
// in is the namespace: a client that knows it is reading the reviews list knows
// what a question id is.
//
// Title may be empty, and that is the domain's answer rather than this package's
// gap: a conversation nobody has named yet carries no title, and the sessions
// endpoint reports it the same way. What this package must not do is invent one
// — a placeholder here would be copy written by the aggregation, shown in place
// of the truth, and impossible to correct at the one place that knows.
type Entry struct {
	ID        string
	Title     string
	Context   string
	UpdatedAt time.Time
}

// Validate reports whether an entry is one to show.
//
// The id is the whole of the rule, and it is enforced here rather than at the
// edge because an entry without one is not a row a client can act on: there is
// nothing to open. A title that is absent is drawn as absent, which is why this
// checks the id and not the text.
func (e Entry) Validate() error {
	if strings.TrimSpace(e.ID) == "" {
		return fmt.Errorf("%w: it names nothing", ErrInvalidEntry)
	}
	return nil
}

// Kind names what kind of move a recommendation is.
//
// The values are the mastery path's step kinds (mastery.StepKind), and they are
// spelled out here rather than imported as that type because the dashboard's
// vocabulary will outgrow it: a recommendation to finish a reading unit is not a
// mastery step, and a type shared with one source would have to be widened by
// every source that follows.
type Kind string

const (
	// KindLearn is a knowledge point nobody has tested yet.
	KindLearn Kind = "learn"
	// KindReview is something the review curve says is due.
	KindReview Kind = "review"
	// KindDone is a path where everything has been tested and the next move is
	// already scheduled.
	KindDone Kind = "done"
)

// Valid reports whether the kind is one this package defines.
func (k Kind) Valid() bool {
	switch k {
	case KindLearn, KindReview, KindDone:
		return true
	default:
		return false
	}
}

// Recommendation is one suggested next move with its grounds (COURSE-04).
//
// Reason and EvidenceIDs are the two fields COURSE-04 refuses to let be empty,
// and the refusal is the reason this type has a Validate at all: a
// recommendation is a claim about what somebody should do, and a claim that
// cannot say what it read is one the surface has no way to justify.
type Recommendation struct {
	Kind Kind
	// PathID and KnowledgePointID say what the move is about. The path is always
	// named; the knowledge point is blank only for a move that names a coordinate
	// no longer in the tree, which cannot reach here (mastery.NextStep reads the
	// tree it came from).
	PathID           string
	KnowledgePointID string
	// Title is what to draw — the knowledge point, or the path when the move is
	// about the path as a whole.
	Title string
	// Reason is the sentence the domain gave for the move, unedited. It is the
	// domain's sentence because the domain is what knows why (mastery.NextStep),
	// and rewording it here would be this package explaining a rule it does not
	// own.
	Reason string
	// EvidenceIDs is what the move stands on, and the ids resolve in the domain
	// they came from.
	EvidenceIDs []string
}

// Validate reports whether a recommendation may be handed out.
func (r Recommendation) Validate() error {
	if !r.Kind.Valid() {
		return fmt.Errorf("%w: %q is not a recommendation kind", ErrInvalidEntry, r.Kind)
	}
	if strings.TrimSpace(r.PathID) == "" {
		return fmt.Errorf("%w: the recommendation names no path", ErrInvalidEntry)
	}
	if strings.TrimSpace(r.Title) == "" {
		return fmt.Errorf("%w: recommendation for path %s has nothing to draw",
			ErrInvalidEntry, r.PathID)
	}
	if strings.TrimSpace(r.Reason) == "" {
		return fmt.Errorf("%w: recommendation for path %s says nothing about why",
			ErrInvalidEntry, r.PathID)
	}
	if len(r.EvidenceIDs) == 0 {
		return fmt.Errorf("%w: recommendation for path %s cites no evidence",
			ErrInvalidEntry, r.PathID)
	}
	return nil
}

// View is the whole dashboard: the categories of what is here, and the moves
// that follow from it.
//
// The four lists are separate rather than one list of mixed kinds, because they
// are answered by different questions and a client draws them in different
// places. Every one of them is an empty list rather than null for a person with
// nothing yet: an empty history is an answer, and a client that had to tell
// "nothing" from "missing" would be reading a distinction this API does not
// make (§2.12 第 2 条).
type View struct {
	Sessions []Entry
	Paths    []Entry
	Reviews  []Entry
	Next     []Recommendation
}

// build validates a recommendation where it is constructed.
//
// It mirrors the mastery path's own rule (mastery.NextStep's build), and for the
// same reason: a recommendation that cannot show its grounds is refused at the
// point it is made, not at the point it is rendered. It is not an error branch
// data can reach — what makes a recommendation is a mastery step that already
// cited evidence — and it is here so that the next source to make one cannot
// quietly skip the rule.
func build(recommendation Recommendation) (Recommendation, error) {
	if err := recommendation.Validate(); err != nil {
		return Recommendation{}, err
	}
	return recommendation, nil
}
