package practice

// The queue against a real database, because every claim in it is a claim about
// what one WHERE and one ORDER BY do with rows that are really there: which
// questions come back, in what order, and which are left out. A case here that
// went through a fake would be testing the fake's reading of `IS NOT TRUE` and
// of a three-key sort, and the engine is the thing that has an opinion about
// those.
//
// The questions are put in the bank through the bank's own upsert — this package
// reads that row, and a fixture that wrote its own would be handing the query a
// shape it did not decide.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"backend/internal/data"
	"backend/internal/notebook"
)

// storeQuestionAt puts a question in the bank as of an instant. The instant is
// the point of the helper: one of the three orders this query sorts by is when
// a question was written, so a case about that order has to say when.
func storeQuestionAt(t *testing.T, resource *data.Resource, entryID string, question notebook.Question, at time.Time) notebook.Question {
	t.Helper()
	question.QuestionID = notebook.QuestionID(question.Text, question.Type)
	_, err := notebook.NewGORMRepository(resource.GORM()).UpsertQuestion(
		context.Background(), entryID, question, at)
	if err != nil {
		t.Fatalf("store question %q: %v", question.Text, err)
	}
	return question
}

// moveAt points the harness at another instant. Due-ness is a comparison against
// a clock, so a case about it that ran at whatever time it was would be a case
// about the day it ran on.
func (h *harness) moveAt(instant time.Time) {
	h.service.now = func() time.Time { return instant }
}

func (h *harness) queue(t *testing.T, size int) []QueueItem {
	t.Helper()
	items, err := h.service.Queue(context.Background(), size)
	if err != nil {
		t.Fatalf("read the queue: %v", err)
	}
	return items
}

// textsOf is the queue's questions and nothing else, which is what an order
// assertion is about.
func textsOf(items []QueueItem) []string {
	texts := make([]string, 0, len(items))
	for _, item := range items {
		texts = append(texts, item.Question.Text)
	}
	return texts
}

func sameTexts(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("the queue has %d questions, wanted %d: %v", len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("the queue is %v, wanted %v", got, want)
		}
	}
}

func TestANewQuestionIsWhatTheQueueIsFor(t *testing.T) {
	h := newHarness(t)
	question := storeQuestion(t, h.resource, "nbe_1", fillFor("闭包是什么"))

	items := h.queue(t, 0)
	if len(items) != 1 {
		t.Fatalf("the queue has %d questions, wanted 1", len(items))
	}
	item := items[0]
	if item.Question.QuestionID != question.QuestionID {
		t.Errorf("the queue holds %s, wanted %s", item.Question.QuestionID, question.QuestionID)
	}
	if item.Reason != ReasonNew {
		t.Errorf("a question nobody has answered is in the queue as %q", item.Reason)
	}
	// No state row means no version at all, which is a different thing from a
	// version of zero: the submission that answers this question sends none, and
	// the row is created by it.
	if item.State != nil {
		t.Errorf("a question with no review has a state: %+v", item.State)
	}
	if item.IsMistake {
		t.Error("a question nobody has answered is marked as a mistake")
	}
	if item.Answer.UserAnswer != "" {
		t.Errorf("a question nobody has answered carries the answer %q", item.Answer.UserAnswer)
	}
	if item.Answer.IsCorrect != nil {
		t.Errorf("a question nobody has answered carries a judgement: %v", *item.Answer.IsCorrect)
	}
}

func TestTheQuestionThatIsDueComesBeforeTheNewOne(t *testing.T) {
	h := newHarness(t)
	answered := storeQuestion(t, h.resource, "nbe_1", fillFor("闭包是什么"))
	storeQuestion(t, h.resource, "nbe_2", choiceFor("下面哪个是闭包"))

	h.submit(t, Review{RequestID: "rev_1", QuestionID: answered.QuestionID,
		UserAnswer: "闭包", Marker: MarkerNone})

	// A day and an hour later the answered question is due again, and the other
	// one has still never been asked.
	h.moveAt(reviewsHappenAt.Add(25 * time.Hour))
	sameTexts(t, textsOf(h.queue(t, 0)), answered.Text, "下面哪个是闭包")

	item := h.queue(t, 0)[0]
	if item.Reason != ReasonDue {
		t.Errorf("an answered question came back as %q", item.Reason)
	}
	if item.State == nil {
		t.Fatalf("the queue hands back no state for an answered question: %+v", item)
	}
	// One review, so the version is zero: the row was created at zero and this
	// is the row's first generation. The number a client sends back is the one
	// it was handed, and it is the row's number rather than a count of reviews.
	if item.State.Version != 0 {
		t.Errorf("one review left the version at %d", item.State.Version)
	}
	if item.State.Streak != 1 || item.State.ReviewCount != 1 {
		t.Errorf("the queue reports the state as %+v", item.State)
	}
	if item.Answer.AttemptCount != 1 || item.Answer.Result != "correct" {
		t.Errorf("the queue reports the answer face as %+v", item.Answer)
	}
}

