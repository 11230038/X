package handler

// The question bank's HTTP surface against a stub. What is being pinned here is
// the seam between the URL and the domain: which query parameter becomes which
// field of a filter, that an absent field stays absent rather than becoming a
// false, and which refusal a client is handed for a question that is not there.
//
// The stub records what reached it, because the interesting failure of a filter
// is a silent one — a handler that dropped a parameter answers with a plausible
// list of the wrong questions, and only the domain knows what it was asked.

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"backend/internal/authz"
	"backend/internal/notebook"
	"github.com/gin-gonic/gin"
)

var handlerQuestionTime = time.Unix(1_800_000_000, 0).UTC()

// questionStub is the question bank use case, recording what it was asked for.
type questionStub struct {
	entry      notebook.Entry
	listed     []notebook.Entry
	categories []notebook.Category
	// report is what the batch import answers with. Its DryRun is set by the stub
	// rather than by the case, because which request this is is the one thing the
	// domain is told and the one thing the answer echoes.
	report notebook.ImportReport
	err    error

	filters []notebook.Filter
	marks   []markCall
	files   []fileCall
	read    []string
	imports []importCall
}

type markCall struct {
	questionID string
	change     notebook.Change
}

type fileCall struct {
	questionID string
	names      []string
}

// importCall is one batch import as the domain saw it: the file's whole text and
// which of the two requests it was.
type importCall struct {
	file   string
	dryRun bool
}

func (s *questionStub) Get(_ context.Context, questionID string, _ authz.Caller) (notebook.Entry, error) {
	s.read = append(s.read, questionID)
	return s.entry, s.err
}

func (s *questionStub) List(_ context.Context, filter notebook.Filter) ([]notebook.Entry, error) {
	s.filters = append(s.filters, filter)
	return s.listed, s.err
}

func (s *questionStub) Mark(_ context.Context, questionID string, change notebook.Change, _ authz.Caller) error {
	s.marks = append(s.marks, markCall{questionID: questionID, change: change})
	return s.err
}

func (s *questionStub) SetCategories(_ context.Context, questionID string, names []string, _ authz.Caller) error {
	s.files = append(s.files, fileCall{questionID: questionID, names: names})
	return s.err
}

func (s *questionStub) Categories(_ context.Context, _ authz.Caller) ([]notebook.Category, error) {
	return s.categories, s.err
}

func (s *questionStub) Import(_ context.Context, file string, dryRun bool, _ authz.Caller) (notebook.ImportReport, error) {
	s.imports = append(s.imports, importCall{file: file, dryRun: dryRun})
	if s.err != nil {
		return notebook.ImportReport{}, s.err
	}
	report := s.report
	report.DryRun = dryRun
	return report, nil
}

// sampleEntry is one question of the bank with both faces filled in, so that a
// case about one field can assert the others are still there.
func sampleEntry() notebook.Entry {
	correct := true
	resolved := false
	return notebook.Entry{
		ID: "nbe_1",
		Question: notebook.Question{
			QuestionID:      "qn_1",
			Text:            "下面哪个是闭包",
			Type:            notebook.QuestionChoice,
			Options:         []notebook.Option{{Key: "A", Text: "甲"}, {Key: "B", Text: "乙"}},
			CorrectAnswer:   "A",
			Explanation:     "因为甲是对的",
			Difficulty:      "medium",
			Source:          "knowledge_base",
			MaterialID:      "doc_1",
			KnowledgePoints: []string{"闭包"},
		},
		Answer: notebook.Answer{
			UserAnswer:   "B",
			IsCorrect:    &correct,
			Result:       "correct",
			Resolved:     &resolved,
			AttemptCount: 2,
			Bookmarked:   true,
			UpdatedAt:    handlerQuestionTime,
		},
		Categories: []string{"数学", "编程"},
		CreatedAt:  handlerQuestionTime,
	}
}

// newQuestionRouter mounts the question bank routes the way the server does:
// behind the bearer middleware, because the handlers read the account out of the
// context it fills in and a route without it would answer 401 to every request
// rather than exercise the handler.
func newQuestionRouter(questions Questions) *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := NewQuestionHandler(questions, testHandlerLogger())
	router := gin.New()
	router.GET("/api/v1/questions", guard(), handler.List)
	router.POST("/api/v1/questions/import", guard(), handler.Import)
	router.GET("/api/v1/questions/:questionID", guard(), handler.Get)
	router.PATCH("/api/v1/questions/:questionID", guard(), handler.Patch)
	router.GET("/api/v1/question-categories", guard(), handler.Categories)
	return router
}

