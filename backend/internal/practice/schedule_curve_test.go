package practice

// The curve as arithmetic: the ease update, the intervals it produces and the
// ratings that move it. These are called directly rather than through Next,
// because what they answer is what the shape of the schedule is — a case that
// asked Next for the same thing would be a case about Next.

import (
	"errors"
	"math"
	"testing"
	"time"
)

// days is how a case writes an interval: the curve is written in days, and a
// day of nanoseconds is what intervalFor itself returns.
func days(count float64) time.Duration {
	return time.Duration(count * float64(24*time.Hour)).Round(time.Second)
}

func TestTheRatingTunesTheEaseAndNotTheBranch(t *testing.T) {
	three := 3.0
	got, err := Next(studied(), Outcome{Correct: true, Rating: &three}, reviewNow)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !closeEnough(got.Ease, 2.36) {
		t.Errorf("ease after rating 3 = %v, want 2.36", got.Ease)
	}
	if got.Streak != 4 {
		t.Errorf("streak = %d, want 4", got.Streak)
	}
	if want := reviewNow.Add(days(6 * 2.36 * 2.36)); !sameTimes(got.DueTime, want) {
		t.Errorf("due = %v, want %v", got.DueTime, want)
	}

	// A rating does not turn a wrong answer into a right one: it says how easy
	// the question felt, not whether the answer counted.
	five := 5.0
	got, err = Next(studied(), Outcome{Correct: false, Rating: &five}, reviewNow)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if got.Streak != 0 || got.Lapses != 2 {
		t.Errorf("a wrong answer rated 5 left streak %d and lapses %d, want 0 and 2", got.Streak, got.Lapses)
	}
	if !closeEnough(got.Ease, 2.6) {
		t.Errorf("ease after a wrong answer rated 5 = %v, want 2.6", got.Ease)
	}
}

func TestTheRatingScaleIsEnforced(t *testing.T) {
	for _, rating := range []float64{0, 1, 5, 2.5} {
		value := rating
		if _, err := Next(State{Ease: EaseInitial}, Outcome{Correct: true, Rating: &value}, reviewNow); err != nil {
			t.Errorf("Next with rating %v: %v", rating, err)
		}
	}
	for _, rating := range []float64{-0.5, 5.1, 100, math.NaN(), math.Inf(1)} {
		value := rating
		if _, err := Next(State{Ease: EaseInitial}, Outcome{Correct: true, Rating: &value}, reviewNow); !errors.Is(err, ErrInvalidReview) {
			t.Errorf("Next with rating %v: err = %v, want ErrInvalidReview", rating, err)
		}
	}
}

func TestTheEaseHasAFloor(t *testing.T) {
	if got := easeAfter(EaseInitial, 2); !closeEnough(got, 2.18) {
		t.Errorf("easeAfter(2.5, 2) = %v, want 2.18", got)
	}
	if got := easeAfter(EaseInitial, 5); !closeEnough(got, 2.6) {
		t.Errorf("easeAfter(2.5, 5) = %v, want 2.6", got)
	}
	// The steps below the middle cost more than the ones above it: 0.14 for a 3,
	// 0.54 for a 1, eight tenths for a 0.
	if got := easeAfter(EaseInitial, 3); !closeEnough(got, 2.36) {
		t.Errorf("easeAfter(2.5, 3) = %v, want 2.36", got)
	}
	if got := easeAfter(EaseInitial, 1); !closeEnough(got, 1.96) {
		t.Errorf("easeAfter(2.5, 1) = %v, want 1.96", got)
	}
	if got := easeAfter(EaseInitial, 0); !closeEnough(got, 1.7) {
		t.Errorf("easeAfter(2.5, 0) = %v, want 1.7", got)
	}

	// The floor holds however far down the ratings push it, and it is the floor
	// the same answer several times over reaches.
	ease := EaseInitial
	for range 10 {
		ease = easeAfter(ease, 0)
	}
	if !closeEnough(ease, EaseMinimum) {
		t.Errorf("the ease after ten 0-ratings = %v, want %v", ease, EaseMinimum)
	}
}

