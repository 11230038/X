package practice

// The write path against a real database. Everything here is about what the two
// tables are left holding, so the assertions read them with SQL of their own
// rather than through the mapping under test — a case that asked this package
// what it had written would be asking the thing on trial.
//
// Two claims are the reason this file is as long as it is, and they are the two
// the design says the whole version is about: the same submission recorded twice
// counts once, and two different submissions cannot both count. Neither is a
// claim about arithmetic — the curve is pinned in schedule_test.go — so both are
// claims about what a transaction, a primary key and a version guard do when
// they meet a database that is really there.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"backend/internal/data"
	"backend/internal/data/testdb"
	"backend/internal/notebook"
)

// reviewsHappenAt is the instant every review in this file happens at, spelled
// out rather than read from the clock: a due time is the answer to a review, and
// a case that has to derive it is a case that tests the clock.
var reviewsHappenAt = time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC)

// reviewedTables are cleared before each case, children first: the two practice
// tables point at the question bank and go when it goes.
var reviewedTables = []string{
	"practice_review_events", "practice_review_state", "notebook_entries", "turns", "sessions",
}

// harness is the service under test with the pieces a case needs to look at what
// it did: the database it wrote to, and the provider it must not have asked.
type harness struct {
	service    *Service
	repository *GORMRepository
	resource   *data.Resource
	provider   *countingProvider
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	resource := testdb.Open(t, "practice")
	testdb.Reset(t, resource, reviewedTables...)
	provider := &countingProvider{}
	service, err := NewService(NewGORMRepository(resource.GORM()), judgeFor(t, provider))
	if err != nil {
		t.Fatalf("new practice service: %v", err)
	}
	service.now = func() time.Time { return reviewsHappenAt }
	return &harness{
		service:    service,
		repository: NewGORMRepository(resource.GORM()),
		resource:   resource,
		provider:   provider,
	}
}

// restarted is a service that has never seen any submission: what a process
// that came back up is, as far as this path is concerned. Nothing it needs is
// held anywhere but the two tables it reads.
func (h *harness) restarted(t *testing.T) *Service {
	t.Helper()
	service, err := NewService(NewGORMRepository(h.resource.GORM()), judgeFor(t, &countingProvider{}))
	if err != nil {
		t.Fatalf("new practice service: %v", err)
	}
	service.now = func() time.Time { return reviewsHappenAt.Add(time.Hour) }
	return service
}

func (h *harness) submit(t *testing.T, review Review) ReviewResult {
	t.Helper()
	result, err := h.service.Review(context.Background(), review)
	if err != nil {
		t.Fatalf("review %s: %v", review.RequestID, err)
	}
	return result
}

func (h *harness) refuse(t *testing.T, review Review) error {
	t.Helper()
	result, err := h.service.Review(context.Background(), review)
	if err == nil {
		t.Fatalf("review %s was accepted: %+v", review.RequestID, result)
	}
	return err
}

// together submits one review many times at once, all of them starting together,
// and returns what each call was answered with.
func (h *harness) together(t *testing.T, reviews []Review) ([]ReviewResult, []error) {
	t.Helper()
	results := make([]ReviewResult, len(reviews))
	errs := make([]error, len(reviews))
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	for index, review := range reviews {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			results[index], errs[index] = h.service.Review(context.Background(), review)
		}()
	}
	start.Done()
	done.Wait()
	return results, errs
}

// storeQuestion puts a question in the bank, because that is where the review
// path expects one: it reads the row the bank writes, and a fixture that wrote
// its own row would be handing this package a shape it does not decide.
func storeQuestion(t *testing.T, resource *data.Resource, entryID string, question notebook.Question) notebook.Question {
	t.Helper()
	question.QuestionID = notebook.QuestionID(question.Text, question.Type)
	_, err := notebook.NewGORMRepository(resource.GORM()).UpsertQuestion(
		context.Background(), entryID, question, reviewsHappenAt.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("store question %q: %v", question.Text, err)
	}
	return question
}

func choiceFor(text string) notebook.Question {
	return notebook.Question{
		Text:          text,
		Type:          notebook.QuestionChoice,
		Options:       []notebook.Option{{Key: "A", Text: "甲"}, {Key: "B", Text: "乙"}},
		CorrectAnswer: "A",
		Explanation:   "因为甲是对的。",
	}
}

func fillFor(text string) notebook.Question {
	return notebook.Question{
		Text:          text,
		Type:          notebook.QuestionFillBlank,
		CorrectAnswer: "闭包",
		Explanation:   "闭包是能读取外部变量的函数。",
	}
}

