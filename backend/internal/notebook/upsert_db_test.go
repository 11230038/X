package notebook

// The write path, checked against a real seekdb: that saving a question writes
// its question face and leaves the answer face alone, that the four columns
// saying where the question came from — the conversation and the mastery
// coordinate — are written once, and that a question already in the bank is
// rewritten rather than duplicated, including when two callers ask at the same
// moment.
//
// None of it is knowable from a double. The upsert is a statement pair whose
// second half is reached by the one behaviour a double has no reason to
// reproduce: MySQL reports zero changed rows for a row written back the values
// it already holds, and that is what sends an identical save down the
// insert-and-collide path.

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"

	"backend/internal/data/testdb"
)

// TestUpsertWritesTheQuestionFaceAndLeavesTheAnswerFaceEmpty is the split this
// package is built on, stated as a row.
func TestUpsertWritesTheQuestionFaceAndLeavesTheAnswerFaceEmpty(t *testing.T) {
	repository, resource := newDBRepository(t)
	given := sampleQuestion("什么是闭包")
	result := saveAt(t, repository, given, atSecond(0))

	if !result.Created {
		t.Fatal("the first save did not report inserting the row")
	}
	row := readRow(t, resource, result.QuestionID)
	if row.NotebookEntriesID != result.ID {
		t.Errorf("row id = %s, want the one the caller handed in, %s", row.NotebookEntriesID, result.ID)
	}
	if row.Question != given.Text || row.QuestionType != string(given.Type) {
		t.Errorf("stored %q as a %s, want %q as a %s",
			row.Question, row.QuestionType, given.Text, given.Type)
	}
	if !slices.Equal(storedOptions(t, row), given.Options) {
		t.Errorf("options = %+v, want the two the question was written with", storedOptions(t, row))
	}
	if !slices.Equal(storedKnowledgePoints(t, row), given.KnowledgePoints) {
		t.Errorf("knowledge points = %q, want %q", storedKnowledgePoints(t, row), given.KnowledgePoints)
	}
	if row.Difficulty == nil || *row.Difficulty != "medium" {
		t.Errorf("difficulty = %v, want medium", row.Difficulty)
	}
	if row.Source == nil || *row.Source != "knowledge_base" {
		t.Errorf("source = %v, want knowledge_base", row.Source)
	}
	if row.MaterialID == nil || *row.MaterialID != "doc_1" {
		t.Errorf("material_id = %v, want doc_1", row.MaterialID)
	}
	if row.CreatTime == nil || !row.CreatTime.Equal(atSecond(0)) {
		t.Errorf("creat_time = %v, want %v", row.CreatTime, atSecond(0))
	}

	// Nothing on the answer face, because nothing has been answered. update_time
	// is on that face too: an insert that filled it would be the question path
	// writing the review path's column.
	answerFace := []struct {
		column  string
		written bool
	}{
		{"update_time", row.UpdateTime != nil},
		{"user_answer", row.UserAnswer != nil},
		{"is_correct", row.IsCorrect != nil},
		{"result", row.Result != nil},
		{"resolved", row.Resolved != nil},
		{"attempt_count", row.AttemptCount != nil},
		{"bookmarked", row.Bookmarked != nil},
	}
	for _, field := range answerFace {
		if field.written {
			t.Errorf("a question nobody has answered has %s written on it", field.column)
		}
	}
}

