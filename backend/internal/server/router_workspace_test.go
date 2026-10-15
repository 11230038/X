package server

// The seven package routes as they are mounted (§2.16).
//
// What this file is the only place for is the mounting itself: that each route
// exists at the path and method a client was told, that it is behind the token,
// and — the one thing this area does differently from every other in the router
// — that it is mounted with no surface at all. What a package endpoint answers
// is handler/workspace_test.go's business, so the stub below answers with ids
// nothing else here would produce and no case reads further into the body than
// that.

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"backend/internal/workspace"
)

// routerWorkspaceStub answers every package call with a job whose id is
// recognisable, so that a route mounted against the wrong handler is a failing
// assertion rather than a plausible body.
type routerWorkspaceStub struct{}

const routerWorkspaceJobID = "job_mounted_here"

func routerWorkspaceJob(state workspace.State) workspace.Job {
	return workspace.Job{
		ID: routerWorkspaceJobID, Kind: workspace.JobExport, State: state, OwnerID: 1,
		ArchivePath: routerWorkspaceJobID + ".zip",
		CreatedAt:   time.Unix(1_700_000_000, 0).UTC(),
		UpdatedAt:   time.Unix(1_700_000_000, 0).UTC(),
	}
}

func (routerWorkspaceStub) Export(context.Context, workspace.ExportRequest) (workspace.Job, error) {
	return routerWorkspaceJob(workspace.StateCommitted), nil
}

func (routerWorkspaceStub) List(context.Context, workspace.ListRequest) ([]workspace.Job, error) {
	return []workspace.Job{routerWorkspaceJob(workspace.StateCommitted)}, nil
}

func (routerWorkspaceStub) Preview(context.Context, workspace.PreviewRequest) (workspace.Preview, error) {
	return workspace.Preview{JobID: routerWorkspaceJobID, Objects: 0}, nil
}

func (routerWorkspaceStub) Import(context.Context, workspace.CommitRequest) (workspace.Job, error) {
	return routerWorkspaceJob(workspace.StateCommitted), nil
}

func (routerWorkspaceStub) Job(context.Context, string) (workspace.Job, error) {
	return routerWorkspaceJob(workspace.StatePreview), nil
}

// Download streams the stub's own id as the package. That is deliberate rather
// than cute: this file's cases fingerprint a route by what only its handler
// answers, and the download is the one package route whose body is not a job —
// so the bytes have to carry the fingerprint instead of a field.
func (routerWorkspaceStub) Download(
	context.Context, workspace.Job,
) (*workspace.StoredArchive, error) {
	return &workspace.StoredArchive{
		Content:   io.NopCloser(strings.NewReader(routerWorkspaceJobID)),
		SizeBytes: int64(len(routerWorkspaceJobID)),
	}, nil
}

func (routerWorkspaceStub) Recover(context.Context, workspace.RecoverRequest) (workspace.Job, error) {
	return routerWorkspaceJob(workspace.StateRecovered), nil
}

func (routerWorkspaceStub) MaxArchiveBytes() int64 { return 1 << 20 }

// signedJSONRequest is an ordinary request with the account's token on it, which
// is what every route in this file is reached by.
func signedJSONRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer "+routerAcceptedToken)
	return request
}

// packageUpload is one multipart request carrying a package, under the field
// name the route expects.
func packageUpload(t *testing.T, path string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", "incoming.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("PK\x03\x04 a package")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+routerAcceptedToken)
	return request
}

