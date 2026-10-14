package practice

// The review write path's vocabulary: the submission that arrives, the answer
// that leaves, and the row-shaped values the two are built from.
//
// The two wire shapes are a contract rather than a convenience. What a
// submission produces is stored verbatim in the event row and handed back
// unchanged when the same request arrives again, so the stored JSON *is* the
// answer to a retry: renaming a field here is a data migration, and the two
// shapes are the only place in this package where a name is promised to
// somebody outside it.

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"backend/internal/notebook"
)

var (
	// ErrNotFound reports a question that is not in the bank, or a review that
	// was never recorded.
	ErrNotFound = errors.New("practice: not found")
	// ErrInvalidRequest reports a submission that cannot be acted on as it
	// stands: a request id or question id that is blank, a marker that is not
	// one of the four, a rating off the scale. It is refused rather than
	// repaired, because every one of these is a client that is not talking
	// about what it thinks it is.
	ErrInvalidRequest = errors.New("practice: invalid request")
	// ErrStaleReview reports a submission made against a version that is no
	// longer the stored one — two devices, one question. It is not retryable as
	// it stands: the caller has to read the state it was handed and decide
	// again. The current state travels with it (see StaleReview).
	ErrStaleReview = errors.New("practice: stale review")
	// ErrConflictingRequestID reports a request id that was already used for a
	// *different* submission. The recorded one is left alone: the id identifies
	// a submission, and a second, different one cannot borrow it.
	ErrConflictingRequestID = errors.New("practice: conflicting request id")
	// ErrDuplicate reports a write that met a row already holding what it meant
	// to write. It is returned for both of the tables this path writes to, and
	// the two collisions mean different things — a request id already used is
	// the same submission arriving twice, a state row already there is a
	// different submission that got in first. The repository reports one fact
	// and the caller decides which of the two it is, because only the caller
	// knows which statement it made.
	ErrDuplicate = errors.New("practice: the row is already there")
)

// Review is one submission: an answer to a question, what the user says about
// it, and which version of the state the client is looking at.
type Review struct {
	// RequestID identifies this submission. It is the client's to generate and
	// the reason a retry is safe: the same id arriving twice must produce one
	// review, not two.
	RequestID string `json:"request_id"`
	// QuestionID is the question's content address, which is what the outside
	// world calls a question.
	QuestionID string `json:"question_id"`
	UserAnswer string `json:"user_answer"`
	// Marker is what the user said about the question rather than about their
	// answer. Empty is an answer and nothing else.
	Marker Marker `json:"marker"`
	// Rating is the self-assessment, 0 to 5, or nil when none was given.
	Rating *float64 `json:"rating"`
	// Version is the state version the client read. Nil means the client did
	// not send one, and the service reads the version itself — which is a
	// read-then-write window, and therefore weaker than the client that sends
	// one.
	Version *int64 `json:"version"`
}

// Validate reports whether the submission can be acted on.
//
// The marker is checked here as well as in the schedule, and the duplication is
// deliberate: this refusal is about the request being malformed, and it must
// happen before anything is read, judged or written. What the schedule checks
// is the same value arriving at the curve.
func (r Review) Validate() error {
	if strings.TrimSpace(r.RequestID) == "" {
		return fmt.Errorf("%w: the request id is blank", ErrInvalidRequest)
	}
	if strings.TrimSpace(r.QuestionID) == "" {
		return fmt.Errorf("%w: the question id is blank", ErrInvalidRequest)
	}
	if !r.Marker.Valid() {
		return fmt.Errorf("%w: %q is not a review marker", ErrInvalidRequest, r.Marker)
	}
	if !ratingWithinScale(r.Rating) {
		return fmt.Errorf("%w: the rating is %v, which is not 0 to %d",
			ErrInvalidRequest, *r.Rating, qualityMaximum)
	}
	if r.Version != nil && *r.Version < 0 {
		return fmt.Errorf("%w: the version is %d", ErrInvalidRequest, *r.Version)
	}
	return nil
}

