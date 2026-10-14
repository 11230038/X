package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"backend/internal/authz"
	"backend/internal/event"
	"backend/internal/httpx"
	"backend/internal/kb"
	"backend/internal/parse"
	"github.com/gin-gonic/gin"
)

// KnowledgeBases is the knowledge base use case consumed by the HTTP handler.
//
// It is the domain service's whole surface, which is wide for one handler: the
// routes below are one resource's CRUD, and every method here is one of its
// verbs. Splitting it would be splitting by who reads it, and the answer is
// every route here.
//
// Every method carries the caller, including the ones the route already names a
// collection for. The route says which collection by id, and an id is a thing a
// client writes down: what makes this request legitimate is that the collection
// is the caller's, so the account travels with the request rather than being
// assumed from the fact that a route exists.
type KnowledgeBases interface {
	CreateKnowledgeBase(ctx context.Context, name, description string, caller authz.Caller) (kb.KnowledgeBase, error)
	KnowledgeBases(ctx context.Context, caller authz.Caller) ([]kb.KnowledgeBase, error)
	KnowledgeBase(ctx context.Context, kbID string, caller authz.Caller) (kb.KnowledgeBase, error)
	SetDefaultKnowledgeBase(ctx context.Context, kbID string, caller authz.Caller) error
	DeleteKnowledgeBase(ctx context.Context, kbID string, caller authz.Caller) error
	Documents(ctx context.Context, kbID string, caller authz.Caller) ([]kb.Document, error)
	Document(ctx context.Context, documentID string, caller authz.Caller) (kb.Document, error)
	AddDocument(ctx context.Context, fileID, kbID string, caller authz.Caller) (kb.Document, error)
	RemoveDocument(ctx context.Context, documentID string, caller authz.Caller) error
	RebuildDocument(ctx context.Context, documentID string, caller authz.Caller) (kb.Document, error)
	Retrieve(ctx context.Context, input kb.RetrieveInput) (kb.Retrieval, error)
}

// KBHandler handles knowledge bases: the collections, the documents parsed into
// them, and the search that reads them back.
type KBHandler struct {
	bases  KnowledgeBases
	logger *slog.Logger
}

// NewKBHandler constructs the knowledge base HTTP handler.
func NewKBHandler(bases KnowledgeBases, logger *slog.Logger) *KBHandler {
	return &KBHandler{bases: bases, logger: logger}
}