// TestRouterRegistersThePackageRoutes pins each path and method, and that every
// one of them is behind the token. The second half is not decoration: a package
// is an account's own work, and a route that answered without a token would be
// handing it to whoever asked.
func TestRouterRegistersThePackageRoutes(t *testing.T) {
	router := newTestRouter(t, readyChecker{})

	routes := []struct {
		method string
		path   string
		// withToken builds the request the mounted route would accept.
		withToken func() *http.Request
		wantCode  int
	}{
		{http.MethodPost, "/api/v1/workspace/exports", func() *http.Request {
			return signedJSONRequest(http.MethodPost, "/api/v1/workspace/exports", `{"refs":[]}`)
		}, http.StatusCreated},
		{http.MethodGet, "/api/v1/workspace/exports", func() *http.Request {
			return signedJSONRequest(http.MethodGet, "/api/v1/workspace/exports", "")
		}, http.StatusOK},
		// The download is the one route here that answers with the package rather
		// than with a job, so the fingerprint below arrives as the body's bytes.
		{http.MethodGet, "/api/v1/workspace/exports/job_1/content", func() *http.Request {
			return signedJSONRequest(http.MethodGet, "/api/v1/workspace/exports/job_1/content", "")
		}, http.StatusOK},
		{http.MethodPost, "/api/v1/workspace/imports/preview", func() *http.Request {
			// The preview's happy path needs the bytes, so this is the one case
			// that builds a multipart body; every other route takes JSON or
			// nothing.
			request := packageUpload(t, "/api/v1/workspace/imports/preview")
			return request
		}, http.StatusOK},
		{http.MethodPost, "/api/v1/workspace/imports", func() *http.Request {
			return signedJSONRequest(http.MethodPost, "/api/v1/workspace/imports", `{"job_id":"job_1"}`)
		}, http.StatusOK},
		{http.MethodGet, "/api/v1/workspace/jobs/job_1", func() *http.Request {
			return signedJSONRequest(http.MethodGet, "/api/v1/workspace/jobs/job_1", "")
		}, http.StatusOK},
		{http.MethodPost, "/api/v1/workspace/jobs/job_1/recover", func() *http.Request {
			return signedJSONRequest(http.MethodPost, "/api/v1/workspace/jobs/job_1/recover", "")
		}, http.StatusOK},
	}

	for _, route := range routes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			// Without a token: refused, and refused as a missing credential
			// rather than as a route that is not there.
			without := httptest.NewRecorder()
			router.ServeHTTP(without, httptest.NewRequest(route.method, route.path, nil))
			if without.Code != http.StatusUnauthorized {
				t.Fatalf("without a token: status = %d, want 401; body=%s",
					without.Code, without.Body.String())
			}

			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, route.withToken())
			if recorder.Code != route.wantCode {
				t.Fatalf("status = %d, want %d; body=%s",
					recorder.Code, route.wantCode, recorder.Body.String())
			}
			// The answer is the package handler's: the job's own id, which no
			// other handler in this router produces.
			if !strings.Contains(recorder.Body.String(), routerWorkspaceJobID) {
				t.Fatalf("the answer is not the package handler's: %s", recorder.Body.String())
			}
		})
	}

	t.Run("a method that is neither", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/v1/workspace/exports", nil))
		if recorder.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", recorder.Code)
		}
	})
}

// TestRouterMountsThePackageRoutesWithNoSurface is the divergence from §1.2
// written down where it can be checked.
//
// Every other area of this router is behind a surface — the library, the reading
// layer, memory, governance — and the package routes are behind the token alone.
// The reason is in the router beside them: a package is an account's own work
// leaving and coming back, and a surface would be a way for a preset to refuse
// an account its own copy of it.
//
// The check is a router built to refuse everything. A surface route beside the
// package routes answers 403 under it, which is what makes the package routes'
// 200 an answer about where they are mounted rather than about a permissive
// stub.
func TestRouterMountsThePackageRoutesWithNoSurface(t *testing.T) {
	router := newTestRouterWithAuthorizer(t, readyChecker{}, nil, routerAuthorizerStub{deny: true})

	t.Run("a surface route is refused", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, signedJSONRequest(http.MethodGet, "/api/v1/library/files/lib_1", ""))
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want the library route to be refused; body=%s",
				recorder.Code, recorder.Body.String())
		}
	})

	t.Run("the package routes are not", func(t *testing.T) {
		for _, path := range []string{
			"/api/v1/workspace/exports",
			"/api/v1/workspace/exports/job_1/content",
			"/api/v1/workspace/jobs/job_1",
		} {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, signedJSONRequest(http.MethodGet, path, ""))
			if recorder.Code == http.StatusForbidden {
				t.Errorf("%s: the package route was refused by the policy: %s",
					path, recorder.Body.String())
			}
			if recorder.Code != http.StatusOK {
				t.Errorf("%s: status = %d, want 200; body=%s", path, recorder.Code, recorder.Body.String())
			}
		}
	})
}
