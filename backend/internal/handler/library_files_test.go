package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"backend/internal/library"
	"backend/internal/middleware"
	"github.com/gin-gonic/gin"
)

type uploaderStub struct {
	result library.UploadResult
	err    error
	input  library.UploadInput
}

func (s *uploaderStub) Upload(_ context.Context, input library.UploadInput) (library.UploadResult, error) {
	s.input = input
	return s.result, s.err
}

func TestLibraryFilesHandlerUploadsSingleFile(t *testing.T) {
	uploader := &uploaderStub{result: library.UploadResult{
		ID: "lib_test", SHA256: strings.Repeat("a", 64), Filename: "notes.md",
		MIMEType: "text/markdown", SizeBytes: 7, Category: "md", CreatedAt: time.Unix(1, 0).UTC(),
	}}
	router := newLibraryHandlerRouter(uploader, 1024)
	request := multipartRequest(t, []multipartPart{{field: "file", filename: "notes.md", content: "# Notes"}})
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		File      library.UploadResult `json:"file"`
		RequestID string               `json:"request_id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.File.ID != "lib_test" || body.RequestID == "" {
		t.Errorf("response = %+v", body)
	}
	if uploader.input.Filename != "notes.md" || uploader.input.Content == nil {
		t.Errorf("uploader input = %+v", uploader.input)
	}
	if strings.Contains(recorder.Body.String(), "library_path") {
		t.Fatal("response exposed internal library path")
	}
}

func TestLibraryFilesHandlerRejectsInvalidMultipart(t *testing.T) {
	tests := []struct {
		name       string
		request    func(*testing.T) *http.Request
		wantStatus int
		wantCode   string
	}{
		{name: "missing file", request: func(t *testing.T) *http.Request { return multipartRequest(t, nil) }, wantStatus: 400, wantCode: "file_required"},
		{name: "multiple files", request: func(t *testing.T) *http.Request {
			return multipartRequest(t, []multipartPart{{field: "file", filename: "a.md", content: "a"}, {field: "file", filename: "b.md", content: "b"}})
		}, wantStatus: 400, wantCode: "multiple_files"},
		{name: "wrong field", request: func(t *testing.T) *http.Request {
			return multipartRequest(t, []multipartPart{{field: "attachment", filename: "a.md", content: "a"}})
		}, wantStatus: 400, wantCode: "unexpected_multipart_field"},
		{name: "extra value", request: func(t *testing.T) *http.Request {
			return multipartRequestWithValue(t, []multipartPart{{field: "file", filename: "a.md", content: "a"}}, "title", "notes")
		}, wantStatus: 400, wantCode: "unexpected_multipart_field"},
		{name: "known request too large", request: func(t *testing.T) *http.Request {
			return multipartRequest(t, []multipartPart{{field: "file", filename: "a.md", content: strings.Repeat("a", 2048)}})
		}, wantStatus: 413, wantCode: "request_too_large"},
		{name: "not multipart", request: func(_ *testing.T) *http.Request {
			return httptest.NewRequest(http.MethodPost, "/api/v1/library/files", strings.NewReader("data"))
		}, wantStatus: 400, wantCode: "invalid_multipart"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := newLibraryHandlerRouter(&uploaderStub{}, 1024)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, tt.request(t))
			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			if !strings.Contains(recorder.Body.String(), `"code":"`+tt.wantCode+`"`) {
				t.Fatalf("body = %s, want code %s", recorder.Body.String(), tt.wantCode)
			}
		})
	}
}

func TestLibraryFilesHandlerRejectsChunkedRequestOverLimit(t *testing.T) {
	request := multipartRequest(t, []multipartPart{{field: "file", filename: "a.md", content: strings.Repeat("a", 2048)}})
	request.ContentLength = -1
	router := newLibraryHandlerRouter(&uploaderStub{}, 1024)
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusRequestEntityTooLarge || !strings.Contains(recorder.Body.String(), `"code":"request_too_large"`) {
		t.Fatalf("status/body = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestLibraryFilesHandlerMapsLibraryErrors(t *testing.T) {
	tests := []struct {
		err        error
		wantStatus int
		wantCode   string
	}{
		{err: library.ErrFileTooLarge, wantStatus: 413, wantCode: "file_too_large"},
		{err: library.ErrUnsupportedFileType, wantStatus: 400, wantCode: "unsupported_file_type"},
		{err: library.ErrFileTypeMismatch, wantStatus: 400, wantCode: "file_type_mismatch"},
		{err: errors.New("private database details"), wantStatus: 500, wantCode: "upload_failed"},
	}
	for _, tt := range tests {
		router := newLibraryHandlerRouter(&uploaderStub{err: tt.err}, 1024)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, multipartRequest(t, []multipartPart{{field: "file", filename: "a.md", content: "a"}}))
		if recorder.Code != tt.wantStatus || !strings.Contains(recorder.Body.String(), tt.wantCode) {
			t.Fatalf("status/body = %d %s", recorder.Code, recorder.Body.String())
		}
		if strings.Contains(recorder.Body.String(), "private database") {
			t.Fatal("response leaked internal error")
		}
	}
}

func newLibraryHandlerRouter(uploader FileUploader, maxBytes int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
	handler := NewLibraryFilesHandler(uploader, maxBytes, logger)
	router := gin.New()
	router.Use(middleware.RequestID())
	router.POST("/api/v1/library/files", handler.Upload)
	return router
}

type multipartPart struct {
	field    string
	filename string
	content  string
}

func multipartRequest(t *testing.T, parts []multipartPart) *http.Request {
	t.Helper()
	return multipartRequestWithValue(t, parts, "", "")
}

func multipartRequestWithValue(t *testing.T, parts []multipartPart, field, value string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, part := range parts {
		file, err := writer.CreateFormFile(part.field, part.filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(part.content)); err != nil {
			t.Fatal(err)
		}
	}
	if field != "" {
		if err := writer.WriteField(field, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/library/files", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}
