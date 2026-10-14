package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"backend/internal/authz"
	"backend/internal/httpx"
	"backend/internal/reading"
	"backend/internal/usage"
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
type ReadingHandler struct {
	readings   Readings
	extensions *reading.Registry
	logger     *slog.Logger
}

// NewReadingHandler constructs the reading HTTP handler.
func NewReadingHandler(readings Readings, extensions *reading.Registry, logger *slog.Logger) *ReadingHandler {
	return &ReadingHandler{readings: readings, extensions: extensions, logger: logger}
}

// materialView is one material as a client reads it.
//
// It carries no text and no document: a material is a row pointing at a
// document, and the words are read through the outline and the content route,
// one unit at a time (§2.8).
type materialView struct {
	MaterialID string    `json:"material_id"`
	ContentID  string    `json:"content_id"`
	Filename   string    `json:"filename"`
	SourceType string    `json:"source_type,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	// Pinned and Opened are the workspace tab's state, and they are absent when
	// the catalogue was read outside a workspace — absent says nobody has said,
	// which is not the same as false saying nobody has.
	Pinned *bool `json:"pinned,omitempty"`
	Opened *bool `json:"opened,omitempty"`
}

// outlineView is one material and what it is made of.
//
// The units are the domain's own type rather than a second struct with the same
// fields. Its JSON is a contract — a client holds a unit's ordinal and span and
// sends them back as a locator — so a view beside it would be a second
// definition of the same coordinates, free to drift.
type outlineView struct {
	Material  materialView   `json:"material"`
	Units     []reading.Unit `json:"units"`
	TextChars int64          `json:"text_chars"`
	// Position is where this session stopped, and it is here rather than behind
	// a route of its own because opening a material is one question — "what is
	// this and where was I" — and a read that answered half of it would cost a
	// second round trip before the first screen could be drawn. It is absent
	// when the client named no session, which is a catalogue read.
	Position *positionView `json:"position,omitempty"`
}

// passageView is one span of text and the coordinate it was read at.
//
// The locator that comes back is the one that was resolved rather than the one
// that was asked for: a client that sent a stale coordinate is shown where the
// text it got actually lives.
type passageView struct {
	MaterialID string          `json:"material_id"`
	Locator    reading.Locator `json:"locator"`
	Text       string          `json:"text"`
}

// positionView is where one reader stopped in one material.
type positionView struct {
	MaterialID string          `json:"material_id"`
	SessionID  string          `json:"session_id"`
	Locator    reading.Locator `json:"locator"`
	Revision   int64           `json:"revision"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// markView is one thing a reader wrote in the margin.
type markView struct {
	MarkID     string           `json:"mark_id"`
	MaterialID string           `json:"material_id"`
	SessionID  string           `json:"session_id,omitempty"`
	Kind       reading.MarkKind `json:"kind"`
	Locator    reading.Locator  `json:"locator"`
	Body       string           `json:"body,omitempty"`
	Revision   int64            `json:"revision"`
	CreatedAt  time.Time        `json:"created_at"`
	UpdatedAt  time.Time        `json:"updated_at"`
}

type createMaterialRequest struct {
	FileID     string `json:"file_id"`
	SourceType string `json:"source_type"`
}

type savePositionRequest struct {
	SessionID string          `json:"session_id"`
	Locator   reading.Locator `json:"locator"`
	// Revision is the revision the client read, and zero means it read none —
	// that is a request to create the position rather than to move one (§2.11).
	Revision int64 `json:"revision"`
}

type addMarkRequest struct {
	SessionID string          `json:"session_id"`
	Kind      string          `json:"kind"`
	Locator   reading.Locator `json:"locator"`
	Body      string          `json:"body"`
}

type runExtensionRequest struct {
	// Locator says which passage the extension is about. The text itself is not
	// on the wire: it is resolved here, server-side, which is what keeps what an
	// extension is handed this service's answer rather than the client's (§2.9).
	Locator reading.Locator `json:"locator"`
}

// List returns the catalogue, most recently touched first.
func (h *ReadingHandler) List(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	found, err := h.readings.Materials(c.Request.Context(), c.Query("workspace_id"), account)
	if err != nil {
		h.fail(c, "list_materials_failed", err)
		return
	}
	listed := make([]materialView, 0, len(found))
	for _, record := range found {
		listed = append(listed, toMaterialView(record))
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"materials": listed})
}

