package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"backend/internal/authz"
	"backend/internal/httpx"
	"backend/internal/reading"
	"github.com/gin-gonic/gin"
)

// Readings is the reading layer's use case as HTTP reaches it.
//
// It is the domain service's whole surface apart from one thing: what runs an
// extension is the registry, which is a value rather than a method on the
// service, so it is held beside this interface rather than inside it (§2.9).
//
// Every method takes the account last, because every one of them reads or writes
// something an account owns: a material is the caller's, and a position, a mark
// or a pairing is the caller's through the material it hangs off.
type Readings interface {
	Materials(ctx context.Context, workspaceID string, caller authz.Caller) ([]reading.Material, error)
	Material(ctx context.Context, materialID string, caller authz.Caller) (reading.Material, error)
	Outline(ctx context.Context, materialID string, caller authz.Caller) (reading.Outline, error)
	Create(ctx context.Context, fileID, sourceType string, caller authz.Caller) (reading.Material, error)
	Position(ctx context.Context, materialID, sessionID string, caller authz.Caller) (reading.Position, bool, error)
	SavePosition(ctx context.Context, request reading.SavePositionRequest, caller authz.Caller) (reading.Position, error)
	Marks(ctx context.Context, materialID string, caller authz.Caller) ([]reading.Mark, error)
	Bookmarks(ctx context.Context, caller authz.Caller) ([]reading.Bookmark, error)
	AddMark(ctx context.Context, request reading.MarkRequest, caller authz.Caller) (reading.Mark, error)
	Resolve(ctx context.Context, materialID string, locator reading.Locator, caller authz.Caller) (reading.Passage, error)
	// Pairings is the bilingual read: whether this material's bilingual reading is
	// enabled and what it could be paired with (§2.10). ConfirmPairing and
	// CancelPairing are the two writes that change it, and cancelling reports
	// whether there was anything left to cancel.
	Pairings(ctx context.Context, materialID string, caller authz.Caller) (reading.Pairings, error)
	ConfirmPairing(ctx context.Context, request reading.PairingRequest, caller authz.Caller) (reading.Pairing, error)
	CancelPairing(ctx context.Context, pairingID string, caller authz.Caller) (bool, error)
}

// ReadingHandler handles the reading layer: the catalogue, one material's units,
// a passage read by its coordinate, where a reader stopped, what they wrote in
// the margin, and the extensions that can be run over a passage.
//
// What it puts on the wire — the views and the bodies it accepts — is
// reading_views.go, and the routes themselves are reading_handlers.go.
type ReadingHandler struct {
	readings   Readings
	extensions *reading.Registry
	logger     *slog.Logger
}

// NewReadingHandler constructs the reading HTTP handler.
func NewReadingHandler(readings Readings, extensions *reading.Registry, logger *slog.Logger) *ReadingHandler {
	return &ReadingHandler{readings: readings, extensions: extensions, logger: logger}
}

func (h *ReadingHandler) ready(c *gin.Context) bool {
	if h != nil && h.readings != nil {
		return true
	}
	httpx.WriteError(c, http.StatusInternalServerError, "reading_unavailable", "the reading layer is not configured")
	return false
}

// fail reports a reading failure, separating what the reader can act on from
// what only an operator can.
//
// A coordinate the material does not have is the client's to fix and is a 400;
// a coordinate that has moved underneath it is a conflict and a 409, because the
// reader's answer is to read again and decide rather than to reword anything.
//
// A quota refusal is asked about first, and it is the one failure here that is
// not about the material at all: the extension asked for a model call and the
// account may not make one, which is the requester's answer rather than the
// material's.
func (h *ReadingHandler) fail(c *gin.Context, code string, err error) {
	if writeQuotaRefusal(c, err) {
		return
	}
	switch {
	case errors.Is(err, reading.ErrNotFound):
		httpx.WriteError(c, http.StatusNotFound, "not_found", "no such material")
	case errors.Is(err, reading.ErrInvalidRequest):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request", detail(err, reading.ErrInvalidRequest))
	case errors.Is(err, reading.ErrStalePosition):
		httpx.WriteError(c, http.StatusConflict, "stale_position", detail(err, reading.ErrStalePosition))
	case errors.Is(err, reading.ErrDuplicate):
		httpx.WriteError(c, http.StatusConflict, "conflict", detail(err, reading.ErrDuplicate))
	default:
		if h.logger != nil {
			h.logger.Error("reading request failed", "error", err, "request_id", httpx.RequestID(c))
		}
		httpx.WriteError(c, http.StatusInternalServerError, code, "the reading request failed")
	}
}
