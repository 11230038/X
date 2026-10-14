package mastery

import (
	"fmt"
	"strings"
	"time"
)

// What to do next on a path (MP-03, MP-05).
//
// It is a rule and not a model call, the same trade the gate and the curve made
// in gate.go, and it is pure for the same reason: it reads no clock, touches no
// storage and can be pinned down case by case.
//
// The answer has a shape — a kind, a coordinate, a sentence and the evidence it
// stands on — because COURSE-04 asks for a recommendation *with its grounds*, and
// a sentence alone cannot be shown to be grounded. It is the same rule the
// dashboard's entries follow, and it is enforced here rather than at the edge: a
// step that cannot say why is refused where it is built, not where it is
// rendered.

// StepKind is what kind of move a step is. It is a closed set, like Result and
// Level, and for the same reason: a kind this package does not define would be a
// kind the surface has no way to draw.
type StepKind string

const (
	// StepMap is a path whose tree is empty. Nothing can be recommended about a
	// coordinate that does not exist yet, so the next move is to draw the tree —
	// and it is the one step that cites no evidence, because what it stands on is
	// the absence of one.
	StepMap StepKind = "map"
	// StepLearn is a knowledge point nobody has ever tested. It is separate from
	// StepReview because the two are different answers to "why this one": a
	// review is due, and a lesson is unstarted (LevelUnknown is not LevelWeak —
	// §2.4).
	StepLearn StepKind = "learn"
	// StepReview is a knowledge point the curve says is due.
	StepReview StepKind = "review"
	// StepDone is a path where every knowledge point has been tested and none is
	// due: the honest answer is that the next move is already scheduled.
	StepDone StepKind = "done"
)

// Valid reports whether the kind is one this package defines.
func (k StepKind) Valid() bool {
	switch k {
	case StepMap, StepLearn, StepReview, StepDone:
		return true
	default:
		return false
	}
}

// Step is the next move on a path: what to do, where, why, and on what evidence.
type Step struct {
	Kind StepKind
	// PathID is the path this is about. It is not optional: a step is always a
	// step on one path, which is what makes it actionable.
	PathID string
	// KnowledgePointID is the coordinate the step is about, and it is blank only
	// for StepMap. It is read against the tree it came from, so it resolves as
	// long as the tree does — a coordinate that no longer exists is dropped
	// rather than reported (§2.2, §8 第 12 条).
	KnowledgePointID string
	// Reason is the sentence a person reads. It is never blank.
	Reason string
	// EvidenceIDs is what the step stands on, and it is never blank for a step
	// that makes a claim about how the learner is doing.
	EvidenceIDs []string
}

// Validate reports whether a step is one that may be handed out.
//
// The rule about evidence is the one worth stating: a review or a "you are done"
// cites the observations it read, and the two kinds that cite none are the two
// whose ground is that there is nothing yet — a tree that has not been drawn, and
// a knowledge point that has never been tested. Anywhere else, a step with no
// evidence would be a recommendation with nothing behind it, which is exactly
// what COURSE-04 refuses.
func (s Step) Validate() error {
	if !s.Kind.Valid() {
		return fmt.Errorf("%w: %q is not a step kind", ErrInvalidRequest, s.Kind)
	}
	if strings.TrimSpace(s.PathID) == "" {
		return fmt.Errorf("%w: the step names no path", ErrInvalidRequest)
	}
	if strings.TrimSpace(s.Reason) == "" {
		return fmt.Errorf("%w: a step of kind %s says nothing about why", ErrInvalidRequest, s.Kind)
	}
	switch s.Kind {
	case StepMap:
		if strings.TrimSpace(s.KnowledgePointID) != "" {
			return fmt.Errorf("%w: a step to draw the tree names a knowledge point", ErrInvalidRequest)
		}
	case StepLearn:
		if strings.TrimSpace(s.KnowledgePointID) == "" {
			return fmt.Errorf("%w: a step to learn something names no knowledge point", ErrInvalidRequest)
		}
	default:
		if strings.TrimSpace(s.KnowledgePointID) == "" {
			return fmt.Errorf("%w: a step of kind %s names no knowledge point", ErrInvalidRequest, s.Kind)
		}
		if len(s.EvidenceIDs) == 0 {
			return fmt.Errorf("%w: a step of kind %s cites no evidence", ErrInvalidRequest, s.Kind)
		}
	}
	return nil
}