func TestAQuestionThatIsNotYetDueIsNotInTheQueue(t *testing.T) {
	h := newHarness(t)
	answered := storeQuestion(t, h.resource, "nbe_1", fillFor("闭包是什么"))
	h.submit(t, Review{RequestID: "rev_1", QuestionID: answered.QuestionID,
		UserAnswer: "闭包", Marker: MarkerNone})

	// The answer scheduled it for tomorrow, and it is still today.
	if items := h.queue(t, 0); len(items) != 0 {
		t.Fatalf("a question scheduled for tomorrow is already in the queue: %v", textsOf(items))
	}
	h.moveAt(reviewsHappenAt.Add(25 * time.Hour))
	sameTexts(t, textsOf(h.queue(t, 0)), answered.Text)
}

func TestAMistakeJumpsTheQueue(t *testing.T) {
	h := newHarness(t)
	// One question was answered correctly two days ago and has been due since
	// yesterday; the other was answered wrong just now and is due now.
	older := storeQuestion(t, h.resource, "nbe_1", fillFor("闭包是什么"))
	mistake := storeQuestion(t, h.resource, "nbe_2", choiceFor("下面哪个是闭包"))

	h.moveAt(reviewsHappenAt.Add(-48 * time.Hour))
	h.submit(t, Review{RequestID: "rev_old", QuestionID: older.QuestionID,
		UserAnswer: "闭包", Marker: MarkerNone})
	h.moveAt(reviewsHappenAt)
	h.submit(t, Review{RequestID: "rev_mistake", QuestionID: mistake.QuestionID,
		UserAnswer: "B", Marker: MarkerNone})

	// The mistake is due now and the other one has been due for a day, so by the
	// due time alone the older one would come first. It does not: the question
	// that was just got wrong is the one worth seeing again.
	items := h.queue(t, 0)
	sameTexts(t, textsOf(items), mistake.Text, older.Text)
	if !items[0].IsMistake {
		t.Error("the question that came back first is not reported as a mistake")
	}
	if items[1].IsMistake {
		t.Error("the question answered correctly is reported as a mistake")
	}
}

func TestTheQuestionsThatAreDueComeBackInTheOrderTheyWereDue(t *testing.T) {
	h := newHarness(t)
	oldest := storeQuestion(t, h.resource, "nbe_1", fillFor("闭包是什么"))
	middle := storeQuestion(t, h.resource, "nbe_2", choiceFor("下面哪个是闭包"))
	newest := storeQuestion(t, h.resource, "nbe_3", fillFor("什么是作用域"))

	// Three correct answers hours apart: each is due a day after it was given,
	// so the order they are due in is the order they were answered in. All three
	// are answered right, so nothing here is lifted by a mistake mark — the due
	// time is the only thing left to sort by.
	h.moveAt(reviewsHappenAt.Add(-100 * time.Hour))
	h.submit(t, Review{RequestID: "rev_1", QuestionID: oldest.QuestionID,
		UserAnswer: "闭包", Marker: MarkerNone})
	h.moveAt(reviewsHappenAt.Add(-50 * time.Hour))
	h.submit(t, Review{RequestID: "rev_2", QuestionID: middle.QuestionID,
		UserAnswer: "A", Marker: MarkerNone})
	h.moveAt(reviewsHappenAt.Add(-26 * time.Hour))
	h.submit(t, Review{RequestID: "rev_3", QuestionID: newest.QuestionID,
		UserAnswer: "闭包", Marker: MarkerNone})

	h.moveAt(reviewsHappenAt)
	sameTexts(t, textsOf(h.queue(t, 0)), oldest.Text, middle.Text, newest.Text)
}

func TestTheNewQuestionsComeBackNewestFirst(t *testing.T) {
	h := newHarness(t)
	first := storeQuestionAt(t, h.resource, "nbe_1", fillFor("闭包是什么"), reviewsHappenAt.Add(-72*time.Hour))
	second := storeQuestionAt(t, h.resource, "nbe_2", choiceFor("下面哪个是闭包"), reviewsHappenAt.Add(-48*time.Hour))
	third := storeQuestionAt(t, h.resource, "nbe_3", fillFor("什么是作用域"), reviewsHappenAt.Add(-24*time.Hour))

	// A new question has no due time to be sorted by, so the only thing that can
	// order them is when they were written — and the interesting end is the new
	// one, because the question just generated is the one the user is looking at.
	sameTexts(t, textsOf(h.queue(t, 0)), third.Text, second.Text, first.Text)
}

