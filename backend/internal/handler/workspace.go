package handler

// The package endpoints (§2.13, §2.16): pack a selection out, take the package
// away, look at one before writing it, commit it, read back what a job did, and
// take one back.
//
// Seven routes and one idea, which is the reason they are one file: a job is the
// subject of six of them. An export makes one, the download hands over what it
// made, a preview makes one, a commit advances one, the status route reads one,
// and the recovery route moves one that failed. What a client holds between two
// requests is therefore a job id rather than a package or a plan, and every
// answer here is a job — except the download, which is the bytes themselves,
// because a package's whole point is that it is the file a person can save.
//
// A request that acts on a job already made — the commit, the recovery — takes
// its account from the token and passes it on, and the service refuses a job
// that is not that account's preview. The two routes that read a job by the id
// in the URL are where that does not hold, and they are scoped here instead:
// `workspace.Repository.Find` reads a job by id and nothing else, deliberately
// — the store's comment says which account a job belongs to is the route's
// business (§2.16) — so the account is applied below, as the same answer an id
// that was never issued gets (AUTH-06). They are the status route and the
// download, and the download is the one where getting that wrong would hand
// somebody another account's work rather than merely tell them about it.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"backend/internal/auth"
	"backend/internal/authz"
	"backend/internal/httpx"
	"backend/internal/workspace"
	"github.com/gin-gonic/gin"
)

// Workspace is the package use case consumed by the HTTP handler.
type Workspace interface {
	Export(context.Context, workspace.ExportRequest) (workspace.Job, error)
	List(context.Context, workspace.ListRequest) ([]workspace.Job, error)
	Preview(context.Context, workspace.PreviewRequest) (workspace.Preview, error)
	Import(context.Context, workspace.CommitRequest) (workspace.Job, error)
	// Job reads one job back by id. It takes no caller, and the route below is
	// where the account is applied rather than the service, because a job is
	// looked up by the id in the URL and the store's read is by that id.
	Job(context.Context, string) (workspace.Job, error)
	// Download opens a job's package as a stream. It takes the job rather than
	// the id because the route has already read the row it is about — the account
	// has to be applied to that row before anything is opened, and handing this
	// an id would be handing it an unscoped read.
	//
	// It is not Open: that one parses a package into memory, which is what the
	// import preview needs and what a download must not do (§2.5).
	Download(context.Context, workspace.Job) (*workspace.StoredArchive, error)
	Recover(context.Context, workspace.RecoverRequest) (workspace.Job, error)
	// MaxArchiveBytes is the deployment's bound on one package
	// (WORKSPACE_MAX_ARCHIVE_BYTES). The upload is bounded by it here so that a
	// body far over the bound is refused before it is written to a temporary
	// file, which is the same reason the library's upload carries its own bound
	// rather than reading whatever arrives.
	MaxArchiveBytes() int64
}

// WorkspaceHandler handles the package routes.
type WorkspaceHandler struct {
	packages Workspace
	logger   *slog.Logger
}

// NewWorkspaceHandler constructs the package HTTP handler.
func NewWorkspaceHandler(packages Workspace, logger *slog.Logger) *WorkspaceHandler {
	return &WorkspaceHandler{packages: packages, logger: logger}
}

// Body bounds. The largest thing a client sends here that is not a file is a
// selection — a list of refs, at some forty bytes each — and a package can hold
// thousands of objects, so this is the one bound in this package measured
// against a list rather than against a sentence.
const (
	maxWorkspaceRequestBytes int64 = 1 << 20
	// multipartOverheadBytes is what an upload of one file weighs beyond its
	// bytes: the boundaries, the part's own headers and the form's trailer. It is
	// here so that a package of exactly the bound is not refused for the
	// punctuation around it — and it is the reason the reader's bound is not the
	// package's bound: a file part a few kilobytes over the limit still fits
	// inside `max + this`, so the part's own size is checked separately below.
	multipartOverheadBytes int64 = 8 << 10
)

// workspaceActor is the account behind a package job, with the name as it was.
//
// The name travels because an audit entry is a snapshot (§2.6) and this is the
// last place before the write that still has one. It is a different actor type
// from the library's for the reason every domain has its own: a job is the
// package service's subject, and the two structs being the same today is not a
// reason for one package to be handed the other's.
func workspaceActor(user auth.User) workspace.Actor {
	return workspace.Actor{
		Caller: authz.Caller{UserID: user.UserID, Role: user.Role},
		Name:   user.Username,
	}
}

// exportRequest is a selection to pack.
type exportRequest struct {
	// Refs are the objects to pack, by kind and id. §2.13's export takes what the
	// user selected, and there is no rule here that decides what that should
	// have been — an export that widened a selection would be packing something
	// the user did not ask for.
	Refs []workspace.Ref `json:"refs"`
}

