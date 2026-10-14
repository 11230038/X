package handler

import (
	"net/http"

	"backend/internal/httpx"
	"backend/internal/notebook"
	"github.com/gin-gonic/gin"
)

// The question bank's batch import (QN-05, §2.13): one file in, a report out, and
// the same request with `dry_run` writes nothing.
//
// The two requests are one endpoint and not two, because they are one walk in the
// domain: the report a preview returns is the report the commit returns, and what
// differs is only whether the walk was allowed to write. A second endpoint would be
// a second place for "which line goes in" to be decided, and the acceptance this
// section is held to is that the two agree — a preview that disagreed with the
// commit would be a preview that lies, and a lie about what is about to be written
// is worse than no preview at all.
//
// The body is the file itself: JSON lines, one question per line. It is not an
// envelope around a JSON string, because a file of a few hundred questions encoded
// as one string is a file with every newline escaped, and the whole reason this
// format was chosen over CSV is that a person can read it, fix the line that is
// wrong, and send it again.

// importEntryView is one line of the file and what the import decided about it.
//
// No field is omitted, the empty ones least of all. A client reading this renders
// a line and needs to know whether the reason it is not showing is empty or
// absent, and a report whose fields come and go is read wrong exactly when
// something is wrong with the file.
type importEntryView struct {
	// Line is the line's number in the file, counted from one, blank lines
	// included — a number a text editor agrees with.
	Line int `json:"line"`
	// Outcome is "writable", "missing_answer", "duplicate" or "invalid".
	Outcome string `json:"outcome"`
	// QuestionID is the content address this line's own text and type have, for the
	// two outcomes that are a question this bank takes: "writable" and "duplicate".
	// The question standing in the way of a duplicate is named by
	// ConflictingQuestionID instead.
	QuestionID string `json:"question_id"`
	// Conflict is "" for a new question, "same_question", "same_question_different_content"
	// or "same_text_other_type".
	Conflict string `json:"conflict"`
	// ConflictingQuestionID names the question this line met, and ConflictingLine
	// says which earlier line of this same file put it there — zero when it was
	// already in the bank before this request.
	ConflictingQuestionID string `json:"conflicting_question_id"`
	ConflictingLine       int    `json:"conflicting_line"`
	// Reason says why a line will not be written, in the words the reader needs to
	// fix it.
	Reason string `json:"reason"`
}

// importView is the answer to both requests.
type importView struct {
	// DryRun echoes which of the two requests this is, so that a client holding two
	// answers for one file can tell them apart.
	DryRun bool `json:"dry_run"`
	// Lines is how many lines the file held, blank ones excluded, and the four
	// counts beside it are the outcomes: they add up to Lines.
	Lines         int `json:"lines"`
	Parsed        int `json:"parsed"`
	Writable      int `json:"writable"`
	MissingAnswer int `json:"missing_answer"`
	Duplicate     int `json:"duplicate"`
	Invalid       int `json:"invalid"`
	// Entries is the report line by line, in file order.
	Entries []importEntryView `json:"entries"`
}

// Import reads a file of questions and reports what writing it would do, writing
// it when `dry_run` was not asked for (§2.13).
//
// Both answers are 200, and that is deliberate: a commit whose report says some
// lines were refused is not a failed request, it is a request that did part of
// what it was asked and said which part. The lines it refused are in the report
// with their reasons, and a client that turned the report into a 4xx would be
// throwing away the half of it that succeeded.
func (h *QuestionHandler) Import(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	dryRun, err := optionalBoolParam(c, "dry_run")
	if err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	file, ok := readText(c, maxQuestionImportBytes)
	if !ok {
		return
	}
	report, err := h.questions.Import(c.Request.Context(), file, dryRun != nil && *dryRun)
	if err != nil {
		h.fail(c, "import_questions_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, toImportView(report))
}

func toImportView(report notebook.ImportReport) importView {
	entries := make([]importEntryView, 0, len(report.Entries))
	for _, entry := range report.Entries {
		entries = append(entries, importEntryView{
			Line:                  entry.Line,
			Outcome:               string(entry.Outcome),
			QuestionID:            entry.QuestionID,
			Conflict:              string(entry.Conflict),
			ConflictingQuestionID: entry.ConflictingQuestionID,
			ConflictingLine:       entry.ConflictingLine,
			Reason:                entry.Reason,
		})
	}
	return importView{
		DryRun:        report.DryRun,
		Lines:         report.Lines,
		Parsed:        report.Parsed,
		Writable:      report.Writable,
		MissingAnswer: report.MissingAnswer,
		Duplicate:     report.Duplicate,
		Invalid:       report.Invalid,
		Entries:       entries,
	}
}
