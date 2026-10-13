package practice

import (
	"errors"
	"math"
	"testing"
	"time"
)

// reviewNow is the clock every case is run against. The curve is entirely about
// when, so a case that read the clock would say something different tomorrow;
// and it carries no sub-second part, since a case about whole seconds should
// not be the case that invents one.
var reviewNow = time.Date(2026, 10, 13, 9, 30, 15, 0, time.UTC)

// studied is a question with a history: a run of three correct answers, one
// lapse behind it, and the mistake mark already on it.
func studied() State {
	first := reviewNow.Add(-4 * 24 * time.Hour)
	return State{
		Streak:         3,
		Lapses:         1,
		ReviewCount:    4,
		Ease:           2.5,
		IsMistake:      true,
		FirstWrongTime: &first,
		DueTime:        reviewNow.Add(-time.Hour),
		LastReviewTime: reviewNow.Add(-2 * 24 * time.Hour),
	}
}

func TestAnUnreviewedQuestionStartsTheCurve(t *testing.T) {
	got, err := Next(State{}, Outcome{Correct: true}, reviewNow)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	checkState(t, got, State{
		Streak:         1,
		ReviewCount:    1,
		Ease:           2.6,
		DueTime:        reviewNow.Add(firstInterval),
		LastReviewTime: reviewNow,
	})
}

func TestACorrectAnswerMovesTheCurveForward(t *testing.T) {
	got, err := Next(studied(), Outcome{Correct: true}, reviewNow)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	want := studied()
	want.Streak = 4
	want.ReviewCount = 5
	want.Ease = 2.6
	// A run of four: six days, times the ease once for each answer past the
	// second one.
	want.DueTime = reviewNow.Add(days(6 * 2.6 * 2.6))
	want.LastReviewTime = reviewNow
	checkState(t, got, want)
}

func TestAWrongAnswerBringsTheQuestionBackAndCountsTheLapse(t *testing.T) {
	got, err := Next(studied(), Outcome{Correct: false}, reviewNow)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	want := studied()
	want.Streak = 0
	want.Lapses = 2
	want.ReviewCount = 5
	want.Ease = 2.18
	want.DueTime = reviewNow
	want.LastReviewTime = reviewNow
	checkState(t, got, want)
}

// The two markers share the state transition with the plain correct and wrong
// answers, and this is the pair of cases that says so. What the markers add is
// outside this function: a mastered question is also marked resolved and a
// needs_review one unmarked, which the write path does because it is about the
// question rather than about the schedule.
func TestAMasteredQuestionIsScheduledAsACorrectAnswer(t *testing.T) {
	answered, err := Next(studied(), Outcome{Correct: true}, reviewNow)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	marked, err := Next(studied(), Outcome{Marker: MarkerMastered}, reviewNow)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	checkState(t, marked, answered)
}

func TestAQuestionMarkedForReviewIsScheduledAsAWrongAnswer(t *testing.T) {
	answered, err := Next(studied(), Outcome{Correct: false}, reviewNow)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	marked, err := Next(studied(), Outcome{Marker: MarkerNeedsReview}, reviewNow)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	checkState(t, marked, answered)
}

// The marker is the user's own statement about their learning, and it outranks
// the judge for scheduling. What was judged is still recorded; it just does not
// decide which way the curve moves.
func TestTheMarkerOutranksTheJudgement(t *testing.T) {
	// The judge read the answer as wrong and the user says they know it.
	mastered, err := Next(State{Ease: EaseInitial}, Outcome{Correct: false, Marker: MarkerMastered}, reviewNow)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if mastered.Streak != 1 {
		t.Errorf("streak after mastered = %d, want 1", mastered.Streak)
	}
	if mastered.IsMistake {
		t.Error("a mastered question was marked as a mistake")
	}
	if mastered.DueTime != reviewNow.Add(firstInterval) {
		t.Errorf("mastered due = %v, want %v", mastered.DueTime, reviewNow.Add(firstInterval))
	}

	// The judge read the answer as right and the user says they do not.
	needsReview, err := Next(studied(), Outcome{Correct: true, Marker: MarkerNeedsReview}, reviewNow)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if needsReview.Streak != 0 {
		t.Errorf("streak after needs_review = %d, want 0", needsReview.Streak)
	}
	if needsReview.DueTime != reviewNow {
		t.Errorf("needs_review due = %v, want %v", needsReview.DueTime, reviewNow)
	}
}

