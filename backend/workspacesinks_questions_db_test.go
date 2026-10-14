package main

// The two sinks whose rows are addressed rather than named: the question filed
// under the address its own text hashes to, and the collection that carries a
// catalogue and nothing else.
//
// The last case here is the divergence this stage reports rather than hides: a
// collection's documents are refused by a foreign key to library_files when the
// file is not on this instance, which is a sentence in 00007 and not a line of
// Go.
//
// The fixture and the two helpers are workspacesinks_db_test.go's.

import (
	"context"
	"errors"
	"testing"

	"backend/internal/kb"
	"backend/internal/notebook"
	"backend/internal/workspace"
)

// TestDBTheQuestionSinkFilesTheQuestionUnderItsOwnAddress pins the one thing
// this sink does that the other three do not: it goes through the bank's own
// save, so the address is computed rather than taken, and a package whose object
// is filed under an address its own text does not hash to is refused before
// anything is written.
func TestDBTheQuestionSinkFilesTheQuestionUnderItsOwnAddress(t *testing.T) {
	fixture := newSinkFixture(t)
	question := notebook.Question{
		Text: "什么是闭包", Type: notebook.QuestionChoice,
		Options: []notebook.Option{{Key: "A", Text: "一个函数"}, {Key: "B", Text: "一个变量"}},
	}
	address := notebook.QuestionID(question.Text, question.Type)
	ref := workspace.Ref{Kind: workspace.KindQuestion, ID: address}
	object := objectOf(t, ref, questionPayload{
		Entry: notebook.Entry{
			Question:   question,
			Categories: []string{"编程"},
			Answer:     notebook.Answer{Bookmarked: true},
		},
	})

	landed, err := fixture.questions.Write(context.Background(), fixture.caller, []workspace.Object{object})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if len(landed) != 1 || landed[0].ID != address {
		t.Fatalf("Write() reported %+v, want the address %s", landed, address)
	}
	if rows := rowsIn(t, fixture.resource, "notebook_entries", "question_id", address); rows != 1 {
		t.Fatalf("the question landed %d times, want once", rows)
	}
	// The bookmark travels: it is the user's own filing, and a notebook that
	// arrived without it would have lost the part somebody made by hand.
	var bookmarked bool
	if err := fixture.resource.GORM().Raw("SELECT bookmarked FROM notebook_entries WHERE question_id = ?", address).
		Scan(&bookmarked).Error; err != nil {
		t.Fatalf("read the question: %v", err)
	}
	if !bookmarked {
		t.Error("the bookmark did not travel with the question")
	}

	if err := fixture.questions.Undo(context.Background(), fixture.caller, landed); err != nil {
		t.Fatalf("Undo() error = %v", err)
	}
	if rows := rowsIn(t, fixture.resource, "notebook_entries", "", nil); rows != 0 {
		t.Errorf("the question outlived its recovery: %d rows", rows)
	}
}

// TestDBTheQuestionSinkRefusesAnObjectFiledUnderTheWrongAddress is the negative
// half of the case above, and it is the one a package that was edited by hand
// would meet. Nothing lands, because the row the recovery could take back is
// addressed by the ref and the row the bank would write is addressed by the
// text.
func TestDBTheQuestionSinkRefusesAnObjectFiledUnderTheWrongAddress(t *testing.T) {
	fixture := newSinkFixture(t)
	object := objectOf(t, workspace.Ref{Kind: workspace.KindQuestion, ID: "qn_notthisaddress"},
		questionPayload{Entry: notebook.Entry{
			Question: notebook.Question{Text: "什么是闭包", Type: notebook.QuestionChoice},
		}})

	if _, err := fixture.questions.Write(context.Background(), fixture.caller, []workspace.Object{object}); !errors.Is(err, workspace.ErrBadPackage) {
		t.Fatalf("Write() error = %v, want ErrBadPackage", err)
	}
	if rows := rowsIn(t, fixture.resource, "notebook_entries", "", nil); rows != 0 {
		t.Errorf("a question filed under somebody else's address was written anyway: %d rows", rows)
	}
}

