package handler

// The batch import's HTTP surface against a stub (QN-05, §2.13).
//
// Two things are pinned here and nothing else, because nothing else is the
// handler's: which of the two requests the client is making, and that the file
// arrives at the domain exactly as it was sent. What each line of the file means
// is the domain's answer — that is where preview and commit are one walk — and a
// test of it here would be a test of the domain through a second door.
//
// The report's own shape is checked here too, for the same reason: the fields a
// client renders are the handler's contract, and the one it must not break is the
// empty field. A client that read the report by which fields were present would
// read a quiet line as a missing one.

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"backend/internal/notebook"
)

// importFile is a file of two questions as the client writes it: JSON lines, and
// the newlines are part of the body. It is deliberately not pretty-printed, which
// is what a file of JSON lines is not.
const importFile = `{"question":"什么是闭包","question_type":"choice","options":[{"key":"A","text":"甲"},{"key":"B","text":"乙"}],"correct_answer":"A","explanation":"因为甲是对的"}
{"question":"什么是柯里化","question_type":"short_answer","correct_answer":"把多参数函数变成单参数函数链","explanation":"因为每次只接受一个参数"}
`

// sampleImportReport is one report with all four outcomes in it, so that a case
// about one field can see the others still there.
func sampleImportReport() notebook.ImportReport {
	return notebook.ImportReport{
		Lines:         4,
		Parsed:        3,
		Writable:      1,
		MissingAnswer: 1,
		Duplicate:     1,
		Invalid:       1,
		Entries: []notebook.ImportEntry{
			{
				Line:       1,
				Outcome:    notebook.ImportWritable,
				QuestionID: "qn_1",
			},
			{
				Line:                  2,
				Outcome:               notebook.ImportDuplicate,
				QuestionID:            "qn_2",
				Conflict:              notebook.ImportConflictSameTextOtherType,
				ConflictingQuestionID: "qn_3",
				Reason:                "the bank already holds this text as a choice question",
			},
			{
				Line:    3,
				Outcome: notebook.ImportMissingAnswer,
				Reason:  "the line names no correct answer",
			},
			{
				Line:    4,
				Outcome: notebook.ImportInvalid,
				Reason:  "the line is not a question",
			},
		},
	}
}

// TestQuestionImportHandlerTurnsDryRunIntoTheRequestItIs is the whole of what the
// query parameter decides: an absent `dry_run` is the commit rather than the
// preview, and that is the one place this endpoint could write when it was asked
// not to.
//
// It also pins that the file reaches the domain unchanged, newlines and all. A
// handler that re-encoded the body — through a JSON string, through a line
// reader — would be a second parser over the same file, and the line numbers in
// the report are the ones this text has.
func TestQuestionImportHandlerTurnsDryRunIntoTheRequestItIs(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{name: "no parameter is the commit", query: "", want: false},
		{name: "dry_run=true is the preview", query: "?dry_run=true", want: true},
		{name: "dry_run=false is the commit", query: "?dry_run=false", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &questionStub{}
			recorder := call(t, newQuestionRouter(stub), http.MethodPost,
				"/api/v1/questions/import"+test.query, importFile)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
			}
			if len(stub.imports) != 1 {
				t.Fatalf("the domain was handed %d files, want one", len(stub.imports))
			}
			if got := stub.imports[0]; got.file != importFile || got.dryRun != test.want {
				t.Errorf("the domain was handed dry_run=%v over %q, want %v over the file as sent",
					got.dryRun, got.file, test.want)
			}
			var envelope struct {
				Data importView `json:"data"`
			}
			decodeBody(t, recorder, &envelope)
			if envelope.Data.DryRun != test.want {
				t.Errorf("the answer says dry_run=%v about a request that was %v", envelope.Data.DryRun, test.want)
			}
		})
	}
}

// TestQuestionImportHandlerRefusesADryRunThatIsNotABoolean follows the listing's
// rule for a parameter it cannot act on. Here it is worth more than elsewhere: a
// `dry_run` read as "no" would turn a preview into the write it was asked to
// preview, and answer with a report of a bank that had already changed.
func TestQuestionImportHandlerRefusesADryRunThatIsNotABoolean(t *testing.T) {
	stub := &questionStub{}
	recorder := call(t, newQuestionRouter(stub), http.MethodPost,
		"/api/v1/questions/import?dry_run=perhaps", importFile)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeBody(t, recorder, &envelope)
	if envelope.Error.Code != "invalid_query" {
		t.Errorf("error code = %q, want invalid_query", envelope.Error.Code)
	}
	if len(stub.imports) != 0 {
		t.Error("a request that was refused reached the bank anyway")
	}
}