// TestQuestionHandlerListTurnsTheQueryIntoAFilter walks the URL one parameter at
// a time. A parameter that stopped being read would show up here as a field that
// stayed empty, which is the failure this whole test file is about.
func TestQuestionHandlerListTurnsTheQueryIntoAFilter(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  notebook.Filter
	}{
		{name: "nothing asked", query: "", want: notebook.Filter{Caller: handlerCaller()}},
		{
			name:  "every text filter at once",
			query: "?source=knowledge_base&material_id=doc_1&difficulty=medium&q=闭包",
			want: notebook.Filter{
				Caller: handlerCaller(),
				Source: "knowledge_base", MaterialID: "doc_1", Difficulty: "medium", Query: "闭包",
			},
		},
		{name: "by question type", query: "?question_type=choice", want: notebook.Filter{Caller: handlerCaller(), Type: notebook.QuestionChoice}},
		{name: "by resolution", query: "?resolved=done", want: notebook.Filter{Caller: handlerCaller(), Resolution: notebook.ResolutionDone}},
		{name: "by category", query: "?category=数学", want: notebook.Filter{Caller: handlerCaller(), Category: "数学"}},
		{name: "by mastery path", query: "?mastery_path_id=mp_1", want: notebook.Filter{Caller: handlerCaller(), MasteryPathID: "mp_1"}},
		{name: "paging", query: "?limit=20&offset=40", want: notebook.Filter{Caller: handlerCaller(), Limit: 20, Offset: 40}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &questionStub{listed: []notebook.Entry{sampleEntry()}}
			recorder := callAs(t, newQuestionRouter(stub), http.MethodGet, "/api/v1/questions"+test.query, "")

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
			}
			if len(stub.filters) != 1 {
				t.Fatalf("the bank was asked %d times, want once", len(stub.filters))
			}
			if got := stub.filters[0]; got != test.want {
				t.Errorf("the filter is %+v, want %+v", got, test.want)
			}
		})
	}
}

// TestQuestionHandlerListAsksForTheMarkedQuestionsOrTheRest pins the one filter
// whose two values are not a value and its absence: a listing that was not asked
// about bookmarks must send no opinion, and a listing that was asked for the
// unmarked ones must send false.
func TestQuestionHandlerListAsksForTheMarkedQuestionsOrTheRest(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  *bool
	}{
		{name: "not asked", query: "", want: nil},
		{name: "the marked ones", query: "?bookmarked=true", want: boolPointer(true)},
		{name: "the rest", query: "?bookmarked=false", want: boolPointer(false)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &questionStub{}
			recorder := callAs(t, newQuestionRouter(stub), http.MethodGet, "/api/v1/questions"+test.query, "")
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
			}
			got := stub.filters[0].Bookmarked
			switch {
			case got == nil && test.want == nil:
			case got == nil || test.want == nil:
				t.Errorf("bookmarked is %v, want %v", got, test.want)
			case *got != *test.want:
				t.Errorf("bookmarked is %v, want %v", *got, *test.want)
			}
		})
	}
}

func boolPointer(value bool) *bool { return &value }

// TestQuestionHandlerRefusesAFilterItCannotActOn is the other half of the
// parameter-reading rule: a value this version cannot act on is refused rather
// than dropped, because a dropped filter is a list of the wrong questions with
// nothing to say that it is.
func TestQuestionHandlerRefusesAFilterItCannotActOn(t *testing.T) {
	tests := []struct{ name, query, wantCode string }{
		{name: "a resolution that is not one", query: "?resolved=maybe", wantCode: "invalid_filter"},
		{name: "a question type that is not one", query: "?question_type=essay", wantCode: "invalid_filter"},
		{name: "a bookmark flag that is not a boolean", query: "?bookmarked=perhaps", wantCode: "invalid_query"},
		{name: "a limit that is not a number", query: "?limit=lots", wantCode: "invalid_query"},
		{name: "a negative offset", query: "?offset=-1", wantCode: "invalid_query"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &questionStub{}
			recorder := callAs(t, newQuestionRouter(stub), http.MethodGet, "/api/v1/questions"+test.query, "")

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
			var envelope struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			decodeBody(t, recorder, &envelope)
			if envelope.Error.Code != test.wantCode {
				t.Errorf("error code = %q, want %q", envelope.Error.Code, test.wantCode)
			}
			if envelope.Error.Message == "" {
				t.Error("the refusal says nothing about what was wrong")
			}
			if len(stub.filters) != 0 {
				t.Error("a filter that was refused reached the bank anyway")
			}
		})
	}
}

