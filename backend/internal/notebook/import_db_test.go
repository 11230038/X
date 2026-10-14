package notebook

// The batch import's storage contract, checked against a real seekdb.
//
// Three claims only a database can settle are here, and the first two are the
// ones §7.1 names as acceptances: a preview leaves the bank byte for byte as it
// was, and only the lines the report called writable are written. Neither is a
// claim a recording repository can be wrong about in the interesting direction —
// what a preview must not do is exactly what a fake would be asked to notice, and
// a fake agrees for free.
//
// The third is the overwrite. That the same content address replaces the question
// face and leaves the answer face alone is the upsert's rule, and it is the rule
// the import's report is promising when it says "same address, different
// content": if storage did something else with that line, the report would be
// describing a write that does not happen.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"backend/internal/data"
	"backend/internal/data/testdb"
)

// newImportShelf is the service, its repository and the database, with an empty
// bank for the test to fill.
func newImportShelf(t *testing.T) (*Service, *GORMRepository, *data.Resource) {
	t.Helper()
	repository, resource := newDBRepository(t)
	service, err := NewService(repository)
	if err != nil {
		t.Fatalf("new notebook service: %v", err)
	}
	return service, repository, resource
}

// lineOfQuestion writes a question back out as a line of a file, which is what
// an export followed by an import does. It goes through the line's own fields
// rather than through a string built by hand, so that a test about a conflict is
// about the conflict and not about the test's own spelling.
func lineOfQuestion(t *testing.T, question Question) string {
	t.Helper()
	line := importLine{
		Question:        question.Text,
		QuestionType:    string(question.Type),
		CorrectAnswer:   question.CorrectAnswer,
		Explanation:     question.Explanation,
		Difficulty:      question.Difficulty,
		Source:          question.Source,
		MaterialID:      question.MaterialID,
		KnowledgePoints: question.KnowledgePoints,
		Categories:      question.Categories,
	}
	for _, option := range question.Options {
		line.Options = append(line.Options, importOption{Key: option.Key, Text: option.Text})
	}
	encoded, err := json.Marshal(line)
	if err != nil {
		t.Fatalf("write question %q back out: %v", question.Text, err)
	}
	return string(encoded)
}

// sameMoment reports whether two nullable timestamps are the same one, which
// includes both being absent: a question nobody has rewritten has no update time
// at all, and a comparison that dereferenced it would fail on the row the test
// is trying to show was left alone.
func sameMoment(one, other *time.Time) bool {
	if one == nil || other == nil {
		return one == nil && other == nil
	}
	return one.Equal(*other)
}

// TestDBAImportPreviewLeavesTheBankUntouched is §7.1's 「库里零变化」 as a row
// count and a comparison of the row that is there: a preview is not a slow
// commit, and what it must not do is anything at all.
func TestDBAImportPreviewLeavesTheBankUntouched(t *testing.T) {
	service, repository, resource := newImportShelf(t)
	held := sampleQuestion("银行里已经有的题")
	saveAt(t, repository, held, atSecond(0))
	before := readRow(t, resource, QuestionID(held.Text, held.Type))

	report, err := service.Import(context.Background(), file(
		line("一道新的题", nil),
		lineOfQuestion(t, held),
		line("没有答案的题", map[string]any{"correct_answer": ""}),
	), true, testCaller())
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if report.Writable != 1 || report.Duplicate != 1 || report.MissingAnswer != 1 {
		t.Fatalf("the report is %+v, want one writable, one duplicate and one missing an answer", report)
	}
	if got := countRows(t, resource, "notebook_entries", ""); got != 1 {
		t.Errorf("a preview left %d rows in the bank, want the one that was there", got)
	}
	after := readRow(t, resource, QuestionID(held.Text, held.Type))
	if after.NotebookEntriesID != before.NotebookEntriesID || after.Question != before.Question {
		t.Errorf("the row changed under a preview: %+v, was %+v", after, before)
	}
	if !sameMoment(after.UpdateTime, before.UpdateTime) {
		t.Errorf("a preview moved update_time to %v, want it left at %v", after.UpdateTime, before.UpdateTime)
	}
	if got := countRows(t, resource, "notebook_entry_categories", ""); got != 0 {
		t.Errorf("a preview filed %d categories", got)
	}
}