// ReviewResult is what one submission produced, and what a replay of it
// returns.
//
// `Replayed` is the only difference between the two: a client does not need it,
// but it makes "was this recorded now or handed back from before" readable in
// the response rather than something to infer from the clock.
type ReviewResult struct {
	RequestID  string      `json:"request_id"`
	QuestionID string      `json:"question_id"`
	Replayed   bool        `json:"replayed"`
	Judgment   *Judgment   `json:"judgment"`
	State      ReviewState `json:"state"`
	Entry      AnswerState `json:"entry"`
}

// ReviewState is the review state on the wire.
//
// It is not the row, and the two differences are decisions. The column named
// `case` is called `ease` here because that is what it means and the column
// name is a legacy it was not worth migrating; and the two timestamps the curve
// keeps for its own use — when the row was last looked at, when the question was
// first answered wrong — do not travel: the surface shows when the question
// comes back, not the bookkeeping behind that.
type ReviewState struct {
	DueTime     time.Time `json:"due_time"`
	Streak      int       `json:"streak"`
	Lapses      int       `json:"lapses"`
	ReviewCount int       `json:"review_count"`
	Ease        float64   `json:"ease"`
	IsMistake   bool      `json:"is_mistake"`
	Version     int64     `json:"version"`
}

// stateOnTheWire reads a review state as the surface shows it.
func stateOnTheWire(state StoredState) ReviewState {
	return ReviewState{
		DueTime:     state.DueTime,
		Streak:      state.Streak,
		Lapses:      state.Lapses,
		ReviewCount: state.ReviewCount,
		Ease:        state.Ease,
		IsMistake:   state.IsMistake,
		Version:     state.Version,
	}
}

// AnswerState is the answer face after a submission: the snapshot the question
// card shows, and the four fields of it the review path owns.
type AnswerState struct {
	IsCorrect    *bool  `json:"is_correct"`
	AttemptCount int    `json:"attempt_count"`
	Resolved     *bool  `json:"resolved"`
	Result       string `json:"result"`
}

// answerOnTheWire reads the answer face of a row as the surface shows it.
func answerOnTheWire(answer notebook.Answer) AnswerState {
	return AnswerState{
		IsCorrect:    answer.IsCorrect,
		AttemptCount: answer.AttemptCount,
		Resolved:     answer.Resolved,
		Result:       answer.Result,
	}
}

// StoredState is one row of the review state table: the curve's state and the
// version that guards it.
//
// The version is not part of the curve — the schedule neither reads it nor
// writes it — so it is not a field of State. It belongs to the row, and the row
// is what a writer updates under a condition.
type StoredState struct {
	State
	Version int64
}

// StoredReview is one row of the review event table, as the idempotency check
// reads it and as a new one is written.
//
// Outcome is the response body the submission stored, verbatim: reading a
// recorded submission back is decoding it, which is why the two cannot be
// allowed to come apart. The content columns beside it are what a retry is
// compared on; the marker is not a column, and reading it back out of Outcome is
// what ReviewResult.marker does.
type StoredReview struct {
	// RequestID is the row's primary key, and the whole of what makes a retry
	// land on the row it is a retry of.
	RequestID         string
	NotebookEntriesID string
	UserAnswer        string
	Rating            *float64
	// IsCorrect is the judgement, lifted out of Outcome and stored beside it as
	// a column. The two are written in the same statement and mean the same
	// thing; the column exists because the question "which of this question's
	// reviews were right" has to be answerable by an index rather than by
	// decoding JSON.
	//
	// nil is skipped — nothing was judged — and is not the same as false.
	IsCorrect  *bool
	Outcome    []byte
	ReviewTime time.Time
}

