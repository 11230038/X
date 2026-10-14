package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"backend/internal/httpx"
	"backend/internal/notebook"
	"github.com/gin-gonic/gin"
)

// Questions is the question bank use case consumed by the HTTP handler.
type Questions interface {
	Get(context.Context, string) (notebook.Entry, error)
	List(context.Context, notebook.Filter) ([]notebook.Entry, error)
	Mark(context.Context, string, notebook.Change) error
	SetCategories(context.Context, string, []string) error
	Categories(context.Context) ([]notebook.Category, error)
}

// QuestionHandler handles the question bank: the list, one question, the marks
// set on it, and the labels the bank is filed under.
type QuestionHandler struct {
	questions Questions
	logger    *slog.Logger
}

// NewQuestionHandler constructs the question bank HTTP handler.
func NewQuestionHandler(questions Questions, logger *slog.Logger) *QuestionHandler {
	return &QuestionHandler{questions: questions, logger: logger}
}

// optionView is one choice of a multiple-choice question.
type optionView struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}

// questionView is one question as a client reads it: the face that was decided
// when the question was written.
//
// The stored answer and the explanation are here, and not behind a second
// request, because the reader is the person studying: the card shows the
// verdict and then says why, and withholding the explanation would make "why" a
// different endpoint's answer to the same question. A question still in the
// queue carries the same face — one shape for a question, everywhere it appears.
type questionView struct {
	QuestionID string `json:"question_id"`
	Text       string `json:"text"`
	// QuestionType is named after the filter that selects on it rather than
	// after the column it is stored in, because the API is what the client
	// writes.
	QuestionType    string       `json:"question_type"`
	Options         []optionView `json:"options"`
	CorrectAnswer   string       `json:"correct_answer,omitempty"`
	Explanation     string       `json:"explanation,omitempty"`
	Difficulty      string       `json:"difficulty,omitempty"`
	Source          string       `json:"source,omitempty"`
	MaterialID      string       `json:"material_id,omitempty"`
	KnowledgePoints []string     `json:"knowledge_points,omitempty"`
	// Categories are the labels the question is filed under. The list is empty
	// rather than absent for an unfiled question: "not filed" is an answer, and
	// a client should not have to tell a missing field from an empty one.
	Categories []string `json:"categories"`
}

// answerView is what has happened to a question: the last answer given to it,
// how it was judged, and the marks set on it.
type answerView struct {
	UserAnswer string `json:"user_answer,omitempty"`
	// IsCorrect is absent while nothing has been judged, which is a different
	// thing from a judgement that came back false.
	IsCorrect *bool `json:"is_correct"`
	// Result is how the last review was labelled ("correct" / "wrong" /
	// "mastered" / "needs_review" / "skipped").
	Result string `json:"result,omitempty"`
	// Resolved is three-state: absent means nobody has marked the question
	// either way, which is what most questions are.
	Resolved     *bool     `json:"resolved"`
	AttemptCount int       `json:"attempt_count"`
	Bookmarked   bool      `json:"bookmarked"`
	UpdatedAt    time.Time `json:"updated_at"`
	// DueAt is when the review curve says to come back to this question (V4
	// §1.2). It is absent for a question that has never been reviewed, which is
	// not the same as one that is due: the first has no schedule at all, and a
	// client that read them the same way would put every new question at the top
	// of the "overdue" list.
	DueAt *time.Time `json:"due_at"`
}

// entryView is one row of the bank as a client reads it: a question with the
// answer record kept beside it.
type entryView struct {
	Question  questionView `json:"question"`
	Answer    answerView   `json:"answer"`
	CreatedAt time.Time    `json:"created_at"`
}

