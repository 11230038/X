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
	"math"
	"sync"
	"testing"
	"time"

	"backend/internal/authz"
	"backend/internal/data"
	"backend/internal/data/testdb"
	"backend/internal/notebook"
)

// The account these tests act as, and the one that holds every question the
// fixtures seed. A number rather than a name because owner_id holds a number, and
// the point of the constant is that the seeded rows and the caller agree about it:
// the review paths narrow the bank to this account, so a fixture that seeded rows
// owned by nobody would leave every case reading an empty bank.
const testOwnerID int64 = 7

func testCaller() authz.Caller {
	return authz.Caller{UserID: testOwnerID, Role: authz.RoleUser}
}

// otherCaller is a second account, for the one thing a single-account fixture
// cannot show: that another account's questions are not in this account's bank.
func otherCaller() authz.Caller {
	return authz.Caller{UserID: testOwnerID + 1, Role: authz.RoleUser}
}

// reviewsHappenAt is the instant every review in this file happens at, spelled
// out rather than read from the clock: a due time is the answer to a review, and
// a case that has to derive it is a case that tests the clock.
var reviewsHappenAt = time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC)

// reviewedTables are cleared before each case, children first: the two practice
// tables point at the question bank and go when it goes, and every owned row
// points at an account, so users goes last.
var reviewedTables = []string{
	"practice_review_events", "practice_review_state", "notebook_entries", "turns", "sessions",
	"users",
}

// seedAccounts writes the accounts the fixtures attribute questions to.
//
// Migration 00014 made owner_id a foreign key into users, so a case that stored
// a question owned by testOwnerID and left the account unwritten would be
// refused by the database before its subject was reached.
func seedAccounts(t *testing.T, resource *data.Resource) {
	t.Helper()
	testdb.Account(t, resource, testOwnerID, "bank_owner")
	testdb.Account(t, resource, testOwnerID+1, "bank_other")
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
	seedAccounts(t, resource)
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
	result, err := h.service.Review(context.Background(), review, testCaller())
	if err != nil {
		t.Fatalf("review %s: %v", review.RequestID, err)
	}
	return result
}

func (h *harness) refuse(t *testing.T, review Review) error {
	t.Helper()
	result, err := h.service.Review(context.Background(), review, testCaller())
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
			results[index], errs[index] = h.service.Review(context.Background(), review, testCaller())
		}()
	}
	start.Done()
	done.Wait()
	return results, errs
}

// storeQuestion puts a question in the bank, because that is where the review
// path expects one: it reads the row the bank writes, and a fixture that wrote
// its own row would be handing this package a shape it does not decide.
//
// It writes the question a day before the reviews in this file happen, so that
// the question is the older thing. storeQuestionAt is the same write with the
// instant chosen, for the cases that are about when a question was written.
func storeQuestion(t *testing.T, resource *data.Resource, entryID string, question notebook.Question) notebook.Question {
	t.Helper()
	return storeQuestionAt(t, resource, entryID, question, reviewsHappenAt.Add(-24*time.Hour))
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
		again, err := harness.service.Review(context.Background(), review, testCaller())
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
	again, err := harness.restarted(t).Review(context.Background(), review, testCaller())
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

	recorded, err := harness.repository.RecordedReview(context.Background(), "rev_1", testCaller())
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

// "I do not know" is a declaration rather than an answer, and it is recorded as
// one: the question is marked wrong and scheduled like a wrong answer, with no
// judging behind it. The model is what must not be asked — a subjective question
// would otherwise spend a call to learn what the user just said, and the button
// would stop working whenever the provider was down.
func TestSayingIDoNotKnowIsRecordedAsWrongWithoutJudging(t *testing.T) {
	harness := newHarness(t)
	question := storeQuestion(t, harness.resource, "nbe_1", shortAnswerQuestion())

	result := harness.submit(t, Review{RequestID: "rev_1", QuestionID: question.QuestionID,
		Marker: MarkerNeedsReview})

	if harness.provider.calls != 0 {
		t.Fatalf("the model was asked %d times about an answer that was not given", harness.provider.calls)
	}
	if result.Judgment == nil {
		t.Fatal("the submission was answered without a judgement, which is what a skip is")
	}
	if result.Judgment.Kind != KindSelfReport || result.Judgment.IsCorrect {
		t.Errorf("the judgement is %q / correct = %v, want a self report of wrong",
			result.Judgment.Kind, result.Judgment.IsCorrect)
	}
	if result.Entry.IsCorrect == nil || *result.Entry.IsCorrect {
		t.Errorf("the answer face is correct = %v, want a recorded wrong answer", result.Entry.IsCorrect)
	}
	if result.Entry.Result != string(MarkerNeedsReview) {
		t.Errorf("the answer face says %q, want the marker that was sent", result.Entry.Result)
	}
	if result.Entry.AttemptCount != 1 {
		t.Errorf("the attempt count is %d, want this submission counted once", result.Entry.AttemptCount)
	}
	if !result.State.IsMistake || result.State.Lapses != 1 || result.State.ReviewCount != 1 {
		t.Errorf("the state is mistake=%v lapses=%d reviews=%d, want a wrong answer's state",
			result.State.IsMistake, result.State.Lapses, result.State.ReviewCount)
	}

	face := harness.faceOf(t, question.QuestionID)
	if face.Result == nil || *face.Result != string(MarkerNeedsReview) {
		t.Errorf("the stored face says %v, want the marker", face.Result)
	}
	if face.IsCorrect == nil || *face.IsCorrect {
		t.Errorf("the stored face is correct = %v, want wrong", face.IsCorrect)
	}
	if face.AttemptCount == nil || *face.AttemptCount != 1 {
		t.Errorf("the stored attempt count is %v, want 1", face.AttemptCount)
	}
}
