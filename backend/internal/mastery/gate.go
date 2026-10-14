package mastery

import (
	"fmt"
	"time"
)

// The gate and the review curve: what the evidence says about a knowledge point,
// and when to come back to it.
//
// Both are rules and not model calls — the fourth time this repository has made
// that trade (V1 §2.4, V2 §2.7, V3 §2.3, here). The reason is the one V3 wrote
// out: rules can be pinned down case by case and a model cannot, and "what
// should I learn next" is a decision that has to be *more* explicable than "was
// this answer right", not less.
//
// This file is deliberately pure. It reads no clock, touches no storage and
// knows no turn: it takes the evidence a path has and answers with a level and
// an interval. That is what makes both of them testable as arithmetic.

// GateOptions are the two thresholds the gate is configured with. They are
// deployment settings because they are about how much evidence is enough, which
// a small installation and a large one answer differently; the *arithmetic*
// below is not, and does not move.
type GateOptions struct {
	// Consecutive is how many right answers in a row promote a knowledge point
	// to mastered.
	Consecutive int
	// MinEvidence is how many observations a knowledge point needs before the
	// gate will call it anything but unknown.
	MinEvidence int
}

// Validate reports whether the thresholds are usable.
func (o GateOptions) Validate() error {
	if o.Consecutive <= 0 {
		return fmt.Errorf("mastery: the gate's consecutive threshold must be positive, not %d", o.Consecutive)
	}
	if o.MinEvidence <= 0 {
		return fmt.Errorf("mastery: the gate's evidence floor must be positive, not %d", o.MinEvidence)
	}
	return nil
}

// The review curve's intervals, in days, indexed by the length of the current
// run of right answers.
//
// It is a curve of its own and not the question-level one in
// `practice/schedule.go`, and the inputs are what differ: that one reads a
// question's streak and lapses because a question is asked over and over, and
// this one reads the evidence sequence because a *knowledge point* is not asked
// — it is observed, in different ways, and what it has is a history rather than
// a counter. Copying the question curve here would have produced a second copy
// of the same arithmetic reading a different column, which is worse than a
// second curve.
//
// The two do agree at the start — one day, then the question curve's six against
// this one's three — and separating from the second interval on is the intended
// shape: coming back to a question and coming back to an idea are the same
// decision the first time and different decisions after that.
var reviewIntervalDays = [...]int{1, 3, 7, 16, 35, 75, 160, 340}

// maxReviewIntervalDays caps the curve. Without a cap it is a geometric series
// with no ceiling, and a knowledge point with a long enough run would be
// scheduled past what a DATETIME holds — the same wall, and the same answer,
// as the question curve's.
const maxReviewIntervalDays = 365

// partialDivisor is what a partly-right observation divides the interval by.
// Partial credit is neither right nor wrong, so it neither extends the run nor
// resets it: the knowledge point is scheduled at half of what its run has
// earned, which keeps it closer than a mastered point and further than one just
// got wrong.
const partialDivisor = 2

// ReviewInterval is how long after its most recent observation a knowledge point
// should be seen again.
//
// It answers zero for a knowledge point with no evidence, and zero is "nothing
// to schedule" rather than "now": a knowledge point nobody has observed is not
// due, it is unstarted, and the level says so (LevelUnknown). The distinction is
// the same one the gate makes and it has to be made in both places, or the
// insight endpoint would file every new knowledge point as overdue.
func ReviewInterval(evidences []Evidence) time.Duration {
	run, last, ok := currentRun(evidences)
	if !ok {
		return 0
	}
	if last == ResultIncorrect {
		// Back to the beginning. A wrong answer is the one observation that says the
		// last interval was too generous, and the honest response is to come back
		// sooner rather than to split the difference.
		return time.Duration(reviewIntervalDays[0]) * 24 * time.Hour
	}

	days := reviewIntervalDays[0]
	if run > 0 {
		index := run
		if index >= len(reviewIntervalDays) {
			index = len(reviewIntervalDays) - 1
		}
		days = reviewIntervalDays[index-1]
	}
	if last == ResultPartial {
		days = days / partialDivisor
	}
	// Never less than a day. The floor is what keeps halving from ever producing an
	// interval of zero, which would schedule the knowledge point for the instant it
	// was observed and mean "come back now" — a state this curve has no way to
	// express and no reason to want.
	if days < 1 {
		days = 1
	}
	if days > maxReviewIntervalDays {
		days = maxReviewIntervalDays
	}
	return time.Duration(days) * 24 * time.Hour
}

// GateDecide returns the level a knowledge point's evidence implies.
//
// The three-valued outcome is the whole point of the minimum: a knowledge point
// with one wrong answer has been tried once, and calling it weak would tell a
// learner they are bad at something they have barely met. Below the floor it is
// unknown — and the insight endpoint reports unknown as its own thing, in the
// same way it reports a knowledge point still in the review queue as its own
// (MP-05, PRD §19.3).
func GateDecide(evidences []Evidence, options GateOptions) Level {
	if len(evidences) < options.MinEvidence {
		return LevelUnknown
	}
	run, last, _ := currentRun(evidences)
	if run >= options.Consecutive {
		return LevelMastered
	}
	if last == ResultIncorrect {
		return LevelWeak
	}
	return LevelLearning
}

// GateTree returns the tree with every knowledge point's cached level recomputed
// from the evidence.
//
// This is the only writer of KnowledgePoint.Mastery, and it runs on every write
// of the state so that the cache cannot be a stale copy of a different answer.
// Every knowledge point gets a level and not only the ones with evidence,
// because the cache has to be total: a knowledge point left blank would be
// renderable as a fourth thing — "not yet decided" — that the domain does not
// have, and MP-05 would have to guess which of its three kinds of "not mastered"
// a blank meant.
//
// It does not add or remove knowledge points: evidence for a coordinate that is
// not in the tree is ignored, because the service refuses such evidence where it
// enters and a reader of a tree that no longer holds a coordinate should not
// resurrect it.
func GateTree(tree Tree, evidences []Evidence, options GateOptions) Tree {
	byPoint := make(map[string][]Evidence, len(evidences))
	for _, evidence := range evidences {
		byPoint[evidence.KnowledgePoint] = append(byPoint[evidence.KnowledgePoint], evidence)
	}
	next := tree
	for _, id := range tree.KnowledgePointIDs() {
		next = next.SetLevel(id, GateDecide(byPoint[id], options))
	}
	return next
}

// currentRun reads the trailing run of right answers, the most recent result,
// and whether there is any evidence at all.
//
// "Trailing" and not "total": what a knowledge point's level is about is whether
// the learner knows it *now*, and a learner who got four right and then two
// wrong is not four-sixths of the way to knowing it. Partial credit ends the run
// without being a failure — it is stored and reported, and it decides neither
// half of this pair.
func currentRun(evidences []Evidence) (int, Result, bool) {
	if len(evidences) == 0 {
		return 0, "", false
	}
	last := evidences[len(evidences)-1].Result
	run := 0
	for index := len(evidences) - 1; index >= 0; index-- {
		if evidences[index].Result != ResultCorrect {
			break
		}
		run++
	}
	return run, last, true
}
