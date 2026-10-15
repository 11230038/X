package handler

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"backend/internal/middleware"
	"backend/internal/workspace"
	"github.com/gin-gonic/gin"
)

// workspaceStub is the package service as the routes see it: every call answered
// with whatever the case set, and every argument kept.
type workspaceStub struct {
	exportResult workspace.Job
	exportErr    error
	exportInput  workspace.ExportRequest

	listResult workspace.Job
	listErr    error
	listInput  workspace.ListRequest

	previewResult workspace.Preview
	previewErr    error
	previewBytes  []byte
	previewSize   int64

	importResult workspace.Job
	importErr    error
	importInput  workspace.CommitRequest

	jobResult workspace.Job
	jobErr    error
	jobInput  string

	downloadResult workspace.StoredArchive
	downloadErr    error
	downloadInput  workspace.Job

	recoverResult workspace.Job
	recoverErr    error
	recoverInput  workspace.RecoverRequest

	maxArchiveBytes int64
}

func (s *workspaceStub) Export(_ context.Context, request workspace.ExportRequest) (workspace.Job, error) {
	s.exportInput = request
	return s.exportResult, s.exportErr
}

func (s *workspaceStub) List(_ context.Context, request workspace.ListRequest) ([]workspace.Job, error) {
	s.listInput = request
	if s.listErr != nil {
		return nil, s.listErr
	}
	return []workspace.Job{s.listResult}, nil
}

func (s *workspaceStub) Preview(_ context.Context, request workspace.PreviewRequest) (workspace.Preview, error) {
	s.previewSize = request.Size
	if request.Package != nil {
		s.previewBytes = make([]byte, request.Size)
		if _, err := request.Package.ReadAt(s.previewBytes, 0); err != nil && !errors.Is(err, io.EOF) {
			return workspace.Preview{}, err
		}
	}
	return s.previewResult, s.previewErr
}

func (s *workspaceStub) Import(_ context.Context, request workspace.CommitRequest) (workspace.Job, error) {
	s.importInput = request
	return s.importResult, s.importErr
}

func (s *workspaceStub) Job(_ context.Context, jobID string) (workspace.Job, error) {
	s.jobInput = jobID
	return s.jobResult, s.jobErr
}

func (s *workspaceStub) Download(_ context.Context, job workspace.Job) (*workspace.StoredArchive, error) {
	s.downloadInput = job
	if s.downloadErr != nil {
		return nil, s.downloadErr
	}
	return &s.downloadResult, nil
}

func (s *workspaceStub) Recover(_ context.Context, request workspace.RecoverRequest) (workspace.Job, error) {
	s.recoverInput = request
	return s.recoverResult, s.recoverErr
}

func (s *workspaceStub) MaxArchiveBytes() int64 { return s.maxArchiveBytes }

// newWorkspaceRouter mounts the seven routes the way the server does — including
// the absence of a surface, which is the deliberate divergence the router
// records: these routes are behind the token and nothing else.
func newWorkspaceRouter(service Workspace) *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := NewWorkspaceHandler(service, testHandlerLogger())
	router := gin.New()
	router.Use(middleware.RequestID())
	authenticated := router.Group("/api/v1")
	authenticated.Use(guard())
	authenticated.POST("/workspace/exports", handler.Export)
	authenticated.GET("/workspace/exports", handler.ListExports)
	authenticated.GET("/workspace/exports/:jobID/content", handler.DownloadExports)
	authenticated.POST("/workspace/imports/preview", handler.Preview)
	authenticated.POST("/workspace/imports", handler.Import)
	authenticated.GET("/workspace/jobs/:jobID", handler.Job)
	authenticated.POST("/workspace/jobs/:jobID/recover", handler.Recover)
	return router
}

func aJob(id string, state workspace.State, owner int64) workspace.Job {
	return workspace.Job{
		ID: id, Kind: workspace.JobImport, State: state, OwnerID: owner,
		ArchivePath: id + ".zip",
		Manifest:    []workspace.Entry{{Kind: workspace.KindSession, ID: "ses_1", SHA256: strings.Repeat("a", 64), Bytes: 12}},
		Counts:      map[string]int{"session": 1},
		Written:     []workspace.Written{{Kind: workspace.KindSession, ID: "ses_1"}},
		CreatedAt:   time.Unix(1_700_000_000, 0).UTC(),
		UpdatedAt:   time.Unix(1_700_000_001, 0).UTC(),
	}
}

// workspaceUpload is a multipart request carrying one package, the way a client
// sends one: the bytes under the field named `file`, and a token beside them.
func workspaceUpload(t *testing.T, path string, parts []multipartPart) *http.Request {
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
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+handlerToken)
	return request
}

func serve(t *testing.T, router *gin.Engine, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

// postAs is a JSON request with the token on it, for the routes whose body is an
// object rather than a file.
func postAs(t *testing.T, router *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", handlerBodyAccept)
	}
	request.Header.Set("Authorization", "Bearer "+handlerToken)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

// failAs is the error envelope the refusal cases read: a code and a message, and
// no part of the cause in either.
func failAs(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeBody(t, recorder, &envelope)
	return envelope.Error.Code
}
