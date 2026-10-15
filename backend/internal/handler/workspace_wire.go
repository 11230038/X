package handler

// What every package route reads its request through and answers it with: the
// multipart reader that produces the one file a package is, the job view's
// counterpart for a failure, and the four helpers that turn an outcome into a
// response.
//
// These are apart from the routes because they are the things the routes have in
// common rather than any one of them. The five of them together are what makes
// seven route bodies short enough to read as what each does: a route says which
// call it makes and which account it makes it for, and how the answer is shaped
// is decided once, here.

import (
	"errors"
	"net/http"

	"backend/internal/httpx"
	"backend/internal/workspace"
	"github.com/gin-gonic/gin"
)

// readPackage reads exactly one uploaded file under the field named `file`.
//
// The bound is the deployment's own package bound plus the multipart overhead,
// and what it buys is that a body over it is refused before it is written to a
// temporary file: the service checks the package's size too, but by then the
// bytes are already on this machine's disk.
func readPackage(c *gin.Context, maxPackageBytes int64) (packageFile, int64, bool) {
	if maxPackageBytes <= 0 {
		httpx.WriteError(c, http.StatusInternalServerError, "workspace_unavailable", "the package service is not configured")
		return nil, 0, false
	}
	responseWriter := http.ResponseWriter(c.Writer)
	if unwrapper, ok := responseWriter.(interface{ Unwrap() http.ResponseWriter }); ok {
		responseWriter = unwrapper.Unwrap()
	}
	c.Request.Body = http.MaxBytesReader(responseWriter, c.Request.Body, maxPackageBytes+multipartOverheadBytes)
	if err := c.Request.ParseMultipartForm(multipartMemoryBytes); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpx.WriteError(c, http.StatusRequestEntityTooLarge, "package_too_large", "the package is over this deployment's limit")
			return nil, 0, false
		}
		httpx.WriteError(c, http.StatusBadRequest, "invalid_multipart", "invalid multipart request")
		return nil, 0, false
	}
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}
	// A package is the only thing this request carries, and a second file or a
	// stray field is a request that means something this route does not — which
	// is refused rather than half-read, the same call the library's upload makes.
	fileHeaders, held := c.Request.MultipartForm.File["file"]
	count := 0
	for _, headers := range c.Request.MultipartForm.File {
		count += len(headers)
	}
	if !held || count != 1 || len(fileHeaders) != 1 || len(c.Request.MultipartForm.Value) != 0 {
		httpx.WriteError(c, http.StatusBadRequest, "file_required", "exactly one package is required, under the field named file")
		return nil, 0, false
	}
	// The part's own size, which the reader above cannot stand in for: its bound
	// allows for the form's punctuation, so a package over this deployment's limit
	// by less than that allowance is read whole. Answering it here is what keeps the
	// bytes from being read at all, which is the difference between a refusal and a
	// file that was written and then thrown away.
	if size := fileHeaders[0].Size; size > maxPackageBytes {
		httpx.WriteError(c, http.StatusRequestEntityTooLarge, "package_too_large", "the package is over this deployment's limit")
		return nil, 0, false
	}
	file, err := fileHeaders[0].Open()
	if err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_file", "the uploaded package is unreadable")
		return nil, 0, false
	}
	return file, fileHeaders[0].Size, true
}

// packageFile is what a package upload has to be: bytes that can be read at an
// offset (the archive reader seeks to its members) and closed.
type packageFile interface {
	ReadAt(p []byte, off int64) (int, error)
	Close() error
}

