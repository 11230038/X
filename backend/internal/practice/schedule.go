package practice

// The review schedule: what one answered question does to the next time that
// question is due.
//
// It is a function and not a service. There is no job, no timer and no worker:
// a question is due when a column says it is, and this file is only what writes
// that column. So it takes the state it is given, reads no clock, touches no
// storage, and returns the state to store — which is what makes the whole curve
// testable as arithmetic rather than as behaviour.
//
// The curve is a simplified SM-2 (see intervalFor and easeAfter for what the
// simplification is). It is *our* choice rather than a requirement: nothing in
// the product asks for SM-2, which is why the column it maintains is called
// `case` and not `ease`.

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// ErrInvalidReview reports a review the schedule cannot act on: a marker that
// is not one of the three, or a self-rating outside the scale. It is refused
// rather than corrected, because a silently clamped rating is a curve the user
// cannot explain.
var ErrInvalidReview = errors.New("practice: the review cannot be scheduled")

// EaseInitial and EaseMinimum bound the memory difficulty coefficient.
//
// They are SM-2's conventional values, and they are exported because they are
// also the meaning of an empty column: a question nobody has reviewed has no
// ease, and the schedule reads that as an initial one.
const (
	EaseInitial = 2.5
	EaseMinimum = 1.3
)

// The rest of the curve is not configuration, and that is deliberate: these are
// the algorithm, not a deployment's preference. The keys that do live in the
// environment are about how much practice to hand out at a time.
const (
	// easeQualityBonus, easeQualitySlope and easeQualityPenalty are the
	// coefficients of SM-2's own ease update. They are written out rather than
	// inlined so that the line below them reads as the formula it is.
	easeQualityBonus   = 0.1
	easeQualityPenalty = 0.08
	easeQualitySlope   = 0.02

	// qualityMaximum is the top of the self-rating scale, and the quality a
	// correct answer is credited with when the user did not rate it — the answer
	// was right, and there is nothing to take away from that.
	qualityMaximum = 5
	// qualityWrong is what a wrong answer is credited with, absent a rating. It
	// is SM-2's "incorrect, but the right answer seemed familiar": a wrong
	// answer says something about the question's difficulty, and it says it
	// less harshly than a wrong answer the user also rated 0.
	qualityWrong = 2

	// secondIntervalDays is SM-2's second anchor. The first is one day, and the
	// two of them are the whole starting shape of the curve: today, tomorrow,
	// then a week.
	secondIntervalDays = 6
	// maxIntervalDays caps how far a question can be scheduled. Without it the
	// curve is a geometric series with no ceiling, and thirty correct answers
	// with a rising ease would schedule the question for the year 3000 — which
	// is not a schedule, and is also past what a DATETIME column holds. A year is
	// past the horizon of anything the product does with the answer, so this is
	// where the growth stops.
	maxIntervalDays = 365
)

// The three intervals that are not part of the rising curve.
var (
	// firstInterval is where a run of correct answers starts.
	firstInterval = 24 * time.Hour
	// mistakeInterval is how long a wrong answer stays out of the queue, which
	// is not at all: it is due immediately and is lifted to the front of the
	// queue by its mistake mark. The alternative — sending it away for a day —
	// would mean the question someone just got wrong is the one they are least
	// likely to see again today.
	mistakeInterval time.Duration
	// skipInterval is how long a skipped question stays out of the queue. It has
	// to move, or the next fetch hands back the question that was just skipped
	// and skipping reads as doing nothing. It moves by half an hour rather than
	// by a day: skipping is "not this one now", and the question belongs at the
	// back of today's queue rather than out of it.
	skipInterval = 30 * time.Minute
)

// Marker is what the user says about a question along with their answer.
//
// Three of the five marks in the design's table are here, because only three of
// them belong to answering a question. The other two — bookmarking and marking
// a question resolved by hand — are property changes: they are idempotent and
// commutative, they take no version, and they are the question bank's business
// rather than this package's.
type Marker string