type questionListView struct {
	Questions []entryView `json:"questions"`
	// Limit and Offset are the page that was asked for. An oversized limit is
	// clamped rather than refused, so what a client needs to carry forward is
	// what it sent, and a second page is asked for from there.
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// categoryView is one filing label with how many questions carry it.
type categoryView struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type categoryListView struct {
	Categories []categoryView `json:"categories"`
}

// patchRequest is one property change.
//
// Every field is a pointer, and the two booleans have to be: "bookmark this"
// and "say nothing about bookmarking" are different requests, and a plain bool
// reads the second as the first — which would clear a mark nobody asked to
// clear. For the same reason the labels are a pointer to a list, because
// emptying the list is a request to unfile the question and leaving it out is a
// request to leave the filing alone.
type patchRequest struct {
	Bookmarked *bool     `json:"bookmarked"`
	Resolved   *bool     `json:"resolved"`
	Categories *[]string `json:"categories"`
}

// List returns one page of the bank, narrowed by whatever filters were given.
func (h *QuestionHandler) List(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	filter, ok := h.filter(c)
	if !ok {
		return
	}
	found, err := h.questions.List(c.Request.Context(), filter)
	if err != nil {
		h.fail(c, "list_questions_failed", err)
		return
	}
	listed := make([]entryView, 0, len(found))
	for _, entry := range found {
		listed = append(listed, toEntryView(entry))
	}
	// A listing that matched nothing is an empty list and not a 404: "nothing
	// matches this filter" and "this filter is not a thing" are different
	// answers, and only the second one is a missing resource.
	httpx.WriteSuccess(c, http.StatusOK, questionListView{
		Questions: listed,
		Limit:     filter.Limit,
		Offset:    filter.Offset,
	})
}

// Get returns one question with the record of what has happened to it.
func (h *QuestionHandler) Get(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	entry, err := h.questions.Get(c.Request.Context(), c.Param("questionID"))
	if err != nil {
		h.fail(c, "get_question_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, toEntryView(entry))
}

// Patch sets the marks on one question.
//
// The marks and the filing are two writes and not one transaction, and that is
// a decision rather than an oversight: both are idempotent and commutative, so
// a request that names both and fails on the second is repaired by sending it
// again, and a request that named one of them never had a second write to lose.
// The marks go first because a question that is not in the bank is answered by
// the first write that looks for it, rather than by the filing of a question
// that turned out not to exist.
func (h *QuestionHandler) Patch(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var request patchRequest
	if !readJSON(c, maxQuestionRequestBytes, &request) {
		return
	}
	if request.Bookmarked == nil && request.Resolved == nil && request.Categories == nil {
		httpx.WriteError(c, http.StatusBadRequest, "empty_change", "the request names nothing to change")
		return
	}
	questionID := c.Param("questionID")
	if request.Bookmarked != nil || request.Resolved != nil {
		change := notebook.Change{Bookmarked: request.Bookmarked, Resolved: request.Resolved}
		if err := h.questions.Mark(c.Request.Context(), questionID, change); err != nil {
			h.fail(c, "mark_question_failed", err)
			return
		}
	}
	if request.Categories != nil {
		err := h.questions.SetCategories(c.Request.Context(), questionID, *request.Categories)
		if err != nil {
			h.fail(c, "file_question_failed", err)
			return
		}
	}
	// The answer is the question as it now stands, so that a client does not
	// have to follow a write with a read to find out what it wrote.
	entry, err := h.questions.Get(c.Request.Context(), questionID)
	if err != nil {
		h.fail(c, "mark_question_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, toEntryView(entry))
}

// Categories returns the labels the bank is filed under.
func (h *QuestionHandler) Categories(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	found, err := h.questions.Categories(c.Request.Context())
	if err != nil {
		h.fail(c, "list_categories_failed", err)
		return
	}
	listed := make([]categoryView, 0, len(found))
	for _, category := range found {
		listed = append(listed, categoryView{Name: category.Name, Count: category.Count})
	}
	httpx.WriteSuccess(c, http.StatusOK, categoryListView{Categories: listed})
}

// filter reads the listing's filters, one query parameter each.
//
// A parameter that cannot be acted on is refused rather than dropped: a filter
// silently ignored answers a different question than the one that was asked,
// and the client has no way to tell that the list it got is not the list it
// asked for. The values themselves are checked by the domain, which is where
// the three states of `resolved` and the three question types are defined.
func (h *QuestionHandler) filter(c *gin.Context) (notebook.Filter, bool) {
	var filter notebook.Filter
	filter.Source = strings.TrimSpace(c.Query("source"))
	filter.MaterialID = strings.TrimSpace(c.Query("material_id"))
	filter.Difficulty = strings.TrimSpace(c.Query("difficulty"))
	filter.Category = strings.TrimSpace(c.Query("category"))
	filter.Query = strings.TrimSpace(c.Query("q"))
	filter.Type = notebook.QuestionType(strings.TrimSpace(c.Query("question_type")))
	filter.Resolution = notebook.Resolution(strings.TrimSpace(c.Query("resolved")))

	bookmarked, err := optionalBoolParam(c, "bookmarked")
	if err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_query", err.Error())
		return notebook.Filter{}, false
	}
	filter.Bookmarked = bookmarked
	if filter.Limit, err = intParam(c, "limit"); err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_query", err.Error())
		return notebook.Filter{}, false
	}
	if filter.Offset, err = intParam(c, "offset"); err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_query", err.Error())
		return notebook.Filter{}, false
	}
	if err := filter.Validate(); err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_filter", detail(err, notebook.ErrInvalidFilter))
		return notebook.Filter{}, false
	}
	return filter, true
}

// optionalBoolParam reads an optional boolean query parameter, keeping "absent"
// apart from "false": a listing that was not asked to filter on bookmarks is
// not a listing asking for the unmarked ones.
func optionalBoolParam(c *gin.Context, name string) (*bool, error) {
	raw, exists := c.GetQuery(name)
	if !exists || strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, errors.New(name + " must be true or false")
	}
	return &value, nil
}

