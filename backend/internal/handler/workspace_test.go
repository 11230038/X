package handler

// The six package routes (§2.13, §2.16).
//
// What is under test here is the route's own work and nothing behind it: which
// request reaches which call, what a refused request is answered with, and the
// one rule this area adds — a failure that produced a job is answered as a job.
// The service's own behaviour is `internal/workspace`'s, and the two are kept
// apart on purpose: a case here that asserted what a commit *did* would have to
// restate the service's rules in the handler's file.

import (
	"bytes"
	"context"
	"encoding/json"
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

func (s *workspaceStub) Recover(_ context.Context, request workspace.RecoverRequest) (workspace.Job, error) {
	s.recoverInput = request
	return s.recoverResult, s.recoverErr
}

func (s *workspaceStub) MaxArchiveBytes() int64 { return s.maxArchiveBytes }

// newWorkspaceRouter mounts the six routes the way the server does — including
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

// ── export ──────────────────────────────────────────────────────────────────

func TestWorkspaceExportPacksTheSelectionAsTheTokenAccount(t *testing.T) {
	stub := &workspaceStub{exportResult: aJob("job_1", workspace.StateCommitted, handlerAccountID)}
	recorder := postAs(t, newWorkspaceRouter(stub), http.MethodPost, "/api/v1/workspace/exports",
		`{"refs":[{"kind":"session","id":"ses_1"}]}`)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data struct {
			Job jobView `json:"job"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if envelope.Data.Job.ID != "job_1" || envelope.Data.Job.State != string(workspace.StateCommitted) {
		t.Errorf("the answer carries %+v", envelope.Data.Job)
	}
	// The whole row travels, because the status route is what a client comes
	// back to: a client that had to ask twice would be reading a screen made of
	// two answers that can disagree.
	if len(envelope.Data.Job.Manifest) != 1 || envelope.Data.Job.Counts["session"] != 1 ||
		envelope.Data.Job.ArchivePath != "job_1.zip" {
		t.Errorf("the answer is missing part of the job: %+v", envelope.Data.Job)
	}
	// The selection is the caller's, and the account is the token's — there is no
	// owner anywhere in the request for a client to set.
	if len(stub.exportInput.Refs) != 1 || stub.exportInput.Refs[0].ID != "ses_1" {
		t.Errorf("the service was asked for %+v", stub.exportInput.Refs)
	}
	if stub.exportInput.Actor.UserID != handlerAccountID || stub.exportInput.Actor.Name != "alice" {
		t.Errorf("the service was given the actor %+v", stub.exportInput.Actor)
	}
}

func TestWorkspaceExportAnswersAFailedJobAsAJob(t *testing.T) {
	failed := aJob("job_1", workspace.StateFailed, handlerAccountID)
	failed.FailureCode = string(workspace.FailureCollectFailed)
	stub := &workspaceStub{exportResult: failed, exportErr: errors.New("a session could not be read")}
	recorder := postAs(t, newWorkspaceRouter(stub), http.MethodPost, "/api/v1/workspace/exports",
		`{"refs":[{"kind":"session","id":"ses_1"}]}`)

	// The request is over — it was made and the work was done — and what went
	// wrong is the job's state. A 500 here would leave a client with a failed
	// package and no handle on the record of it.
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want the job; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data struct {
			Job jobView `json:"job"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if envelope.Data.Job.State != string(workspace.StateFailed) ||
		envelope.Data.Job.FailureCode != string(workspace.FailureCollectFailed) {
		t.Errorf("the answer carries %+v, want the failed job", envelope.Data.Job)
	}
	if strings.Contains(recorder.Body.String(), "a session could not be read") {
		t.Error("the refusal's cause was answered to the caller")
	}
}

func TestWorkspaceExportRefusesASelectionItCannotPack(t *testing.T) {
	// No job: a request that never got as far as making one is a failed request,
	// and the answer says which kind of failed request it was.
	stub := &workspaceStub{exportErr: workspace.ErrInvalidRef}
	recorder := postAs(t, newWorkspaceRouter(stub), http.MethodPost, "/api/v1/workspace/exports",
		`{"refs":[{"kind":"session","id":"../secrets"}]}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	if code := failAs(t, recorder); code != "invalid_selection" {
		t.Errorf("code = %q, want invalid_selection", code)
	}
}

// ── preview ─────────────────────────────────────────────────────────────────

func TestWorkspacePreviewReadsThePackageAndNamesTheJob(t *testing.T) {
	stub := &workspaceStub{
		maxArchiveBytes: 1 << 20,
		previewResult: workspace.Preview{
			JobID: "job_2", Objects: 1,
			Items:   []workspace.Item{{Ref: workspace.Ref{Kind: workspace.KindSession, ID: "ses_1"}, Action: workspace.ActionCreate}},
			Actions: map[string]int{"create": 1},
		},
	}
	router := newWorkspaceRouter(stub)
	recorder := serve(t, router, workspaceUpload(t, "/api/v1/workspace/imports/preview",
		[]multipartPart{{field: "file", filename: "incoming.zip", content: "PK\x03\x04a package"}}))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data struct {
			Preview workspace.Preview `json:"preview"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if envelope.Data.Preview.JobID != "job_2" || envelope.Data.Preview.Actions["create"] != 1 {
		t.Errorf("the answer carries %+v", envelope.Data.Preview)
	}
	// The bytes that reached the service are the bytes the client sent, and the
	// size beside them is how long they are — a ReaderAt cannot say for itself.
	if string(stub.previewBytes) != "PK\x03\x04a package" {
		t.Errorf("the service was handed %q", stub.previewBytes)
	}
	if stub.previewSize != int64(len("PK\x03\x04a package")) {
		t.Errorf("the service was told the package is %d bytes", stub.previewSize)
	}
	if stub.previewBytes != nil && len(stub.previewBytes) != int(stub.previewSize) {
		t.Errorf("the bytes and the size disagree: %d and %d", len(stub.previewBytes), stub.previewSize)
	}
}

func TestWorkspacePreviewRefusesARequestThatIsNotOnePackage(t *testing.T) {
	router := newWorkspaceRouter(&workspaceStub{maxArchiveBytes: 1 << 20})
	for name, parts := range map[string][]multipartPart{
		"two files": {
			{field: "file", filename: "a.zip", content: "one"},
			{field: "file", filename: "b.zip", content: "two"},
		},
		"the wrong field": {{field: "package", filename: "a.zip", content: "one"}},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := serve(t, router, workspaceUpload(t, "/api/v1/workspace/imports/preview", parts))
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
			if code := failAs(t, recorder); code != "file_required" {
				t.Errorf("code = %q, want file_required", code)
			}
		})
	}
}

func TestWorkspacePreviewRefusesAPackageOverTheBound(t *testing.T) {
	stub := &workspaceStub{maxArchiveBytes: 64}
	recorder := serve(t, newWorkspaceRouter(stub), workspaceUpload(t,
		"/api/v1/workspace/imports/preview",
		[]multipartPart{{field: "file", filename: "huge.zip", content: strings.Repeat("x", 4096)}}))

	// Over the bound is refused before the bytes are written to a temporary file,
	// which is the whole reason the route carries the service's own bound rather
	// than reading whatever arrives.
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%s", recorder.Code, recorder.Body.String())
	}
	if code := failAs(t, recorder); code != "package_too_large" {
		t.Errorf("code = %q, want package_too_large", code)
	}
	if stub.previewSize != 0 {
		t.Errorf("a package over the bound reached the service, %d bytes of it", stub.previewSize)
	}
}

