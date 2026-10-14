package handler

import (
	"context"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"backend/internal/auth"
	"backend/internal/authz"
	"backend/internal/httpx"
	"backend/internal/library"
	"github.com/gin-gonic/gin"
)

// LibraryLifecycle is the file library's listing and its recycle bin.
//
// Every method takes the actor rather than the caller, because everything here
// except the listing changes the state of somebody's library and has to say who
// did it (§2.14). The listing only reads, so it takes the query and no actor.
type LibraryLifecycle interface {
	Search(context.Context, library.SearchQuery) ([]library.FileSummary, error)
	SoftDelete(context.Context, string, library.Actor) (library.FileSummary, error)
	Restore(context.Context, string, library.Actor) (library.FileSummary, error)
	Purge(context.Context, string, library.Actor) error
}

// FileOpener opens one file's bytes for reading.
//
// The download route and the parsers go through one method because there is one
// answer to "may this caller have these bytes" (see Service.Open). What the
// caller does with the reader is the caller's business, and the two callers do
// very different things with it.
type FileOpener interface {
	Open(ctx context.Context, id string, caller authz.Caller) (library.StoredFile, error)
}

// LibraryLifecycleHandler answers the file library's reads and writes beyond the
// upload: the listing, the download, and the two levels of deletion (§2.14).
//
// It is separate from LibraryReadHandler, which answers what a file *is* and
// what was parsed out of it. The split is the shape of the routes: these five are
// about the library as a collection and about a file's place in it, while those
// two are about one file's contents.
type LibraryLifecycleHandler struct {
	files  LibraryLifecycle
	opener FileOpener
	logger *slog.Logger
}

// NewLibraryLifecycleHandler constructs the file library's lifecycle handler.
func NewLibraryLifecycleHandler(files LibraryLifecycle, opener FileOpener, logger *slog.Logger) *LibraryLifecycleHandler {
	return &LibraryLifecycleHandler{files: files, opener: opener, logger: logger}
}

// fileListView is a page of one account's files, live or binned.
type fileListView struct {
	Files []library.FileSummary `json:"files"`
}

