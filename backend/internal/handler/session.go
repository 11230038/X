package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"backend/internal/authz"
	"backend/internal/httpx"
	"backend/internal/session"
	"github.com/gin-gonic/gin"
)

// Sessions is the session use case consumed by the HTTP handler.
type Sessions interface {
	Create(context.Context, authz.Caller, session.Kind) (session.Session, error)
	Get(context.Context, string, authz.Caller) (session.Session, error)
	List(context.Context, session.ListOptions) ([]session.Session, error)
	Rename(context.Context, string, string, authz.Caller) (session.Session, error)
	SoftDelete(context.Context, string, authz.Caller) error
	Restore(context.Context, string, authz.Caller) error
	DeletePermanently(context.Context, string, authz.Caller) error
	// Claim takes over a session that belongs to no account (§2.5). It is the
	// exit from the state every row written before V5 is in, and the only write
	// here whose subject is not something the caller already had.
	Claim(context.Context, string, authz.Caller) error
	Messages(context.Context, string, authz.Caller) ([]session.Message, error)
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
	SessionID string `json:"session_id"`
	Title     string `json:"title"`
	// Kind is which list the session belongs to. It is present on every
	// session, including one just created: a client that only ever asks for
	// one kind still needs it to label a row it got from the other list.
	Kind      string     `json:"kind"`
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
//
// The kind is a query parameter rather than a body because this request has
// never had one: a client that wants a chat session sends exactly what it sent
// before this version, and only the pages that own a partition add
// `?kind=writing` or `?kind=mastery`.
func (h *SessionHandler) Create(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	kind, _, err := kindParam(c, false)
	if err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	created, err := h.sessions.Create(c.Request.Context(), account, kind)
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
	account, ok := caller(c)
	if !ok {
		return
	}
	deleted, err := boolParam(c, "deleted")
	if err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	kind, allKinds, err := kindParam(c, true)
	if err != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	options := session.ListOptions{
		Caller:   account,
		Deleted:  deleted,
		Kind:     kind,
		AllKinds: allKinds,
	}
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
	account, ok := caller(c)
	if !ok {
		return
	}
	record, err := h.sessions.Get(c.Request.Context(), c.Param("sessionID"), account)
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
	account, ok := caller(c)
	if !ok {
		return
	}
	renamed, err := h.sessions.Rename(c.Request.Context(), c.Param("sessionID"), request.Title, account)
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
	account, ok := caller(c)
	if !ok {
		return
	}
	if err := h.sessions.SoftDelete(c.Request.Context(), c.Param("sessionID"), account); err != nil {
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
	account, ok := caller(c)
	if !ok {
		return
	}
	if err := h.sessions.Restore(c.Request.Context(), c.Param("sessionID"), account); err != nil {
		h.fail(c, "restore_session_failed", err)
		return
	}
	record, err := h.sessions.Get(c.Request.Context(), c.Param("sessionID"), account)
	if err != nil {
		h.fail(c, "restore_session_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, toSessionView(record))
}

// Claim takes over a session that belongs to no account (§2.5).
//
// It answers with the session rather than with a 204, because the one thing the
// caller learns from this is that the row is now theirs — and the list they were
// looking at a moment ago did not contain it, so there is a row they do not have
// in hand. A 404 here means either that no such session exists or that it
// already belongs to somebody else, and the two are deliberately one answer: the
// second fact is not one an account is entitled to learn about another's row.
func (h *SessionHandler) Claim(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	sessionID := c.Param("sessionID")
	if err := h.sessions.Claim(c.Request.Context(), sessionID, account); err != nil {
		h.fail(c, "claim_session_failed", err)
		return
	}
	record, err := h.sessions.Get(c.Request.Context(), sessionID, account)
	if err != nil {
		h.fail(c, "claim_session_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, toSessionView(record))
}

// DeletePermanently erases a session that is already in the recycle bin.
func (h *SessionHandler) DeletePermanently(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	if err := h.sessions.DeletePermanently(c.Request.Context(), c.Param("sessionID"), account); err != nil {
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
	account, ok := caller(c)
	if !ok {
		return
	}
	history, err := h.sessions.Messages(c.Request.Context(), c.Param("sessionID"), account)
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
	if errors.Is(err, session.ErrInvalidKind) {
		// The query parameter is validated before the call, so reaching this
		// means a caller inside the process passed a kind nobody writes.
		httpx.WriteError(c, http.StatusBadRequest, "invalid_kind", "the session kind is not one this version writes")
		return
	}
	h.logger.Error("session request failed", "error", err, "request_id", httpx.RequestID(c))
	httpx.WriteError(c, http.StatusInternalServerError, code, "the session request failed")
}

func toSessionView(record session.Session) sessionView {
	kind := record.Kind
	if kind == "" {
		kind = session.KindChat
	}
	response := sessionView{
		SessionID: record.ID,
		Title:     record.Title,
		Kind:      string(kind),
		CreatedAt: record.CreatedAt,
		UpdatedAt: record.UpdatedAt,
	}
	if record.Deleted() {
		deletedAt := record.DeletedAt
		response.DeletedAt = &deletedAt
	}
	return response
}

// kindParam reads the optional kind selector.
//
// Absent means chat, not "both". Every request written before writing sessions
// existed was about the chat list, and a request that forgets the parameter
// afterwards should keep meaning that rather than silently merge two histories.
//
// `all` is only accepted where narrowing by surface makes no sense: the recycle
// bin and the export picker ask for every row so a deleted writing, mastery or
// question-bank session stays reachable. Creating a session must name one door.
func kindParam(c *gin.Context, allowAll bool) (session.Kind, bool, error) {
	raw := strings.TrimSpace(c.Query("kind"))
	if raw == "" {
		return session.KindChat, false, nil
	}
	if allowAll && raw == "all" {
		return "", true, nil
	}
	kind := session.Kind(raw)
	if !kind.Valid() {
		if allowAll {
			return "", false, fmt.Errorf("kind must be chat, writing, mastery, question_bank or all")
		}
		return "", false, fmt.Errorf("kind must be chat, writing, mastery or question_bank")
	}
	return kind, false, nil
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