// ── import, status, recovery ────────────────────────────────────────────────

func TestWorkspaceImportAnswersTheJobEvenWhenTheCommitFailed(t *testing.T) {
	failed := aJob("job_2", workspace.StateFailed, handlerAccountID)
	failed.FailureCode = string(workspace.FailureWriteFailed)
	stub := &workspaceStub{importResult: failed, importErr: errors.New("the second batch failed")}
	recorder := postAs(t, newWorkspaceRouter(stub), http.MethodPost, "/api/v1/workspace/imports", `{"job_id":"job_2"}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data struct {
			Job jobView `json:"job"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	// What the record names is what a recovery takes back, so a client that is
	// told the commit failed is told it in a body it can drive a button from.
	if len(envelope.Data.Job.Written) != 1 || envelope.Data.Job.State != string(workspace.StateFailed) {
		t.Errorf("the answer carries %+v", envelope.Data.Job)
	}
	if stub.importInput.JobID != "job_2" || stub.importInput.Actor.UserID != handlerAccountID {
		t.Errorf("the service was asked to commit %+v", stub.importInput)
	}
}

func TestWorkspaceImportAnswersTheCodesAClientCanActOn(t *testing.T) {
	for name, refusal := range map[string]struct {
		err    error
		status int
		code   string
	}{
		"a turn is running": {workspace.ErrBusy, http.StatusConflict, "turn_in_progress"},
		"nothing was previewed": {workspace.ErrNotPreview, http.StatusConflict,
			"not_previewed"},
		"a package that is not one": {workspace.ErrBadPackage, http.StatusBadRequest, "invalid_package"},
		"no such job":               {workspace.ErrNoSuchJob, http.StatusNotFound, "not_found"},
	} {
		t.Run(name, func(t *testing.T) {
			stub := &workspaceStub{importErr: refusal.err}
			recorder := postAs(t, newWorkspaceRouter(stub), http.MethodPost,
				"/api/v1/workspace/imports", `{"job_id":"job_2"}`)
			if recorder.Code != refusal.status {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, refusal.status, recorder.Body.String())
			}
			if code := failAs(t, recorder); code != refusal.code {
				t.Errorf("code = %q, want %q", code, refusal.code)
			}
		})
	}
}

