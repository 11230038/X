package mastery

// The gate and the review curve, pinned case by case.
//
// These are pure arithmetic over a slice of evidence — no clock, no storage, no
// turn — and §7.1 asks for them to be pinned exactly, for the reason the file
// they live in gives: a rule about what a learner should do next has to be more
// explicable than a model's reading of one answer, and the way to keep it
// explicable is to write down what it answers for every shape of history that
// matters.
//
// What is deliberately *not* here: anything that would go through a database.
// The level is stored in the tree's cache and the interval is stored in nothing
// at all, so both are decided entirely by these two functions, and a case that
// needed a row would be a case about the adapter.

import (
	"testing"
	"time"

	"backend/internal/practice"
)

// theGate is the deployment's thresholds, named here rather than read from one:
// three right answers in a row to promote, two observations before the gate says
// anything at all.
var theGate = GateOptions{Consecutive: 3, MinEvidence: 2}

func days(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }

// run builds a history out of its results, in order.
func history(results ...Result) []Evidence {
	evidences := make([]Evidence, 0, len(results))
	for index, result := range results {
		evidences = append(evidences, Evidence{
			ID:        string(rune('a' + index)),
			Result:    result,
			CreatedAt: atNine.Add(time.Duration(index) * time.Hour),
		})
	}
	return evidences
}

func TestTheGateReadsTheTrailingRunAndNotTheTotal(t *testing.T) {
	cases := []struct {
		name     string
		results  []Result
		expected Level
	}{
		// Below the floor nothing is decided, and "unknown" is not a fourth kind
		// of weak: a knowledge point with one observation has barely been met, so
		// the gate declines to have an opinion about it. The wrong answer is in
		// here on purpose — a single mistake must not read as weak.
		{"nothing observed", nil, LevelUnknown},
		{"one right answer", []Result{ResultCorrect}, LevelUnknown},
		{"one wrong answer", []Result{ResultIncorrect}, LevelUnknown},
		{"one partial answer", []Result{ResultPartial}, LevelUnknown},

		{"two right answers", []Result{ResultCorrect, ResultCorrect}, LevelLearning},
		{"a right then a wrong", []Result{ResultCorrect, ResultIncorrect}, LevelWeak},
		{"a partial is not a failure", []Result{ResultCorrect, ResultPartial}, LevelLearning},

		// Promotion is the trailing run reaching the threshold, so the same
		// evidence in a different order is a different answer: four right then
		// one wrong is not a learner who knows it.
		{"three in a row", []Result{ResultCorrect, ResultCorrect, ResultCorrect}, LevelMastered},
		{"four in a row", []Result{ResultCorrect, ResultCorrect, ResultCorrect, ResultCorrect}, LevelMastered},
		{"three then a wrong", []Result{
			ResultCorrect, ResultCorrect, ResultCorrect, ResultIncorrect}, LevelWeak},

		// A partial ends the run without being a failure: it demotes a mastered
		// point back to learning rather than to weak, which is the whole of the
		// difference between the two.
		{"three then a partial", []Result{
			ResultCorrect, ResultCorrect, ResultCorrect, ResultPartial}, LevelLearning},
		{"a wrong then three right", []Result{
			ResultIncorrect, ResultCorrect, ResultCorrect, ResultCorrect}, LevelMastered},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := GateDecide(history(testCase.results...), theGate)
			if got != testCase.expected {
				t.Errorf("%v reads as %q, want %q", testCase.results, got, testCase.expected)
			}
		})
	}
}

func TestTheGateWillNotBeMovedByTheThresholdsAlone(t *testing.T) {
	// The thresholds are deployment settings and the arithmetic is not, so the
	// same evidence has to answer differently under different settings — which is
	// what makes the two numbers configuration rather than constants.
	evidences := history(ResultCorrect, ResultCorrect, ResultCorrect)
	if got := GateDecide(evidences, theGate); got != LevelMastered {
		t.Errorf("three in a row under %+v reads as %q", theGate, got)
	}
	if got := GateDecide(evidences, GateOptions{Consecutive: 4, MinEvidence: 2}); got != LevelLearning {
		t.Errorf("three in a row under a threshold of four reads as %q", got)
	}
	// And a floor above the evidence leaves it unknown however good the answers
	// were: the point of the floor is that a short history cannot be read.
	if got := GateDecide(evidences, GateOptions{Consecutive: 1, MinEvidence: 5}); got != LevelUnknown {
		t.Errorf("three in a row under a floor of five reads as %q", got)
	}

	// The settings themselves are refused when they cannot mean anything.
	if err := (GateOptions{Consecutive: 0, MinEvidence: 1}).Validate(); err == nil {
		t.Error("a threshold of zero consecutive answers was accepted")
	}
	if err := (GateOptions{Consecutive: 1, MinEvidence: 0}).Validate(); err == nil {
		t.Error("a floor of zero observations was accepted")
	}
	if err := theGate.Validate(); err != nil {
		t.Errorf("the thresholds these cases use are not usable: %v", err)
	}
}