// TestUpsertRewritesTheQuestionFaceAndNothingElse is what a second generation of
// the same question does to a record of answering it.
func TestUpsertRewritesTheQuestionFaceAndNothingElse(t *testing.T) {
	repository, resource := newDBRepository(t)
	given := sampleQuestion("什么是闭包")
	first := saveAt(t, repository, given, atSecond(0))

	// Somebody answered it in between, which is what the answer face is for.
	answered := atSecond(30)
	testdb.Exec(t, resource, `UPDATE notebook_entries
		SET user_answer = ?, is_correct = TRUE, result = 'right', resolved = TRUE,
		    attempt_count = 2, bookmarked = TRUE, update_time = ?
		WHERE question_id = ?`, "闭包是能读取外部变量的函数", answered, first.QuestionID)

	rewritten := given
	rewritten.Options = append(rewritten.Options, Option{Key: "C", Text: "丙"})
	rewritten.CorrectAnswer = "B"
	rewritten.Explanation = "换成乙了"
	rewritten.Difficulty = "hard"
	rewritten.Source = "attachment"
	rewritten.MaterialID = "file_9"
	rewritten.KnowledgePoints = []string{"闭包"}
	second := saveAt(t, repository, rewritten, atSecond(60))

	if second.Created {
		t.Fatal("rewriting a question reported inserting a row")
	}
	if second.ID != first.ID {
		t.Errorf("the rewrite made row %s, want the row that was there, %s", second.ID, first.ID)
	}
	row := readRow(t, resource, first.QuestionID)
	if row.Explanation == nil || *row.Explanation != "换成乙了" {
		t.Errorf("explanation = %v, want the rewritten one", row.Explanation)
	}
	if row.Difficulty == nil || *row.Difficulty != "hard" {
		t.Errorf("difficulty = %v, want hard", row.Difficulty)
	}
	if row.MaterialID == nil || *row.MaterialID != "file_9" {
		t.Errorf("material_id = %v, want file_9", row.MaterialID)
	}
	if !slices.Equal(storedOptions(t, row), rewritten.Options) {
		t.Errorf("options = %+v, want the rewritten set", storedOptions(t, row))
	}
	if !slices.Equal(storedKnowledgePoints(t, row), rewritten.KnowledgePoints) {
		t.Errorf("knowledge points = %q, want the rewritten ones", storedKnowledgePoints(t, row))
	}
	if row.CreatTime == nil || !row.CreatTime.Equal(atSecond(0)) {
		t.Errorf("creat_time = %v, want it left at %v", row.CreatTime, atSecond(0))
	}

	// The answer, and the time it was given, are exactly as they were.
	if row.UserAnswer == nil || *row.UserAnswer != "闭包是能读取外部变量的函数" {
		t.Errorf("user_answer = %v, want the answer that was given", row.UserAnswer)
	}
	if row.IsCorrect == nil || !*row.IsCorrect {
		t.Errorf("is_correct = %v, want TRUE", row.IsCorrect)
	}
	if row.Result == nil || *row.Result != "right" {
		t.Errorf("result = %v, want right", row.Result)
	}
	if row.Resolved == nil || !*row.Resolved {
		t.Errorf("resolved = %v, want TRUE", row.Resolved)
	}
	if row.AttemptCount == nil || *row.AttemptCount != 2 {
		t.Errorf("attempt_count = %v, want 2", row.AttemptCount)
	}
	if row.Bookmarked == nil || !*row.Bookmarked {
		t.Errorf("bookmarked = %v, want TRUE", row.Bookmarked)
	}
	if row.UpdateTime == nil || !row.UpdateTime.Equal(answered) {
		t.Errorf("update_time = %v, want it left at the answer's time, %v", row.UpdateTime, answered)
	}
}

// TestUpsertOfIdenticalValuesIsNotAnInsert walks the path the upsert takes
// whenever nothing about the question changed.
//
// The update reports zero changed rows for a row written back what it already
// holds, which is indistinguishable from "there is no such row" unless the code
// keeps going — so it inserts, meets the unique key on question_id, and finishes
// by updating after all. Read as a duplicate this would fail every time a
// conversation generated the same question twice.
func TestUpsertOfIdenticalValuesIsNotAnInsert(t *testing.T) {
	repository, resource := newDBRepository(t)
	given := sampleQuestion("什么是闭包")
	first := saveAt(t, repository, given, atSecond(0))
	second := saveAt(t, repository, given, atSecond(60))

	if second.Created {
		t.Fatal("writing back the values that were already stored reported an insert")
	}
	if second.ID != first.ID || second.QuestionID != first.QuestionID {
		t.Errorf("the second save reported row %s question %s, want %s and %s",
			second.ID, second.QuestionID, first.ID, first.QuestionID)
	}
	if count := countRows(t, resource, "notebook_entries", ""); count != 1 {
		t.Errorf("the bank holds %d rows, want the one", count)
	}
	if row := readRow(t, resource, second.QuestionID); !row.CreatTime.Equal(atSecond(0)) {
		t.Errorf("creat_time = %v, want it left at %v", row.CreatTime, atSecond(0))
	}
}

