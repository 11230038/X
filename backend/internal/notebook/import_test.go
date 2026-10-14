package notebook

// What the batch import decides, over the recording repository beside these
// tests (§2.13).
//
// Three claims are pinned here and they are the three the design makes. A
// preview is zero writes, stated as a count of what reached storage. The report
// and the commit are one walk over one file, stated by running both on the same
// input and comparing them line by line. And every conflict the report can name
// is a conflict the bank's own identity produces — the same address, the same
// address with another face, the same text under another type.
//
// What a line has to look like to be a question is the fourth: the bar is the
// one save_question uses, so that the two doors onto this bank store the same
// questions, and the table below is the whole of what that bar refuses.

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

// line is one line of an import file, written from the fields a test cares about
// and defaulted to a question that is complete. A field set to nil is left out
// of the line, which is how a test writes a line that is missing one.
func line(text string, overrides map[string]any) string {
	fields := map[string]any{
		"question":       text,
		"question_type":  "choice",
		"options":        []map[string]string{{"key": "A", "text": "甲"}, {"key": "B", "text": "乙"}},
		"correct_answer": "A",
		"explanation":    "因为甲是对的",
	}
	for name, value := range overrides {
		fields[name] = value
	}
	for name, value := range fields {
		if value == nil {
			delete(fields, name)
		}
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		panic("a test line could not be encoded: " + err.Error())
	}
	return string(encoded)
}

