package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"backend/internal/authz"
	"backend/internal/event"
	"backend/internal/kb"
	"backend/internal/middleware"
	"backend/internal/parse"
	"github.com/gin-gonic/gin"
)

// kbStub answers from what a test put in and records what it was asked for.
type kbStub struct {
	base      kb.KnowledgeBase
	bases     []kb.KnowledgeBase
	documents []kb.Document
	document  kb.Document
	window    kb.Window
	spans     []kb.ChunkSpan
	retrieval kb.Retrieval
	err       error

	createdName        string
	createdDescription string
	askedKBID          string
	askedFileID        string
	askedDocumentID    string
	askedOffset        int64
	askedLimit         int64
	removed            string
	rebuilt            string
	searched           kb.RetrieveInput
}

func (s *kbStub) CreateKnowledgeBase(_ context.Context, name, description string, caller authz.Caller) (kb.KnowledgeBase, error) {
	s.createdName, s.createdDescription = name, description
	return s.base, s.err
}

func (s *kbStub) KnowledgeBases(_ context.Context, caller authz.Caller) ([]kb.KnowledgeBase, error) {
	return s.bases, s.err
}

func (s *kbStub) KnowledgeBase(_ context.Context, kbID string, caller authz.Caller) (kb.KnowledgeBase, error) {
	s.askedKBID = kbID
	return s.base, s.err
}

func (s *kbStub) SetDefaultKnowledgeBase(_ context.Context, kbID string, caller authz.Caller) error {
	s.askedKBID = kbID
	return s.err
}

func (s *kbStub) DeleteKnowledgeBase(_ context.Context, kbID string, caller authz.Caller) error {
	s.askedKBID = kbID
	return s.err
}

func (s *kbStub) Documents(_ context.Context, kbID string, caller authz.Caller) ([]kb.Document, error) {
	s.askedKBID = kbID
	return s.documents, s.err
}

func (s *kbStub) Document(_ context.Context, documentID string, caller authz.Caller) (kb.Document, error) {
	return s.document, s.err
}

func (s *kbStub) ReadWindow(_ context.Context, documentID string, offset, limit int64, caller authz.Caller) (kb.Window, error) {
	s.askedDocumentID, s.askedOffset, s.askedLimit = documentID, offset, limit
	return s.window, s.err
}

func (s *kbStub) ChunkSpans(_ context.Context, documentID string, caller authz.Caller) ([]kb.ChunkSpan, error) {
	s.askedDocumentID = documentID
	return s.spans, s.err
}

func (s *kbStub) AddDocument(_ context.Context, fileID, kbID string, caller authz.Caller) (kb.Document, error) {
	s.askedFileID, s.askedKBID = fileID, kbID
	return s.document, s.err
}

func (s *kbStub) RemoveDocument(_ context.Context, documentID string, caller authz.Caller) error {
	s.removed = documentID
	return s.err
}

func (s *kbStub) RebuildDocument(_ context.Context, documentID string, caller authz.Caller) (kb.Document, error) {
	s.rebuilt = documentID
	return s.document, s.err
}

func (s *kbStub) Retrieve(_ context.Context, input kb.RetrieveInput) (kb.Retrieval, error) {
	s.searched = input
	return s.retrieval, s.err
}

func newKBRouter(stub *kbStub) *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := NewKBHandler(stub, testHandlerLogger())
	router := newKBTestRouter(handler)
	return router
}

func newKBTestRouter(handler *KBHandler) *gin.Engine {
	router := gin.New()
	router.Use(middleware.RequestID())
	guarded := guard()
	router.POST("/api/v1/knowledge-bases", guarded, handler.Create)
	router.GET("/api/v1/knowledge-bases", guarded, handler.List)
	router.GET("/api/v1/knowledge-bases/:kbID", guarded, handler.Get)
	router.DELETE("/api/v1/knowledge-bases/:kbID", guarded, handler.Delete)
	router.POST("/api/v1/knowledge-bases/:kbID/documents", guarded, handler.AddDocument)
	router.GET("/api/v1/knowledge-bases/:kbID/documents/:documentID/text", guarded, handler.DocumentText)
	router.DELETE("/api/v1/knowledge-bases/:kbID/documents/:documentID", guarded, handler.RemoveDocument)
	router.POST("/api/v1/knowledge-bases/:kbID/rebuild", guarded, handler.Rebuild)
	router.POST("/api/v1/knowledge-bases/:kbID/search", guarded, handler.Search)
	router.POST("/api/v1/knowledge-bases/:kbID/default", guarded, handler.SetDefault)
	return router
}

