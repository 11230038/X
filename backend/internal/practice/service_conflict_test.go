package practice

import (
	"errors"
	"fmt"
	"testing"
)

func TestASpentRequestIDWillNotCarryADifferentSubmission(t *testing.T) {
	harness := newHarness(t)
	question := storeQuestion(t, harness.resource, "nbe_1", choiceFor("什么是闭包？"))
	other := storeQuestion(t, harness.resource, "nbe_2", choiceFor("什么是作用域？"))
	recorded := Review{RequestID: "rev_1", QuestionID: question.QuestionID, UserAnswer: "A"}
	first := harness.submit(t, recorded)

	rating := 2.0
	for _, test := range []struct {
		name   string
		review Review
	}{
		{"another answer", Review{RequestID: "rev_1", QuestionID: question.QuestionID, UserAnswer: "B"}},
		{"another marker", Review{RequestID: "rev_1", QuestionID: question.QuestionID,
			UserAnswer: "A", Marker: MarkerMastered}},
		{"another rating", Review{RequestID: "rev_1", QuestionID: question.QuestionID,
			UserAnswer: "A", Rating: &rating}},
		{"another question", Review{RequestID: "rev_1", QuestionID: other.QuestionID, UserAnswer: "A"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := harness.refuse(t, test.review)
			if !errors.Is(err, ErrConflictingRequestID) {
				t.Fatalf("review = %v, want ErrConflictingRequestID", err)
			}
		})
	}

	// The version is not part of what a submission is, so a retry carrying a
	// different one is the same submission and is answered as one. The guard is
	// never consulted: there is nothing left for it to guard, the review it
	// would have guarded having already been recorded.
	behind := int64(0)
	retry := recorded
	retry.Version = &behind
	sameResponse(t, harness.submit(t, retry), first)

	// The recorded submission is what stands: a conflict changes nothing.
	if count := harness.count(t, "practice_review_events", ""); count != 1 {
		t.Errorf("the conflicts left %d events, want the one that was recorded", count)
	}
	state, _ := harness.stateOf(t, "nbe_1")
	if state.ReviewCount == nil || *state.ReviewCount != 1 || state.Streak == nil || *state.Streak != 1 {
		t.Errorf("the state is %+v, want what the recorded submission left", state)
	}
	again := harness.submit(t, recorded)
	sameResponse(t, again, first)
}

func TestALostGuardTakesTheRecordWithIt(t *testing.T) {
	harness := newHarness(t)
	question := storeQuestion(t, harness.resource, "nbe_1", choiceFor("什么是闭包？"))
	first := harness.submit(t, Review{RequestID: "rev_1", QuestionID: question.QuestionID, UserAnswer: "A"})

	// The version the client holds is one the row never had. The submission is
	// otherwise perfectly good, so everything up to the guard succeeds — which is
	// what makes this the case that asks whether the record goes back with it.
	behind := first.State.Version + 7
	err := harness.refuse(t, Review{RequestID: "rev_2", QuestionID: question.QuestionID,
		UserAnswer: "A", Version: &behind})

	var stale *StaleReview
	if !errors.As(err, &stale) {
		t.Fatalf("the refusal is %v, want a conflict that carries the state", err)
	}
	if !errors.Is(err, ErrStaleReview) {
		t.Errorf("the conflict does not carry ErrStaleReview: %v", err)
	}
	if !sameJSON(stale.State, first.State) {
		t.Errorf("the conflict reports %+v, want the state the client is behind: %+v",
			stale.State, first.State)
	}
	if count := harness.count(t, "practice_review_events", "request_id = ?", "rev_2"); count != 0 {
		t.Errorf("the refused submission left %d records behind, want none", count)
	}
	state, _ := harness.stateOf(t, "nbe_1")
	if state.ReviewCount == nil || *state.ReviewCount != 1 || state.Version == nil || *state.Version != 0 {
		t.Errorf("the state moved to %+v, want the first submission's", state)
	}
}

func TestEachSubmissionThatReadsTheVersionItWritesCountsOnce(t *testing.T) {
	harness := newHarness(t)
	question := storeQuestion(t, harness.resource, "nbe_1", choiceFor("什么是闭包？"))

	first := harness.submit(t, Review{RequestID: "rev_1", QuestionID: question.QuestionID, UserAnswer: "A"})
	second := harness.submit(t, Review{RequestID: "rev_2", QuestionID: question.QuestionID,
		UserAnswer: "A", Version: &first.State.Version})

	if second.State.Version != first.State.Version+1 {
		t.Errorf("the version went to %d, want %d", second.State.Version, first.State.Version+1)
	}
	if second.State.ReviewCount != 2 || second.Entry.AttemptCount != 2 {
		t.Errorf("the second submission left %+v, want two of each", second)
	}
	// The first version is now stale, so a third submission holding it is behind
	// even though it was right a moment ago.
	if err := harness.refuse(t, Review{RequestID: "rev_3", QuestionID: question.QuestionID,
		UserAnswer: "A", Version: &first.State.Version}); !errors.Is(err, ErrStaleReview) {
		t.Errorf("the superseded version = %v, want ErrStaleReview", err)
	}
}