// file is a file of lines, the way a person writes one: a newline after every
// line, including the last.
func file(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

// theSameQuestionAs is the question a default line describes, so that a test
// about a conflict can put it in the bank.
func theSameQuestionAs(text string) Question {
	return Question{
		Text:          text,
		Type:          QuestionChoice,
		Options:       []Option{{Key: "A", Text: "甲"}, {Key: "B", Text: "乙"}},
		CorrectAnswer: "A",
		Explanation:   "因为甲是对的",
	}
}

// TestTheImportReadsAFileOfQuestions is the plain case: three lines, three
// questions, each addressed by its content like every other question in this
// bank.
func TestTheImportReadsAFileOfQuestions(t *testing.T) {
	service, repository := newFakeService(t)

	report, err := service.Import(context.Background(), file(
		line("什么是闭包", nil),
		line("下面哪个是闭包", nil),
		line("闭包捕获的是什么", nil),
	), true)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if report.Lines != 3 || report.Parsed != 3 || report.Writable != 3 {
		t.Fatalf("the report is %+v, want three writable lines", report)
	}
	texts := []string{"什么是闭包", "下面哪个是闭包", "闭包捕获的是什么"}
	for index, entry := range report.Entries {
		if entry.Line != index+1 {
			t.Errorf("entry %d is on line %d, want the line it is on in the file", index, entry.Line)
		}
		if want := QuestionID(texts[index], QuestionChoice); entry.QuestionID != want {
			t.Errorf("line %d is addressed %s, want the address of its text and type, %s",
				entry.Line, entry.QuestionID, want)
		}
		if entry.Conflict != ImportConflictNone || entry.Reason != "" {
			t.Errorf("a new question came back as %+v, want no conflict and no reason", entry)
		}
	}
	if len(repository.upserted) != 0 {
		t.Errorf("a preview wrote %d questions", len(repository.upserted))
	}
}

// TestAPreviewWritesNothing is §7.1's acceptance stated as a count of what
// reached storage. The report still says the lines are writable, because it
// describes the bank rather than the request: a preview that downgraded its own
// verdicts would be a preview of a different run.
func TestAPreviewWritesNothing(t *testing.T) {
	service, repository := newFakeService(t)

	report, err := service.Import(context.Background(), file(
		line("什么是闭包", nil),
		line("一道没有答案的题", map[string]any{"correct_answer": ""}),
		line("什么是闭包", nil),
	), true)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if !report.DryRun {
		t.Error("the report does not say it answered a preview")
	}
	if report.Writable != 1 || report.MissingAnswer != 1 || report.Duplicate != 1 {
		t.Fatalf("the report is %+v, want one writable, one missing an answer and one duplicate", report)
	}
	if len(repository.upserted) != 0 || len(repository.filed) != 0 {
		t.Errorf("a preview reached storage: %d questions and %d categories",
			len(repository.upserted), len(repository.filed))
	}
}

// TestTheReportAndTheCommitAreOneWalkOverOneFile is the design's one technical
// requirement (§2.13): a preview that classified lines by its own rules would be
// a preview that lies. The two runs are compared entry by entry, and the commit
// is then checked for having written exactly the lines the report named.
func TestTheReportAndTheCommitAreOneWalkOverOneFile(t *testing.T) {
	service, repository := newFakeService(t)
	input := file(
		line("什么是闭包", nil),
		line("哪一个是闭包", nil),
		line("没有答案的题", map[string]any{"correct_answer": ""}),
		line("什么是闭包", nil),
		// A line that is not a JSON object at all, written by hand rather than
		// through line(): the helper's whole job is to produce a line that is one.
		"这不是 JSON",
	)

	preview, err := service.Import(context.Background(), input, true)
	if err != nil {
		t.Fatalf("Import(dry run) error = %v", err)
	}
	commit, err := service.Import(context.Background(), input, false)
	if err != nil {
		t.Fatalf("Import(commit) error = %v", err)
	}
	if !slices.Equal(preview.Entries, commit.Entries) {
		t.Fatalf("the two reports differ:\npreview: %+v\ncommit:  %+v",
			preview.Entries, commit.Entries)
	}
	if preview.Writable != commit.Writable || preview.Duplicate != commit.Duplicate {
		t.Errorf("the counts differ: %+v against %+v", preview, commit)
	}

	// The commit wrote the lines the report called writable, and no others: the
	// set of what reached storage is the set the preview announced.
	written := make([]string, 0, len(repository.upserted))
	for _, question := range repository.upserted {
		written = append(written, question.QuestionID)
	}
	wanted := make([]string, 0, commit.Writable)
	for _, entry := range commit.Entries {
		if entry.Outcome == ImportWritable {
			wanted = append(wanted, entry.QuestionID)
		}
	}
	if !slices.Equal(written, wanted) {
		t.Errorf("the commit wrote %v, and the report said it would write %v", written, wanted)
	}
	if commit.Writable != 2 {
		t.Errorf("the report calls %d lines writable, want the two new questions", commit.Writable)
	}
}

// TestALineWithoutAnAnswerIsReportedAndNotWritten: a question nobody can answer
// is not a question, and the bucket is separate from "not a question at all"
// because it is the mistake a file is most likely to make.
func TestALineWithoutAnAnswerIsReportedAndNotWritten(t *testing.T) {
	service, repository := newFakeService(t)

	report, err := service.Import(context.Background(), file(
		line("一道没有答案的题", map[string]any{"correct_answer": ""}),
	), false)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	entry := report.Entries[0]
	if entry.Outcome != ImportMissingAnswer {
		t.Fatalf("the line is %+v, want a line missing its answer", entry)
	}
	if entry.Reason == "" || entry.Line != 1 {
		t.Errorf("the report does not say where the line is and what is wrong with it: %+v", entry)
	}
	if report.Parsed != 1 {
		t.Errorf("the report parsed %d lines, want one: the line is a question, it just has no answer",
			report.Parsed)
	}
	if len(repository.upserted) != 0 {
		t.Errorf("a line with no answer was written anyway: %+v", repository.upserted)
	}
}

// TestAQuestionTheBankAlreadyHoldsIsNotWrittenAgain is the duplicate the design
// names "同一道题": the same content address with the same content behind it.
// Importing the same file twice is this case, every line of it.
func TestAQuestionTheBankAlreadyHoldsIsNotWrittenAgain(t *testing.T) {
	service, repository := newFakeService(t)
	repository.withQuestion(theSameQuestionAs("什么是闭包"))

	report, err := service.Import(context.Background(), file(line("什么是闭包", nil)), false)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	entry := report.Entries[0]
	if entry.Outcome != ImportDuplicate || entry.Conflict != ImportConflictSameQuestion {
		t.Fatalf("the line is %+v, want a duplicate of the same question", entry)
	}
	if entry.ConflictingLine != 0 {
		t.Errorf("the conflict is blamed on line %d of a file with one line", entry.ConflictingLine)
	}
	if len(repository.upserted) != 0 {
		t.Errorf("a question the bank already holds was written again: %+v", repository.upserted)
	}
}

// TestTheSameQuestionWrittenDifferentlyIsAnOverwrite is the second conflict kind
// the report has to name: the same address with another face. It stays writable,
// because correcting a question through a file is exactly what the upsert is
// for, and the report says what it will replace.
func TestTheSameQuestionWrittenDifferentlyIsAnOverwrite(t *testing.T) {
	service, repository := newFakeService(t)
	held := theSameQuestionAs("什么是闭包")
	held.Explanation = "旧的说法"
	repository.withQuestion(held)

	report, err := service.Import(context.Background(), file(line("什么是闭包", nil)), false)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	entry := report.Entries[0]
	if entry.Outcome != ImportWritable || entry.Conflict != ImportConflictSameQuestionDifferentContent {
		t.Fatalf("the line is %+v, want a writable line that overwrites the question face", entry)
	}
	if entry.ConflictingQuestionID != QuestionID("什么是闭包", QuestionChoice) {
		t.Errorf("the conflict is named %s, want the question it will replace", entry.ConflictingQuestionID)
	}
	if len(repository.upserted) != 1 || repository.upserted[0].Explanation != "因为甲是对的" {
		t.Errorf("the commit wrote %+v, want the line's own explanation", repository.upserted)
	}
}

// TestSpacingAloneIsEnoughToMakeItAContentChange pins the edge of the comparison
// above: the address is computed over text whose whitespace has been folded, and
// the column holds the text as it was written. A line that differs from the
// stored question only in the width of a gap therefore does rewrite the stored
// text, and calling that "the same question" would be wrong about the one column
// a reader looks at first.
func TestSpacingAloneIsEnoughToMakeItAContentChange(t *testing.T) {
	service, repository := newFakeService(t)
	repository.withQuestion(theSameQuestionAs("什么是 闭包"))

	report, err := service.Import(context.Background(), file(line("什么是  闭包", nil)), false)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	entry := report.Entries[0]
	if want := QuestionID("什么是 闭包", QuestionChoice); entry.QuestionID != want {
		t.Fatalf("the two spellings are %s, want them to be one address", entry.QuestionID)
	}
	if entry.Outcome != ImportWritable || entry.Conflict != ImportConflictSameQuestionDifferentContent {
		t.Errorf("the line is %+v, want it reported as a rewrite of the stored text", entry)
	}
}

// TestTheSameTextUnderAnotherTypeIsADuplicate is the conflict the design names
// "不同 question_id 但题干相同是重复": one question to a reader, two questions to
// this bank. It is refused rather than written, and it names the question that
// is in the way.
func TestTheSameTextUnderAnotherTypeIsADuplicate(t *testing.T) {
	service, repository := newFakeService(t)
	fillIn := Question{
		Text: "什么是闭包", Type: QuestionFillBlank, CorrectAnswer: "函数", Explanation: "因为它捕获变量",
	}
	repository.withQuestion(fillIn)
	held := QuestionID(fillIn.Text, fillIn.Type)

	report, err := service.Import(context.Background(), file(line("什么是闭包", nil)), false)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	entry := report.Entries[0]
	if entry.Outcome != ImportDuplicate || entry.Conflict != ImportConflictSameTextOtherType {
		t.Fatalf("the line is %+v, want a duplicate of the same text under another type", entry)
	}
	if entry.ConflictingQuestionID != held {
		t.Errorf("the conflict names %s, want the question that is in the way, %s",
			entry.ConflictingQuestionID, held)
	}
	if !strings.Contains(entry.Reason, string(QuestionFillBlank)) {
		t.Errorf("the reason reads %q, want it to say which type the bank already holds", entry.Reason)
	}
	if len(repository.upserted) != 0 {
		t.Errorf("the same sentence was stored twice: %+v", repository.upserted)
	}
}

// TestAFileThatRepeatsItselfIsReportedAsADuplicate is the same rule seen from
// inside one file: the second line meets the first, which is why a preview has
// to remember what the walk has already accepted rather than only what storage
// holds.
func TestAFileThatRepeatsItselfIsReportedAsADuplicate(t *testing.T) {
	service, repository := newFakeService(t)

	report, err := service.Import(context.Background(), file(
		line("什么是闭包", nil),
		line("哪一个是闭包", nil),
		line("什么是闭包", nil),
	), false)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	entry := report.Entries[2]
	if entry.Outcome != ImportDuplicate || entry.Conflict != ImportConflictSameQuestion {
		t.Fatalf("the repeated line is %+v, want a duplicate", entry)
	}
	if entry.ConflictingLine != 1 {
		t.Errorf("the conflict is blamed on line %d, want the line of the file it repeats", entry.ConflictingLine)
	}
	if len(repository.upserted) != 2 {
		t.Errorf("the commit wrote %d questions, want the two distinct ones", len(repository.upserted))
	}
}

// TestALineThatIsNotAQuestionIsReportedWithItsLineNumber collects the bar. Every
// case is one line of a two-line file, so that the number in the report is
// checked as well as the verdict: a report that said what was wrong without
// saying where is a report nobody can act on.
func TestALineThatIsNotAQuestionIsReportedWithItsLineNumber(t *testing.T) {
	cases := []struct {
		name  string
		bad   string
		needs string
	}{
		{"not JSON at all", "这是一道题，但它不是 JSON", "not a question"},
		{"a JSON array", `["什么是闭包"]`, "not a question"},
		{"two questions on one line",
			line("什么是闭包", nil) + line("哪一个是闭包", nil), "more than one JSON value"},
		{"a field nobody reads", line("什么是闭包", map[string]any{"answer": "A"}), "unknown field"},
		{"a blank question", line(" ", nil), "the question text is blank"},
		{"a question of two characters", line("闭包", nil), "at least 4"},
		{"a type this bank does not have",
			line("什么是闭包", map[string]any{"question_type": "essay"}), "not a question type"},
		{"a choice question with one option",
			line("什么是闭包", map[string]any{"options": []map[string]string{{"key": "A", "text": "甲"}}}),
			"between 2 and 8 options"},
		{"options on a question that has none",
			line("什么是闭包", map[string]any{"question_type": "short_answer"}), "have no options"},
		{"an option with no key",
			line("什么是闭包", map[string]any{"options": []map[string]string{{"text": "甲"}, {"key": "B", "text": "乙"}}}),
			"missing its key"},
		{"two options with one key",
			line("什么是闭包", map[string]any{"options": []map[string]string{{"key": "a", "text": "甲"}, {"key": "A", "text": "乙"}}}),
			"two options are keyed"},
		{"an answer naming an option that is not offered",
			line("什么是闭包", map[string]any{"correct_answer": "C"}), "not one of the options"},
		{"a question with no explanation",
			line("什么是闭包", map[string]any{"explanation": ""}), "the explanation is 0 characters"},
		{"a difficulty this bank does not have",
			line("什么是闭包", map[string]any{"difficulty": "impossible"}), "is not a difficulty"},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			service, repository := newFakeService(t)
			report, err := service.Import(context.Background(),
				file(line("什么是闭包", nil), scenario.bad), false)
			if err != nil {
				t.Fatalf("Import() error = %v", err)
			}
			if report.Invalid != 1 || report.Writable != 1 {
				t.Fatalf("the report is %+v, want one invalid line and one writable", report)
			}
			entry := report.Entries[1]
			if entry.Line != 2 || entry.Outcome != ImportInvalid {
				t.Fatalf("the bad line is %+v, want an invalid line reported on line 2", entry)
			}
			if !strings.Contains(entry.Reason, scenario.needs) {
				t.Errorf("the reason reads %q, want it to say %q", entry.Reason, scenario.needs)
			}
			if entry.QuestionID != "" {
				t.Errorf("a line that is not a question was addressed %s", entry.QuestionID)
			}
			for _, written := range repository.upserted {
				if written.Text != "什么是闭包" {
					t.Errorf("a refused line reached storage: %+v", written)
				}
			}
		})
	}
}

