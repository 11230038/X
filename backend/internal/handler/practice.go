package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"backend/internal/authz"
	"backend/internal/httpx"
	"backend/internal/practice"
	"github.com/gin-gonic/gin"
)

// Practice is the reviewing use case consumed by the HTTP handler.
//
// Every method but Stats takes the caller last; Stats takes it inside its query.
// The account is read from the request rather than passed in from outside,
// because it is the request's: it comes from the token this call is being served
// under, and nowhere else.
type Practice interface {
	Queue(context.Context, int, authz.Caller) ([]practice.QueueItem, error)
	Review(context.Context, practice.Review, authz.Caller) (practice.ReviewResult, error)
	Recorded(context.Context, string, authz.Caller) (practice.ReviewResult, error)
	Stats(context.Context, practice.StatsQuery) (practice.Stats, error)
	// Mistakes reads the wrong-answer set for a window: how many of its
	// questions were reviewed in it, and how many are in it now.
	Mistakes(context.Context, time.Time, time.Time, authz.Caller) (practice.MistakeProgress, error)
}

// PracticeHandler handles reviewing: the queue, one submission, the record of
// one submission, and the trend.
type PracticeHandler struct {
	reviews Practice
	// queueSize is how much practice one queue hands out. It is the
	// deployment's number rather than the request's (§2.7): a client that named
	// its own would be a client setting its own dose.
	queueSize int
	logger    *slog.Logger
}

// NewPracticeHandler constructs the practice HTTP handler.
func NewPracticeHandler(reviews Practice, queueSize int, logger *slog.Logger) *PracticeHandler {
	return &PracticeHandler{reviews: reviews, queueSize: queueSize, logger: logger}
}

// queueItemView is one question to practise now.
type queueItemView struct {
	Question  questionView `json:"question"`
	Answer    answerView   `json:"answer"`
	Reason    string       `json:"reason"`
	IsMistake bool         `json:"is_mistake"`
	// State is the review state the queue hands over for an answered question,
	// and absent for one that has never been reviewed: the submission that
	// answers it sends no version, and the state row is created by that
	// submission. Absent and zero are different answers, which is why this is a
	// pointer.
	State *practice.ReviewState `json:"state"`
}

type queueView struct {
	Queue []queueItemView `json:"queue"`
}

// staleReviewMessage is what both halves of a version conflict say, and it is
// one sentence rather than two because the refusal is one refusal: the state
// travels beside it when the server could read it, and the client that reads a
// conflict has one way to read it either way.
const staleReviewMessage = "the question was reviewed again while this submission was in flight"

type bucketStatView struct {
	Label    string  `json:"label"`
	Reviews  int     `json:"reviews"`
	Correct  int     `json:"correct"`
	Wrong    int     `json:"wrong"`
	Added    int     `json:"added"`
	Accuracy float64 `json:"accuracy"`
}

type distributionView struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// statsView is the trend a chart is drawn from, with the two breakdowns of the
// same window.
//
// From and To are reported because they are not necessarily the ones that were
// asked for: a window is widened outwards to whole buckets, and a chart that
// assumed the range it requested would draw a half day as a whole one. The
// labels are the first day of each bucket, so they sort as dates.
type statsView struct {
	From       time.Time          `json:"from"`
	To         time.Time          `json:"to"`
	Bucket     string             `json:"bucket"`
	Series     []bucketStatView   `json:"series"`
	Sources    []distributionView `json:"sources"`
	Categories []distributionView `json:"categories"`
}

// Queue returns the questions to practise now.
func (h *PracticeHandler) Queue(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	items, err := h.reviews.Queue(c.Request.Context(), h.queueSize, account)
	if err != nil {
		h.fail(c, "read_queue_failed", err)
		return
	}
	listed := make([]queueItemView, 0, len(items))
	for _, item := range items {
		listed = append(listed, queueItemView{
			Question:  toQuestionView(item.Question, nil),
			Answer:    toAnswerView(item.Answer),
			Reason:    string(item.Reason),
			IsMistake: item.IsMistake,
			State:     item.State,
		})
	}
	httpx.WriteSuccess(c, http.StatusOK, queueView{Queue: listed})
}