// knowledgeBaseView is a collection as a client reads it.
type knowledgeBaseView struct {
	KBID        string `json:"kb_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Provider and EmbeddingModel are what this base was bound to when it was
	// created (KB-02). They are read-only here: changing either is a rebuild.
	Provider       string    `json:"provider"`
	EmbeddingModel string    `json:"embedding_model"`
	IsDefault      bool      `json:"is_default"`
	Documents      int       `json:"documents"`
	ReadyDocuments int       `json:"ready_documents"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// documentView is one parsed file as a client reads it.
//
// It carries no text. A document is two hundred thousand characters at the
// limit, and every list here is one a client renders: the text is read through
// the preview endpoint, a window at a time.
type documentView struct {
	DocumentID string `json:"document_id"`
	KBID       string `json:"kb_id,omitempty"`
	FileID     string `json:"file_id"`
	Filename   string `json:"filename"`
	Engine     string `json:"engine"`
	TextChars  int64  `json:"text_chars"`
	Truncated  bool   `json:"truncated"`
	ChunkSize  int    `json:"chunk_size"`
	// ChunkOverlap and ActiveRevision are here because a citation carries a
	// revision: a reader opening one on a document that has been rebuilt since
	// can see that the coordinates it holds are not this revision's.
	ChunkOverlap   int       `json:"chunk_overlap"`
	ActiveRevision int       `json:"active_revision"`
	VectorState    string    `json:"vector_state"`
	VectorError    string    `json:"vector_error,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type knowledgeBaseListView struct {
	KnowledgeBases []knowledgeBaseView `json:"knowledge_bases"`
}

// knowledgeBaseDetailView is one collection with what is in it. The document
// list is here rather than behind a route of its own because a base's build
// progress (KB-03) is per document, and a client watching a rebuild is watching
// this.
type knowledgeBaseDetailView struct {
	KnowledgeBase knowledgeBaseView `json:"knowledge_base"`
	Documents     []documentView    `json:"documents"`
}

type createKnowledgeBaseRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type documentViewResponse struct {
	Document documentView `json:"document"`
}

type addDocumentRequest struct {
	FileID string `json:"file_id"`
}

type rebuildRequest struct {
	DocumentID string `json:"document_id"`
}

type searchRequest struct {
	Query string `json:"query"`
	TopK  int    `json:"top_k"`
}

type searchResultView struct {
	Sources []event.Source `json:"sources"`
}

// Create makes a collection.
func (h *KBHandler) Create(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	var request createKnowledgeBaseRequest
	if !readJSON(c, maxKBRequestBytes, &request) {
		return
	}
	created, err := h.bases.CreateKnowledgeBase(c.Request.Context(), request.Name, request.Description, account)
	if err != nil {
		h.fail(c, "create_knowledge_base_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusCreated, gin.H{"knowledge_base": toKnowledgeBaseView(created)})
}

// List returns every collection.
func (h *KBHandler) List(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	found, err := h.bases.KnowledgeBases(c.Request.Context(), account)
	if err != nil {
		h.fail(c, "list_knowledge_bases_failed", err)
		return
	}
	listed := make([]knowledgeBaseView, 0, len(found))
	for _, record := range found {
		listed = append(listed, toKnowledgeBaseView(record))
	}
	httpx.WriteSuccess(c, http.StatusOK, knowledgeBaseListView{KnowledgeBases: listed})
}

// Get returns one collection and its documents.
func (h *KBHandler) Get(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	kbID := c.Param("kbID")
	record, err := h.bases.KnowledgeBase(c.Request.Context(), kbID, account)
	if err != nil {
		h.fail(c, "get_knowledge_base_failed", err)
		return
	}
	documents, err := h.bases.Documents(c.Request.Context(), record.ID, account)
	if err != nil {
		h.fail(c, "get_knowledge_base_failed", err)
		return
	}
	listed := make([]documentView, 0, len(documents))
	for _, document := range documents {
		listed = append(listed, toDocumentView(document))
	}
	httpx.WriteSuccess(c, http.StatusOK, knowledgeBaseDetailView{
		KnowledgeBase: toKnowledgeBaseView(record),
		Documents:     listed,
	})
}

// Delete removes a collection and everything under it.
func (h *KBHandler) Delete(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	if err := h.bases.DeleteKnowledgeBase(c.Request.Context(), c.Param("kbID"), account); err != nil {
		h.fail(c, "delete_knowledge_base_failed", err)
		return
	}
	c.Status(http.StatusNoContent)
}

// SetDefault moves the default mark onto one collection.
//
// It answers with the base, because what a client shows after this is the list
// with the mark somewhere else, and the mark is the one field it cannot work out
// from the request.
func (h *KBHandler) SetDefault(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	kbID := c.Param("kbID")
	if err := h.bases.SetDefaultKnowledgeBase(c.Request.Context(), kbID, account); err != nil {
		h.fail(c, "set_default_knowledge_base_failed", err)
		return
	}
	record, err := h.bases.KnowledgeBase(c.Request.Context(), kbID, account)
	if err != nil {
		h.fail(c, "set_default_knowledge_base_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"knowledge_base": toKnowledgeBaseView(record)})
}

// AddDocument parses a stored file into a collection.
//
// The parse is synchronous and only the parse: the document is searchable by
// text the moment this answers, and its vectors arrive behind it. A client that
// wants to know how far behind reads vector_state — "pending" is that state, not
// a failure.
func (h *KBHandler) AddDocument(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	var request addDocumentRequest
	if !readJSON(c, maxKBRequestBytes, &request) {
		return
	}
	document, err := h.bases.AddDocument(c.Request.Context(), request.FileID, c.Param("kbID"), account)
	if err != nil {
		h.fail(c, "add_document_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusCreated, documentViewResponse{Document: toDocumentView(document)})
}

// RemoveDocument takes one document out of a collection.
//
// The document has to be in the collection the path names: a document id is
// enough to find a row, and the path is what says which base this request is
// about — without the check, a client holding one base's path could delete
// another's document, and the two would never look wrong afterwards.
func (h *KBHandler) RemoveDocument(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	document, ok := h.documentIn(c, account, c.Param("kbID"), c.Param("documentID"))
	if !ok {
		return
	}
	if err := h.bases.RemoveDocument(c.Request.Context(), document.ID, account); err != nil {
		h.fail(c, "remove_document_failed", err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Rebuild re-parses one document and switches it to the new revision.
//
// It is per document rather than per collection: one document is one parse, and
// a base's worth of parses in one request is a request that can outlast the
// server's write timeout. The client has the document list from the detail
// endpoint and can walk it, which also gives it somewhere to show progress.
func (h *KBHandler) Rebuild(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	var request rebuildRequest
	if !readJSON(c, maxKBRequestBytes, &request) {
		return
	}
	document, ok := h.documentIn(c, account, c.Param("kbID"), request.DocumentID)
	if !ok {
		return
	}
	rebuilt, err := h.bases.RebuildDocument(c.Request.Context(), document.ID, account)
	if err != nil {
		h.fail(c, "rebuild_document_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, documentViewResponse{Document: toDocumentView(rebuilt)})
}

// Search runs one retrieval over a collection. It is the quality-diagnosis view
// of what a turn would have got (KB-05): every source carries its locator, its
// engine and its score.
func (h *KBHandler) Search(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	var request searchRequest
	if !readJSON(c, maxKBRequestBytes, &request) {
		return
	}
	result, err := h.bases.Retrieve(c.Request.Context(), kb.RetrieveInput{
		KBIDs: []string{c.Param("kbID")}, Caller: account,
		Query: request.Query,
		TopK:  request.TopK,
	})
	if err != nil {
		h.fail(c, "search_knowledge_base_failed", err)
		return
	}
	if len(result.Sources) == 0 && len(result.Unavailable) > 0 {
		// The base was gone before the search ran. Reporting the scope as
		// unavailable is what a turn does with this; a search endpoint has a
		// status code for it instead, and no turn's material to protect.
		httpx.WriteError(c, http.StatusNotFound, "not_found", "no such knowledge base")
		return
	}
	sources := result.Sources
	if sources == nil {
		sources = []event.Source{}
	}
	httpx.WriteSuccess(c, http.StatusOK, searchResultView{Sources: sources})
}

// documentIn reads a document and checks it belongs to the base the path names.
//
// The ownership check is now two deep: the read is the caller's, so a document
// belonging to another account is already ErrNotFound before the base is
// compared, and the comparison then says the document is in the base the path
// claims. Both halves are needed. The read alone would let a client holding one
// base's path act on a document of another of its own bases; the comparison alone
// would let it act on anybody's.
func (h *KBHandler) documentIn(c *gin.Context, account authz.Caller, kbID, documentID string) (kb.Document, bool) {
	document, err := h.bases.Document(c.Request.Context(), documentID, account)
	if err != nil {
		h.fail(c, "document_request_failed", err)
		return kb.Document{}, false
	}
	if document.KBID != strings.TrimSpace(kbID) {
		httpx.WriteError(c, http.StatusNotFound, "not_found", "no such document in this knowledge base")
		return kb.Document{}, false
	}
	return document, true
}

func (h *KBHandler) ready(c *gin.Context) bool {
	if h != nil && h.bases != nil {
		return true
	}
	httpx.WriteError(c, http.StatusInternalServerError, "knowledge_bases_unavailable", "the knowledge base is not configured")
	return false
}

// fail reports a knowledge base failure, distinguishing what the client can act
// on from what only an operator can.
//
// A parse failure keeps its own code and its own message: the message is written
// for the person who uploaded the file, and ATT-04's rule is that an unreadable
// file is answered with a reason they can act on rather than a truncated one.
func (h *KBHandler) fail(c *gin.Context, code string, err error) {
	var parseErr *parse.Error
	errors.As(err, &parseErr)
	switch {
	case errors.Is(err, kb.ErrNotFound):
		httpx.WriteError(c, http.StatusNotFound, "not_found", "no such knowledge base or document")
	case parseErr != nil && parseErr.Code == parse.CodeParserUnavailable:
		// The engine that reads this kind of file did not answer. Nothing about
		// the file is wrong, and the rest of the application works (AC-14).
		httpx.WriteError(c, http.StatusServiceUnavailable, string(parseErr.Code), parseErr.Message)
	case parseErr != nil:
		httpx.WriteError(c, http.StatusUnprocessableEntity, string(parseErr.Code), parseErr.Message)
	case errors.Is(err, kb.ErrQueryTooShort):
		httpx.WriteError(c, http.StatusBadRequest, "query_too_short", detail(err, kb.ErrQueryTooShort))
	case errors.Is(err, kb.ErrInvalidInput):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_input", detail(err, kb.ErrInvalidInput))
	case errors.Is(err, kb.ErrConflict):
		httpx.WriteError(c, http.StatusConflict, "conflict", "the document changed while this request was in flight")
	default:
		if h.logger != nil {
			h.logger.Error("knowledge base request failed", "error", err, "request_id", httpx.RequestID(c))
		}
		httpx.WriteError(c, http.StatusInternalServerError, code, "the knowledge base request failed")
	}
}

// detail is the part of a domain error's text that follows its sentinel: the
// sentence the domain wrote for a reader. The sentinel is a package name and a
// category, and repeating it in a response says nothing a client can use.
func detail(err error, sentinel error) string {
	prefix := sentinel.Error() + ": "
	text := err.Error()
	if strings.HasPrefix(text, prefix) {
		return strings.TrimPrefix(text, prefix)
	}
	return text
}

func toKnowledgeBaseView(record kb.KnowledgeBase) knowledgeBaseView {
	return knowledgeBaseView{
		KBID: record.ID, Name: record.Name, Description: record.Description,
		Provider: record.Provider, EmbeddingModel: record.EmbeddingModel,
		IsDefault: record.IsDefault, Documents: record.Documents,
		ReadyDocuments: record.ReadyDocuments,
		CreatedAt:      record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}

func toDocumentView(record kb.Document) documentView {
	return documentView{
		DocumentID: record.ID, KBID: record.KBID, FileID: record.FileID,
		Filename: record.Filename, Engine: record.Engine, TextChars: record.TextChars,
		Truncated: record.Truncated, ChunkSize: record.ChunkSize,
		ChunkOverlap: record.ChunkOverlap, ActiveRevision: record.ActiveRevision,
		VectorState: record.VectorState, VectorError: record.VectorError,
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}