func TestASkipMovesOnlyTheClock(t *testing.T) {
	// Both judgements, because neither is looked at: nothing was judged.
	for _, correct := range []bool{true, false} {
		got, err := Next(studied(), Outcome{Correct: correct, Marker: MarkerSkipped}, reviewNow)
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		want := studied()
		want.DueTime = reviewNow.Add(skipInterval)
		want.LastReviewTime = reviewNow
		checkState(t, got, want)
	}

	// Spelled out, because "only the clock" is the rule the whole branch exists
	// for and a reader should not have to diff two states to believe it.
	got, err := Next(studied(), Outcome{Marker: MarkerSkipped}, reviewNow)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	before := studied()
	switch {
	case got.Streak != before.Streak:
		t.Errorf("a skip changed the streak: %d, want %d", got.Streak, before.Streak)
	case got.Lapses != before.Lapses:
		t.Errorf("a skip changed the lapse count: %d, want %d", got.Lapses, before.Lapses)
	case got.ReviewCount != before.ReviewCount:
		t.Errorf("a skip counted as a review: %d, want %d", got.ReviewCount, before.ReviewCount)
	case got.Ease != before.Ease:
		t.Errorf("a skip changed the ease: %v, want %v", got.Ease, before.Ease)
	}
}

// The design's acceptance for stage 5 reads the two against each other: a wrong
// answer is at the front of the queue, and a skipped one is not shown at once.
func TestAWrongAnswerComesBackAtOnceAndASkipDoesNot(t *testing.T) {
	// Due in a month, answered early and wrong: the schedule pulls it forward to
	// now rather than leaving it where the last run put it.
	scheduled := studied()
	scheduled.DueTime = reviewNow.Add(30 * 24 * time.Hour)
	scheduled.LastReviewTime = reviewNow

	wrong, err := Next(scheduled, Outcome{Correct: false}, reviewNow)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !wrong.DueTime.Equal(reviewNow) {
		t.Errorf("a wrong answer is due at %v, want now (%v)", wrong.DueTime, reviewNow)
	}

	skipped, err := Next(scheduled, Outcome{Marker: MarkerSkipped}, reviewNow)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !skipped.DueTime.After(reviewNow) {
		t.Errorf("a skipped answer is due at %v, which is not after now (%v)", skipped.DueTime, reviewNow)
	}
}

// The mistake mark is a mark on the question, not a report on the last answer:
// it is written once and answering correctly later does not take it off.
func TestTheFirstWrongAnswerIsRecordedOnceAndNeverCleared(t *testing.T) {
	first, err := Next(State{Ease: EaseInitial}, Outcome{Correct: false}, reviewNow)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !first.IsMistake {
		t.Error("a first wrong answer did not mark the question")
	}
	if first.FirstWrongTime == nil || !first.FirstWrongTime.Equal(reviewNow) {
		t.Fatalf("first wrong time = %v, want %v", first.FirstWrongTime, reviewNow)
	}

	// A correct answer later: the mark stays and the time stays with it.
	later := reviewNow.Add(7 * 24 * time.Hour)
	correct, err := Next(first, Outcome{Correct: true}, later)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !correct.IsMistake {
		t.Error("answering correctly cleared the mistake mark")
	}
	if !correct.FirstWrongTime.Equal(reviewNow) {
		t.Errorf("first wrong time = %v, want it left at %v", correct.FirstWrongTime, reviewNow)
	}

	// And a second wrong answer does not move it either.
	wrongAgain, err := Next(correct, Outcome{Correct: false}, later.Add(time.Hour))
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !wrongAgain.FirstWrongTime.Equal(reviewNow) {
		t.Errorf("first wrong time = %v, want it left at %v", wrongAgain.FirstWrongTime, reviewNow)
	}
}

// A row can carry one of the two columns and not the other — it is written in
// two statements by anyone doing it by hand, and a half-written row must not be
// copied forward as a half-written row.
func TestAHalfWrittenMistakeIsRepairedRatherThanCopied(t *testing.T) {
	got, err := Next(State{IsMistake: true, Ease: EaseInitial}, Outcome{Correct: false}, reviewNow)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if got.FirstWrongTime == nil || !got.FirstWrongTime.Equal(reviewNow) {
		t.Errorf("first wrong time = %v, want %v", got.FirstWrongTime, reviewNow)
	}

	first := reviewNow.Add(-time.Hour)
	got, err = Next(State{FirstWrongTime: &first, Ease: EaseInitial}, Outcome{Correct: false}, reviewNow)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !got.IsMistake {
		t.Error("a row with a first wrong time but no mark stayed unmarked")
	}
	if !got.FirstWrongTime.Equal(first) {
		t.Errorf("first wrong time = %v, want it left at %v", got.FirstWrongTime, first)
	}
}