// kbData decodes the data envelope's payload into out. A response with no data
// is left to the test to notice, since the tests that send one assert on the
// status and the error code instead.
func kbData(t *testing.T, recorder *httptest.ResponseRecorder, out any) {
	t.Helper()
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if len(envelope.Data) == 0 {
		t.Fatalf("the response carries no data: %s", recorder.Body.String())
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		t.Fatalf("data is not the expected shape: %v (%s)", err, envelope.Data)
	}
}

func testBase() kb.KnowledgeBase {
	return kb.KnowledgeBase{
		ID: "kb_1", Name: "手册", Description: "产品手册",
		Provider: kb.ProviderNative, EmbeddingModel: "embedding-3",
		IsDefault: true, Documents: 2, ReadyDocuments: 1,
		CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(2, 0).UTC(),
	}
}

func testDocument() kb.Document {
	return kb.Document{
		ID: "doc_1", KBID: "kb_1", FileID: "lib_1", Filename: "手册.pdf",
		Engine: "docling", TextChars: 4200, ChunkSize: 512, ChunkOverlap: 50,
		ActiveRevision: 3, VectorState: kb.VectorStatePending,
		CreatedAt: time.Unix(3, 0).UTC(), UpdatedAt: time.Unix(4, 0).UTC(),
	}
}

func TestCreateKnowledgeBaseAsksTheDomainForIt(t *testing.T) {
	stub := &kbStub{base: testBase()}
	router := newKBRouter(stub)

	recorder := callAs(t, router, http.MethodPost, "/api/v1/knowledge-bases", `{"name":"手册","description":"产品手册"}`)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		KnowledgeBase knowledgeBaseView `json:"knowledge_base"`
	}
	kbData(t, recorder, &body)
	if body.KnowledgeBase.KBID != "kb_1" || !body.KnowledgeBase.IsDefault {
		t.Errorf("created = %+v", body.KnowledgeBase)
	}
	if stub.createdName != "手册" || stub.createdDescription != "产品手册" {
		t.Errorf("the domain was asked for %q / %q", stub.createdName, stub.createdDescription)
	}
}