// StoredEntry is a question row as the review path needs it: the row's own key
// for the two tables beside it, the question to judge an answer against, the
// answer face as it stands, and the review state waiting beside it.
//
// The answer face is read even though a review usually rewrites it: one of the
// branches — a skipped question — writes none of it, and the response reports
// the face either way.
type StoredEntry struct {
	ID       string
	Question notebook.Question
	Answer   notebook.Answer
	// State is nil for a question no review has been recorded for, which is a
	// different thing from a state row holding nothing: the second one is a row
	// a version guard can be met against, and the first has no version at all.
	State *StoredState
}

// AnswerFace is the answer face as one submission writes it.
//
// It is not AnswerState, and the difference is the point: `resolved` is nil
// for an answer that merely answered, because that column belongs to the
// property path and a review that has no opinion about it must not write it.
// Writing back the value the row happened to carry would read as a decision and
// would quietly undo a mark set while this review was being judged. The response
// about the same submission reports the stored value, which is why the two
// shapes are two shapes.
type AnswerFace struct {
	UserAnswer   string
	IsCorrect    bool
	Result       string
	AttemptCount int
	// Resolved is written only when the marker had something to say about it:
	// true for mastered, false for needs_review, nil otherwise.
	Resolved *bool
}

// StaleReview is ErrStaleReview carrying the state the client is behind.
//
// A 409 that only says "conflict" leaves the client to fetch the state and try
// again; one that carries the state it missed is a single round trip. The state
// travels in the error rather than as a second return value because it is only
// meaningful when the error is this one.
type StaleReview struct {
	State ReviewState
}

func (e *StaleReview) Error() string {
	return fmt.Sprintf("%s: the stored version is %d", ErrStaleReview, e.State.Version)
}

// Unwrap makes the sentinel reachable: callers that only care that it was a
// conflict ask errors.Is, and callers that need the state ask errors.As.
func (e *StaleReview) Unwrap() error { return ErrStaleReview }

// marker reads back the marker a recorded result was made with.
//
// The event table has no marker column, and the four markers have to be told
// apart: a retry arriving with a *different* one is a different submission, not
// a replay of this one. Two things in the stored response carry it, and neither
// carries it on its own:
//
// A judgement that is absent says skipped, and says it first, because skipping
// is the only branch that judges nothing. It has to be read first: the answer
// face is not written for that branch either, so the label beside it is whatever
// the question carried before — a question skipped after being answered wrong
// would otherwise read back as a plain wrong answer.
//
// Everything else records the marker's effect. The label on the answer face is
// the marker itself when the user gave one and the verdict otherwise, so it is
// read back as the marker it came from. Reading the label rather than the
// judgement is what keeps this deterministic: a subjective answer judged again
// could come back judged differently, and a retry of it would then look like a
// different submission and be refused.
func (r ReviewResult) marker() (Marker, error) {
	if r.Judgment == nil {
		return MarkerSkipped, nil
	}
	switch r.Entry.Result {
	case "correct", "wrong":
		return MarkerNone, nil
	case string(MarkerMastered):
		return MarkerMastered, nil
	case string(MarkerNeedsReview):
		return MarkerNeedsReview, nil
	case string(MarkerSkipped):
		return MarkerSkipped, nil
	default:
		return "", fmt.Errorf("%w: the recorded review says %q, which is not a result",
			ErrInvalidRequest, r.Entry.Result)
	}
}

// resultOf is the qualitative label the answer face keeps: the marker when the
// user gave one, and how the answer was judged otherwise.
//
// It is what recordedMarker reads back, so the two are one rule written twice —
// and the pair of them is why the label carries the marker rather than only the
// verdict.
func resultOf(marker Marker, correct bool) string {
	switch marker {
	case MarkerMastered, MarkerNeedsReview, MarkerSkipped:
		return string(marker)
	}
	if correct {
		return "correct"
	}
	return "wrong"
}

// sameRating reports whether a recorded rating is the one a request carries.
// The two are compared as they are stored, so "no rating" and "a rating of
// zero" stay different answers.
func sameRating(recorded, asked *float64) bool {
	switch {
	case recorded == nil && asked == nil:
		return true
	case recorded == nil || asked == nil:
		return false
	default:
		return *recorded == *asked
	}
}