func TestAMarkerThatIsNotOneIsRefused(t *testing.T) {
	for _, marker := range []Marker{"later", "mastered ", "Mastered", "resolved!"} {
		if _, err := Next(studied(), Outcome{Marker: marker}, reviewNow); !errors.Is(err, ErrInvalidReview) {
			t.Errorf("Next with marker %q: err = %v, want ErrInvalidReview", marker, err)
		}
	}

	// The other two marks in the design's table are property changes rather than
	// reviews, and this is where a client that sends them here is turned away.
	for _, marker := range []Marker{"bookmarked", "resolved"} {
		if _, err := Next(studied(), Outcome{Marker: marker}, reviewNow); !errors.Is(err, ErrInvalidReview) {
			t.Errorf("Next with marker %q: err = %v, want ErrInvalidReview", marker, err)
		}
	}

	// And the refusal is a refusal: nothing half-computed comes back with it.
	got, err := Next(studied(), Outcome{Marker: "later"}, reviewNow)
	if !errors.Is(err, ErrInvalidReview) {
		t.Fatalf("err = %v, want ErrInvalidReview", err)
	}
	checkState(t, got, State{})
}

func TestTheStoredDueTimeIsNotAnInput(t *testing.T) {
	never := State{Ease: EaseInitial}
	overdue := never
	overdue.DueTime = reviewNow.Add(-400 * 24 * time.Hour)
	overdue.LastReviewTime = reviewNow.Add(-401 * 24 * time.Hour)
	future := never
	future.DueTime = reviewNow.Add(400 * 24 * time.Hour)
	future.LastReviewTime = reviewNow

	from := func(state State) State {
		t.Helper()
		got, err := Next(state, Outcome{Correct: true}, reviewNow)
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		return got
	}
	checkState(t, from(overdue), from(never))
	checkState(t, from(future), from(never))
}

// The time columns hold whole seconds, so the schedule returns whole seconds:
// what it hands back has to be the state the write path will store, not a
// slightly different one that storage has already rounded.
func TestTheTimesAreWholeSeconds(t *testing.T) {
	// A clock with a fraction on it, which is what a real one has.
	now := reviewNow.Add(700*time.Millisecond + time.Microsecond)
	for _, outcome := range []Outcome{
		{Correct: true},
		{Correct: false},
		{Marker: MarkerSkipped},
		{Marker: MarkerMastered},
	} {
		got, err := Next(studied(), outcome, now)
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if got.DueTime.Nanosecond() != 0 {
			t.Errorf("due time %v has a fraction of a second on it", got.DueTime)
		}
		if !got.LastReviewTime.Equal(reviewNow) {
			t.Errorf("last review time = %v, want %v", got.LastReviewTime, reviewNow)
		}
	}
}

func reviewed(t *testing.T, state State, outcome Outcome, at time.Time) State {
	t.Helper()
	got, err := Next(state, outcome, at)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	return got
}

// closeEnough compares the ease. The arithmetic is a handful of floating point
// operations, so the last bit or two is not something a case should depend on.
func closeEnough(got, want float64) bool {
	return math.Abs(got-want) < 1e-9
}

// sameTimes compares two times to the second, which is the resolution the
// columns have and therefore the resolution these cases work at.
func sameTimes(got, want time.Time) bool {
	return got.Round(time.Second).Equal(want.Round(time.Second))
}

// checkState compares a whole state, field by field, so that a case says which
// field of the curve moved rather than only that something did.
func checkState(t *testing.T, got, want State) {
	t.Helper()
	if got.Streak != want.Streak {
		t.Errorf("streak = %d, want %d", got.Streak, want.Streak)
	}
	if got.Lapses != want.Lapses {
		t.Errorf("lapses = %d, want %d", got.Lapses, want.Lapses)
	}
	if got.ReviewCount != want.ReviewCount {
		t.Errorf("review count = %d, want %d", got.ReviewCount, want.ReviewCount)
	}
	if !closeEnough(got.Ease, want.Ease) {
		t.Errorf("ease = %v, want %v", got.Ease, want.Ease)
	}
	if got.IsMistake != want.IsMistake {
		t.Errorf("is mistake = %v, want %v", got.IsMistake, want.IsMistake)
	}
	switch {
	case (got.FirstWrongTime == nil) != (want.FirstWrongTime == nil):
		t.Errorf("first wrong time = %v, want %v", got.FirstWrongTime, want.FirstWrongTime)
	case got.FirstWrongTime != nil && !sameTimes(*got.FirstWrongTime, *want.FirstWrongTime):
		t.Errorf("first wrong time = %v, want %v", got.FirstWrongTime, want.FirstWrongTime)
	}
	if !sameTimes(got.DueTime, want.DueTime) {
		t.Errorf("due time = %v, want %v", got.DueTime, want.DueTime)
	}
	if !sameTimes(got.LastReviewTime, want.LastReviewTime) {
		t.Errorf("last review time = %v, want %v", got.LastReviewTime, want.LastReviewTime)
	}
}