// Create opens a file that has already been uploaded in the reader.
func (h *ReadingHandler) Create(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	var request createMaterialRequest
	if !readJSON(c, maxReadingRequestBytes, &request) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	created, err := h.readings.Create(c.Request.Context(), request.FileID, request.SourceType, account)
	if err != nil {
		h.fail(c, "create_material_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusCreated, gin.H{"material": toMaterialView(created)})
}

// Get returns one material's outline: the units of its text in reading order,
// and — when the client named a session — where that session stopped.
func (h *ReadingHandler) Get(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	materialID := c.Param("materialID")
	outline, err := h.readings.Outline(c.Request.Context(), materialID, account)
	if err != nil {
		h.fail(c, "get_material_failed", err)
		return
	}
	view := outlineView{
		Material:  toMaterialView(outline.Material),
		Units:     outline.Units,
		TextChars: outline.TextChars,
	}
	if sessionID := strings.TrimSpace(c.Query("session_id")); sessionID != "" {
		position, saved, err := h.readings.Position(c.Request.Context(), materialID, sessionID, account)
		if err != nil {
			h.fail(c, "get_material_failed", err)
			return
		}
		if saved {
			stopped := toPositionView(position)
			view.Position = &stopped
		}
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"outline": view})
}

// Content reads the text one coordinate names.
//
// The coordinate is on the query string rather than in a body because this is a
// read: `char_start` and `char_end` are what the text is read by, and `ordinal`
// is the unit the client believed it was in — a hint, and one the answer does
// not depend on (§2.8).
func (h *ReadingHandler) Content(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	materialID := c.Param("materialID")
	locator, ok := h.locatorFromQuery(c, materialID)
	if !ok {
		return
	}
	account, authenticated := caller(c)
	if !authenticated {
		return
	}
	passage, err := h.readings.Resolve(c.Request.Context(), materialID, locator, account)
	if err != nil {
		h.fail(c, "read_passage_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"passage": passageView{
		MaterialID: passage.MaterialID, Locator: passage.Locator, Text: passage.Text,
	}})
}

// Marks lists what a reader has written on one material, oldest first.
func (h *ReadingHandler) Marks(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	found, err := h.readings.Marks(c.Request.Context(), c.Param("materialID"), account)
	if err != nil {
		h.fail(c, "list_marks_failed", err)
		return
	}
	listed := make([]markView, 0, len(found))
	for _, mark := range found {
		listed = append(listed, toMarkView(mark))
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"marks": listed})
}