func TestOneSubmissionArrivingManyTimesAtOnceIsRecordedOnce(t *testing.T) {
	harness := newHarness(t)
	question := storeQuestion(t, harness.resource, "nbe_1", choiceFor("什么是闭包？"))
	review := Review{RequestID: "rev_1", QuestionID: question.QuestionID, UserAnswer: "A"}

	results, errs := harness.together(t, []Review{review, review, review, review, review, review})
	recorded := 0
	for index, err := range errs {
		if err != nil {
			t.Fatalf("call %d: %v", index, err)
		}
		if !results[index].Replayed {
			recorded++
		}
	}
	if recorded != 1 {
		t.Errorf("%d of the six calls recorded a review, want exactly one", recorded)
	}
	if count := harness.count(t, "practice_review_events", ""); count != 1 {
		t.Errorf("the six calls left %d events, want one", count)
	}
	state, _ := harness.stateOf(t, "nbe_1")
	if state.ReviewCount == nil || *state.ReviewCount != 1 {
		t.Errorf("the review count is %v, want 1", state.ReviewCount)
	}
}

func TestSubmissionsArrivingAtOnceEachCountOnceOrNotAtAll(t *testing.T) {
	harness := newHarness(t)
	question := storeQuestion(t, harness.resource, "nbe_1", choiceFor("什么是闭包？"))

	reviews := make([]Review, 0, 8)
	for index := 0; index < 8; index++ {
		reviews = append(reviews, Review{
			RequestID:  fmt.Sprintf("rev_%d", index),
			QuestionID: question.QuestionID,
			UserAnswer: "A",
		})
	}
	_, errs := harness.together(t, reviews)
	succeeded := 0
	for index, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrStaleReview):
			// A guard that lost, which is what eight submissions reading the same
			// version with nothing to tell them apart must come to.
		default:
			t.Fatalf("submission %d: %v", index, err)
		}
	}
	if succeeded == 0 {
		t.Fatal("no submission got through")
	}
	// How many got through is a race and is not asserted; that whatever got
	// through was counted exactly once is. The number is reported because a run
	// where one submission wins every time is a run that never exercised the
	// refusals, and a reader should be able to see that without instrumenting
	// anything.
	t.Logf("%d of the %d submissions got through", succeeded, len(reviews))

	// Nothing was lost and nothing was counted twice: the three agree, and the
	// refused ones left no record behind.
	state, _ := harness.stateOf(t, "nbe_1")
	if state.ReviewCount == nil || int(*state.ReviewCount) != succeeded {
		t.Errorf("the review count is %v, want the %d that succeeded", state.ReviewCount, succeeded)
	}
	if count := harness.count(t, "practice_review_events", ""); count != succeeded {
		t.Errorf("%d events were written, want the %d that succeeded", count, succeeded)
	}
	// The row starts at zero (§2.6's insert), so the version counts the advances
	// rather than the writes.
	if state.Version == nil || int(*state.Version) != succeeded-1 {
		t.Errorf("the version is %v, want %d", state.Version, succeeded-1)
	}
	if face := harness.faceOf(t, question.QuestionID); face.AttemptCount == nil ||
		int(*face.AttemptCount) != succeeded {
		t.Errorf("the answer count is %v, want %d", face.AttemptCount, succeeded)
	}
}

func TestSubmissionsOnDifferentQuestionsDoNotWaitForEachOther(t *testing.T) {
	harness := newHarness(t)
	const questions = 5
	reviews := make([]Review, 0, questions)
	for index := 0; index < questions; index++ {
		question := storeQuestion(t, harness.resource, fmt.Sprintf("nbe_%d", index),
			choiceFor(fmt.Sprintf("问题 %d 是什么？", index)))
		reviews = append(reviews, Review{
			RequestID:  fmt.Sprintf("rev_%d", index),
			QuestionID: question.QuestionID,
			UserAnswer: "A",
		})
	}

	results, errs := harness.together(t, reviews)
	for index, err := range errs {
		if err != nil {
			t.Fatalf("submission %d: %v", index, err)
		}
		// Each question is its own row and its own guard, so nothing here has to
		// be refused — and each one's first review leaves the row at zero, which
		// is where a row begins (§2.6).
		if results[index].Replayed {
			t.Errorf("submission %d was answered as a retry", index)
		}
		if results[index].State.ReviewCount != 1 || results[index].State.Version != 0 {
			t.Errorf("submission %d left %+v", index, results[index].State)
		}
	}
	if count := harness.count(t, "practice_review_events", ""); count != questions {
		t.Errorf("%d events were written, want %d", count, questions)
	}
	if count := harness.count(t, "practice_review_state", ""); count != questions {
		t.Errorf("%d state rows were written, want %d", count, questions)
	}
	if harness.provider.calls != 0 {
		t.Errorf("%d of the questions reached a model, and they were all choices", harness.provider.calls)
	}
}