// answer reports the outcome of a request that makes or advances a job.
//
// The rule it keeps in one place: a job that exists is answered as a job, and a
// request that failed before any job existed is answered as a failure. That is
// §2.13's recovery read backwards — the failure is recoverable exactly when
// there is a row recording what it did — and it is why this is not a `fail` call
// with a status code beside it.
func (h *WorkspaceHandler) answer(c *gin.Context, job workspace.Job, err error, status int) {
	if err != nil && job.ID == "" {
		h.fail(c, "workspace_failed", err)
		return
	}
	if err != nil && h.logger != nil {
		// The work failed and is recorded as a failed job; the line here is for
		// whoever operates the deployment, and it is the same sentence the job
		// row and the audit entry carry.
		h.logger.Error("package job failed", "job_id", job.ID, "state", job.State,
			"failure_code", job.FailureCode, "error", err, "request_id", httpx.RequestID(c))
	}
	httpx.WriteSuccess(c, status, gin.H{"job": toJobView(job)})
}

// errNoSuchJob is the status route's answer for a read that did not produce this
// account's job.
//
// The two ways that happens — no such id, and somebody else's id — are one
// error here, because the answer to the caller has to be one answer: a client
// that could tell them apart has been told that a job exists.
func errNoSuchJob(job workspace.Job, err error) error {
	if err != nil {
		return err
	}
	return workspace.ErrNoSuchJob
}

func (h *WorkspaceHandler) ready(c *gin.Context) bool {
	if h != nil && h.packages != nil {
		return true
	}
	httpx.WriteError(c, http.StatusInternalServerError, "workspace_unavailable", "the package service is not configured")
	return false
}

// fail maps the package service's refusals onto answers.
//
// The codes are the ones §2.16 lists for this area plus the ones the service
// already answers with, and each is a different thing for a client to do: an
// account that is refused because something is running waits and retries, a
// package over the bound is never going to fit, and a package that does not
// match its manifest is one to pack again.
func (h *WorkspaceHandler) fail(c *gin.Context, code string, err error) {
	switch {
	case errors.Is(err, workspace.ErrBusy):
		httpx.WriteError(c, http.StatusConflict, "turn_in_progress",
			"a turn is running, so nothing was written")
	case errors.Is(err, workspace.ErrTooLarge):
		httpx.WriteError(c, http.StatusRequestEntityTooLarge, "package_too_large",
			"the package is over this deployment's limit")
	case errors.Is(err, workspace.ErrBadPackage):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_package", detail(err, workspace.ErrBadPackage))
	case errors.Is(err, workspace.ErrDigestMismatch):
		httpx.WriteError(c, http.StatusBadRequest, "package_digest_mismatch",
			"the package does not match its manifest, so nothing was written")
	case errors.Is(err, workspace.ErrEmptySelection), errors.Is(err, workspace.ErrInvalidRef),
		errors.Is(err, workspace.ErrUnknownKind):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_selection",
			"the selection is empty or names something this build cannot pack")
	case errors.Is(err, workspace.ErrNotPreview):
		httpx.WriteError(c, http.StatusConflict, "not_previewed",
			"this job has not previewed a package")
	case errors.Is(err, workspace.ErrNotRecoverable):
		httpx.WriteError(c, http.StatusConflict, "not_recoverable",
			"this job has nothing to take back")
	case errors.Is(err, workspace.ErrNoSuchJob), errors.Is(err, workspace.ErrNotFound):
		httpx.WriteError(c, http.StatusNotFound, "not_found", "no such job")
	case errors.Is(err, workspace.ErrNoActor), errors.Is(err, workspace.ErrNotConfigured):
		// Both are wiring faults rather than anything a caller did: the route is
		// behind a guard, so a job with no account behind it was reached by a
		// caller the middleware did not see, and a service without its parts
		// would have failed at startup.
		if h.logger != nil {
			h.logger.Error("package route is not configured", "error", err, "request_id", httpx.RequestID(c))
		}
		httpx.WriteError(c, http.StatusInternalServerError, "workspace_unavailable", "the package service is not configured")
	default:
		if h.logger != nil {
			h.logger.Error("package request failed", "error", err, "request_id", httpx.RequestID(c))
		}
		httpx.WriteError(c, http.StatusInternalServerError, code, "the package request failed")
	}
}