const (
	// MarkerNone is an answer and nothing else: the question advances by how it
	// was judged.
	MarkerNone Marker = ""
	// MarkerMastered is the user saying they know it. It schedules the question
	// as a correct answer would and takes it out of the queue for good.
	MarkerMastered Marker = "mastered"
	// MarkerNeedsReview is the user saying they do not, and it schedules the
	// question the way a wrong answer does.
	MarkerNeedsReview Marker = "needs_review"
	// MarkerSkipped is the user declining to be judged. Nothing is judged, and
	// nothing about their memory of the question is recorded.
	MarkerSkipped Marker = "skipped"
)

// Valid reports whether the marker is one of the three.
func (m Marker) Valid() bool {
	switch m {
	case MarkerNone, MarkerMastered, MarkerNeedsReview, MarkerSkipped:
		return true
	default:
		return false
	}
}

// Outcome is one review, as the schedule sees it: what was decided about the
// answer, what the user said about the question, and how hard it felt.
type Outcome struct {
	// Correct is the judgement. It decides the branch only when the marker is
	// empty — a marker is the user's own statement about their learning, and it
	// outranks the judge for scheduling. What was judged is still recorded; this
	// only decides which way the curve moves.
	//
	// It is ignored when the marker is skipped, because nothing was judged.
	Correct bool
	Marker  Marker
	// Rating is the self-assessment, 0 to 5, or nil when the user gave none. It
	// tunes how easy the question is taken to be; it does not decide whether the
	// answer counted, which is what the branch is.
	Rating *float64
}

// State is the review state of one question: what the curve is computed from,
// and what one review writes back.
//
// The zero value is a question nobody has reviewed — no streak, no lapses, no
// count and no ease — which is exactly a row that does not exist yet.
type State struct {
	Streak      int
	Lapses      int
	ReviewCount int
	// Ease is the memory difficulty coefficient (SM-2's ease). A zero value is
	// read as EaseInitial: the column is nullable, so "no ease" and "an ease of
	// zero" cannot be told apart in storage, and only one of them means
	// anything.
	Ease float64
	// IsMistake says the question has been answered wrong at least once, and is
	// never cleared: it is a mark on the question, not a report on the last
	// answer. FirstWrongTime is when that first wrong answer happened.
	IsMistake      bool
	FirstWrongTime *time.Time
	// DueTime and LastReviewTime are outputs. The incoming values are not read:
	// the next time a question is due is computed from now and the curve, and
	// there is nothing in the stored pair that changes the answer.
	DueTime        time.Time
	LastReviewTime time.Time
}

// Next applies one review and returns the state to store.
//
// now is the caller's, not this function's: the whole curve is a decision about
// *when*, and a clock read inside would make every case here a case about the
// day it ran on.
func Next(state State, outcome Outcome, now time.Time) (State, error) {
	if !outcome.Marker.Valid() {
		return State{}, fmt.Errorf("%w: %q is not a review marker", ErrInvalidReview, outcome.Marker)
	}
	branch := branchOf(outcome)
	quality, err := qualityOf(branch, outcome)
	if err != nil {
		return State{}, err
	}
	// The time columns are DATETIME, which holds whole seconds. Truncating here
	// rather than at the write keeps what this function returns equal to what
	// storage will hold — otherwise a due time of 12:00:00.7 becomes 12:00:01
	// and the state the caller was handed never existed.
	now = now.Truncate(time.Second)

	next := state
	next.Ease = state.Ease
	if next.Ease <= 0 {
		next.Ease = EaseInitial
	}
	// Every branch records that the review path touched this question, the
	// branch that moves nothing else included: the column says when the row was
	// last looked at, and a skip did look at it.
	next.LastReviewTime = now

	switch branch {
	case branchSkipped:
		// The one branch that moves only the clock. Leaving the due time alone
		// would hand the question straight back, and counting the skip as a
		// review would put a question nobody answered into the memory curve.
		next.DueTime = now.Add(skipInterval)
		return next, nil

	case branchWrong:
		next.Streak = 0
		next.Lapses = state.Lapses + 1
		next.ReviewCount = state.ReviewCount + 1
		next.Ease = easeAfter(next.Ease, quality)
		// Written on the first wrong answer and never cleared, checked on each
		// of the two columns rather than on one of them: a row that has one of
		// them and not the other is repaired here rather than copied forward.
		if !state.IsMistake {
			next.IsMistake = true
		}
		if state.FirstWrongTime == nil {
			first := now
			next.FirstWrongTime = &first
		}
		next.DueTime = now.Add(mistakeInterval)
		return next, nil

	default: // branchCorrect
		next.Streak = state.Streak + 1
		next.ReviewCount = state.ReviewCount + 1
		next.Ease = easeAfter(next.Ease, quality)
		next.DueTime = now.Add(intervalFor(next.Streak, next.Ease))
		return next, nil
	}
}