func TestTheReviewCurveWalksItsTableAndStopsAtTheEnd(t *testing.T) {
	cases := []struct {
		name     string
		results  []Result
		expected time.Duration
	}{
		// Nothing observed is nothing to schedule — not "due now". An unstarted
		// knowledge point reported as overdue would file every new point in the
		// insight's overdue list on the day it was created.
		{"nothing observed", nil, 0},

		// The curve is read by the length of the trailing run, so it is worth
		// walking the whole table rather than the first two entries.
		{"one right", []Result{ResultCorrect}, days(1)},
		{"two right", []Result{ResultCorrect, ResultCorrect}, days(3)},
		{"three right", []Result{ResultCorrect, ResultCorrect, ResultCorrect}, days(7)},
		{"four right", []Result{ResultCorrect, ResultCorrect, ResultCorrect, ResultCorrect}, days(16)},
		{"five right", []Result{ResultCorrect, ResultCorrect, ResultCorrect, ResultCorrect,
			ResultCorrect}, days(35)},
		{"six right", []Result{ResultCorrect, ResultCorrect, ResultCorrect, ResultCorrect,
			ResultCorrect, ResultCorrect}, days(75)},
		{"seven right", []Result{ResultCorrect, ResultCorrect, ResultCorrect, ResultCorrect,
			ResultCorrect, ResultCorrect, ResultCorrect}, days(160)},
		// The last entry of the table, and the one that says the table is read to
		// its end rather than to its end minus one.
		{"eight right", []Result{ResultCorrect, ResultCorrect, ResultCorrect, ResultCorrect,
			ResultCorrect, ResultCorrect, ResultCorrect, ResultCorrect}, days(340)},
		// Past the end the curve holds at its last entry rather than growing: the
		// table is the whole of it, and a run longer than the table is the same
		// answer as the table's last.
		{"twelve right", []Result{ResultCorrect, ResultCorrect, ResultCorrect, ResultCorrect,
			ResultCorrect, ResultCorrect, ResultCorrect, ResultCorrect,
			ResultCorrect, ResultCorrect, ResultCorrect, ResultCorrect}, days(340)},

		// A wrong answer is the observation that says the last interval was too
		// generous, and the answer is to come back tomorrow rather than to split
		// the difference.
		{"one wrong", []Result{ResultIncorrect}, days(1)},
		{"three right then a wrong", []Result{ResultCorrect, ResultCorrect, ResultCorrect,
			ResultIncorrect}, days(1)},

		// A partial is scheduled at half of what the run it ended had earned, which
		// puts it between the two: closer than the point would have been had the
		// answer been right, and further out than a wrong answer, which comes back
		// tomorrow. With no run behind it there is nothing to halve, so a lone
		// partial is the first interval like everything else.
		{"one partial", []Result{ResultPartial}, days(1)},
		{"a right then a partial", []Result{ResultCorrect, ResultPartial}, days(1)},
		{"three right then a partial", []Result{ResultCorrect, ResultCorrect, ResultCorrect,
			ResultPartial}, days(3)},
		{"four right then a partial", []Result{ResultCorrect, ResultCorrect, ResultCorrect,
			ResultCorrect, ResultPartial}, days(8)},
		// And a right answer after a partial starts the run over, because a partial
		// ended it: this one is a run of one, not a run of four.
		{"three right, a partial, then a right", []Result{ResultCorrect, ResultCorrect,
			ResultCorrect, ResultPartial, ResultCorrect}, days(1)},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := ReviewInterval(history(testCase.results...)); got != testCase.expected {
				t.Errorf("%v is scheduled %v out, want %v", testCase.results, got, testCase.expected)
			}
		})
	}
}

func TestTheReviewCurveIsTheQuestionCurvesTableAndNotItsArithmetic(t *testing.T) {
	// §7.1 asks for this one by name: if the two curves always agreed, one of
	// them would be redundant. They agree at the first interval — a day is a day
	// — and part company at the second, which is the shape the file's comment
	// claims.
	//
	// The question curve is run through its own entry point rather than by
	// reading `secondIntervalDays`, so that the case is about the two answers and
	// not about two constants that happen to look different.
	knowledgePoint := ReviewInterval(history(ResultCorrect, ResultCorrect))
	if knowledgePoint != days(3) {
		t.Fatalf("the knowledge point curve's second interval is %v, want 3 days", knowledgePoint)
	}

	question, err := practice.Next(practice.State{},
		practice.Outcome{Correct: true}, atNine)
	if err != nil {
		t.Fatalf("the first review of a question: %v", err)
	}
	if first := question.DueTime.Sub(atNine); first != days(1) {
		t.Errorf("the question curve's first interval is %v, want the day both curves agree on", first)
	}
	question, err = practice.Next(question, practice.Outcome{Correct: true}, atTen)
	if err != nil {
		t.Fatalf("the second review of a question: %v", err)
	}
	if second := question.DueTime.Sub(atTen); second == knowledgePoint {
		t.Errorf("both curves put the second review %v out, so one of them is redundant", second)
	} else if second != days(6) {
		t.Errorf("the question curve's second interval is %v, want its own six days", second)
	}
}
