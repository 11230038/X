package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"backend/internal/httpx"
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
		Filename: fileHeaders[0].Filename, DeclaredMIME: fileHeaders[0].Header.Get("Content-Type"), Content: file,
	})
	if err != nil {
		h.writeUploadError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"file": result, "request_id": httpx.RequestID(c)})
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