// branch is which of the three ways a review can go.
type branch int

const (
	branchCorrect branch = iota
	branchWrong
	branchSkipped
)

// branchOf reads the marker and the judgement as one of the three branches.
//
// The marker wins where it speaks. A question the user marked as mastered is
// scheduled as mastered even if the judge read the answer as wrong, and the
// reverse: the two are answers to different questions — the judge read the
// text, the user knows what they remember — and the curve is about the second.
// What the judge decided still goes on the record, which is why the two are
// separate columns and separate packages.
func branchOf(outcome Outcome) branch {
	switch outcome.Marker {
	case MarkerSkipped:
		return branchSkipped
	case MarkerMastered:
		return branchCorrect
	case MarkerNeedsReview:
		return branchWrong
	default:
		if outcome.Correct {
			return branchCorrect
		}
		return branchWrong
	}
}

// qualityOf is the self-rating, or the one the branch implies.
//
// The implied one comes from the branch rather than from the judgement, and a
// case found the difference: a question the user marked mastered while the
// judge read the answer as wrong would otherwise be scheduled forward with a
// wrong answer's ease — the one place where the marker's authority over the
// curve has to be complete rather than partial.
func qualityOf(branch branch, outcome Outcome) (float64, error) {
	if outcome.Rating != nil {
		if math.IsNaN(*outcome.Rating) || *outcome.Rating < 0 || *outcome.Rating > qualityMaximum {
			return 0, fmt.Errorf("%w: the rating is %v, which is not 0 to %d",
				ErrInvalidReview, *outcome.Rating, qualityMaximum)
		}
		return *outcome.Rating, nil
	}
	if branch == branchCorrect {
		return qualityMaximum, nil
	}
	return qualityWrong, nil
}

// intervalFor is how many days a run of correct answers has earned, as SM-2
// shapes it: one day, then six, then six multiplied by the ease for each
// further answer in the run.
//
// The simplification is in the ease: SM-2 multiplies by the ease the question
// had at each step, and this multiplies by the ease it has now, at the end of
// the run. The two differ only when the ease moved during the run, and then by
// a few per cent — not enough to be worth carrying the ease of every past
// review into a column this table does not have.
// The returned duration is rounded to a whole second, and that is not tidiness:
// the interval is computed in days, which do not land on a whole number of
// nanoseconds, and a due time with a fraction of a second on it is not a time
// the column can hold. Rounding here is what lets Next promise that what it
// returns is what storage will keep.
func intervalFor(streak int, ease float64) time.Duration {
	if streak <= 1 {
		return firstInterval
	}
	days := float64(secondIntervalDays)
	for step := 2; step < streak; step++ {
		days *= ease
		if days >= maxIntervalDays {
			return time.Duration(maxIntervalDays) * 24 * time.Hour
		}
	}
	return time.Duration(days * float64(24*time.Hour)).Round(time.Second)
}

// easeAfter is SM-2's own update of the difficulty coefficient.
//
// It is worth reading as what it is, because the shape is not what "0 to 5"
// suggests: a 5 adds a tenth, a 4 changes nothing at all, and each step below
// that takes off more than the one above it — 0.14 for a 3, 0.32 for a 2, 0.54
// for a 1, and 0.8 for a 0. The middle of the scale is where the ease is
// expected to sit, which is why the neutral answer is a 4 and not a 3. A
// question rated hard therefore comes back much sooner and keeps coming back,
// which is the whole point of tracking difficulty separately from the streak.
func easeAfter(ease, quality float64) float64 {
	miss := qualityMaximum - quality
	updated := ease + easeQualityBonus - miss*(easeQualityPenalty+miss*easeQualitySlope)
	if updated < EaseMinimum {
		return EaseMinimum
	}
	return updated
}