// TestDBTheKnowledgeBaseSinkCarriesTheCatalogueAndNothingElse is the case §2.13
// leaves to the indexer: the collection and what was in it, without the text,
// the chunks or the vectors — a document that lands empty and pending, which is
// the state the worker already knows how to pick up.
func TestDBTheKnowledgeBaseSinkCarriesTheCatalogueAndNothingElse(t *testing.T) {
	fixture := newSinkFixture(t)
	fileID := addSinkLibraryFile(t, fixture.resource, fixture.owner)
	ref := workspace.Ref{Kind: workspace.KindKnowledgeBase, ID: "kb_1"}
	object := objectOf(t, ref, knowledgeBasePayload{
		KnowledgeBase: kb.KnowledgeBase{
			ID: "kb_1", Name: "操作系统", Provider: kb.ProviderNative,
			EmbeddingModel: "test-embed", CreatedAt: atSecond(0),
		},
		Documents: []kb.Document{{
			ID: "doc_1", FileID: fileID, Filename: fileID + ".md", Engine: "markdown",
			// The counts and the state a package carries are about the instance
			// it came from, and the write is what replaces them.
			TextChars: 4096, VectorState: kb.VectorStateReady,
			ChunkSize: 800, ChunkOverlap: 100, ActiveRevision: 1, CreatedAt: atSecond(0),
		}},
	})

	landed, err := fixture.bases.Write(context.Background(), fixture.caller, []workspace.Object{object})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	var row struct {
		TextChars   int64  `gorm:"column:text_chars"`
		VectorState string `gorm:"column:vector_state"`
	}
	if err := fixture.resource.GORM().Raw(
		"SELECT text_chars, vector_state FROM documents WHERE document_id = ?", "doc_1").
		Scan(&row).Error; err != nil {
		t.Fatalf("read the document: %v", err)
	}
	if row.TextChars != 0 || row.VectorState != kb.VectorStatePending {
		t.Errorf("the document landed as %+v, want no text and vectors pending", row)
	}
	if rows := rowsIn(t, fixture.resource, "document_chunks", "", nil); rows != 0 {
		t.Errorf("the import wrote %d chunks, and it carried none", rows)
	}

	if err := fixture.bases.Undo(context.Background(), fixture.caller, landed); err != nil {
		t.Fatalf("Undo() error = %v", err)
	}
	for _, table := range []string{"knowledge_bases", "documents", "document_chunks"} {
		if rows := rowsIn(t, fixture.resource, table, "", nil); rows != 0 {
			t.Errorf("%s outlived the recovery: %d rows", table, rows)
		}
	}
}

// TestDBTheKnowledgeBaseSinkCannotImportADocumentWithoutItsFile is the divergence
// this stage reports rather than hides.
//
// `documents.file_id` is NOT NULL with a foreign key to `library_files` (00007),
// and a package carries no files — a file's bytes are the library's. So a
// collection whose documents name files this instance does not have cannot be
// imported at all, and the database is what says so. The preview does not warn of
// it: the plan looks at collections, and a file is not a kind. What is left
// behind is a recoverable job, which is why this case checks both halves.
func TestDBTheKnowledgeBaseSinkCannotImportADocumentWithoutItsFile(t *testing.T) {
	fixture := newSinkFixture(t)
	ref := workspace.Ref{Kind: workspace.KindKnowledgeBase, ID: "kb_1"}
	object := objectOf(t, ref, knowledgeBasePayload{
		KnowledgeBase: kb.KnowledgeBase{
			ID: "kb_1", Name: "操作系统", Provider: kb.ProviderNative,
			EmbeddingModel: "test-embed", CreatedAt: atSecond(0),
		},
		Documents: []kb.Document{{
			ID: "doc_1", FileID: "lib_00000000000000000000000000000000",
			Filename: "nowhere.md", Engine: "markdown",
			ChunkSize: 800, ChunkOverlap: 100, ActiveRevision: 1, CreatedAt: atSecond(0),
		}},
	})

	landed, err := fixture.bases.Write(context.Background(), fixture.caller, []workspace.Object{object})
	if err == nil {
		t.Fatal("a collection whose file is not here was imported")
	}
	// The base's own row is written before its documents are, and it is recorded
	// before them too — so what a recovery has to take back is exactly what the
	// sink reported on its way out.
	if len(landed) != 1 {
		t.Fatalf("Write() reported %+v, want the one row it wrote before the refusal", landed)
	}
	if rows := rowsIn(t, fixture.resource, "documents", "", nil); rows != 0 {
		t.Errorf("the refused document left %d rows behind", rows)
	}
	if err := fixture.bases.Undo(context.Background(), fixture.caller, landed); err != nil {
		t.Fatalf("Undo() error = %v", err)
	}
	if rows := rowsIn(t, fixture.resource, "knowledge_bases", "", nil); rows != 0 {
		t.Errorf("the collection outlived the recovery: %d rows", rows)
	}
}
