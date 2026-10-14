package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"backend/internal/authz"
	"backend/internal/httpx"
	"backend/internal/kb"
	"backend/internal/library"
	"github.com/gin-gonic/gin"
)

const multipartMemoryBytes int64 = 1 << 20

// FileUploader is the file-library use case consumed by the HTTP handler.
type FileUploader interface {
	Upload(context.Context, library.UploadInput) (library.UploadResult, error)
}

// LibraryFilesHandler handles file-library HTTP requests.
type LibraryFilesHandler struct {
	uploader        FileUploader
	maxRequestBytes int64
	logger          *slog.Logger
}

// NewLibraryFilesHandler constructs the file-library HTTP handler.
func NewLibraryFilesHandler(uploader FileUploader, maxRequestBytes int64, logger *slog.Logger) *LibraryFilesHandler {
	return &LibraryFilesHandler{uploader: uploader, maxRequestBytes: maxRequestBytes, logger: logger}
}

// Upload accepts exactly one multipart file under the field named file.
func (h *LibraryFilesHandler) Upload(c *gin.Context) {
	if h == nil || h.uploader == nil || h.maxRequestBytes <= 0 {
		httpx.WriteError(c, http.StatusInternalServerError, "upload_failed", "file upload failed")
		return
	}
	// The account is read before the body is: every file that gets stored has to
	// belong to somebody, and reading half a gigabyte before discovering that
	// nobody is signed in is work done for a request that cannot succeed.
	//
	// It is read as the account and not as the caller, because an upload can turn
	// out to be a restore of a file this account had thrown away (§2.14), and a
	// restore is recorded under the name the account had at the time.
	account, ok := callerAccount(c)
	if !ok {
		return
	}
	c.Request = c.Request.WithContext(library.WithRequestID(c.Request.Context(), httpx.RequestID(c)))
	if c.Request.ContentLength > h.maxRequestBytes {
		httpx.WriteError(c, http.StatusRequestEntityTooLarge, "request_too_large", "upload request is too large")
		return
	}
	responseWriter := http.ResponseWriter(c.Writer)
	if unwrapper, ok := responseWriter.(interface{ Unwrap() http.ResponseWriter }); ok {
		responseWriter = unwrapper.Unwrap()
	}
	c.Request.Body = http.MaxBytesReader(responseWriter, c.Request.Body, h.maxRequestBytes)
	if err := c.Request.ParseMultipartForm(multipartMemoryBytes); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			httpx.WriteError(c, http.StatusRequestEntityTooLarge, "request_too_large", "upload request is too large")
			return
		}
		httpx.WriteError(c, http.StatusBadRequest, "invalid_multipart", "invalid multipart request")
		return
	}
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}

	if len(c.Request.MultipartForm.Value) != 0 {
		httpx.WriteError(c, http.StatusBadRequest, "unexpected_multipart_field", "unexpected multipart field")
		return
	}
	fileHeaders, ok := c.Request.MultipartForm.File["file"]
	if !ok || len(fileHeaders) == 0 {
		if len(c.Request.MultipartForm.File) != 0 {
			httpx.WriteError(c, http.StatusBadRequest, "unexpected_multipart_field", "unexpected multipart field")
			return
		}
		httpx.WriteError(c, http.StatusBadRequest, "file_required", "file is required")
		return
	}
	fileCount := 0
	for _, headers := range c.Request.MultipartForm.File {
		fileCount += len(headers)
	}
	if fileCount != 1 || len(fileHeaders) != 1 {
		httpx.WriteError(c, http.StatusBadRequest, "multiple_files", "only one file is allowed")
		return
	}

	file, err := fileHeaders[0].Open()
	if err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_file", "uploaded file is invalid")
		return
	}
	defer file.Close()
	result, err := h.uploader.Upload(c.Request.Context(), library.UploadInput{
		Filename: fileHeaders[0].Filename, DeclaredMIME: fileHeaders[0].Header.Get("Content-Type"),
		Content: file, Actor: actorOf(account),
	})
	if err != nil {
		h.writeUploadError(c, err)
		return
	}
	httpx.WriteSuccess(c, http.StatusCreated, gin.H{"file": result})
}

