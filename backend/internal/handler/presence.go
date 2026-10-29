package handler

// Being here, as the home page counts it.
//
// Two routes, one shape: a beat says "this minute is being spent" and answers
// with the day so far, and a read asks the same question without writing. They
// are one handler because the answer is one view — a client that just drew the
// page and a client that got its minute counted should not have to learn two
// shapes to draw the same two numbers.
//
// The account is the token's. There is no parameter naming somebody else's
// attendance and no administrative read of it: who was here is about a person,
// and the only person this route answers about is the one holding the token.

import (
	"context"
	"log/slog"
	"net/http"

	"backend/internal/authz"
	"backend/internal/httpx"
	"backend/internal/presence"
	"github.com/gin-gonic/gin"
)

// Presence is the attendance service as this layer reaches it.
type Presence interface {
	Read(ctx context.Context, caller authz.Caller) (presence.Summary, error)
	Touch(ctx context.Context, caller authz.Caller) (presence.Summary, error)
}

// PresenceHandler serves the two presence routes.
type PresenceHandler struct {
	presence Presence
	logger   *slog.Logger
}

// NewPresenceHandler constructs the handler.
func NewPresenceHandler(presence Presence, logger *slog.Logger) *PresenceHandler {
	return &PresenceHandler{presence: presence, logger: logger}
}

// presenceView is the whole answer.
//
// `minutes` is today and `yesterday_minutes` is the day before, because the
// card's line is a comparison and a client that wanted one without the other
// would be asking again. `streak_days` counts days in a row, today included,
// and counts from yesterday when today has no minute yet.
type presenceView struct {
	Minutes          int `json:"minutes"`
	YesterdayMinutes int `json:"yesterday_minutes"`
	StreakDays       int `json:"streak_days"`
}

// Get answers without writing.
func (h *PresenceHandler) Get(c *gin.Context) {
	h.answer(c, false)
}

// Touch records the current minute and answers with the day so far.
func (h *PresenceHandler) Touch(c *gin.Context) {
	h.answer(c, true)
}

func (h *PresenceHandler) answer(c *gin.Context, touch bool) {
	if h == nil || h.presence == nil {
		httpx.WriteError(c, http.StatusInternalServerError, "presence_unavailable", "presence is not configured")
		return
	}
	actor, ok := caller(c)
	if !ok {
		return
	}
	var summary presence.Summary
	var err error
	if touch {
		summary, err = h.presence.Touch(c.Request.Context(), actor)
	} else {
		summary, err = h.presence.Read(c.Request.Context(), actor)
	}
	if err != nil {
		if h.logger != nil {
			h.logger.Error("presence request failed", "error", err, "request_id", httpx.RequestID(c))
		}
		httpx.WriteError(c, http.StatusInternalServerError, "read_presence_failed", "presence could not be read")
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, presenceView{
		Minutes:          summary.TodayMinutes,
		YesterdayMinutes: summary.YesterdayMinutes,
		StreakDays:       summary.StreakDays,
	})
}