// commitRequest names the preview to commit.
type commitRequest struct {
	JobID string `json:"job_id"`
}

// jobView is one job as a client reads it.
//
// The whole row is here and not a summary of it, because a job is what the
// status route exists to answer: a client that had to ask a second question to
// learn which objects landed, or what the package held, would be reading a
// screen assembled out of two answers that can disagree.
type jobView struct {
	ID string `json:"job_id"`
	// Kind is "export" or "import".
	Kind string `json:"kind"`
	// State is "preview", "running", "committed", "failed" or "recovered". A
	// failed job is answered as a job rather than as a failed request, and this
	// is the field that says so.
	State string `json:"state"`
	// ArchivePath is where the package sits under the configured archive root.
	// It is relative, so it is a fact about the deployment that can be said
	// without naming where the deployment's disk is.
	ArchivePath string `json:"archive_path,omitempty"`
	// Manifest is what the package holds, and Written what an import has landed
	// so far. The second is what a client draws the recovery button against.
	Manifest []workspace.Entry   `json:"manifest,omitempty"`
	Written  []workspace.Written `json:"written,omitempty"`
	Counts   map[string]int      `json:"counts,omitempty"`
	// FailureCode is why a failed job failed, from the closed set in types.go.
	FailureCode string    `json:"failure_code,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func toJobView(job workspace.Job) jobView {
	return jobView{
		ID: job.ID, Kind: string(job.Kind), State: string(job.State),
		ArchivePath: job.ArchivePath, Manifest: job.Manifest, Written: job.Written,
		Counts: job.Counts, FailureCode: job.FailureCode,
		CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt,
	}
}

// Export packs a selection and answers with the job that did it (§2.13).
//
// It is synchronous, and 201 rather than 202: there is no queue behind this, and
// a client told that a package is being made when it has already been made would
// have to poll for something it could have been handed. A package that was
// collected and could not be written is the case the job table is for, and it
// answers below with a job in state `failed` and the code that says why.
func (h *WorkspaceHandler) Export(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := callerAccount(c)
	if !ok {
		return
	}
	var request exportRequest
	if !readJSON(c, maxWorkspaceRequestBytes, &request) {
		return
	}
	job, err := h.packages.Export(c.Request.Context(), workspace.ExportRequest{
		Actor: workspaceActor(account), Refs: request.Refs,
	})
	h.answer(c, job, err, http.StatusCreated)
}

// ListExports lists the packages this account has made (§2.16).
//
// The direction is not a parameter. These are the export route's own, and the
// two directions are two listings of two things a client draws in two places —
// a request that could ask for either would be a request whose meaning is in its
// query string.
func (h *WorkspaceHandler) ListExports(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := callerAccount(c)
	if !ok {
		return
	}
	limit, err := intParam(c, "limit")
	if err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	jobs, err := h.packages.List(c.Request.Context(), workspace.ListRequest{
		OwnerID: account.UserID, Kind: workspace.JobExport, Limit: limit,
	})
	if err != nil {
		h.fail(c, "workspace_failed", err)
		return
	}
	views := make([]jobView, 0, len(jobs))
	for _, job := range jobs {
		views = append(views, toJobView(job))
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"jobs": views, "count": len(views)})
}

// Preview looks at an uploaded package and answers what committing it would do
// (§2.13).
//
// The body is the package itself, multipart, under the field named `file` — the
// same shape the library's upload takes — because this is bytes rather than an
// object: a package encoded as a string in JSON would be a ZIP with every byte
// escaped, and the point of the format is that it is the same file a person can
// save and open.
//
// The answer carries the job id the commit names. That is what makes the second
// request about the thing that was looked at rather than about a package the
// client sends again: the commit reads the staged bytes back.
func (h *WorkspaceHandler) Preview(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := callerAccount(c)
	if !ok {
		return
	}
	file, size, ok := readPackage(c, h.packages.MaxArchiveBytes())
	if !ok {
		return
	}
	defer file.Close()
	preview, err := h.packages.Preview(c.Request.Context(), workspace.PreviewRequest{
		Actor: workspaceActor(account), Package: file, Size: size,
	})
	if err != nil {
		h.fail(c, "preview_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"preview": preview})
}

// Import commits a package that has been previewed (§2.13).
//
// A commit that failed is answered with the job rather than with an error, and
// that is the one thing this route insists on. The job is what holds the ids
// that landed, and the recovery route takes those ids and nothing else; a
// failure flattened into a 500 would leave a client holding a half-written
// import and no handle on it. The status code says the request is over, and the
// job's state says how it went.
func (h *WorkspaceHandler) Import(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := callerAccount(c)
	if !ok {
		return
	}
	var request commitRequest
	if !readJSON(c, maxWorkspaceRequestBytes, &request) {
		return
	}
	job, err := h.packages.Import(c.Request.Context(), workspace.CommitRequest{
		Actor: workspaceActor(account), JobID: request.JobID,
	})
	h.answer(c, job, err, http.StatusOK)
}

// Job reads one job back (§2.16).
//
// A job that belongs to another account is answered as a job that is not there,
// which is AUTH-06's rule and the store's own comment taken at its word: the row
// is read by id and the account is applied here, so "not yours" and "never was"
// are one answer and neither confirms the other.
func (h *WorkspaceHandler) Job(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := callerAccount(c)
	if !ok {
		return
	}
	job, err := h.packages.Job(c.Request.Context(), c.Param("jobID"))
	if err != nil || job.OwnerID != account.UserID {
		h.fail(c, "workspace_failed", errNoSuchJob(job, err))
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"job": toJobView(job)})
}

// DownloadExports streams one job's package back to its owner (§2.5, §2.9).
//
// It is the route the export existed without: a package could be made, listed
// and read back as a job, and the bytes it was made of were reachable only from
// inside the process. An export nobody can take away is not an export, which is
// why this is here and why it is this route's whole existence.
//
// Two things about how it is written are the design, and neither shows in a
// response:
//
//   - The account is applied here, before anything is opened, exactly as the
//     status route applies it. `Repository.Find` reads a job by id and nothing
//     else — that is the store's deliberate shape — so an id belonging to
//     another account and an id that was never issued get one answer here and
//     neither confirms the other (AUTH-06).
//   - The package is streamed, not parsed. The primitive behind this hands over
//     a file and its length; `workspace.Open` — which parses every member of a
//     package into memory, up to WORKSPACE_MAX_ARCHIVE_BYTES — is deliberately
//     not on this path. This is the one place in the version where getting it
//     wrong cannot be caught by a case: a parsed 512 MiB package still streams
//     bytes that match the disk, and only the process's memory would say
//     otherwise.
//
// The type is a constant rather than a stored one. A package has no type to read
// — its row is a ZIP — and the only thing a stored type could add is a value
// that came from what somebody uploaded, which is not a thing to hand a browser
// as the type of a download.
func (h *WorkspaceHandler) DownloadExports(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := callerAccount(c)
	if !ok {
		return
	}
	job, err := h.packages.Job(c.Request.Context(), c.Param("jobID"))
	if err != nil || job.OwnerID != account.UserID {
		h.fail(c, "workspace_failed", errNoSuchJob(job, err))
		return
	}
	stored, err := h.packages.Download(c.Request.Context(), job)
	if err != nil {
		h.failDownload(c, err)
		return
	}
	defer stored.Content.Close()

	// The same two headers the library's download sets, and for the same reason:
	// what arrives is a file to save, named, and not something for the browser to
	// decide about.
	c.Header("Content-Disposition", contentDisposition(job.ID+".zip", job.ID))
	c.Header("X-Content-Type-Options", "nosniff")
	c.DataFromReader(http.StatusOK, stored.SizeBytes, "application/zip", stored.Content, nil)
}

// failDownload answers a download that could not be opened.
//
// The one thing it says that the shared mapping does not: a job that has no
// package is 404 here rather than the 400 an upload of a bad package gets.
// `ErrBadPackage` means two different things by direction — on the way in it is
// the caller's package that is wrong, and on the way out it is this job that has
// nothing to hand over — and the answer to the second is the answer to a job
// that is not there, which is what a client can act on: there is nothing to
// fetch. A 200 with an empty body would be the one answer that is a lie.
func (h *WorkspaceHandler) failDownload(c *gin.Context, err error) {
	if errors.Is(err, workspace.ErrBadPackage) {
		httpx.WriteError(c, http.StatusNotFound, "not_found", "no such package")
		return
	}
	h.fail(c, "download_export_failed", err)
}

// Recover takes back an import that did not commit (§2.13).
//
// Nothing here asks whether the job failed: that is the service's decision, and
// it refuses a committed job because its work is kept and a recovery that ran on
// it would be a delete wearing recovery's name. The route's whole job is to name
// the job and pass the account on.
func (h *WorkspaceHandler) Recover(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := callerAccount(c)
	if !ok {
		return
	}
	job, err := h.packages.Recover(c.Request.Context(), workspace.RecoverRequest{
		Actor: workspaceActor(account), JobID: c.Param("jobID"),
	})
	h.answer(c, job, err, http.StatusOK)
}

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
