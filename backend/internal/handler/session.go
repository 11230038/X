package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"backend/internal/httpx"
	"backend/internal/session"
	"github.com/gin-gonic/gin"
)

// Sessions is the session use case consumed by the HTTP handler.
type Sessions interface {
	Create(context.Context) (session.Session, error)
	Get(context.Context, string) (session.Session, error)
	List(context.Context, session.ListOptions) ([]session.Session, error)
	Rename(context.Context, string, string) (session.Session, error)
	SoftDelete(context.Context, string) error
	Restore(context.Context, string) error
	DeletePermanently(context.Context, string) error
	Messages(context.Context, string) ([]session.Message, error)
}

// SessionHandler handles the session list, its titles, its recycle bin and its
// message history.
type SessionHandler struct {
	sessions Sessions
	logger   *slog.Logger
}

// NewSessionHandler constructs the session HTTP handler.
func NewSessionHandler(sessions Sessions, logger *slog.Logger) *SessionHandler {
	return &SessionHandler{sessions: sessions, logger: logger}
}

// sessionView is a session as a client reads it.
//
// DeletedAt is absent on a live session rather than null, because the two
// states a client acts on are "in the list" and "in the bin", and the way to
// tell them apart is whether the field is there at all.
type sessionView struct {
	SessionID string     `json:"session_id"`
	Title     string     `json:"title"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

type messageView struct {
	MessageID  string    `json:"message_id"`
	SessionID  string    `json:"session_id"`
	Role       string    `json:"role"`
	Content    string    `json:"content"`
	Capability string    `json:"capability,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type sessionListView struct {
	Sessions []sessionView `json:"sessions"`
	Limit    int           `json:"limit"`
	Offset   int           `json:"offset"`
}

type messageListView struct {
	Messages []messageView `json:"messages"`
}

type renameRequest struct {
	Title string `json:"title"`
}

// Create starts an empty conversation.
func (h *SessionHandler) Create(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	created, err := h.sessions.Create(c.Request.Context())
	if err != nil {
		h.fail(c, "create_session_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusCreated, toSessionView(created))
}

// List returns the live sessions or the recycle bin, never both.
func (h *SessionHandler) List(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	deleted, err := boolParam(c, "deleted")
	if err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	options := session.ListOptions{Deleted: deleted}
	if options.Limit, err = intParam(c, "limit"); err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	if options.Offset, err = intParam(c, "offset"); err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}

	found, err := h.sessions.List(c.Request.Context(), options)
	if err != nil {
		h.fail(c, "list_sessions_failed", err)
		return
	}
	listed := make([]sessionView, 0, len(found))
	for _, record := range found {
		listed = append(listed, toSessionView(record))
	}
	httpx.WriteSuccess(c, http.StatusOK, sessionListView{
		Sessions: listed,
		Limit:    options.Limit,
		Offset:   options.Offset,
	})
}

// Get returns one session.
func (h *SessionHandler) Get(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	record, err := h.sessions.Get(c.Request.Context(), c.Param("sessionID"))
	if err != nil {
		h.fail(c, "get_session_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, toSessionView(record))
}

// Rename sets a session's title.
func (h *SessionHandler) Rename(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var request renameRequest
	if !readJSON(c, maxSessionRequestBytes, &request) {
		return
	}
	renamed, err := h.sessions.Rename(c.Request.Context(), c.Param("sessionID"), request.Title)
	if err != nil {
		h.fail(c, "rename_session_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, toSessionView(renamed))
}

// SoftDelete moves a session to the recycle bin.
func (h *SessionHandler) SoftDelete(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	if err := h.sessions.SoftDelete(c.Request.Context(), c.Param("sessionID")); err != nil {
		h.fail(c, "delete_session_failed", err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Restore takes a session back out of the recycle bin.
func (h *SessionHandler) Restore(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	if err := h.sessions.Restore(c.Request.Context(), c.Param("sessionID")); err != nil {
		h.fail(c, "restore_session_failed", err)
		return
	}
	record, err := h.sessions.Get(c.Request.Context(), c.Param("sessionID"))
	if err != nil {
		h.fail(c, "restore_session_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, toSessionView(record))
}

// DeletePermanently erases a session that is already in the recycle bin.
func (h *SessionHandler) DeletePermanently(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	if err := h.sessions.DeletePermanently(c.Request.Context(), c.Param("sessionID")); err != nil {
		h.fail(c, "destroy_session_failed", err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Messages returns a session's history, which is what a client renders when it
// opens a conversation it was not present for.
func (h *SessionHandler) Messages(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	history, err := h.sessions.Messages(c.Request.Context(), c.Param("sessionID"))
	if err != nil {
		h.fail(c, "list_messages_failed", err)
		return
	}
	listed := make([]messageView, 0, len(history))
	for _, record := range history {
		listed = append(listed, messageView{
			MessageID:  record.ID,
			SessionID:  record.SessionID,
			Role:       string(record.Role),
			Content:    record.Content,
			Capability: record.Capability,
			CreatedAt:  record.CreatedAt,
		})
	}
	httpx.WriteSuccess(c, http.StatusOK, messageListView{Messages: listed})
}

func (h *SessionHandler) ready(c *gin.Context) bool {
	if h != nil && h.sessions != nil {
		return true
	}
	httpx.WriteError(c, http.StatusInternalServerError, "sessions_unavailable", "the session store is not configured")
	return false
}

// fail reports a session failure, distinguishing what the client can act on
// from what only an operator can.
func (h *SessionHandler) fail(c *gin.Context, code string, err error) {
	if errors.Is(err, session.ErrNotFound) {
		httpx.WriteError(c, http.StatusNotFound, "not_found", "no such session")
		return
	}
	if errors.Is(err, session.ErrInvalidTitle) {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_title", "the title must not be empty")
		return
	}
	h.logger.Error("session request failed", "error", err, "request_id", httpx.RequestID(c))
	httpx.WriteError(c, http.StatusInternalServerError, code, "the session request failed")
}

func toSessionView(record session.Session) sessionView {
	response := sessionView{
		SessionID: record.ID,
		Title:     record.Title,
		CreatedAt: record.CreatedAt,
		UpdatedAt: record.UpdatedAt,
	}
	if record.Deleted() {
		deletedAt := record.DeletedAt
		response.DeletedAt = &deletedAt
	}
	return response
}

// boolParam reads an optional boolean query parameter.
func boolParam(c *gin.Context, name string) (bool, error) {
	raw, exists := c.GetQuery(name)
	if !exists || strings.TrimSpace(raw) == "" {
		return false, nil
	}
	return strconv.ParseBool(raw)
}

// intParam reads an optional non-negative integer query parameter.
func intParam(c *gin.Context, name string) (int, error) {
	raw, exists := c.GetQuery(name)
	if !exists || strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, errors.New(name + " must be a non-negative integer")
	}
	return value, nil
}