// Submit records one review.
//
// The body is the submission itself, field for field: it travels to the domain
// as the shape it already is rather than through a second struct whose only job
// would be to have the same fields and drift from them.
//
// The status is 200 for a first submission and for a retry of it, and that is
// deliberate: the two are the same review, and a retry answered with a different
// status would be telling a client it did something it did not. What tells them
// apart is `replayed` in the body, which is a fact about the record rather than
// about this request.
func (h *PracticeHandler) Submit(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	var review practice.Review
	if !readJSON(c, maxQuestionRequestBytes, &review) {
		return
	}
	result, err := h.reviews.Review(c.Request.Context(), review, account)
	if err != nil {
		h.fail(c, "submit_review_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, result)
}

// Read returns one review that was recorded.
//
// The body is the stored one, unchanged: it is the same value a retry of that
// submission is answered with, which is what makes the idempotency rule
// readable from outside rather than something to infer by submitting twice.
func (h *PracticeHandler) Read(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	result, err := h.reviews.Recorded(c.Request.Context(), c.Param("requestID"), account)
	if err != nil {
		h.fail(c, "read_review_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, result)
}

// Stats returns the practice history of one window.
func (h *PracticeHandler) Stats(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	query, ok := statsQuery(c, account)
	if !ok {
		return
	}
	stats, err := h.reviews.Stats(c.Request.Context(), query)
	if err != nil {
		h.fail(c, "read_stats_failed", err)
		return
	}
	series := make([]bucketStatView, 0, len(stats.Series))
	for _, point := range stats.Series {
		series = append(series, bucketStatView{
			Label: point.Label, Reviews: point.Reviews, Correct: point.Correct,
			Wrong: point.Wrong, Added: point.Added, Accuracy: point.Accuracy,
		})
	}
	httpx.WriteSuccess(c, http.StatusOK, statsView{
		From: stats.From, To: stats.To, Bucket: string(stats.Bucket),
		Series:     series,
		Sources:    toDistributions(stats.Sources),
		Categories: toDistributions(stats.Categories),
	})
}

// mistakesView is the wrong-answer set as the home page's bar draws it: what was
// reviewed in the window over what the set holds now.
//
// Both numbers are present and neither is a percentage, because the bar has to
// divide and the sentence beside it has to be able to print the terms.
type mistakesView struct {
	Reviewed int `json:"reviewed"`
	Total    int `json:"total"`
}

// Mistakes counts the wrong-answer set for a window.
//
// Both ends are required for the same reason the mastery window's are: the page
// counts "this week", and the server does not get to guess which calendar that
// is. The total is the set now, not at the window's start — a mistake is never
// un-marked, so every question that was in the set during the window is still in
// it, and the two numbers stay comparable.
func (h *PracticeHandler) Mistakes(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	from, ok := instantParam(c, "from")
	if !ok {
		return
	}
	to, ok := instantParam(c, "to")
	if !ok {
		return
	}
	if from.IsZero() || to.IsZero() {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_query", "from and to are required")
		return
	}
	progress, err := h.reviews.Mistakes(c.Request.Context(), from, to, account)
	if err != nil {
		if errors.Is(err, practice.ErrInvalidStats) {
			httpx.WriteError(c, http.StatusBadRequest, "invalid_query", "from must be before to")
			return
		}
		h.fail(c, "read_mistakes_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, mistakesView{Reviewed: progress.Reviewed, Total: progress.Total})
}

// statsQuery reads the window a statistics request names. A parameter that is
// absent is left zero, and the domain decides what an unnamed window is.
//
// The account is put in here rather than left to the domain, because it is not
// part of the window: it never appears in the request, and a query built without
// it would be a trend for nobody.
func statsQuery(c *gin.Context, account authz.Caller) (practice.StatsQuery, bool) {
	query := practice.StatsQuery{Caller: account}
	from, ok := instantParam(c, "from")
	if !ok {
		return practice.StatsQuery{}, false
	}
	to, ok := instantParam(c, "to")
	if !ok {
		return practice.StatsQuery{}, false
	}
	query.From, query.To = from, to
	query.Bucket = practice.Bucket(strings.TrimSpace(c.Query("bucket")))
	return query, true
}

// instantParam reads an optional instant. Both a full timestamp and a bare date
// are accepted, because a daily chart is asked for by the day: "from=2026-10-06"
// is what a client writes for one, and making it spell out midnight would be a
// rule about the URL rather than about the window.
func instantParam(c *gin.Context, name string) (time.Time, bool) {
	raw, exists := c.GetQuery(name)
	if !exists || strings.TrimSpace(raw) == "" {
		return time.Time{}, true
	}
	text := strings.TrimSpace(raw)
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed, true
		}
	}
	httpx.WriteError(c, http.StatusBadRequest, "invalid_query",
		name+" must be an RFC 3339 instant or a date")
	return time.Time{}, false
}

func toDistributions(rows []practice.Distribution) []distributionView {
	listed := make([]distributionView, 0, len(rows))
	for _, row := range rows {
		listed = append(listed, distributionView{Name: row.Name, Count: row.Count})
	}
	return listed
}

func (h *PracticeHandler) ready(c *gin.Context) bool {
	if h != nil && h.reviews != nil {
		return true
	}
	httpx.WriteError(c, http.StatusInternalServerError, "practice_unavailable", "the review store is not configured")
	return false
}

// fail reports a review failure, distinguishing what the client can act on from
// what only an operator can.
//
// The two conflicts are kept apart because they ask for different things. A
// stale version is a client that is behind and has to decide again, and the
// state it is behind travels with the refusal so that deciding again costs no
// extra round trip. A request id that was spent on a different submission is a
// client that has lost track of which id belongs to which submission, and no
// amount of reading the question's state fixes it.
//
// Judging is the one failure that is neither the client's nor ours: the provider
// was not there, the answer may be perfectly good, and the surface offers a
// retry for this code and for nothing else (§7.3 第 6 条).
//
// A quota refusal is asked about before all of that, because it arrives wrapped
// in the judging failure that carried it: the submit was refused before the
// model was called, and answering `judge_unavailable` would offer a retry that
// is going to be refused the same way until the period ends.
func (h *PracticeHandler) fail(c *gin.Context, code string, err error) {
	if writeQuotaRefusal(c, err) {
		return
	}
	var stale *practice.StaleReview
	switch {
	case errors.As(err, &stale):
		httpx.WriteErrorDetails(c, http.StatusConflict, "stale_review", staleReviewMessage,
			gin.H{"state": stale.State})
	case errors.Is(err, practice.ErrStaleReview):
		// The conflict without the state, and otherwise the same answer: one
		// code and one sentence, so that a client reads a conflict in one
		// branch and takes the state when the state is there.
		httpx.WriteError(c, http.StatusConflict, "stale_review", staleReviewMessage)
	case errors.Is(err, practice.ErrConflictingRequestID):
		httpx.WriteError(c, http.StatusConflict, "conflicting_request_id",
			detail(err, practice.ErrConflictingRequestID))
	case errors.Is(err, practice.ErrInvalidRequest):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request",
			detail(err, practice.ErrInvalidRequest))
	case errors.Is(err, practice.ErrInvalidStats):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_query",
			detail(err, practice.ErrInvalidStats))
	case errors.Is(err, practice.ErrNotFound):
		httpx.WriteError(c, http.StatusNotFound, "not_found", "no such question or review")
	case errors.Is(err, practice.ErrUnusableQuestion):
		httpx.WriteError(c, http.StatusUnprocessableEntity, "question_not_judgeable",
			detail(err, practice.ErrUnusableQuestion))
	case errors.Is(err, practice.ErrJudgeUnavailable):
		httpx.WriteError(c, http.StatusServiceUnavailable, "judge_unavailable",
			detail(err, practice.ErrJudgeUnavailable))
	default:
		if h.logger != nil {
			h.logger.Error("practice request failed", "error", err, "request_id", httpx.RequestID(c))
		}
		httpx.WriteError(c, http.StatusInternalServerError, code, "the practice request failed")
	}
}