// TestTheCountsAreTheLinesOfTheFile is the report's arithmetic: every line lands
// in exactly one bucket, blank lines are not lines, and the numbers add up to
// the number of lines the file holds.
func TestTheCountsAreTheLinesOfTheFile(t *testing.T) {
	service, repository := newFakeService(t)
	repository.withQuestion(theSameQuestionAs("银行里已经有的那道题"))

	report, err := service.Import(context.Background(),
		file(
			line("什么是闭包", nil),
			"",
			line("没有答案的题", map[string]any{"correct_answer": ""}),
			line("银行里已经有的那道题", nil),
			"这不是 JSON",
		)+"\n", false)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if report.Lines != 4 {
		t.Fatalf("the file is reported as %d lines, want the four that hold something", report.Lines)
	}
	if report.Writable != 1 || report.MissingAnswer != 1 || report.Duplicate != 1 || report.Invalid != 1 {
		t.Errorf("the counts are %+v, want one of each", report)
	}
	if report.Parsed != 3 {
		t.Errorf("the report parsed %d lines, want the three that are questions: %+v", report.Parsed, report)
	}
	if len(report.Entries) != report.Lines {
		t.Errorf("%d entries for %d lines", len(report.Entries), report.Lines)
	}
	// The blank lines did not move the numbering: the last entry is the fifth
	// physical line of the file.
	if last := report.Entries[len(report.Entries)-1].Line; last != 5 {
		t.Errorf("the last entry is on line %d, want the line it is on in the file", last)
	}
}