func TestAQuestionTheUserAlreadyAnsweredIsNotANewOne(t *testing.T) {
	h := newHarness(t)
	question := storeQuestion(t, h.resource, "nbe_1", fillFor("闭包是什么"))

	// An answer with no review state behind it is what a question answered
	// before this table existed looks like — and what a lost write looks like.
	// It is not new: asking it as if it had never been answered would hide the
	// damage rather than show it.
	err := h.resource.GORM().Exec(
		"UPDATE notebook_entries SET user_answer = ? WHERE question_id = ?",
		"闭包", question.QuestionID).Error
	if err != nil {
		t.Fatalf("answer the question by hand: %v", err)
	}

	items := h.queue(t, 0)
	if len(items) != 0 {
		t.Fatalf("a question that has been answered came back as new: %v", textsOf(items))
	}
}

func TestAMasteredQuestionLeavesTheQueue(t *testing.T) {
	h := newHarness(t)
	overdue := storeQuestion(t, h.resource, "nbe_1", fillFor("闭包是什么"))
	never := storeQuestion(t, h.resource, "nbe_2", choiceFor("下面哪个是闭包"))

	h.moveAt(reviewsHappenAt.Add(-25 * time.Hour))
	h.submit(t, Review{RequestID: "rev_1", QuestionID: overdue.QuestionID,
		UserAnswer: "闭包", Marker: MarkerNone})
	h.moveAt(reviewsHappenAt)

	bank := notebook.NewGORMRepository(h.resource.GORM())
	done := true
	for _, question := range []notebook.Question{overdue, never} {
		if err := bank.Mark(context.Background(), question.QuestionID, notebook.Change{Resolved: &done}); err != nil {
			t.Fatalf("mark %s as resolved: %v", question.Text, err)
		}
	}

	// One of them is overdue and the other has never been asked. Neither belongs
	// in a queue for a user who has said they are done with them.
	items := h.queue(t, 0)
	if len(items) != 0 {
		t.Fatalf("a resolved question is in the queue: %v", textsOf(items))
	}
}

func TestAQuestionMarkedForReviewIsStillInTheQueue(t *testing.T) {
	h := newHarness(t)
	question := storeQuestion(t, h.resource, "nbe_1", fillFor("闭包是什么"))
	h.submit(t, Review{RequestID: "rev_1", QuestionID: question.QuestionID,
		UserAnswer: "闭包", Marker: MarkerNeedsReview})

	// `resolved` is three-state, and the queue reads it as "not marked done"
	// rather than as "marked not done". A question the user has just said they
	// need to see again is the last thing that should leave the queue.
	items := h.queue(t, 0)
	sameTexts(t, textsOf(items), question.Text)
	if items[0].Reason != ReasonDue {
		t.Errorf("a question marked for review is in the queue as %q", items[0].Reason)
	}
}

func TestASkippedQuestionStepsAsideAndComesBack(t *testing.T) {
	h := newHarness(t)
	question := storeQuestion(t, h.resource, "nbe_1", fillFor("闭包是什么"))
	h.submit(t, Review{RequestID: "rev_1", QuestionID: question.QuestionID, Marker: MarkerSkipped})

	// Skipping has to move something, or the next question handed out is the one
	// just skipped and skipping reads as doing nothing.
	if items := h.queue(t, 0); len(items) != 0 {
		t.Fatalf("a question that was just skipped is still in the queue: %v", textsOf(items))
	}
	h.moveAt(reviewsHappenAt.Add(31 * time.Minute))
	items := h.queue(t, 0)
	sameTexts(t, textsOf(items), question.Text)
	// And it comes back as a question nobody answered: the skip moved the clock
	// and nothing else, so the count of what the user has actually reviewed is
	// still zero.
	if items[0].State == nil {
		t.Fatalf("a skipped question has no state: %+v", items[0])
	}
	if items[0].State.ReviewCount != 0 || items[0].State.Streak != 0 {
		t.Errorf("the skip moved more than the clock: %+v", items[0].State)
	}
}

func TestTheQueueIsCutToTheSizeThatWasAsked(t *testing.T) {
	h := newHarness(t)
	for index := 0; index < 5; index++ {
		storeQuestionAt(t, h.resource, fmt.Sprintf("nbe_%d", index),
			fillFor(fmt.Sprintf("第 %d 个问题", index+1)), reviewsHappenAt)
	}

	if items := h.queue(t, 2); len(items) != 2 {
		t.Errorf("a queue of 5 cut to 2 has %d questions", len(items))
	}
	// A size nobody set is the default, not an empty queue: the caller that
	// passes nothing wants the default queue, and returning nothing would make
	// "did not say" and "said zero" the same request.
	if items := h.queue(t, 0); len(items) != 5 {
		t.Errorf("a queue with no size given has %d of 5 questions", len(items))
	}
}

func TestAnEmptyBankHasAnEmptyQueue(t *testing.T) {
	h := newHarness(t)
	items := h.queue(t, 0)
	// Empty and not absent: the listing rule this codebase follows is that a
	// filter which matched nothing is an empty list rather than no answer.
	if items == nil {
		t.Fatal("an empty queue came back as nothing rather than as an empty list")
	}
	if len(items) != 0 {
		t.Fatalf("an empty bank has a queue of %d", len(items))
	}
}