// List answers what this account has: the live files, or the recycle bin
// (FILE-01, §2.14).
//
// The bin is this route with `deleted=true` rather than a route of its own,
// because it is the same question asked of one more filter — and because a
// separate route would be a second place the ownership filter has to be
// remembered, which is the mistake §2.3 is about.
func (h *LibraryLifecycleHandler) List(c *gin.Context) {
	query, ok := h.searchQuery(c)
	if !ok {
		return
	}
	files, err := h.files.Search(c.Request.Context(), query)
	if err != nil {
		h.fail(c, "list_files_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, fileListView{Files: files})
}

// Download streams one file's bytes back to its owner (§2.14).
//
// The response is a file transfer rather than a JSON answer, which is why it is
// the one route in the library that does not use the success envelope. The two
// headers it sets beyond the content type are the two that make a browser treat
// what arrives as a download of a file named what the user named it, and not as
// something to render: `Content-Disposition` with the stored name, and
// `X-Content-Type-Options: nosniff` so that the browser does not decide for
// itself that an uploaded file is really HTML. Without the second one, a file
// whose name ends in .html and whose stored type says otherwise is a page this
// origin serves.
func (h *LibraryLifecycleHandler) Download(c *gin.Context) {
	if h == nil || h.opener == nil {
		httpx.WriteError(c, http.StatusInternalServerError, "library_unavailable", "the file library is not configured")
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	stored, err := h.opener.Open(c.Request.Context(), c.Param("fileID"), account)
	if err != nil {
		if errors.Is(err, library.ErrNotFound) {
			httpx.WriteError(c, http.StatusNotFound, "not_found", "no such file")
			return
		}
		if errors.Is(err, library.ErrInvalidFile) {
			httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "the file id is not one this library issued")
			return
		}
		h.fail(c, "download_file_failed", err)
		return
	}
	defer stored.Content.Close()

	c.Header("Content-Disposition", contentDisposition(stored))
	c.Header("X-Content-Type-Options", "nosniff")
	c.DataFromReader(http.StatusOK, stored.SizeBytes, stored.MIMEType, stored.Content, nil)
}

// SoftDelete moves one file into the recycle bin.
func (h *LibraryLifecycleHandler) SoftDelete(c *gin.Context) {
	actor, id, ok := h.actorAndID(c)
	if !ok {
		return
	}
	file, err := h.files.SoftDelete(c.Request.Context(), id, actor)
	if err != nil {
		h.fail(c, "delete_file_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"file": file})
}

// Restore brings one file back out of the recycle bin.
func (h *LibraryLifecycleHandler) Restore(c *gin.Context) {
	actor, id, ok := h.actorAndID(c)
	if !ok {
		return
	}
	file, err := h.files.Restore(c.Request.Context(), id, actor)
	if err != nil {
		h.fail(c, "restore_file_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"file": file})
}

// Purge destroys one file, and refuses while anything still uses it (AC-15).
//
// It answers 204 rather than the file, because after this there is no file to
// describe. The refusal is the interesting answer, and it carries what is in the
// way: a client that showed only "this file is in use" would be telling the user
// to go and find the problem themselves.
func (h *LibraryLifecycleHandler) Purge(c *gin.Context) {
	actor, id, ok := h.actorAndID(c)
	if !ok {
		return
	}
	err := h.files.Purge(c.Request.Context(), id, actor)
	if err != nil {
		var inUse *library.InUseError
		if errors.As(err, &inUse) {
			httpx.WriteErrorDetails(c, http.StatusConflict, "file_in_use",
				"this file is still used, so it was not destroyed", gin.H{"references": inUse.References})
			return
		}
		h.fail(c, "purge_file_failed", err)
		return
	}
	c.Status(http.StatusNoContent)
}

// searchQuery reads the listing's query string, or answers why it cannot.
//
// The account is not a separate answer here, as it is on the routes below: the
// listing carries it inside the query it is narrowed by, and there is no second
// use for it — the rows that come back are already only this account's.
func (h *LibraryLifecycleHandler) searchQuery(c *gin.Context) (library.SearchQuery, bool) {
	if h == nil || h.files == nil {
		httpx.WriteError(c, http.StatusInternalServerError, "library_unavailable", "the file library is not configured")
		return library.SearchQuery{}, false
	}
	account, ok := caller(c)
	if !ok {
		return library.SearchQuery{}, false
	}
	query := library.SearchQuery{
		Caller:   account,
		Filename: strings.TrimSpace(c.Query("filename")),
		Category: strings.TrimSpace(c.Query("category")),
	}
	for _, param := range []struct {
		name  string
		value *time.Time
	}{{"since", &query.Since}, {"until", &query.Until}} {
		raw, exists := c.GetQuery(param.name)
		if !exists || strings.TrimSpace(raw) == "" {
			continue
		}
		// RFC 3339 and nothing else. A listing bound is compared against a stored
		// timestamp, and a format that is guessed at is a format that will one day
		// be guessed at wrongly — an off-by-a-day that looks like data.
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			httpx.WriteError(c, http.StatusBadRequest, "invalid_query",
				param.name+" must be an RFC 3339 timestamp")
			return library.SearchQuery{}, false
		}
		*param.value = parsed
	}
	raw, exists := c.GetQuery("deleted")
	if exists && strings.TrimSpace(raw) != "" {
		deleted, err := strconv.ParseBool(raw)
		if err != nil {
			httpx.WriteError(c, http.StatusBadRequest, "invalid_query", "deleted must be true or false")
			return library.SearchQuery{}, false
		}
		query.Deleted = deleted
	}
	limit, err := intParam(c, "limit")
	if err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_query", err.Error())
		return library.SearchQuery{}, false
	}
	offset, err := intParam(c, "offset")
	if err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_query", err.Error())
		return library.SearchQuery{}, false
	}
	query.Limit, query.Offset = limit, offset
	return query, true
}

// actorAndID reads who is asking and which file, or answers why it cannot.
//
// The context is marked with the request id here rather than in each method, so
// that the entry the operation writes lands beside its own request in the access
// log. Every route below this one changes a file's state, and every one of those
// changes is recorded.
func (h *LibraryLifecycleHandler) actorAndID(c *gin.Context) (library.Actor, string, bool) {
	if h == nil || h.files == nil {
		httpx.WriteError(c, http.StatusInternalServerError, "library_unavailable", "the file library is not configured")
		return library.Actor{}, "", false
	}
	account, ok := callerAccount(c)
	if !ok {
		return library.Actor{}, "", false
	}
	id := strings.TrimSpace(c.Param("fileID"))
	if id == "" {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "the file id is required")
		return library.Actor{}, "", false
	}
	c.Request = c.Request.WithContext(library.WithRequestID(c.Request.Context(), httpx.RequestID(c)))
	return actorOf(account), id, true
}

// actorOf is an account as the file library's audit wants it.
//
// The caller it is scoped by and the name it had on this request, and both are
// needed for the same reason: the scope is what makes another account's file not
// found, and the name is the snapshot the audit stores (§2.6). It is one
// function because the two spellings of it — the upload's and the four below —
// must not drift.
func actorOf(user auth.User) library.Actor {
	return library.Actor{
		Caller: authz.Caller{UserID: user.UserID, Role: user.Role},
		Name:   user.Username,
	}
}

// fail maps the library's refusals onto answers, and everything else onto a 500.
func (h *LibraryLifecycleHandler) fail(c *gin.Context, code string, err error) {
	switch {
	case errors.Is(err, library.ErrNotFound):
		// A file in the recycle bin is not found too, and that is the rule rather
		// than an oversight (§2.14): while it is in there it is not in the
		// library, and a distinct answer would say that the id names something.
		httpx.WriteError(c, http.StatusNotFound, "not_found", "no such file")
	case errors.Is(err, library.ErrInvalidFile):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "the file id is not one this library issued")
	case errors.Is(err, library.ErrNoActor):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "no account was given for this operation")
	default:
		if h.logger != nil {
			h.logger.Error("file library request failed", "request_id", httpx.RequestID(c), "error", err)
		}
		httpx.WriteError(c, http.StatusInternalServerError, code, "the file library could not be reached")
	}
}

// contentDisposition is the header that names a downloaded file.
//
// mime.FormatMediaType and not a quoted string built by hand: a filename is
// whatever the user called their file, and it is the one value on this response
// that comes from them. FormatMediaType encodes anything that is not plain ASCII
// as an RFC 2231 `filename*`, so a file called 报告.pdf arrives under that name
// rather than under a row of percent signs — and it refuses to put a value into
// a header that a header cannot hold.
func contentDisposition(stored library.StoredFile) string {
	name := stored.Filename
	// The stored name has already been through cleanFilename, which refuses
	// control characters, so this is a second gate rather than the only one. It
	// is here because this is where a filename becomes part of a header, and the
	// id is always a safe name to fall back to.
	if name == "" || strings.ContainsAny(name, "\r\n\x00") {
		name = stored.ID
	}
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": name})
	if disposition == "" {
		return "attachment"
	}
	return disposition
}