// sameResponse reports whether two submissions were answered the same way,
// leaving out when each one arrived: whether a submission was replayed is the
// one thing a retry is told that the first submission was not.
func sameResponse(t *testing.T, got, want ReviewResult) {
	t.Helper()
	got.Replayed, want.Replayed = false, false
	if !sameJSON(got, want) {
		t.Errorf("the two answers differ:\n got %+v\nwant %+v", got, want)
	}
}

// storedState is the review state row as the database holds it, read without the
// mapping this package is being tested for. Every column is a pointer so that
// "written false" and "never written" stay two different answers.
type storedState struct {
	NotebookEntriesID *string    `gorm:"column:notebook_entries_id"`
	FirstWrongTime    *time.Time `gorm:"column:first_wrong_time"`
	DueTime           *time.Time `gorm:"column:due_time"`
	LastReviewTime    *time.Time `gorm:"column:last_review_time"`
	IsMistake         *bool      `gorm:"column:is_mistake"`
	Ease              *float64   `gorm:"column:ease"`
	Streak            *int32     `gorm:"column:streak"`
	ReviewCount       *int32     `gorm:"column:review_count"`
	Lapses            *int32     `gorm:"column:lapses"`
	Version           *int64     `gorm:"column:version"`
}

func (h *harness) stateOf(t *testing.T, entryID string) (storedState, bool) {
	t.Helper()
	var row storedState
	// `case` is backquoted because it is a MySQL reserved word.
	err := h.resource.GORM().Raw("SELECT notebook_entries_id, first_wrong_time, due_time, "+
		"last_review_time, is_mistake, `case` AS ease, streak, review_count, lapses, version "+
		"FROM practice_review_state WHERE notebook_entries_id = ?", entryID).Scan(&row).Error
	if err != nil {
		t.Fatalf("read the review state of %s: %v", entryID, err)
	}
	return row, row.NotebookEntriesID != nil
}

// storedFace is the answer face of a question row as the database holds it.
type storedFace struct {
	UserAnswer   *string    `gorm:"column:user_answer"`
	IsCorrect    *bool      `gorm:"column:is_correct"`
	Result       *string    `gorm:"column:result"`
	Resolved     *bool      `gorm:"column:resolved"`
	AttemptCount *int32     `gorm:"column:attempt_count"`
	Bookmarked   *bool      `gorm:"column:bookmarked"`
	UpdateTime   *time.Time `gorm:"column:update_time"`
}

func (h *harness) faceOf(t *testing.T, questionID string) storedFace {
	t.Helper()
	var row storedFace
	err := h.resource.GORM().Raw("SELECT user_answer, is_correct, result, resolved, "+
		"attempt_count, bookmarked, update_time FROM notebook_entries WHERE question_id = ?",
		questionID).Scan(&row).Error
	if err != nil {
		t.Fatalf("read the answer face of %s: %v", questionID, err)
	}
	return row
}

func (h *harness) count(t *testing.T, table string, where string, args ...any) int {
	t.Helper()
	var count int64
	query := "SELECT COUNT(*) FROM `" + table + "`"
	if where != "" {
		query += " WHERE " + where
	}
	if err := h.resource.GORM().Raw(query, args...).Scan(&count).Error; err != nil {
		t.Fatalf("count the rows of %s: %v", table, err)
	}
	return int(count)
}

// nanRating is a rating that is not a number: no comparison accepts it, so a
// client that sent one would otherwise be rated by whatever the arithmetic
// happened to do with it.
func nanRating() float64 { return math.NaN() }

// unanswerable is a submission that cannot be acted on, and the question it
// names does not exist: a refusal that came after reading the question would
// report the missing question instead.
func unanswerable(questionID string) []struct {
	name   string
	review Review
} {
	rating := 7.0
	nan := nanRating()
	behind := int64(-1)
	return []struct {
		name   string
		review Review
	}{
		{"no request id", Review{QuestionID: questionID, UserAnswer: "闭包"}},
		{"a request id of nothing but space", Review{RequestID: " ", QuestionID: questionID}},
		{"no question id", Review{RequestID: "rev_1", UserAnswer: "闭包"}},
		{"a marker that is not one", Review{RequestID: "rev_1", QuestionID: questionID,
			UserAnswer: "闭包", Marker: "later"}},
		{"a rating off the scale", Review{RequestID: "rev_1", QuestionID: questionID,
			UserAnswer: "闭包", Rating: &rating}},
		{"a rating that is not a number", Review{RequestID: "rev_1", QuestionID: questionID,
			UserAnswer: "闭包", Rating: &nan}},
		{"a version behind zero", Review{RequestID: "rev_1", QuestionID: questionID,
			UserAnswer: "闭包", Version: &behind}},
	}
}