func (h *LibraryFilesHandler) writeUploadError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, library.ErrFileTooLarge):
		httpx.WriteError(c, http.StatusRequestEntityTooLarge, "file_too_large", "uploaded file is too large")
	case errors.Is(err, library.ErrInvalidFilename):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_filename", "uploaded filename is invalid")
	case errors.Is(err, library.ErrUnsupportedFileType):
		httpx.WriteError(c, http.StatusBadRequest, "unsupported_file_type", "uploaded file type is not supported")
	case errors.Is(err, library.ErrFileTypeMismatch):
		httpx.WriteError(c, http.StatusBadRequest, "file_type_mismatch", "uploaded file content does not match its type")
	case errors.Is(err, library.ErrInvalidFile), errors.Is(err, library.ErrEmptyFile):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_file", "uploaded file is invalid")
	default:
		if h.logger != nil {
			h.logger.Error("file upload failed", "request_id", httpx.RequestID(c), "error", err)
		}
		httpx.WriteError(c, http.StatusInternalServerError, "upload_failed", "file upload failed")
	}
}

// FileLibrary is the read side of the file library: one file's metadata.
type FileLibrary interface {
	Find(ctx context.Context, id string, caller authz.Caller) (library.UploadResult, error)
}

// MaterialReader is the parsed side of a file: the document it was read into,
// a window of the text a model was shown, and where the chunk boundaries of that
// document's active revision fall.
//
// The caller is on every method for the same reason it is on FileLibrary above:
// the file id in the path has already been proved to be the caller's, and a
// document read that took the id on trust would be the same read arriving by a
// second door. A document has no owner of its own — it is the caller's when the
// collection or the file behind it is.
type MaterialReader interface {
	DocumentOfFile(ctx context.Context, fileID string, caller authz.Caller) (kb.Document, error)
	ReadWindow(ctx context.Context, documentID string, offset, limit int64, caller authz.Caller) (kb.Window, error)
	ChunkSpans(ctx context.Context, documentID string, caller authz.Caller) ([]kb.ChunkSpan, error)
}

// LibraryReadHandler answers what one file is and what was read out of it
// (ATT-01, ATT-02).
//
// It is a separate handler from the upload because the two answer different
// questions — what was stored, and what was read out of it — and because the
// second has to reach the knowledge base while the first only reaches the disk.
// Both are now behind the token: a file belongs to the account that uploaded it,
// and reading one back as somebody else is not found (V5 §2.3).
type LibraryReadHandler struct {
	files     FileLibrary
	materials MaterialReader
	logger    *slog.Logger
}

// NewLibraryReadHandler constructs the file-library read handler.
func NewLibraryReadHandler(files FileLibrary, materials MaterialReader, logger *slog.Logger) *LibraryReadHandler {
	return &LibraryReadHandler{files: files, materials: materials, logger: logger}
}

// fileView is one file and what has been parsed from it.
//
// Document is null rather than absent when the file has never been read — it was
// uploaded and used for nothing yet. That is not a failure: the upload is the
// whole of what happened, and a client showing "not parsed" has to be told so
// before it offers to open the text.
type fileView struct {
	File     library.UploadResult `json:"file"`
	Document *documentView        `json:"document"`
}

// chunkSpanView is one chunk boundary. It carries no text: the text is in the
// window beside it, and the boundary is what a citation's character span is
// read against.
type chunkSpanView struct {
	Ordinal   int   `json:"ordinal"`
	CharStart int64 `json:"char_start"`
	CharEnd   int64 `json:"char_end"`
	Page      *int  `json:"page,omitempty"`
}

// fileTextView is a window of the text a model was shown, with the boundaries
// of the chunks the sources were located by.
type fileTextView struct {
	Document documentView    `json:"document"`
	Text     string          `json:"text"`
	Chunks   []chunkSpanView `json:"chunks"`
	// CharStart and CharEnd are the span actually returned, which is the
	// requested span clamped to what the document has; TotalChars is the whole
	// document, so a client can tell a window that ends because it was asked to
	// from one that ends because the text does.
	CharStart  int64 `json:"char_start"`
	CharEnd    int64 `json:"char_end"`
	TotalChars int64 `json:"total_chars"`
}

