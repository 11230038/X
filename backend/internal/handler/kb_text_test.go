package handler

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"backend/internal/kb"
)

// TestDocumentTextReturnsAWindowOfThatDocument pins the preview's coordinate
// and its identity: the window is read from the document the path names, at the
// character offsets the query names, and the answer carries the boundaries a
// reader marks the text with.
func TestDocumentTextReturnsAWindowOfThatDocument(t *testing.T) {
	document := testDocument()
	stub := &kbStub{
		document: document,
		window: kb.Window{
			Document: document, Text: "保修期是两年。",
			CharStart: 10, CharEnd: 17, TotalChars: 4200,
		},
		spans: []kb.ChunkSpan{{Ordinal: 0, CharStart: 0, CharEnd: 512}},
	}
	router := newKBRouter(stub)

	recorder := callAs(t, router, http.MethodGet,
		"/api/v1/knowledge-bases/kb_1/documents/doc_1/text?offset=10&limit=20", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Document   documentView    `json:"document"`
		Text       string          `json:"text"`
		Chunks     []chunkSpanView `json:"chunks"`
		CharStart  int64           `json:"char_start"`
		CharEnd    int64           `json:"char_end"`
		TotalChars int64           `json:"total_chars"`
	}
	kbData(t, recorder, &body)
	if body.Document.DocumentID != "doc_1" || body.Text != "保修期是两年。" {
		t.Errorf("document/text = %+v / %q", body.Document, body.Text)
	}
	if body.CharStart != 10 || body.CharEnd != 17 || body.TotalChars != 4200 {
		t.Errorf("span = %d-%d of %d", body.CharStart, body.CharEnd, body.TotalChars)
	}
	if len(body.Chunks) != 1 || body.Chunks[0].CharEnd != 512 {
		t.Errorf("chunks = %+v", body.Chunks)
	}
	if stub.askedDocumentID != "doc_1" || stub.askedOffset != 10 || stub.askedLimit != 20 {
		t.Errorf("the domain was asked for %q at %d/%d",
			stub.askedDocumentID, stub.askedOffset, stub.askedLimit)
	}
}

// TestDocumentTextKeepsTheDocumentInsideTheBaseItWasAskedFrom is the path's
// half of the ownership check: a document id is enough to find a row, and the
// base in the path is what says this request is about that row.
func TestDocumentTextKeepsTheDocumentInsideTheBaseItWasAskedFrom(t *testing.T) {
	document := testDocument()
	document.KBID = "kb_2"
	stub := &kbStub{document: document}
	router := newKBRouter(stub)

	recorder := callAs(t, router, http.MethodGet,
		"/api/v1/knowledge-bases/kb_1/documents/doc_1/text", "")

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.askedDocumentID != "" {
		t.Errorf("the text was read from another base's document: %q", stub.askedDocumentID)
	}
}

// TestDocumentTextRejectsANegativeOffset keeps the coordinate honest: character
// offsets start at zero, and a negative one is a client's mistake rather than a
// window clamped to the front.
func TestDocumentTextRejectsANegativeOffset(t *testing.T) {
	stub := &kbStub{document: testDocument()}
	router := newKBRouter(stub)

	recorder := callAs(t, router, http.MethodGet,
		"/api/v1/knowledge-bases/kb_1/documents/doc_1/text?offset=-1", "")

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.askedDocumentID != "" {
		t.Error("the domain was asked for text after the query was refused")
	}
}

// TestDocumentTextReportsAMissingDocument keeps the preview's failure the same
// one the rest of the base answers with: a document the caller cannot see is
// not found, not forbidden.
func TestDocumentTextReportsAMissingDocument(t *testing.T) {
	stub := &kbStub{err: fmt.Errorf("%w: no such document", kb.ErrNotFound)}
	router := newKBRouter(stub)

	recorder := callAs(t, router, http.MethodGet,
		"/api/v1/knowledge-bases/kb_1/documents/doc_1/text", "")

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"code":"not_found"`) {
		t.Errorf("body = %s", recorder.Body.String())
	}
}
