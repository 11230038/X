package practice

import (
	"testing"

	"backend/internal/data/testdb"
)

func TestASkipMovesTheClockAndNothingElse(t *testing.T) {
	harness := newHarness(t)
	question := storeQuestion(t, harness.resource, "nbe_1", choiceFor("什么是闭包？"))

	skipped := harness.submit(t, Review{RequestID: "rev_1", QuestionID: question.QuestionID,
		Marker: MarkerSkipped})

	if skipped.Judgment != nil {
		t.Errorf("a skip was judged: %+v", skipped.Judgment)
	}
	if skipped.State.ReviewCount != 0 || skipped.State.Streak != 0 || skipped.State.Lapses != 0 {
		t.Errorf("a skip counted as a review: %+v", skipped.State)
	}
	if skipped.State.Ease != EaseInitial {
		t.Errorf("the ease is %v, want the initial %v", skipped.State.Ease, EaseInitial)
	}
	if want := reviewsHappenAt.Add(skipInterval); !skipped.State.DueTime.Equal(want) {
		t.Errorf("the question comes back at %v, want %v", skipped.State.DueTime, want)
	}
	if skipped.Entry != (AnswerState{}) {
		t.Errorf("a skip reported an answer face: %+v", skipped.Entry)
	}

	state, ok := harness.stateOf(t, "nbe_1")
	if !ok {
		t.Fatal("a skip wrote no review state, so nothing holds the due time it moved")
	}
	if state.LastReviewTime == nil || !state.LastReviewTime.Equal(reviewsHappenAt) {
		t.Errorf("the skip is recorded at %v, want %v", state.LastReviewTime, reviewsHappenAt)
	}
	if state.ReviewCount == nil || *state.ReviewCount != 0 {
		t.Errorf("the stored review count is %v, want 0", state.ReviewCount)
	}

	face := harness.faceOf(t, question.QuestionID)
	if face.UserAnswer != nil || face.IsCorrect != nil || face.Result != nil ||
		face.Resolved != nil || face.AttemptCount != nil || face.UpdateTime != nil {
		t.Errorf("a skip wrote the answer face: %+v", face)
	}
}

func TestAMarkerSpeaksForTheAnswerFaceAndTheJudgeForTheRecord(t *testing.T) {
	harness := newHarness(t)
	question := storeQuestion(t, harness.resource, "nbe_1", choiceFor("什么是闭包？"))

	// Answered wrong and marked mastered: the marker schedules it as known, and
	// what was judged still goes on the record. The two are answers to different
	// questions, which is why they end up in different places.
	mastered := harness.submit(t, Review{RequestID: "rev_1", QuestionID: question.QuestionID,
		UserAnswer: "B", Marker: MarkerMastered})
	if mastered.Judgment == nil || mastered.Judgment.IsCorrect {
		t.Errorf("the wrong answer was judged %+v", mastered.Judgment)
	}
	if mastered.State.Streak != 1 || mastered.State.Lapses != 0 {
		t.Errorf("mastered scheduled as %+v, want the correct branch", mastered.State)
	}
	if mastered.Entry.Result != "mastered" || mastered.Entry.Resolved == nil || !*mastered.Entry.Resolved {
		t.Errorf("mastered reported %+v, want a mastered answer that is settled", mastered.Entry)
	}
	if mastered.Entry.IsCorrect == nil || *mastered.Entry.IsCorrect {
		t.Errorf("the record says the answer was %v, want the judgement's false", mastered.Entry.IsCorrect)
	}

	other := storeQuestion(t, harness.resource, "nbe_2", choiceFor("什么是作用域？"))
	needs := harness.submit(t, Review{RequestID: "rev_2", QuestionID: other.QuestionID,
		UserAnswer: "A", Marker: MarkerNeedsReview})
	if needs.Judgment == nil || !needs.Judgment.IsCorrect {
		t.Errorf("the right answer was judged %+v", needs.Judgment)
	}
	if needs.State.Streak != 0 || needs.State.Lapses != 1 {
		t.Errorf("needs_review scheduled as %+v, want the wrong branch", needs.State)
	}
	if needs.Entry.Result != "needs_review" || needs.Entry.Resolved == nil || *needs.Entry.Resolved {
		t.Errorf("needs_review reported %+v, want an answer that is not settled", needs.Entry)
	}
}

func TestAnAnswerWithNoMarkerLeavesTheMarksAlone(t *testing.T) {
	harness := newHarness(t)
	question := storeQuestion(t, harness.resource, "nbe_1", choiceFor("什么是闭包？"))
	testdb.Exec(t, harness.resource,
		"UPDATE notebook_entries SET resolved = TRUE, bookmarked = TRUE WHERE question_id = ?",
		question.QuestionID)

	answered := harness.submit(t, Review{RequestID: "rev_1", QuestionID: question.QuestionID,
		UserAnswer: "A"})

	if answered.Entry.Resolved == nil || !*answered.Entry.Resolved {
		t.Errorf("a plain answer reported resolved = %v, want the stored true", answered.Entry.Resolved)
	}
	if answered.Entry.Result != "correct" || answered.Entry.AttemptCount != 1 {
		t.Errorf("a plain answer reported %+v", answered.Entry)
	}
	face := harness.faceOf(t, question.QuestionID)
	if face.Resolved == nil || !*face.Resolved {
		t.Errorf("the resolved column was written back as %v", face.Resolved)
	}
	if face.Bookmarked == nil || !*face.Bookmarked {
		t.Errorf("the bookmarked column was written back as %v", face.Bookmarked)
	}
	if face.UpdateTime == nil || !face.UpdateTime.Equal(reviewsHappenAt) {
		t.Errorf("the answer face is stamped %v, want %v", face.UpdateTime, reviewsHappenAt)
	}

	// The mark is not this branch's to make, but it is the next one's: a
	// question marked as needing review is not settled.
	needs := harness.submit(t, Review{RequestID: "rev_2", QuestionID: question.QuestionID,
		UserAnswer: "A", Marker: MarkerNeedsReview, Version: &answered.State.Version})
	if needs.Entry.Resolved == nil || *needs.Entry.Resolved {
		t.Errorf("needs_review reported resolved = %v, want false", needs.Entry.Resolved)
	}
	if face := harness.faceOf(t, question.QuestionID); face.Resolved == nil || *face.Resolved {
		t.Errorf("the resolved column is %v, want false", face.Resolved)
	}
}
