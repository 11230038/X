package kb

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The passages of a document that answer a question come back first, numbered
// like any other source, with the document's own coordinates on them.
func TestLocateFindsThePassagesThatAnswerAQuery(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")
	document := h.addDocument(t, base.ID, "lib_one", attachmentText)

	found, err := h.service.Locate(context.Background(), document.ID, "熔断器 打开", 2)
	if err != nil {
		t.Fatalf("Locate() error = %v", err)
	}
	if len(found) == 0 {
		t.Fatal("nothing was found in a document that is about the query")
	}
	if len(found) > 2 {
		t.Fatalf("got %d sources, want at most the 2 asked for", len(found))
	}
	for index, source := range found {
		if source.Index != index+1 {
			t.Errorf("source %d has index %d", index, source.Index)
		}
		if source.Locator.Kind != locatorChunk {
			t.Errorf("source %d has locator kind %q", index, source.Locator.Kind)
		}
		if source.DocumentID != document.ID || source.FileID != "lib_one" {
			t.Errorf("source %d points at %s / %s", index, source.DocumentID, source.FileID)
		}
		if source.Snippet == "" {
			t.Errorf("source %d has no snippet", index)
		}
	}
	// The passage that holds the question's words outranks the ones that hold a
	// few of them in passing.
	if !strings.Contains(found[0].Snippet, "熔断器") {
		t.Errorf("the best passage is %q, which does not hold the question", found[0].Snippet)
	}
	if found[0].Score <= found[len(found)-1].Score {
		t.Errorf("scores are not in order: %v", found[0].Score)
	}
	if found[0].Score > 1 {
		t.Errorf("score = %v, want at most 1: it is a share of the question", found[0].Score)
	}
}

// A question the document does not answer returns the document's opening, not
// nothing: an attachment is material the model was given, and "these words are
// not in it" is not the same claim as "there is nothing here".
func TestLocateFallsBackToTheDocumentsOpening(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")
	document := h.addDocument(t, base.ID, "lib_one", attachmentText)

	found, err := h.service.Locate(context.Background(), document.ID, "量子色动力学", 2)
	if err != nil {
		t.Fatalf("Locate() error = %v", err)
	}
	if len(found) == 0 {
		t.Fatal("nothing came back for a question the document does not answer")
	}

	chunks := h.repo.activeChunks(document.ID)
	if found[0].Locator.Ordinal != chunks[0].Ordinal {
		t.Errorf("the fallback starts at ordinal %d, want the document's first chunk %d",
			found[0].Locator.Ordinal, chunks[0].Ordinal)
	}
	if found[0].Locator.CharStart != 0 {
		t.Errorf("the fallback starts at character %d, want 0", found[0].Locator.CharStart)
	}
	for index, source := range found {
		if source.Score != 0 {
			t.Errorf("source %d has score %v, want the zero of a passage that was not matched", index, source.Score)
		}
	}
}

func TestLocateReadsOnlyTheActiveRevision(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")
	document := h.addDocument(t, base.ID, "lib_one", attachmentText)
	if _, err := h.service.RebuildDocument(context.Background(), document.ID); err != nil {
		t.Fatalf("RebuildDocument() error = %v", err)
	}

	found, err := h.service.Locate(context.Background(), document.ID, "熔断器", 5)
	if err != nil {
		t.Fatalf("Locate() error = %v", err)
	}
	for _, source := range found {
		if source.Revision != 2 {
			t.Errorf("source %d is from revision %d, want the active one", source.Index, source.Revision)
		}
	}
}

func TestLocateRefusesAQueryItCannotTokenise(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")
	document := h.addDocument(t, base.ID, "lib_one", attachmentText)

	for _, query := range []string{"", "  ", "熔"} {
		if _, err := h.service.Locate(context.Background(), document.ID, query, 3); !errors.Is(err, ErrQueryTooShort) {
			t.Errorf("Locate(%q) error = %v, want ErrQueryTooShort", query, err)
		}
	}
}