// TestConcurrentUpsertsOfOneQuestionLeaveOneRow is the reason the write is an
// update-first rather than a read followed by a write.
//
// A read would leave a window between asking whether the question is there and
// answering, and two turns generating the same question would both decide it was
// not. Here every writer races the same statement pair, and the unique key on
// question_id is what settles it: one insert goes through, the others collide
// and end as rewrites of the row that won.
func TestConcurrentUpsertsOfOneQuestionLeaveOneRow(t *testing.T) {
	repository, resource := newDBRepository(t)
	given := sampleQuestion("什么是闭包")
	given.QuestionID = QuestionID(given.Text, given.Type)

	const writers = 4
	// Each writer offers a different explanation, so that the row that survives
	// can be told apart from a row assembled out of several of them. The fields
	// that are not part of the address are the ones two generations of the same
	// question can disagree about; the text and the type they cannot.
	offered := make([]string, writers)
	var wait sync.WaitGroup
	results := make([]Upserted, writers)
	errs := make([]error, writers)
	for index := range writers {
		offered[index] = fmt.Sprintf("第 %d 个写入", index)
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			entryID, err := newID(entryIDPrefix)
			if err != nil {
				errs[index] = err
				return
			}
			question := given
			question.Explanation = offered[index]
			results[index], errs[index] = repository.UpsertQuestion(
				context.Background(), entryID, question, atSecond(0))
		}(index)
	}
	wait.Wait()

	created := 0
	ids := map[string]bool{}
	for index, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", index, err)
		}
		if results[index].Created {
			created++
		}
		ids[results[index].ID] = true
	}
	if created != 1 {
		t.Errorf("%d of %d writers reported inserting the row, want exactly one", created, writers)
	}
	if len(ids) != 1 {
		t.Errorf("the writers reported %d different row ids, want one", len(ids))
	}
	if count := countRows(t, resource, "notebook_entries", ""); count != 1 {
		t.Errorf("the bank holds %d rows, want one: the question is the same one", count)
	}

	// Whoever wrote last, the row holds one writer's whole question face rather
	// than a mixture: every one of these writes is one statement, so a field from
	// one writer beside a field from another is not something this can produce.
	// Which writer is not decided — that is what a race means — so the claim is
	// that the explanation is one of the ones offered.
	row := readRow(t, resource, given.QuestionID)
	if row.Explanation == nil || !slices.Contains(offered, *row.Explanation) {
		t.Errorf("the surviving explanation is %v, want one of the writers' %q", row.Explanation, offered)
	}
}

// TestUpsertKeepsTheConversationAQuestionCameFrom pins that session_id and
// turn_id are written once.
//
// They say where a question was first asked, not who owns it: the question is a
// shared asset and a later conversation regenerating it does not take it over.
// The old values are what a reader would use to find the turn the question came
// out of, and a rewrite that moved them would point that reader at the wrong
// one.
func TestUpsertKeepsTheConversationAQuestionCameFrom(t *testing.T) {
	repository, resource := newDBRepository(t)
	storeConversation(t, resource, "ses_1", "trn_1")
	storeConversation(t, resource, "ses_2", "trn_2")

	given := sampleQuestion("什么是闭包")
	given.SessionID, given.TurnID = "ses_1", "trn_1"
	first := saveAt(t, repository, given, atSecond(0))

	elsewhere := given
	elsewhere.SessionID, elsewhere.TurnID = "ses_2", "trn_2"
	elsewhere.Difficulty = "hard"
	saveAt(t, repository, elsewhere, atSecond(60))

	row := readRow(t, resource, first.QuestionID)
	if row.SessionID == nil || *row.SessionID != "ses_1" {
		t.Errorf("session_id = %v, want ses_1, the conversation it was first asked in", row.SessionID)
	}
	if row.TurnID == nil || *row.TurnID != "trn_1" {
		t.Errorf("turn_id = %v, want trn_1, the turn it came out of", row.TurnID)
	}
}