func TestWorkspaceTheJobStatusIsTheTokenAccountsAlone(t *testing.T) {
	stub := &workspaceStub{jobResult: aJob("job_2", workspace.StatePreview, handlerAccountID)}
	router := newWorkspaceRouter(stub)
	recorder := postAs(t, router, http.MethodGet, "/api/v1/workspace/jobs/job_2", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.jobInput != "job_2" {
		t.Errorf("the service was asked for %q", stub.jobInput)
	}

	// Somebody else's job reads as one that was never issued, byte for byte the
	// same answer, because an answer that could tell them apart has said that the
	// row exists (AUTH-06).
	mine := postAs(t, newWorkspaceRouter(&workspaceStub{
		jobResult: aJob("job_2", workspace.StatePreview, handlerAccountID+1),
	}), http.MethodGet, "/api/v1/workspace/jobs/job_2", "")
	never := postAs(t, newWorkspaceRouter(&workspaceStub{
		jobErr: workspace.ErrNoSuchJob,
	}), http.MethodGet, "/api/v1/workspace/jobs/nobody", "")
	for _, recorder := range []*httptest.ResponseRecorder{mine, never} {
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", recorder.Code, recorder.Body.String())
		}
		if code := failAs(t, recorder); code != "not_found" {
			t.Errorf("code = %q, want not_found", code)
		}
	}
}

func TestWorkspaceRecoverAnswersTheRecoveredJobAndItsRefusals(t *testing.T) {
	stub := &workspaceStub{recoverResult: aJob("job_2", workspace.StateRecovered, handlerAccountID)}
	recorder := postAs(t, newWorkspaceRouter(stub), http.MethodPost,
		"/api/v1/workspace/jobs/job_2/recover", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.recoverInput.JobID != "job_2" || stub.recoverInput.Actor.UserID != handlerAccountID {
		t.Errorf("the service was asked to recover %+v", stub.recoverInput)
	}

	// A commit that finished has work worth keeping, and taking it back is a
	// delete wearing recovery's name. The route does not decide that — it passes
	// the refusal on, which is what makes the answer the same whoever asked.
	refused := postAs(t, newWorkspaceRouter(&workspaceStub{
		recoverErr: workspace.ErrNotRecoverable,
	}), http.MethodPost, "/api/v1/workspace/jobs/job_2/recover", "")
	if refused.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", refused.Code, refused.Body.String())
	}
	if code := failAs(t, refused); code != "not_recoverable" {
		t.Errorf("code = %q, want not_recoverable", code)
	}
}