func TestCreateRepeatsWhatTheDomainSaidAboutTheName(t *testing.T) {
	// The name rule belongs to the domain; the handler must not invent a second
	// one. What it owes the client is the domain's own sentence, without the
	// package prefix, which says nothing a client can use.
	stub := &kbStub{err: fmt.Errorf("%w: a knowledge base needs a name", kb.ErrInvalidInput)}
	router := newKBRouter(stub)

	recorder := callAs(t, router, http.MethodPost, "/api/v1/knowledge-bases", `{"name":""}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	raw := recorder.Body.String()
	if !strings.Contains(raw, "a knowledge base needs a name") || strings.Contains(raw, "kb:") {
		t.Errorf("body = %s", raw)
	}
}

func TestGetReturnsTheBaseAndItsParseProgress(t *testing.T) {
	// KB-03's build progress is per document, so the detail carries the
	// documents: a client watching a rebuild is watching vector_state.
	stub := &kbStub{base: testBase(), documents: []kb.Document{testDocument()}}
	router := newKBRouter(stub)

	recorder := callAs(t, router, http.MethodGet, "/api/v1/knowledge-bases/kb_1", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var body knowledgeBaseDetailView
	kbData(t, recorder, &body)
	if body.KnowledgeBase.KBID != "kb_1" || body.KnowledgeBase.ReadyDocuments != 1 {
		t.Errorf("base = %+v", body.KnowledgeBase)
	}
	if len(body.Documents) != 1 || body.Documents[0].VectorState != kb.VectorStatePending {
		t.Fatalf("documents = %+v", body.Documents)
	}
	// The text does not travel in a list: a document is two hundred thousand
	// characters at the limit, and there is an endpoint that reads a window of it.
	if strings.Contains(recorder.Body.String(), "extracted_text") {
		t.Errorf("the detail carries the extracted text: %s", recorder.Body.String())
	}
	if stub.askedKBID != "kb_1" {
		t.Errorf("the domain was asked for %q", stub.askedKBID)
	}
}

func TestMissingThingsAreNotFound(t *testing.T) {
	stub := &kbStub{err: fmt.Errorf("%w: knowledge base kb_9", kb.ErrNotFound)}
	router := newKBRouter(stub)

	for _, test := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/knowledge-bases/kb_9", ""},
		{http.MethodDelete, "/api/v1/knowledge-bases/kb_9", ""},
		{http.MethodPost, "/api/v1/knowledge-bases/kb_9/documents", `{"file_id":"lib_1"}`},
		{http.MethodPost, "/api/v1/knowledge-bases/kb_9/rebuild", `{"document_id":"doc_1"}`},
		{http.MethodPost, "/api/v1/knowledge-bases/kb_9/default", ""},
		{http.MethodDelete, "/api/v1/knowledge-bases/kb_9/documents/doc_1", ""},
	} {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			recorder := callAs(t, router, test.method, test.path, test.body)
			if recorder.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404; body=%s", recorder.Code, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), `"code":"not_found"`) {
				t.Errorf("body = %s", recorder.Body.String())
			}
		})
	}
}

func TestDeleteAnswersWithNoContent(t *testing.T) {
	stub := &kbStub{}
	router := newKBRouter(stub)

	recorder := callAs(t, router, http.MethodDelete, "/api/v1/knowledge-bases/kb_1", "")

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.askedKBID != "kb_1" {
		t.Errorf("the domain was asked to delete %q", stub.askedKBID)
	}
}

func TestListReturnsEveryBase(t *testing.T) {
	stub := &kbStub{bases: []kb.KnowledgeBase{testBase()}}
	router := newKBRouter(stub)

	recorder := callAs(t, router, http.MethodGet, "/api/v1/knowledge-bases", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var body knowledgeBaseListView
	kbData(t, recorder, &body)
	if len(body.KnowledgeBases) != 1 || body.KnowledgeBases[0].Documents != 2 {
		t.Errorf("bases = %+v", body.KnowledgeBases)
	}
}

func TestAddDocumentParsesTheFileIntoTheBase(t *testing.T) {
	stub := &kbStub{document: testDocument()}
	router := newKBRouter(stub)

	recorder := callAs(t, router, http.MethodPost, "/api/v1/knowledge-bases/kb_1/documents", `{"file_id":"lib_1"}`)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	var body documentViewResponse
	kbData(t, recorder, &body)
	if body.Document.DocumentID != "doc_1" || body.Document.VectorState != kb.VectorStatePending {
		t.Errorf("document = %+v", body.Document)
	}
	if stub.askedFileID != "lib_1" || stub.askedKBID != "kb_1" {
		t.Errorf("the domain was asked for file %q in %q", stub.askedFileID, stub.askedKBID)
	}
}

func TestAnUnreadableFileIsAnsweredWithItsOwnReason(t *testing.T) {
	// ATT-04: a parse failure reaches the client with the code and the sentence
	// the parser wrote for whoever uploaded the file, rather than as a 500.
	tests := []struct {
		name       string
		parseError *parse.Error
		wantStatus int
	}{
		{
			name: "a format this build cannot read",
			parseError: &parse.Error{
				Code: parse.CodeUnsupportedFormat, Engine: parse.EngineText,
				Message: "这个格式需要 PDF 或 Office 引擎，本服务没有配置",
			},
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name: "the engine did not answer",
			parseError: &parse.Error{
				Code: parse.CodeParserUnavailable, Engine: parse.EngineDocling,
				Message: "解析服务没有响应，请稍后再试",
			},
			wantStatus: http.StatusServiceUnavailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := newKBRouter(&kbStub{err: test.parseError})

			recorder := callAs(t, router, http.MethodPost, "/api/v1/knowledge-bases/kb_1/documents", `{"file_id":"lib_1"}`)

			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			raw := recorder.Body.String()
			if !strings.Contains(raw, string(test.parseError.Code)) || !strings.Contains(raw, test.parseError.Message) {
				t.Errorf("body = %s", raw)
			}
		})
	}
}

func TestRemoveDocumentRefusesOneInAnotherBase(t *testing.T) {
	// A document id finds a row; the path is what says which base this request
	// is about. Without the check, a client holding one base's path could delete
	// another's document and nothing afterwards would look wrong.
	stub := &kbStub{document: kb.Document{ID: "doc_1", KBID: "kb_2"}}
	router := newKBRouter(stub)

	recorder := callAs(t, router, http.MethodDelete, "/api/v1/knowledge-bases/kb_1/documents/doc_1", "")

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.removed != "" {
		t.Errorf("a document of another base was removed: %q", stub.removed)
	}
}

func TestRebuildNeedsTheDocumentItIsRebuilding(t *testing.T) {
	stub := &kbStub{document: testDocument()}
	router := newKBRouter(stub)

	if recorder := callAs(t, router, http.MethodPost, "/api/v1/knowledge-bases/kb_1/rebuild", ""); recorder.Code != http.StatusBadRequest {
		t.Fatalf("no body: status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}

	recorder := callAs(t, router, http.MethodPost, "/api/v1/knowledge-bases/kb_1/rebuild", `{"document_id":"doc_1"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var body documentViewResponse
	kbData(t, recorder, &body)
	if stub.rebuilt != "doc_1" || body.Document.ActiveRevision != 3 {
		t.Errorf("rebuild = %q, document = %+v", stub.rebuilt, body.Document)
	}
}

func TestSearchReturnsTheSourcesWithTheirLocators(t *testing.T) {
	// KB-05: the search view is the diagnosis surface, so a source keeps its
	// locator, its engine and its score — the same source object a turn hands
	// the model and the user.
	stub := &kbStub{retrieval: kb.Retrieval{Sources: []event.Source{{
		Index: 1, DocumentID: "doc_1", FileID: "lib_1", Title: "手册.pdf",
		Engine: "docling", Score: 0.5, Revision: 3,
		Locator: event.Locator{Kind: "chunk", ChunkID: "chk_1", CharStart: 100, CharEnd: 200},
		Snippet: "保修期是两年。",
	}}}}
	router := newKBRouter(stub)

	recorder := callAs(t, router, http.MethodPost, "/api/v1/knowledge-bases/kb_1/search", `{"query":"保修期多久","top_k":3}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var body searchResultView
	kbData(t, recorder, &body)
	if len(body.Sources) != 1 || body.Sources[0].Locator.CharStart != 100 || body.Sources[0].Score != 0.5 {
		t.Errorf("sources = %+v", body.Sources)
	}
	// The base in the path is the whole scope: a search endpoint that quietly
	// fell back to the default base would answer with material nobody asked for.
	if len(stub.searched.KBIDs) != 1 || stub.searched.KBIDs[0] != "kb_1" {
		t.Errorf("scope = %v", stub.searched.KBIDs)
	}
	if stub.searched.Query != "保修期多久" || stub.searched.TopK != 3 {
		t.Errorf("search = %+v", stub.searched)
	}
}

func TestSearchWithNoMatchesIsAnEmptyListNotAnError(t *testing.T) {
	// A search endpoint always has a query: an empty body is a mistake about the
	// call, and the domain's rule about the query's length is the one that
	// answers "a", below.
	stub := &kbStub{}
	router := newKBRouter(stub)

	recorder := callAs(t, router, http.MethodPost, "/api/v1/knowledge-bases/kb_1/search", `{"query":"保修期多久"}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	// null and [] are different things to a client: one says "nothing matched",
	// the other says "the field is not there".
	if !strings.Contains(recorder.Body.String(), `"sources":[]`) {
		t.Errorf("body = %s", recorder.Body.String())
	}
}

func TestSearchRepeatsTheQueryRule(t *testing.T) {
	stub := &kbStub{err: fmt.Errorf("%w: it needs at least 2 characters", kb.ErrQueryTooShort)}
	router := newKBRouter(stub)

	recorder := callAs(t, router, http.MethodPost, "/api/v1/knowledge-bases/kb_1/search", `{"query":"a"}`)

	raw := recorder.Body.String()
	if recorder.Code != http.StatusBadRequest || !strings.Contains(raw, `"code":"query_too_short"`) {
		t.Fatalf("status = %d, body = %s", recorder.Code, raw)
	}
	if !strings.Contains(raw, "it needs at least 2 characters") {
		t.Errorf("body = %s", raw)
	}
}

func TestSetDefaultAnswersWithTheBase(t *testing.T) {
	stub := &kbStub{base: testBase()}
	router := newKBRouter(stub)

	recorder := callAs(t, router, http.MethodPost, "/api/v1/knowledge-bases/kb_1/default", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		KnowledgeBase knowledgeBaseView `json:"knowledge_base"`
	}
	kbData(t, recorder, &body)
	// The mark is the one field a client cannot work out from the request, which
	// is why the answer carries the base rather than a bare 204.
	if !body.KnowledgeBase.IsDefault || stub.askedKBID != "kb_1" {
		t.Errorf("base = %+v, asked %q", body.KnowledgeBase, stub.askedKBID)
	}
}

func TestAnUnconfiguredKnowledgeBaseAnswersRatherThanPanicking(t *testing.T) {
	// The router holds the handler before its dependencies are wired in a test
	// that only needs the routes, and a nil service must be an answer.
	gin.SetMode(gin.TestMode)
	router := newKBTestRouter(NewKBHandler(nil, nil))

	recorder := callAs(t, router, http.MethodGet, "/api/v1/knowledge-bases", "")

	if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), "knowledge_bases") {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}