func TestTheIntervalGrowsWithTheRun(t *testing.T) {
	const day = 24 * time.Hour
	cases := []struct {
		streak int
		want   time.Duration
	}{
		{streak: 0, want: day},
		{streak: 1, want: day},
		{streak: 2, want: 6 * day},
		{streak: 3, want: 15 * day},
		{streak: 4, want: 37*day + 12*time.Hour}, // six days times the ease twice
	}
	for _, one := range cases {
		if got := intervalFor(one.streak, EaseInitial); got != one.want {
			t.Errorf("intervalFor(%d, 2.5) = %v, want %v", one.streak, got, one.want)
		}
	}

	// Strictly increasing, so that answering again never schedules the question
	// nearer than it already was.
	previous := time.Duration(0)
	for streak := 1; streak <= 40; streak++ {
		got := intervalFor(streak, EaseInitial)
		if got < previous {
			t.Fatalf("intervalFor(%d) = %v, which is less than %v", streak, got, previous)
		}
		previous = got
	}
}

func TestTheIntervalStopsAtAYear(t *testing.T) {
	cap := time.Duration(maxIntervalDays) * 24 * time.Hour
	for _, streak := range []int{7, 20, 200} {
		got := intervalFor(streak, EaseInitial)
		if got != cap {
			t.Errorf("intervalFor(%d, 2.5) = %v, want %v", streak, got, cap)
		}
	}
	// A lower ease still reaches the ceiling, and a lower ceiling is not the
	// point: the cap is about how far ahead a question can be put, not about
	// which ease got it there.
	if got := intervalFor(40, EaseMinimum); got != cap {
		t.Errorf("intervalFor(40, %v) = %v, want %v", EaseMinimum, got, cap)
	}
}

// The interval is computed from the run and the ease as the run leaves it, not
// from the ease at every step of it. This is the simplification the file's
// comment names, and it is pinned here so that changing it is a decision rather
// than a drift.
func TestTheIntervalUsesTheEaseTheRunEndsWith(t *testing.T) {
	// Three correct answers from nothing, each one given when it came due.
	first := reviewed(t, State{}, Outcome{Correct: true}, reviewNow)
	second := reviewed(t, first, Outcome{Correct: true}, first.DueTime)
	third := reviewed(t, second, Outcome{Correct: true}, second.DueTime)

	if !closeEnough(third.Ease, 2.8) {
		t.Fatalf("ease after three correct answers = %v, want 2.8", third.Ease)
	}
	// 6 days times the ease the run ended with (2.8), which is not 6 days times
	// the 2.5 the run started from — the two are a day and a half apart, so this
	// case tells them apart rather than describing either one.
	if got, other := third.DueTime, second.DueTime.Add(days(6*2.5)); sameTimes(got, other) {
		t.Errorf("due = %v, which is what the ease at the start of the run would give", got)
	}
	want := second.DueTime.Add(days(6 * 2.8))
	if !sameTimes(third.DueTime, want) {
		t.Errorf("due = %v, want %v (6 days at the ease the run ended with)", third.DueTime, want)
	}
}

func TestAnAnsweredCorrectlyQuestionIsNotScheduledEarlierThanItWas(t *testing.T) {
	state := State{}
	at := reviewNow
	previous := time.Time{}
	for i := range 12 {
		next, err := Next(state, Outcome{Correct: true}, at)
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if next.DueTime.Before(previous) {
			t.Fatalf("review %d scheduled it at %v, before the %v it was due", i, next.DueTime, previous)
		}
		if !next.DueTime.After(at) {
			t.Fatalf("review %d scheduled it at %v, which is not after the %v it was answered", i, next.DueTime, at)
		}
		state, at, previous = next, next.DueTime, next.DueTime
	}
	if state.Streak != 12 {
		t.Errorf("streak after twelve correct answers = %d, want 12", state.Streak)
	}
}

// A rating is what the user says the question felt like, and a low one can pull
// the schedule back even though the answer was right. That is the point of
// having the rating, so it is written down rather than treated as a bug.
func TestAHardRatingCanPullTheScheduleBack(t *testing.T) {
	// A long run at a question the user then rates 0: the ease drops by eight
	// tenths, and six days times that ease three times over is less than the six
	// times the ease it had been running at four times over.
	scheduled := studied()
	scheduled.Streak = 4
	scheduled.DueTime = reviewNow.Add(days(6 * 2.5 * 2.5))
	scheduled.LastReviewTime = reviewNow

	zero := 0.0
	got, err := Next(scheduled, Outcome{Correct: true, Rating: &zero}, reviewNow)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !got.DueTime.Before(scheduled.DueTime) {
		t.Errorf("due = %v, want it pulled before %v", got.DueTime, scheduled.DueTime)
	}
	if want := reviewNow.Add(days(6 * 1.7 * 1.7 * 1.7)); !sameTimes(got.DueTime, want) {
		t.Errorf("due = %v, want %v", got.DueTime, want)
	}
}