// TestDBTheCommitWritesOnlyWhatTheReportNamed is the other half of §7.1: the
// three lines the report called writable are the three that are in the bank, and
// the two it refused are not in it at all.
func TestDBTheCommitWritesOnlyWhatTheReportNamed(t *testing.T) {
	service, repository, resource := newImportShelf(t)
	held := sampleQuestion("银行里已经有的题")
	saveAt(t, repository, held, atSecond(0))

	report, err := service.Import(context.Background(), file(
		line("第一道新题", nil),
		lineOfQuestion(t, held),
		line("第二道新题", nil),
		line("没有答案的题", map[string]any{"correct_answer": ""}),
		line("第三道新题", nil),
	), false, testCaller())
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if report.Writable != 3 {
		t.Fatalf("the report is %+v, want three writable lines", report)
	}
	if got := countRows(t, resource, "notebook_entries", ""); got != 4 {
		t.Fatalf("the bank holds %d questions, want the four the report accounts for", got)
	}
	for _, entry := range report.Entries {
		if entry.Outcome != ImportWritable {
			continue
		}
		// Every writable line is in the bank under the address the report named,
		// which is what a client re-reads it by.
		row := readRow(t, resource, entry.QuestionID)
		if row.QuestionID != entry.QuestionID {
			t.Errorf("the line %d is reported under %s and stored under %s",
				entry.Line, entry.QuestionID, row.QuestionID)
		}
	}
	// The refused lines left nothing behind: the missing-answer question has no
	// row, and the duplicate is one row and not two.
	if got := countRows(t, resource, "notebook_entries", "question_id = ?",
		QuestionID("没有答案的题", QuestionChoice)); got != 0 {
		t.Errorf("a line with no answer was written: %d rows", got)
	}
	if got := countRows(t, resource, "notebook_entries", "question_id = ?",
		QuestionID(held.Text, held.Type)); got != 1 {
		t.Errorf("the question the bank already held is stored %d times, want once", got)
	}
}

// TestDBTheOverwriteReplacesTheQuestionFaceAndKeepsTheAnswerFace is what the
// report is promising when it calls a line "the same question, changed": the
// upsert's own rule, checked against the row rather than against the adapter
// that is supposed to implement it.
func TestDBTheOverwriteReplacesTheQuestionFaceAndKeepsTheAnswerFace(t *testing.T) {
	service, repository, resource := newImportShelf(t)
	held := sampleQuestion("银行里已经有的题")
	saveAt(t, repository, held, atSecond(0))
	testdb.Exec(t, resource, `UPDATE notebook_entries
		SET user_answer = ?, is_correct = TRUE, attempt_count = 1, bookmarked = TRUE
		WHERE question_id = ?`, "闭包是能读取外部变量的函数", QuestionID(held.Text, held.Type))

	changed := held
	changed.Explanation = "换一种说法"
	report, err := service.Import(context.Background(), file(lineOfQuestion(t, changed)), false, testCaller())
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	entry := report.Entries[0]
	if entry.Outcome != ImportWritable || entry.Conflict != ImportConflictSameQuestionDifferentContent {
		t.Fatalf("the line is %+v, want a writable line over the question that is there", entry)
	}
	row := readRow(t, resource, QuestionID(held.Text, held.Type))
	if row.Explanation == nil || *row.Explanation != "换一种说法" {
		t.Errorf("explanation = %v, want the line's own", row.Explanation)
	}
	if row.UserAnswer == nil || *row.UserAnswer != "闭包是能读取外部变量的函数" {
		t.Errorf("user_answer = %v, want the answer that was given left alone", row.UserAnswer)
	}
	if got := countRows(t, resource, "notebook_entries", ""); got != 1 {
		t.Errorf("the overwrite left %d rows, want the one it rewrote", got)
	}
}

// TestDBImportingTheSameFileTwiceAddsNothing is the duplicate a person actually
// makes: the file is imported again, and every line of it meets the question the
// first import wrote.
func TestDBImportingTheSameFileTwiceAddsNothing(t *testing.T) {
	service, _, resource := newImportShelf(t)
	input := file(
		line("第一道题", nil),
		line("第二道题", nil),
		line("第三道题", nil),
	)

	first, err := service.Import(context.Background(), input, false, testCaller())
	if err != nil {
		t.Fatalf("the first import = %v", err)
	}
	if first.Writable != 3 {
		t.Fatalf("the first import is %+v, want three writable lines", first)
	}
	second, err := service.Import(context.Background(), input, false, testCaller())
	if err != nil {
		t.Fatalf("the second import = %v", err)
	}
	if second.Writable != 0 || second.Duplicate != 3 {
		t.Errorf("the second import is %+v, want three lines the bank already answers", second)
	}
	if got := countRows(t, resource, "notebook_entries", ""); got != 3 {
		t.Errorf("the bank holds %d questions after importing the same file twice, want three", got)
	}
}

// TestDBTheSameTextUnderAnotherTypeIsFoundInTheBank is the third conflict kind
// against real storage: finding it is two lookups by content address, which is a
// claim about what an index returns rather than about what a map holds.
func TestDBTheSameTextUnderAnotherTypeIsFoundInTheBank(t *testing.T) {
	service, repository, resource := newImportShelf(t)
	held := sampleQuestion("什么是闭包")
	saveAt(t, repository, held, atSecond(0))

	report, err := service.Import(context.Background(), file(
		line(held.Text, map[string]any{
			"question_type":  "fill_blank",
			"options":        nil,
			"correct_answer": "能读取外部变量的函数",
		}),
	), false, testCaller())
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	entry := report.Entries[0]
	if entry.Outcome != ImportDuplicate || entry.Conflict != ImportConflictSameTextOtherType {
		t.Fatalf("the line is %+v, want a duplicate of the same text under another type", entry)
	}
	if want := QuestionID(held.Text, held.Type); entry.ConflictingQuestionID != want {
		t.Errorf("the conflict names %s, want the question in the bank, %s",
			entry.ConflictingQuestionID, want)
	}
	if got := countRows(t, resource, "notebook_entries", ""); got != 1 {
		t.Errorf("the bank holds %d questions, want the one the file did not add to", got)
	}
}