func TestWorkspaceTheListingIsTheTokensOwnExports(t *testing.T) {
	stub := &workspaceStub{listResult: aJob("job_1", workspace.StateCommitted, handlerAccountID)}
	recorder := postAs(t, newWorkspaceRouter(stub), http.MethodGet,
		"/api/v1/workspace/exports?limit=7", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data struct {
			Jobs  []jobView `json:"jobs"`
			Count int       `json:"count"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if envelope.Data.Count != 1 || len(envelope.Data.Jobs) != 1 {
		t.Errorf("the answer carries %+v", envelope.Data)
	}
	// The direction is not a parameter — these are the export route's own — and
	// the owner is the token's. Both are decided here rather than read from the
	// query string, which is what keeps one account's listing out of another's.
	if stub.listInput.OwnerID != handlerAccountID || stub.listInput.Kind != workspace.JobExport {
		t.Errorf("the service was asked for %+v", stub.listInput)
	}
	if stub.listInput.Limit != 7 {
		t.Errorf("the limit reached the service as %d, want 7", stub.listInput.Limit)
	}

	bad := postAs(t, newWorkspaceRouter(&workspaceStub{}), http.MethodGet,
		"/api/v1/workspace/exports?limit=-1", "")
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a negative limit; body=%s", bad.Code, bad.Body.String())
	}
}

func TestWorkspaceRoutesAnswerUnavailableWithoutAService(t *testing.T) {
	// The routes are behind the guard, so a request that reaches one is from an
	// account the middleware saw. A service that is not there is a wiring fault,
	// and it is answered as one rather than as a request to try again.
	router := newWorkspaceRouter(nil)
	for _, route := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/workspace/exports", `{"refs":[]}`},
		{http.MethodGet, "/api/v1/workspace/exports", ""},
		{http.MethodPost, "/api/v1/workspace/imports", `{"job_id":"job_1"}`},
		{http.MethodGet, "/api/v1/workspace/jobs/job_1", ""},
		{http.MethodPost, "/api/v1/workspace/jobs/job_1/recover", ""},
	} {
		recorder := postAs(t, router, route.method, route.path, route.body)
		if recorder.Code != http.StatusInternalServerError {
			t.Errorf("%s %s: status = %d, want 500; body=%s",
				route.method, route.path, recorder.Code, recorder.Body.String())
			continue
		}
		if code := failAs(t, recorder); code != "workspace_unavailable" {
			t.Errorf("%s %s: code = %q, want workspace_unavailable", route.method, route.path, code)
		}
	}
	// The preview is covered apart because its refusal is answered from a
	// different branch: the bound comes from the service, and a service that is
	// not there has no bound to give.
	recorder := serve(t, router, workspaceUpload(t, "/api/v1/workspace/imports/preview",
		[]multipartPart{{field: "file", filename: "a.zip", content: "one"}}))
	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("preview: status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestWorkspaceARefusalIsReportedAsACodeAndNothingElse(t *testing.T) {
	// The two refusals that carry a cause are the ones whose cause is about the
	// package the caller sent. Even there what leaves is the sentinel's own
	// sentence, not the error the storage layer produced inside it.
	stub := &workspaceStub{previewErr: workspace.ErrBadPackage, maxArchiveBytes: 1 << 20}
	recorder := serve(t, newWorkspaceRouter(stub), workspaceUpload(t,
		"/api/v1/workspace/imports/preview",
		[]multipartPart{{field: "file", filename: "a.zip", content: "not a package"}}))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data  json.RawMessage `json:"data"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeBody(t, recorder, &envelope)
	if envelope.Error == nil || envelope.Error.Code != "invalid_package" {
		t.Fatalf("the answer carries %s", recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), handlerInternal) {
		t.Error("the answer carried something from inside the process")
	}
}
