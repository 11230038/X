package notebook

// Reading one question, marking it, and the two refusals: a question that is not
// there, and a row that this package cannot read back.

import (
	"context"
	"errors"
	"slices"
	"testing"

	"backend/internal/data/testdb"
)

// TestEntryReadsBackWhatWasWritten closes the loop: a question saved is a
// question read, with the whole of both faces.
func TestEntryReadsBackWhatWasWritten(t *testing.T) {
	service, _ := newDBService(t)
	given := withCategories(sampleQuestion("什么是闭包"), "数学")
	given.MasteryPathID, given.KnowledgePointID = "mp_1", "kp_001"
	result := save(t, service, given)
	entry := entryOf(t, service, result.QuestionID)

	if entry.ID != result.ID {
		t.Errorf("row id = %s, want %s", entry.ID, result.ID)
	}
	if entry.Question.Text != given.Text || entry.Question.Type != given.Type {
		t.Errorf("read back %q as a %s, want %q as a %s",
			entry.Question.Text, entry.Question.Type, given.Text, given.Type)
	}
	if !slices.Equal(entry.Question.Options, given.Options) {
		t.Errorf("options = %+v, want %+v", entry.Question.Options, given.Options)
	}
	if !slices.Equal(entry.Question.KnowledgePoints, given.KnowledgePoints) {
		t.Errorf("knowledge points = %q, want %q", entry.Question.KnowledgePoints, given.KnowledgePoints)
	}
	if entry.Question.CorrectAnswer != "A" || entry.Question.Explanation != "因为甲是对的" {
		t.Errorf("answer %q explanation %q, want what was stored",
			entry.Question.CorrectAnswer, entry.Question.Explanation)
	}
	if entry.Question.Difficulty != "medium" || entry.Question.Source != "knowledge_base" {
		t.Errorf("difficulty %q source %q, want what was stored",
			entry.Question.Difficulty, entry.Question.Source)
	}
	if entry.Question.MaterialID != "doc_1" {
		t.Errorf("material_id = %q, want doc_1", entry.Question.MaterialID)
	}
	// The mastery coordinate is part of the question face, so a question read
	// back says which path's knowledge point it was generated for (§2.2): this
	// is what QN-02's "a related knowledge point" reads once there is one.
	if entry.Question.MasteryPathID != "mp_1" || entry.Question.KnowledgePointID != "kp_001" {
		t.Errorf("the question came back filed as %q/%q, want mp_1/kp_001",
			entry.Question.MasteryPathID, entry.Question.KnowledgePointID)
	}
	if entry.CreatedAt.IsZero() {
		t.Error("created_at is the zero time, want the time the row was written")
	}
	// And the answer face is empty rather than absent: nothing was answered.
	if entry.Answer.UserAnswer != "" || entry.Answer.IsCorrect != nil ||
		entry.Answer.Resolved != nil || entry.Answer.AttemptCount != 0 ||
		entry.Answer.Bookmarked || !entry.Answer.UpdatedAt.IsZero() {
		t.Errorf("answer = %+v, want the empty answer", entry.Answer)
	}
}

func TestEntryReportsAQuestionThatIsNotThere(t *testing.T) {
	repository, _ := newDBRepository(t)
	_, err := repository.Entry(context.Background(), "qn_000000000000000000000000")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Entry of a question that is not there = %v, want ErrNotFound", err)
	}
}

// TestEntryRefusesARowItCannotRead is why the JSON columns are decoded strictly.
// The column type keeps a value that is not JSON out; a value of the wrong shape
// is what gets in, and reading it as "a question with no options" would hand a
// caller a question that is not the one stored.
func TestEntryRefusesARowItCannotRead(t *testing.T) {
	repository, resource := newDBRepository(t)
	result := saveAt(t, repository, sampleQuestion("什么是闭包"), atSecond(0))
	testdb.Exec(t, resource, "UPDATE notebook_entries SET options = ? WHERE question_id = ?",
		`{"A":"甲"}`, result.QuestionID)

	if _, err := repository.Entry(context.Background(), result.QuestionID); err == nil {
		t.Fatal("a row whose options are not a list was read as if they were")
	}
}

// TestMarkSetsTheMarksAndIsRepeatable covers the marks that are set directly
// rather than by answering.
//
// The three states of resolved are the subject here: marking a question as not
// resolved writes FALSE, which is a different thing from the NULL it had, and
// the update that writes it changes no value the second time it is asked. A row
// count read as "no such question" would turn that repeated click into an error
// about a question that is right there.
func TestMarkSetsTheMarksAndIsRepeatable(t *testing.T) {
	repository, resource := newDBRepository(t)
	result := saveAt(t, repository, sampleQuestion("什么是闭包"), atSecond(0))
	ctx := context.Background()
	yes, no := true, false

	if err := repository.Mark(ctx, result.QuestionID, Change{Bookmarked: &yes, Resolved: &no}); err != nil {
		t.Fatalf("mark the question: %v", err)
	}
	row := readRow(t, resource, result.QuestionID)
	if row.Bookmarked == nil || !*row.Bookmarked {
		t.Errorf("bookmarked = %v, want TRUE", row.Bookmarked)
	}
	if row.Resolved == nil || *row.Resolved {
		t.Errorf("resolved = %v, want FALSE, which is not the NULL it had", row.Resolved)
	}
	if row.UpdateTime != nil {
		t.Errorf("update_time = %v, want NULL: a bookmark is not an answer", row.UpdateTime)
	}

	if err := repository.Mark(ctx, result.QuestionID, Change{Bookmarked: &yes}); err != nil {
		t.Fatalf("marking an already bookmarked question: %v", err)
	}
	if err := repository.Mark(ctx, result.QuestionID, Change{Resolved: &yes}); err != nil {
		t.Fatalf("resolving it: %v", err)
	}
	row = readRow(t, resource, result.QuestionID)
	if row.Resolved == nil || !*row.Resolved {
		t.Errorf("resolved = %v, want TRUE", row.Resolved)
	}
	if row.Bookmarked == nil || !*row.Bookmarked {
		t.Errorf("bookmarked = %v, want the mark from the earlier call left alone", row.Bookmarked)
	}

	err := repository.Mark(ctx, "qn_000000000000000000000000", Change{Bookmarked: &yes})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("marking a question that is not there = %v, want ErrNotFound", err)
	}
}