func TestAQuestionThatIsNotInTheBankHasNoReview(t *testing.T) {
	harness := newHarness(t)
	err := harness.refuse(t, Review{RequestID: "rev_1", QuestionID: "qn_nowhere", UserAnswer: "A"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("review of a question that is not there = %v, want ErrNotFound", err)
	}
}

func TestASubmissionThatCannotBeActedOnIsRefusedBeforeAnythingHappens(t *testing.T) {
	harness := newHarness(t)
	for _, test := range unanswerable("qn_nowhere") {
		t.Run(test.name, func(t *testing.T) {
			if err := harness.refuse(t, test.review); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("review = %v, want ErrInvalidRequest", err)
			}
		})
	}
	if harness.provider.calls != 0 {
		t.Errorf("a refused submission reached a model %d times", harness.provider.calls)
	}
	for _, table := range []string{"practice_review_events", "practice_review_state"} {
		if count := harness.count(t, table, ""); count != 0 {
			t.Errorf("a refused submission wrote %d rows to %s", count, table)
		}
	}
}

func TestOneSubmissionArrivingThreeTimesIsRecordedOnce(t *testing.T) {
	harness := newHarness(t)
	question := storeQuestion(t, harness.resource, "nbe_1", choiceFor("什么是闭包？"))
	review := Review{RequestID: "rev_1", QuestionID: question.QuestionID, UserAnswer: "A"}

	first := harness.submit(t, review)
	for attempt := 2; attempt <= 3; attempt++ {
		again, err := harness.service.Review(context.Background(), review)
		if err != nil {
			t.Fatalf("submission %d: %v", attempt, err)
		}
		if !again.Replayed {
			t.Errorf("submission %d was recorded again", attempt)
		}
		sameResponse(t, again, first)
	}

	if count := harness.count(t, "practice_review_events", ""); count != 1 {
		t.Errorf("three submissions left %d events", count)
	}
	state, ok := harness.stateOf(t, "nbe_1")
	if !ok {
		t.Fatal("no review state was written")
	}
	if state.ReviewCount == nil || *state.ReviewCount != 1 {
		t.Errorf("the review count is %v, want 1", state.ReviewCount)
	}
	if face := harness.faceOf(t, question.QuestionID); face.AttemptCount == nil || *face.AttemptCount != 1 {
		t.Errorf("the answer count is %v, want 1", face.AttemptCount)
	}
}

func TestTheRecordAnswersARetryAfterEverythingIsForgotten(t *testing.T) {
	harness := newHarness(t)
	question := storeQuestion(t, harness.resource, "nbe_1", choiceFor("什么是闭包？"))
	review := Review{RequestID: "rev_1", QuestionID: question.QuestionID, UserAnswer: "A"}

	first := harness.submit(t, review)
	again, err := harness.restarted(t).Review(context.Background(), review)
	if err != nil {
		t.Fatalf("the retry after a restart: %v", err)
	}
	if !again.Replayed {
		t.Error("the retry was recorded as a new review")
	}
	sameResponse(t, again, first)

	state, _ := harness.stateOf(t, "nbe_1")
	if state.ReviewCount == nil || *state.ReviewCount != 1 {
		t.Errorf("the review count is %v, want 1", state.ReviewCount)
	}
}

func TestTheAnswerIsWhatTheEventStores(t *testing.T) {
	harness := newHarness(t)
	question := storeQuestion(t, harness.resource, "nbe_1", choiceFor("什么是闭包？"))
	rating := 3.0
	first := harness.submit(t, Review{RequestID: "rev_1", QuestionID: question.QuestionID,
		UserAnswer: "A", Rating: &rating})

	recorded, err := harness.repository.RecordedReview(context.Background(), "rev_1")
	if err != nil {
		t.Fatalf("read the recorded review: %v", err)
	}
	if recorded.NotebookEntriesID != "nbe_1" || recorded.UserAnswer != "A" {
		t.Errorf("the event holds %s / %q", recorded.NotebookEntriesID, recorded.UserAnswer)
	}
	if recorded.Rating == nil || *recorded.Rating != rating {
		t.Errorf("the event holds rating %v, want %v", recorded.Rating, rating)
	}
	if !recorded.ReviewTime.Equal(reviewsHappenAt) {
		t.Errorf("the event happened at %v, want %v", recorded.ReviewTime, reviewsHappenAt)
	}
	// The column is decoded rather than compared byte for byte: it is a JSON
	// column, so what comes back is the value, not the bytes that went in.
	var fromRecord ReviewResult
	if err := json.Unmarshal(recorded.Outcome, &fromRecord); err != nil {
		t.Fatalf("the outcome column does not hold a response: %v (%s)", err, recorded.Outcome)
	}
	sameResponse(t, fromRecord, first)
}

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