// TestQuestionHandlerListsAnEmptyBankAsAnEmptyList is the rule the whole listing
// family follows: a filter that matched nothing is an empty list, and never a
// null and never a missing resource.
func TestQuestionHandlerListsAnEmptyBankAsAnEmptyList(t *testing.T) {
	recorder := callAs(t, newQuestionRouter(&questionStub{}), http.MethodGet, "/api/v1/questions", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if body := recorder.Body.String(); !containsJSON(body, `"questions":[]`) {
		t.Errorf("an empty bank reads %s", body)
	}
}

func TestQuestionHandlerGetReportsBothFacesOfAQuestion(t *testing.T) {
	recorder := callAs(t, newQuestionRouter(&questionStub{entry: sampleEntry()}),
		http.MethodGet, "/api/v1/questions/qn_1", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data entryView `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if envelope.Data.Question.QuestionID != "qn_1" || envelope.Data.Question.QuestionType != "choice" {
		t.Errorf("the question face reads %+v", envelope.Data.Question)
	}
	if len(envelope.Data.Question.Options) != 2 {
		t.Errorf("the options read %+v", envelope.Data.Question.Options)
	}
	if got := envelope.Data.Question.Categories; len(got) != 2 || got[0] != "数学" {
		t.Errorf("the labels read %v", got)
	}
	if envelope.Data.Answer.IsCorrect == nil || !*envelope.Data.Answer.IsCorrect {
		t.Errorf("the answer face reads %+v", envelope.Data.Answer)
	}
	if envelope.Data.Answer.Resolved == nil || *envelope.Data.Answer.Resolved {
		t.Errorf("the answer face reads %+v, want a question marked as still open", envelope.Data.Answer)
	}
	if envelope.Data.Answer.AttemptCount != 2 || !envelope.Data.Answer.Bookmarked {
		t.Errorf("the answer face reads %+v", envelope.Data.Answer)
	}
}

// TestQuestionHandlerGetSaysNothingItDoesNotKnow is the three-state half of the
// answer face: a question nobody has judged and nobody has marked reports both
// as null, which is a different answer from a judgement of false.
func TestQuestionHandlerGetSaysNothingItDoesNotKnow(t *testing.T) {
	fresh := notebook.Entry{
		ID:         "nbe_1",
		Question:   notebook.Question{QuestionID: "qn_1", Text: "什么是闭包", Type: notebook.QuestionFillBlank},
		Categories: []string{},
		CreatedAt:  handlerQuestionTime,
	}
	recorder := callAs(t, newQuestionRouter(&questionStub{entry: fresh}),
		http.MethodGet, "/api/v1/questions/qn_1", "")
	if body := recorder.Body.String(); !containsJSON(body, `"is_correct":null`) ||
		!containsJSON(body, `"resolved":null`) {
		t.Errorf("an unanswered question reads %s", body)
	}
}

func TestQuestionHandlerGetReportsAQuestionThatIsNotThere(t *testing.T) {
	stub := &questionStub{err: notebook.ErrNotFound}
	recorder := callAs(t, newQuestionRouter(stub), http.MethodGet, "/api/v1/questions/qn_missing", "")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestQuestionHandlerPatchSetsTheMarksItWasGiven(t *testing.T) {
	stub := &questionStub{entry: sampleEntry()}
	recorder := callAs(t, newQuestionRouter(stub), http.MethodPatch, "/api/v1/questions/qn_1",
		`{"bookmarked":true,"resolved":false}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if len(stub.marks) != 1 {
		t.Fatalf("the bank was marked %d times, want once", len(stub.marks))
	}
	recorded := stub.marks[0]
	if recorded.questionID != "qn_1" {
		t.Errorf("the marks went to %s", recorded.questionID)
	}
	if recorded.change.Bookmarked == nil || !*recorded.change.Bookmarked {
		t.Errorf("the mark reads %+v", recorded.change)
	}
	if recorded.change.Resolved == nil || *recorded.change.Resolved {
		t.Errorf("the mark reads %+v, want a question marked as still open", recorded.change)
	}
	// The answer is the question as it now stands, so that a client does not
	// have to follow a write with a read.
	var envelope struct {
		Data entryView `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if envelope.Data.Question.QuestionID != "qn_1" {
		t.Errorf("the response reads %+v", envelope.Data)
	}
}

// TestQuestionHandlerPatchKeepsAnAbsentFieldAbsent is the reason every field of
// the body is a pointer. A body that says nothing about bookmarks must not clear
// the mark, and a body that says a mark is false must clear it.
func TestQuestionHandlerPatchKeepsAnAbsentFieldAbsent(t *testing.T) {
	t.Run("a body about labels says nothing about marks", func(t *testing.T) {
		stub := &questionStub{entry: sampleEntry()}
		recorder := callAs(t, newQuestionRouter(stub), http.MethodPatch, "/api/v1/questions/qn_1",
			`{"categories":["数学"]}`)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
		}
		if len(stub.marks) != 0 {
			t.Errorf("the marks were written by a request that named none: %+v", stub.marks)
		}
		if len(stub.files) != 1 || len(stub.files[0].names) != 1 || stub.files[0].names[0] != "数学" {
			t.Errorf("the labels read %+v", stub.files)
		}
	})

	t.Run("a mark of false is a mark", func(t *testing.T) {
		stub := &questionStub{entry: sampleEntry()}
		recorder := callAs(t, newQuestionRouter(stub), http.MethodPatch, "/api/v1/questions/qn_1",
			`{"bookmarked":false}`)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
		}
		if len(stub.marks) != 1 {
			t.Fatalf("the bank was marked %d times, want once", len(stub.marks))
		}
		change := stub.marks[0].change
		if change.Bookmarked == nil || *change.Bookmarked {
			t.Errorf("the mark reads %+v, want bookmarked false", change)
		}
		if change.Resolved != nil {
			t.Errorf("the mark reads %+v, want no opinion about resolved", change)
		}
	})
}

// TestQuestionHandlerPatchReplacesTheLabels covers the one write that removes:
// a client editing the filing sends the list it wants, and an empty list is the
// request to leave the question unfiled.
func TestQuestionHandlerPatchReplacesTheLabels(t *testing.T) {
	stub := &questionStub{entry: sampleEntry()}
	recorder := callAs(t, newQuestionRouter(stub), http.MethodPatch, "/api/v1/questions/qn_1",
		`{"categories":[]}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if len(stub.files) != 1 {
		t.Fatalf("the labels were written %d times, want once", len(stub.files))
	}
	if names := stub.files[0].names; len(names) != 0 {
		t.Errorf("an empty list was filed under %v", names)
	}
}

func TestQuestionHandlerPatchRefusesARequestThatNamesNothing(t *testing.T) {
	stub := &questionStub{entry: sampleEntry()}
	for _, body := range []string{`{}`, `{"bookmarked":null}`, ` `} {
		recorder := callAs(t, newQuestionRouter(stub), http.MethodPatch, "/api/v1/questions/qn_1", body)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("body %q gave status %d, want 400; body=%s",
				body, recorder.Code, recorder.Body.String())
		}
	}
	if len(stub.marks) != 0 || len(stub.files) != 0 {
		t.Error("a change with no subject reached the bank anyway")
	}
}

func TestQuestionHandlerPatchReportsAQuestionThatIsNotThere(t *testing.T) {
	stub := &questionStub{err: notebook.ErrNotFound}
	recorder := callAs(t, newQuestionRouter(stub), http.MethodPatch, "/api/v1/questions/qn_missing",
		`{"bookmarked":true}`)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", recorder.Code, recorder.Body.String())
	}
	if len(stub.files) != 0 {
		t.Error("a question that is not there was filed under something")
	}
}

// TestQuestionHandlerCategoriesCountsWhatIsFiled checks that the count made it
// out, and that a bank with no labels is an empty list.
func TestQuestionHandlerCategoriesCountsWhatIsFiled(t *testing.T) {
	t.Run("with labels", func(t *testing.T) {
		stub := &questionStub{categories: []notebook.Category{
			{Name: "数学", Count: 3}, {Name: "编程", Count: 1},
		}}
		recorder := callAs(t, newQuestionRouter(stub), http.MethodGet, "/api/v1/question-categories", "")
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
		}
		var envelope struct {
			Data categoryListView `json:"data"`
		}
		decodeBody(t, recorder, &envelope)
		if len(envelope.Data.Categories) != 2 {
			t.Fatalf("the labels read %+v", envelope.Data.Categories)
		}
		if envelope.Data.Categories[0] != (categoryView{Name: "数学", Count: 3}) {
			t.Errorf("the first label reads %+v", envelope.Data.Categories[0])
		}
	})

	t.Run("with none", func(t *testing.T) {
		recorder := callAs(t, newQuestionRouter(&questionStub{}),
			http.MethodGet, "/api/v1/question-categories", "")
		if body := recorder.Body.String(); !containsJSON(body, `"categories":[]`) {
			t.Errorf("a bank filed under nothing reads %s", body)
		}
	})
}

// TestQuestionHandlerDoesNotReportStorageToTheClient is the one behaviour a
// failure has to have: the client is told the request failed, and the sentence
// that says why is in the log rather than in the response.
func TestQuestionHandlerDoesNotReportStorageToTheClient(t *testing.T) {
	stub := &questionStub{err: errors.New("private database details")}
	recorder := callAs(t, newQuestionRouter(stub), http.MethodGet, "/api/v1/questions", "")
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	if containsJSON(recorder.Body.String(), "private database details") {
		t.Fatalf("the response exposed a storage error: %s", recorder.Body.String())
	}
}