func TestLocateReportsADocumentThatIsNotThere(t *testing.T) {
	h := newHarness(t)
	if _, err := h.service.Locate(context.Background(), "doc_missing", "熔断器", 3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

// The bigram set is what a query is reduced to, and it is the unit this build's
// full-text index uses. Spelling it out here is what makes the ranking above
// legible: case is folded and spaces are dropped, because the passage that
// answers a question does not have to be spaced and cased the same way.
func TestQueryBigramsFoldCaseAndDropSpaces(t *testing.T) {
	bigrams := queryBigrams("Closure 捕获")

	for _, bigram := range []string{"cl", "os", "ur", "捕获"} {
		if !bigrams[bigram] {
			t.Errorf("the bigram set is missing %q: %v", bigram, bigrams)
		}
	}
	// Nine characters make eight pairs, and the space between the two words is
	// not one of them.
	if len(bigrams) != 8 {
		t.Errorf("got %d bigrams, want 8: %v", len(bigrams), bigrams)
	}
	if bigrams[" "] || bigrams["e "] {
		t.Errorf("a space survived into the bigram set: %v", bigrams)
	}
}

func TestRankChunksPrefersThePassageThatHoldsThePhrase(t *testing.T) {
	chunks := []ChunkRecord{
		{ChunkID: "chk_a", Ordinal: 0, Content: "熔断器的状态可以打开，也可以半开。"},
		{ChunkID: "chk_b", Ordinal: 1, Content: "这一段只提到熔断器，没有别的。"},
		{ChunkID: "chk_c", Ordinal: 2, Content: "完全无关的一段话，说的是别的。"},
	}

	ranked := rankChunks(chunks, "熔断器打开", 3)

	if len(ranked) != 2 {
		t.Fatalf("got %d chunks, want the 2 that hold any part of the query: %+v", len(ranked), ranked)
	}
	if ranked[0].chunk.ChunkID != "chk_a" {
		t.Errorf("the best chunk is %s, want the one holding the phrase", ranked[0].chunk.ChunkID)
	}
	if ranked[0].score <= ranked[1].score {
		t.Errorf("scores are not ordered: %v then %v", ranked[0].score, ranked[1].score)
	}
}

func TestRankChunksBreaksTiesByOrdinal(t *testing.T) {
	// The same words in both, so the score cannot decide between them.
	chunks := []ChunkRecord{
		{ChunkID: "chk_a", Ordinal: 0, Content: "熔断器打开。"},
		{ChunkID: "chk_b", Ordinal: 1, Content: "熔断器打开。"},
	}

	ranked := rankChunks(chunks, "熔断器打开", 5)

	if len(ranked) != 2 {
		t.Fatalf("got %d chunks, want 2", len(ranked))
	}
	if ranked[0].score != ranked[1].score {
		t.Fatalf("the fixture is not a tie: %v and %v", ranked[0].score, ranked[1].score)
	}
	if ranked[0].chunk.Ordinal != 0 {
		t.Errorf("the tie went to ordinal %d, want the earlier chunk", ranked[0].chunk.Ordinal)
	}
}

// A document with no chunks at all — an empty file — is the one case where the
// fallback has nothing to fall back to. It is not an error: the document is
// there and it is empty.
func TestLocateOnADocumentWithNoChunks(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "kb")
	h.addFile("lib_empty", "empty.md", "")
	document, err := h.service.AddDocument(context.Background(), "lib_empty", base.ID)
	if err != nil {
		t.Fatalf("AddDocument(empty) error = %v", err)
	}

	found, err := h.service.Locate(context.Background(), document.ID, "熔断器", 3)
	if err != nil {
		t.Fatalf("Locate() error = %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("got %d sources from an empty document", len(found))
	}
}

const attachmentText = "熔断器有三种状态：关闭、打开和半开。\n\n" +
	"当失败次数超过阈值时，熔断器打开，后续请求直接失败，不再访问下游。\n\n" +
	"等待一段时间后进入半开状态，放行少量请求试探下游是否恢复。\n\n" +
	"这段文字与前面无关，只是为了把文档撑到好几个分块。"