func (h *QuestionHandler) ready(c *gin.Context) bool {
	if h != nil && h.questions != nil {
		return true
	}
	httpx.WriteError(c, http.StatusInternalServerError, "questions_unavailable", "the question bank is not configured")
	return false
}

// fail reports a question bank failure, distinguishing what the client can act
// on from what only an operator can.
func (h *QuestionHandler) fail(c *gin.Context, code string, err error) {
	switch {
	case errors.Is(err, notebook.ErrNotFound):
		httpx.WriteError(c, http.StatusNotFound, "not_found", "no such question")
	case errors.Is(err, notebook.ErrInvalidFilter):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_filter", detail(err, notebook.ErrInvalidFilter))
	case errors.Is(err, notebook.ErrInvalidQuestion):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_question", detail(err, notebook.ErrInvalidQuestion))
	case errors.Is(err, notebook.ErrEmptyChange):
		httpx.WriteError(c, http.StatusBadRequest, "empty_change", detail(err, notebook.ErrEmptyChange))
	default:
		if h.logger != nil {
			h.logger.Error("question request failed", "error", err, "request_id", httpx.RequestID(c))
		}
		httpx.WriteError(c, http.StatusInternalServerError, code, "the question request failed")
	}
}

func toEntryView(entry notebook.Entry) entryView {
	return entryView{
		Question:  toQuestionView(entry.Question, entry.Categories),
		Answer:    toAnswerView(entry.Answer),
		CreatedAt: entry.CreatedAt,
	}
}

func toQuestionView(question notebook.Question, categories []string) questionView {
	options := make([]optionView, 0, len(question.Options))
	for _, option := range question.Options {
		options = append(options, optionView{Key: option.Key, Text: option.Text})
	}
	filed := make([]string, 0, len(categories))
	filed = append(filed, categories...)
	return questionView{
		QuestionID:      question.QuestionID,
		Text:            question.Text,
		QuestionType:    string(question.Type),
		Options:         options,
		CorrectAnswer:   question.CorrectAnswer,
		Explanation:     question.Explanation,
		Difficulty:      question.Difficulty,
		Source:          question.Source,
		MaterialID:      question.MaterialID,
		KnowledgePoints: question.KnowledgePoints,
		Categories:      filed,
	}
}

func toAnswerView(answer notebook.Answer) answerView {
	return answerView{
		UserAnswer:   answer.UserAnswer,
		IsCorrect:    answer.IsCorrect,
		Result:       answer.Result,
		Resolved:     answer.Resolved,
		AttemptCount: answer.AttemptCount,
		Bookmarked:   answer.Bookmarked,
		UpdatedAt:    answer.UpdatedAt,
		DueAt:        answer.DueAt,
	}
}
