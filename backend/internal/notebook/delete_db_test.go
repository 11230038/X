package notebook

// Taking a question back out, checked against a real seekdb.
//
// The statement is one DELETE, and what makes it worth a real database is what
// the schema does around it: a question is the parent of its filing and of its
// review, and those two relationships are ON DELETE CASCADE from 00003 rather
// than anything this package arranges. A double can restate that; only the
// database can do it.
//
// The method has one caller — a workspace import that could not finish, taking
// back the rows it wrote (§2.13) — which is why the cases here are about rows,
// and why the second half of each is the scope: a question whose address is
// another account's is not there, the same as one that was never asked.

import (
	"context"
	"errors"
	"testing"

	"backend/internal/data/testdb"
)

func TestDBDeletingAQuestionTakesItsFilingAndItsReviewWithIt(t *testing.T) {
	repository, resource := newDBRepository(t)
	question := withCategories(sampleQuestion("什么是闭包"), "编程", "函数")
	saved := saveAt(t, repository, question, atSecond(0))
	row := readRow(t, resource, saved.QuestionID)

	// The two rows that only exist because the question does: where it is filed
	// and what review has made of it.
	if filed := countRows(t, resource, "notebook_entry_categories", "notebook_entries_id = ?", row.NotebookEntriesID); filed == 0 {
		t.Fatalf("the fixture filed the question under nothing")
	}
	testdb.Exec(t, resource, "INSERT INTO practice_review_state "+
		"(notebook_entries_id, first_wrong_time, due_time, review_count, lapses) VALUES (?, ?, ?, ?, ?)",
		row.NotebookEntriesID, atSecond(0), atSecond(60), 1, 0)
	testdb.Exec(t, resource, "INSERT INTO practice_review_events "+
		"(request_id, notebook_entries_id, user_answer, rating, review_time) VALUES (?, ?, ?, ?, ?)",
		"req_1", row.NotebookEntriesID, "A", 0.8, atSecond(0))

	if err := repository.DeleteQuestion(context.Background(), saved.QuestionID, testCaller()); err != nil {
		t.Fatalf("DeleteQuestion() error = %v", err)
	}
	if rows := countRows(t, resource, "notebook_entries", "question_id = ?", saved.QuestionID); rows != 0 {
		t.Errorf("the question is still there: %d rows", rows)
	}
	if rows := countRows(t, resource, "notebook_entry_categories", "notebook_entries_id = ?", row.NotebookEntriesID); rows != 0 {
		t.Errorf("the filing outlived the question: %d rows", rows)
	}
	if rows := countRows(t, resource, "practice_review_state", "notebook_entries_id = ?", row.NotebookEntriesID); rows != 0 {
		t.Errorf("the review state outlived the question: %d rows", rows)
	}
	if rows := countRows(t, resource, "practice_review_events", "notebook_entries_id = ?", row.NotebookEntriesID); rows != 0 {
		t.Errorf("the review events outlived the question: %d rows", rows)
	}
	// The category itself stays: it is a label the account made, and the last
	// question filed under it going away is not the label going away.
	if rows := countRows(t, resource, "notebook_categories", ""); rows == 0 {
		t.Errorf("the labels went with the question, and they are the account's rather than its")
	}
}

func TestDBDeletingAnotherAccountsQuestionFindsNothing(t *testing.T) {
	repository, resource := newDBRepository(t)
	// The question's address is a content address, so another account can hold the
	// same text — which is exactly the case the scope decides, and the reason the
	// scoped re-read in repository.go exists at all.
	saved := saveAtAs(t, repository, sampleQuestion("别人的题目"), atSecond(0), otherCaller())

	err := repository.DeleteQuestion(context.Background(), saved.QuestionID, testCaller())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteQuestion() error = %v, want ErrNotFound", err)
	}
	if rows := countRows(t, resource, "notebook_entries", "question_id = ?", saved.QuestionID); rows != 1 {
		t.Fatalf("another account's question was deleted anyway: %d rows left", rows)
	}
	// The owner's own delete is the same statement with a different scope, which is
	// what says the refusal above was about the scope rather than about the
	// statement naming the wrong key.
	if err := repository.DeleteQuestion(context.Background(), saved.QuestionID, otherCaller()); err != nil {
		t.Fatalf("the owner's DeleteQuestion() error = %v", err)
	}
	if rows := countRows(t, resource, "notebook_entries", "question_id = ?", saved.QuestionID); rows != 0 {
		t.Errorf("the owner's delete left %d rows", rows)
	}
}

func TestDBDeletingAQuestionThatIsNotThereIsNotFound(t *testing.T) {
	repository, _ := newDBRepository(t)
	if err := repository.DeleteQuestion(context.Background(), QuestionID("never asked", QuestionChoice), testCaller()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteQuestion() error = %v, want ErrNotFound", err)
	}
	// Deleting twice answers ErrNotFound the second time, which is what keeps a
	// recovery that ran twice from reporting that it removed two of something.
	saved := saveAt(t, repository, sampleQuestion("什么是闭包"), atSecond(0))
	if err := repository.DeleteQuestion(context.Background(), saved.QuestionID, testCaller()); err != nil {
		t.Fatalf("DeleteQuestion() error = %v", err)
	}
	if err := repository.DeleteQuestion(context.Background(), saved.QuestionID, testCaller()); !errors.Is(err, ErrNotFound) {
		t.Errorf("the second DeleteQuestion() error = %v, want ErrNotFound", err)
	}
}