// AddMark writes one bookmark, highlight or note.
func (h *ReadingHandler) AddMark(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	materialID := c.Param("materialID")
	var request addMarkRequest
	if !readJSON(c, maxReadingRequestBytes, &request) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	mark, err := h.readings.AddMark(c.Request.Context(), reading.MarkRequest{
		MaterialID: materialID,
		SessionID:  request.SessionID,
		Kind:       reading.MarkKind(strings.TrimSpace(request.Kind)),
		Locator:    h.locatorIn(materialID, request.Locator),
		Body:       request.Body,
	}, account)
	if err != nil {
		h.fail(c, "add_mark_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusCreated, gin.H{"mark": toMarkView(mark)})
}

// SavePosition writes where a reader stopped in one material (§2.11).
//
// A write against a revision somebody has since moved is a conflict and not a
// failure: the reader's answer is to read the material again and decide, which
// is why this answers 409 with the same sentence a stale path does.
func (h *ReadingHandler) SavePosition(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	materialID := c.Param("materialID")
	var request savePositionRequest
	if !readJSON(c, maxReadingRequestBytes, &request) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	position, err := h.readings.SavePosition(c.Request.Context(), reading.SavePositionRequest{
		MaterialID: materialID,
		SessionID:  request.SessionID,
		Locator:    h.locatorIn(materialID, request.Locator),
		Revision:   request.Revision,
	}, account)
	if err != nil {
		h.fail(c, "save_position_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"position": toPositionView(position)})
}

// Extensions lists the extensions and whether each can run, with the reason
// when it cannot (§2.9).
//
// The catalogue is a property of the build rather than of the material, and the
// material is still checked: a list of panels for a book that is not there is an
// answer to a question nobody asked.
func (h *ReadingHandler) Extensions(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	if _, err := h.readings.Material(c.Request.Context(), c.Param("materialID"), account); err != nil {
		h.fail(c, "list_extensions_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"extensions": h.extensions.Catalogue()})
}

// RunExtension runs one extension over one passage.
//
// Every outcome except "this extension does not exist" is a 200: an extension
// that is unavailable, slow or broken is skipped, with a reason, and a client
// renders the reason beside whatever the other extensions produced (§2.9).
func (h *ReadingHandler) RunExtension(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	materialID := c.Param("materialID")
	var request runExtensionRequest
	if !readJSON(c, maxReadingRequestBytes, &request) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	locator := h.locatorIn(materialID, request.Locator)
	passage, err := h.readings.Resolve(c.Request.Context(), materialID, locator, account)
	if err != nil {
		h.fail(c, "run_extension_failed", err)
		return
	}
	// An extension that asks a model something is a model call made for the
	// account that asked for the extension, from a route that is not a
	// conversation: no session, no turn, and the row says so (migration 00017).
	// It is declared here rather than inside the extension because the account is
	// the request's, and by the time the call is made it is out of reach.
	ctx := usage.WithCall(c.Request.Context(), usage.Call{
		OwnerID: account.UserID,
		Source:  usage.SourceReadingExtension,
	})
	outcome, err := h.extensions.Run(ctx, c.Param("name"), reading.Context{
		MaterialID: passage.MaterialID,
		Locator:    passage.Locator,
		Text:       passage.Text,
	})
	if err != nil {
		h.fail(c, "run_extension_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"extension": outcome})
}

// locatorFromQuery builds a coordinate out of the query string, answering the
// request itself when the two offsets are not there to be read.
func (h *ReadingHandler) locatorFromQuery(c *gin.Context, materialID string) (reading.Locator, bool) {
	start, startErr := strconv.ParseInt(strings.TrimSpace(c.Query("char_start")), 10, 64)
	end, endErr := strconv.ParseInt(strings.TrimSpace(c.Query("char_end")), 10, 64)
	if startErr != nil || endErr != nil {
		httpx.WriteError(c, http.StatusBadRequest, "invalid_request",
			"char_start and char_end are required, and both are character offsets")
		return reading.Locator{}, false
	}
	locator := reading.Locator{MaterialID: materialID, CharStart: start, CharEnd: end}
	if ordinal := strings.TrimSpace(c.Query("ordinal")); ordinal != "" {
		parsed, err := strconv.Atoi(ordinal)
		if err != nil {
			httpx.WriteError(c, http.StatusBadRequest, "invalid_request", "ordinal is not a number")
			return reading.Locator{}, false
		}
		locator.Ordinal = parsed
	}
	return locator, true
}

// locatorIn completes a coordinate that arrived in a body.
//
// The route already says which material, so a body that leaves the field out is
// not missing anything. A body that fills it in is held to what it said: the
// offsets were measured in the material the client named, and writing them onto
// another one would be corruption rather than a typo to absorb — which is what
// the domain's own check refuses.
func (h *ReadingHandler) locatorIn(materialID string, locator reading.Locator) reading.Locator {
	if strings.TrimSpace(locator.MaterialID) == "" {
		locator.MaterialID = materialID
	}
	return locator
}

func toMaterialView(record reading.Material) materialView {
	return materialView{
		MaterialID: record.MaterialID, ContentID: record.ContentID,
		Filename: record.Filename, SourceType: record.SourceType,
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
		Pinned: record.Pinned, Opened: record.Opened,
	}
}

func toPositionView(record reading.Position) positionView {
	return positionView{
		MaterialID: record.MaterialID, SessionID: record.SessionID,
		Locator: record.Locator, Revision: record.Revision,
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}

func toMarkView(record reading.Mark) markView {
	return markView{
		MarkID: record.MarkID, MaterialID: record.MaterialID, SessionID: record.SessionID,
		Kind: record.Kind, Locator: record.Locator, Body: record.Body,
		Revision: record.Revision, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
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