// TestTheAnswerIsStoredInTheFormTheJudgeReads pins the two conventions a line
// has to meet the bank's own: the option keys in the answer are the options'
// spelling in ascending order, and a fill-in answer keeps its accepted
// spellings. The judge reads these columns and nothing else.
func TestTheAnswerIsStoredInTheFormTheJudgeReads(t *testing.T) {
	service, repository := newFakeService(t)

	report, err := service.Import(context.Background(), file(
		line("下面哪个是闭包", map[string]any{"correct_answer": " b , a "}),
		line("闭包捕获的是什么", map[string]any{
			"question_type":  "fill_blank",
			"correct_answer": " 变量 | 变量的值 ",
			"options":        nil,
		}),
	), false)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if report.Writable != 2 {
		t.Fatalf("the report is %+v, want two writable lines", report)
	}
	stored := map[string]string{}
	for _, question := range repository.upserted {
		stored[question.Text] = question.CorrectAnswer
	}
	if got := stored["下面哪个是闭包"]; got != "A,B" {
		t.Errorf("the choice answer was stored as %q, want the options' own spelling in order", got)
	}
	if got := stored["闭包捕获的是什么"]; got != "变量|变量的值" {
		t.Errorf("the fill-in answer was stored as %q, want the spellings the judge splits on", got)
	}
}

// TestTheImportReportsWhatStorageSaid keeps the failures a caller can tell from
// a refusal: a bank that could not be read is not a report, and a write that
// failed is not a report either.
func TestTheImportReportsWhatStorageSaid(t *testing.T) {
	failure := errors.New("storage is down")

	service, repository := newFakeService(t)
	repository.entryErr = failure
	if _, err := service.Import(context.Background(), file(line("什么是闭包", nil)), true); !errors.Is(err, failure) {
		t.Errorf("Import() over a bank that could not be read = %v, want the failure storage reported", err)
	}

	service, repository = newFakeService(t)
	repository.upsertErr = failure
	if _, err := service.Import(context.Background(), file(line("什么是闭包", nil)), false); !errors.Is(err, failure) {
		t.Errorf("Import() over a write that failed = %v, want the failure storage reported", err)
	}
}