// Get returns one file's metadata and its parse state.
func (h *LibraryReadHandler) Get(c *gin.Context) {
	file, account, ok := h.file(c)
	if !ok {
		return
	}
	response := fileView{File: file}
	document, err := h.materials.DocumentOfFile(c.Request.Context(), file.ID, account)
	switch {
	case err == nil:
		parsed := toDocumentView(document)
		response.Document = &parsed
	case errors.Is(err, kb.ErrNotFound):
		// Never read. The file is here and nothing was parsed out of it.
	default:
		h.fail(c, "read_file_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, response)
}

// Text returns a window of the text a model was shown, with the chunk
// boundaries a citation's coordinates refer to (ATT-02, AC-06).
//
// offset and limit are characters of the extracted text — the same unit as a
// locator's char_start and char_end — so a citation opens by passing them
// straight through.
func (h *LibraryReadHandler) Text(c *gin.Context) {
	file, account, ok := h.file(c)
	if !ok {
		return
	}
	offset, err := intParam(c, "offset")
	if err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	limit, err := intParam(c, "limit")
	if err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	document, err := h.materials.DocumentOfFile(c.Request.Context(), file.ID, account)
	if errors.Is(err, kb.ErrNotFound) {
		httpx.WriteError(c, http.StatusNotFound, "not_parsed", "this file has not been read into any material yet")
		return
	}
	if err != nil {
		h.fail(c, "read_file_text_failed", err)
		return
	}
	window, err := h.materials.ReadWindow(c.Request.Context(), document.ID, int64(offset), int64(limit), account)
	if err != nil {
		h.fail(c, "read_file_text_failed", err)
		return
	}
	spans, err := h.materials.ChunkSpans(c.Request.Context(), document.ID, account)
	if err != nil {
		h.fail(c, "read_file_text_failed", err)
		return
	}
	boundaries := make([]chunkSpanView, 0, len(spans))
	for _, span := range spans {
		boundaries = append(boundaries, chunkSpanView{
			Ordinal: span.Ordinal, CharStart: span.CharStart,
			CharEnd: span.CharEnd, Page: span.Page,
		})
	}
	httpx.WriteSuccess(c, http.StatusOK, fileTextView{
		Document: toDocumentView(window.Document), Text: window.Text,
		Chunks: boundaries, CharStart: window.CharStart,
		CharEnd: window.CharEnd, TotalChars: window.TotalChars,
	})
}

// file reads the file the request names, or answers why it cannot.
//
// It hands the account back alongside the record rather than letting the caller
// read it again. The account is not an incidental parameter here: it is what the
// lookup was narrowed by, and the reads that follow — the document parsed from
// this file, the window of its text — have to be narrowed by the same one. A
// second read of the token would be a second answer to a question that already
// has one.
func (h *LibraryReadHandler) file(c *gin.Context) (library.UploadResult, authz.Caller, bool) {
	if h == nil || h.files == nil || h.materials == nil {
		httpx.WriteError(c, http.StatusInternalServerError, "library_unavailable", "the file library is not configured")
		return library.UploadResult{}, authz.Caller{}, false
	}
	account, ok := caller(c)
	if !ok {
		return library.UploadResult{}, authz.Caller{}, false
	}
	// The file a request names is looked up as the caller's own. A file belonging
	// to another account is not found rather than forbidden: telling the two
	// apart would say that the id names something, which is the one fact a probe
	// for ids is trying to learn.
	record, err := h.files.Find(c.Request.Context(), c.Param("fileID"), account)
	if err != nil {
		if errors.Is(err, library.ErrNotFound) {
			httpx.WriteError(c, http.StatusNotFound, "not_found", "no such file")
			return library.UploadResult{}, authz.Caller{}, false
		}
		h.fail(c, "read_file_failed", err)
		return library.UploadResult{}, authz.Caller{}, false
	}
	return record, account, true
}

func (h *LibraryReadHandler) fail(c *gin.Context, code string, err error) {
	if errors.Is(err, kb.ErrInvalidInput) {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_query", detail(err, kb.ErrInvalidInput))
		return
	}
	if h.logger != nil {
		h.logger.Error("file read failed", "request_id", httpx.RequestID(c), "error", err)
	}
	httpx.WriteError(c, http.StatusInternalServerError, code, "the file could not be read")
}