// TestQuestionImportHandlerReturnsTheReportLineByLine pins the shape a client
// renders: the counts, and every entry with every field in it — the empty ones
// included, because a line that says nothing is the common case and it must not
// be the case a client cannot tell from a field being missing.
func TestQuestionImportHandlerReturnsTheReportLineByLine(t *testing.T) {
	stub := &questionStub{report: sampleImportReport()}
	recorder := call(t, newQuestionRouter(stub), http.MethodPost,
		"/api/v1/questions/import?dry_run=true", importFile)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, want := range []string{
		`"lines":4`, `"parsed":3`, `"writable":1`, `"missing_answer":1`, `"duplicate":1`, `"invalid":1`,
	} {
		if !containsJSON(body, want) {
			t.Errorf("the report does not carry %s: %s", want, body)
		}
	}
	var envelope struct {
		Data importView `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if len(envelope.Data.Entries) != 4 {
		t.Fatalf("the report holds %d entries, want one per line", len(envelope.Data.Entries))
	}
	writable := envelope.Data.Entries[0]
	if writable.Line != 1 || writable.Outcome != "writable" || writable.QuestionID != "qn_1" {
		t.Errorf("the first line reads %+v", writable)
	}
	// The line nothing happened to still carries its conflict and its reason, as
	// empty strings: that is the field whose absence would be read as a mistake.
	if !containsJSON(body, `"conflict":""`) || !containsJSON(body, `"reason":""`) {
		t.Errorf("a quiet entry leaves its fields out instead of emptying them: %s", body)
	}
	duplicate := envelope.Data.Entries[1]
	if duplicate.Conflict != "same_text_other_type" ||
		duplicate.ConflictingQuestionID != "qn_3" || duplicate.ConflictingLine != 0 {
		t.Errorf("the duplicate line reads %+v", duplicate)
	}
}

// TestQuestionImportHandlerAnswersACommitThatRefusedLines is the decision that an
// import which did part of what it was asked is not a failed request. The lines
// it refused are in the report with their reasons, and a client that got a 4xx
// would have lost the half of the answer that says what to fix.
func TestQuestionImportHandlerAnswersACommitThatRefusedLines(t *testing.T) {
	stub := &questionStub{report: sampleImportReport()}
	recorder := call(t, newQuestionRouter(stub), http.MethodPost, "/api/v1/questions/import", importFile)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if len(stub.imports) != 1 || stub.imports[0].dryRun {
		t.Errorf("the request the domain saw is %+v, want the commit", stub.imports)
	}
}

// TestQuestionImportHandlerRefusesAnEmptyFile is the body reader's own rule,
// applied to the one body that is text: a request with nothing in it is refused
// rather than answered with a report about no questions.
func TestQuestionImportHandlerRefusesAnEmptyFile(t *testing.T) {
	for _, body := range []string{"", "   \n\t\n"} {
		stub := &questionStub{}
		recorder := call(t, newQuestionRouter(stub), http.MethodPost,
			"/api/v1/questions/import", body)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status = %d over %q, want 400", recorder.Code, body)
		}
		if len(stub.imports) != 0 {
			t.Error("an empty file reached the bank")
		}
	}
}

// TestQuestionImportHandlerRefusesAFileOverTheBound is where this body reader
// differs from the JSON one, and why: cutting an oversize JSON object makes it
// malformed and it is refused anyway, but cutting a file at a line boundary would
// hand the import a shorter file and produce a report of questions the client
// never sent. So the bound is a refusal, not a cut.
func TestQuestionImportHandlerRefusesAFileOverTheBound(t *testing.T) {
	stub := &questionStub{}
	oversized := `{"question":"` + strings.Repeat("长", int(maxQuestionImportBytes)) + `"}`
	recorder := call(t, newQuestionRouter(stub), http.MethodPost,
		"/api/v1/questions/import", oversized)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeBody(t, recorder, &envelope)
	if envelope.Error.Code != "request_too_large" {
		t.Errorf("error code = %q, want request_too_large", envelope.Error.Code)
	}
	if len(stub.imports) != 0 {
		t.Error("a file that was refused reached the bank anyway")
	}
}

// TestQuestionImportHandlerReportsAStorageFailure is the one failure that is not
// a refused line: a file that could not be read or written is the server's
// problem, and the report it produces is not one. The sentence stays in the log,
// like every other storage failure's.
func TestQuestionImportHandlerReportsAStorageFailure(t *testing.T) {
	stub := &questionStub{err: errors.New("private database details")}
	recorder := call(t, newQuestionRouter(stub), http.MethodPost, "/api/v1/questions/import", importFile)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeBody(t, recorder, &envelope)
	if envelope.Error.Code != "import_questions_failed" {
		t.Errorf("error code = %q, want import_questions_failed", envelope.Error.Code)
	}
	if containsJSON(recorder.Body.String(), "private database details") {
		t.Errorf("the response exposed a storage error: %s", recorder.Body.String())
	}
}
