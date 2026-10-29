package handler

// POST /api/v1/chat/complete — one question in, one model answer out.
//
// This is the one route that talks to the model without a turn around it: it
// opens no session, writes no message, sends no history and keeps no cache of
// what was asked. The answer exists for exactly as long as the response does.
//
// What it does keep is the usage record. Every model call in this process is
// made through the shared recorder, and this route is not an exception: the
// call is counted and charged against the caller's quota like any other. That
// row carries token counts and timings — never the question or the answer — so
// "nothing is written down" remains true of the conversation itself.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"backend/internal/directchat"
	"backend/internal/httpx"
	"backend/internal/usage"
	"github.com/gin-gonic/gin"
)

// DirectChat is the one-shot model call as this layer reaches it.
type DirectChat interface {
	Ask(ctx context.Context, question string) (string, error)
}

// DirectChatHandler serves the direct chat route.
type DirectChatHandler struct {
	chat   DirectChat
	logger *slog.Logger
}

// NewDirectChatHandler constructs the direct chat handler.
func NewDirectChatHandler(chat DirectChat, logger *slog.Logger) *DirectChatHandler {
	return &DirectChatHandler{chat: chat, logger: logger}
}

// directChatRequest is the whole body: the question, and nothing else.
type directChatRequest struct {
	Question string `json:"question"`
}

// directChatView is the whole answer. It names the field rather than returning a
// bare string so that a later addition does not turn the body into a different
// shape under the same route.
type directChatView struct {
	Answer string `json:"answer"`
}

// Complete answers one question.
func (h *DirectChatHandler) Complete(c *gin.Context) {
	if h == nil || h.chat == nil {
		httpx.WriteError(c, http.StatusInternalServerError, "chat_unavailable",
			"direct chat is not configured")
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	var request directChatRequest
	if !readJSON(c, maxDirectChatRequestBytes, &request) {
		return
	}
	// The account is declared here because this is where it is known: the
	// service is one step away from a model call and has no business reading a
	// token. Source names this route apart from a turn's calls in the usage
	// report, which is what keeps a direct question from being counted as part
	// of a conversation.
	ctx := usage.WithCall(c.Request.Context(), usage.Call{
		OwnerID: account.UserID,
		Source:  usage.SourceDirectChat,
	})
	answer, err := h.chat.Ask(ctx, request.Question)
	if err != nil {
		h.fail(c, err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, directChatView{Answer: answer})
}

// fail answers a question that did not get one.
func (h *DirectChatHandler) fail(c *gin.Context, err error) {
	if writeQuotaRefusal(c, err) {
		return
	}
	switch {
	case errors.Is(err, directchat.ErrEmptyQuestion):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "question must not be empty")
	case errors.Is(err, directchat.ErrNoAnswer):
		httpx.WriteError(c, http.StatusBadGateway, "empty_answer", "the model returned no answer")
	case errors.Is(err, directchat.ErrUnavailable):
		if h.logger != nil {
			h.logger.Error("direct chat call failed", "error", err, "request_id", httpx.RequestID(c))
		}
		httpx.WriteError(c, http.StatusBadGateway, "model_unavailable", "the model call failed")
	default:
		if h.logger != nil {
			h.logger.Error("direct chat request failed", "error", err, "request_id", httpx.RequestID(c))
		}
		httpx.WriteError(c, http.StatusInternalServerError, "chat_failed", "the question could not be answered")
	}
}