// TestUpsertFilesAQuestionOnThePathItCameFrom pins the mastery coordinate: the
// path and the knowledge point a question was generated from are written on the
// way in and stay where they were put (§2.2).
//
// The two columns have no foreign key to hold them — a knowledge point is an
// entry in `mastery_paths.state_json` rather than a row of its own, so there is
// nothing for a constraint to point at — which is why what a write leaves behind
// is read back out of the database here instead of being taken from the write.
func TestUpsertFilesAQuestionOnThePathItCameFrom(t *testing.T) {
	repository, resource := newDBRepository(t)
	generated := sampleQuestion("什么是闭包")
	generated.MasteryPathID, generated.KnowledgePointID = "mp_1", "kp_001"
	first := saveAt(t, repository, generated, atSecond(0))

	row := readRow(t, resource, first.QuestionID)
	if row.MasteryPathID == nil || *row.MasteryPathID != "mp_1" {
		t.Errorf("mastery_path_id = %v, want mp_1", row.MasteryPathID)
	}
	if row.KnowledgePointID == nil || *row.KnowledgePointID != "kp_001" {
		t.Errorf("knowledge_point_id = %v, want kp_001", row.KnowledgePointID)
	}

	// The same question generated again on another path is the same row, and the
	// coordinate stays where it was: where a question came from is a fact about
	// the first write, and a later path that regenerates it does not take it over.
	again := generated
	again.MasteryPathID, again.KnowledgePointID = "mp_2", "kp_002"
	again.Explanation = "在另一条路径里生成的"
	saveAt(t, repository, again, atSecond(60))
	row = readRow(t, resource, first.QuestionID)
	if row.MasteryPathID == nil || *row.MasteryPathID != "mp_1" ||
		row.KnowledgePointID == nil || *row.KnowledgePointID != "kp_001" {
		t.Errorf("the rewrite moved the question to %v/%v, want mp_1/kp_001",
			row.MasteryPathID, row.KnowledgePointID)
	}
	// The rewrite did its own job, or the line above is asserting about a write
	// that never landed.
	if row.Explanation == nil || *row.Explanation != "在另一条路径里生成的" {
		t.Errorf("explanation = %v, want the rewrite's", row.Explanation)
	}

	// A question generated in a conversation carries neither half, and a rewrite
	// of one of those on a path does not quietly fill them in: the column says
	// where the question was made, and this one was made in the chat.
	plain := sampleQuestion("闭包捕获的是什么")
	second := saveAt(t, repository, plain, atSecond(0))
	if row := readRow(t, resource, second.QuestionID); row.MasteryPathID != nil ||
		row.KnowledgePointID != nil {
		t.Errorf("a question generated in a conversation carries %v/%v, want neither",
			row.MasteryPathID, row.KnowledgePointID)
	}
	filed := plain
	filed.MasteryPathID, filed.KnowledgePointID = "mp_1", "kp_001"
	filed.Difficulty = "easy"
	saveAt(t, repository, filed, atSecond(60))
	row = readRow(t, resource, second.QuestionID)
	if row.MasteryPathID != nil || row.KnowledgePointID != nil {
		t.Errorf("a path write filed a question that was already in the bank as %v/%v",
			row.MasteryPathID, row.KnowledgePointID)
	}
	if row.Difficulty == nil || *row.Difficulty != "easy" {
		t.Errorf("difficulty = %v, want the rewrite's", row.Difficulty)
	}
}

// TestUpsertOfAWhitespaceVariantIsTheSameQuestion goes through the service, so
// that the address is the one the rule computes rather than one a test chose.
func TestUpsertOfAWhitespaceVariantIsTheSameQuestion(t *testing.T) {
	service, resource := newDBService(t)
	first := save(t, service, sampleQuestion("什么是 闭包"))
	second := save(t, service, sampleQuestion("什么是  闭包"))

	if second.QuestionID != first.QuestionID {
		t.Errorf("the two texts got addresses %s and %s, want one", first.QuestionID, second.QuestionID)
	}
	if second.Created {
		t.Error("a variant that folds to the same text reported inserting a row")
	}
	if second.ID != first.ID {
		t.Errorf("the variant made row %s, want %s", second.ID, first.ID)
	}
	if count := countRows(t, resource, "notebook_entries", ""); count != 1 {
		t.Errorf("the bank holds %d rows, want one", count)
	}
}

// TestUpsertOfDifferentTextIsADifferentQuestion is the other side of the same
// rule, and the reason a wrong merge is the failure worth designing against: two
// questions are two rows, and nothing rewrites the first one's text.
func TestUpsertOfDifferentTextIsADifferentQuestion(t *testing.T) {
	service, resource := newDBService(t)
	first := save(t, service, sampleQuestion("什么是闭包"))
	second := save(t, service, sampleQuestion("什么是作用域"))

	if second.QuestionID == first.QuestionID {
		t.Fatal("two different texts got one address")
	}
	if !second.Created {
		t.Error("a new question did not report inserting a row")
	}
	if count := countRows(t, resource, "notebook_entries", ""); count != 2 {
		t.Errorf("the bank holds %d rows, want two", count)
	}
	if row := readRow(t, resource, first.QuestionID); row.Question != "什么是闭包" {
		t.Errorf("the first question now reads %q, want it untouched", row.Question)
	}
}