// NextStep decides what a learner should do next on a path.
//
// The order is the whole of the rule, and it is three questions in a row:
//
//  1. is there anything to be about? An empty tree is the first answer, because
//     every other answer names a coordinate.
//  2. is anything due? A due knowledge point outranks an untested one, and the
//     reason is what the curve is: coming back when the curve says to is a
//     promise this path made, and a point nobody has met has made no such
//     promise. Among the due ones the most overdue wins, and tree order breaks
//     the tie — the learner's own ordering, used to make the answer single-
//     valued rather than to express a preference.
//  3. otherwise, is anything untested? Then that is the move: the first one in
//     tree order, because a tree is written top to bottom.
//
// What is left after all three is a path where everything has been tested and
// nothing is due, and that is a real state rather than an empty one: the answer
// is when the next review is, with the evidence that says so.
func NextStep(path Path, evidences []Evidence, now time.Time) (Step, error) {
	byPoint := evidenceByPoint(evidences)
	ids := path.Tree.KnowledgePointIDs()
	if len(ids) == 0 {
		return build(Step{
			Kind:   StepMap,
			PathID: path.ID,
			Reason: "这条路径还是空的：先用 mastery_map 把要学的知识点按模块列出来，再开始出题。",
		})
	}

	var (
		due      string
		dueAt    time.Time
		untested string
		tested   string
		testedAt time.Time
	)
	for _, id := range ids {
		point := byPoint[id]
		if len(point) == 0 {
			if untested == "" {
				untested = id
			}
			continue
		}
		at := point[len(point)-1].CreatedAt.Add(ReviewInterval(point))
		// The earliest due time, so that a knowledge point overdue by a month is
		// chosen over one that came due yesterday. Equality keeps the first in tree
		// order, which is what makes the answer single-valued.
		if tested == "" || at.Before(testedAt) {
			tested, testedAt = id, at
		}
		if !at.After(now) && (due == "" || at.Before(dueAt)) {
			due, dueAt = id, at
		}
	}

	if due != "" {
		return build(Step{
			Kind:             StepReview,
			PathID:           path.ID,
			KnowledgePointID: due,
			Reason: fmt.Sprintf("「%s」该复习了（上次观察是 %s）。用 mastery_question 出一道题，再用 mastery_grade 判一下。",
				nameOf(path.Tree, due), day(dueAt)),
			EvidenceIDs: evidenceIDs(byPoint[due]),
		})
	}
	if untested != "" {
		return build(Step{
			Kind:             StepLearn,
			PathID:           path.ID,
			KnowledgePointID: untested,
			Reason: fmt.Sprintf("「%s」还没考过：先出一道题看看它会落在哪里（没考过的知识点不算薄弱）。",
				nameOf(path.Tree, untested)),
		})
	}
	return build(Step{
		Kind:             StepDone,
		PathID:           path.ID,
		KnowledgePointID: tested,
		Reason: fmt.Sprintf("这条路径上的知识点都考过了，最近的一条是「%s」（%s 该复习）。",
			nameOf(path.Tree, tested), day(testedAt)),
		EvidenceIDs: evidenceIDs(byPoint[tested]),
	})
}

// build validates what NextStep produced.
//
// It is deliberately not an error branch that can be reached by data: it is here
// so that the rule about reasons and evidence is enforced at the one place that
// constructs a step, which is what COURSE-04 asks for and what a renderer sitting
// downstream could not check for itself.
func build(step Step) (Step, error) {
	if err := step.Validate(); err != nil {
		return Step{}, err
	}
	return step, nil
}

// evidenceByPoint groups a path's evidence by the knowledge point it is about,
// keeping the order it arrived in — oldest first, since that is how a repository
// reads it and how the run in gate.go counts it.
func evidenceByPoint(evidences []Evidence) map[string][]Evidence {
	byPoint := make(map[string][]Evidence, len(evidences))
	for _, evidence := range evidences {
		byPoint[evidence.KnowledgePoint] = append(byPoint[evidence.KnowledgePoint], evidence)
	}
	return byPoint
}

// evidenceIDs lists the ids of one knowledge point's evidence, which is what a
// step cites as its grounds.
func evidenceIDs(evidences []Evidence) []string {
	ids := make([]string, 0, len(evidences))
	for _, evidence := range evidences {
		ids = append(ids, evidence.ID)
	}
	return ids
}

// nameOf reads a knowledge point's name for a sentence. A coordinate that is not
// in the tree gives back the id: a step is never returned for a coordinate the
// tree does not hold, and if one ever were, the id is the honest answer.
func nameOf(tree Tree, id string) string {
	point, _, ok := tree.KnowledgePoint(id)
	if !ok {
		return id
	}
	return point.Name
}

// day renders an instant as a date, which is the granularity the curve works in.
func day(at time.Time) string {
	return at.UTC().Format("2006-01-02")
}
