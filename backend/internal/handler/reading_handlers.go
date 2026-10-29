package handler

// The reading layer's routes. What they answer with is reading_views.go, and the
// interface they answer through and the failure mapping they close with are
// reading.go.

import (
	"net/http"
	"strconv"
	"strings"

	"backend/internal/httpx"
	"backend/internal/reading"
	"backend/internal/usage"
	"github.com/gin-gonic/gin"
)

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

// Bookmarks lists every bookmark the caller has saved, newest first.
//
// It is the one mark read that is not about one material: a favourites page
// collects what was saved anywhere, so the material's name travels with each
// one and the words at the saved coordinate come along.
func (h *ReadingHandler) Bookmarks(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	found, err := h.readings.Bookmarks(c.Request.Context(), account)
	if err != nil {
		h.fail(c, "list_bookmarks_failed", err)
		return
	}
	listed := make([]bookmarkView, 0, len(found))
	for _, bookmark := range found {
		listed = append(listed, toBookmarkView(bookmark))
	}
	httpx.WriteSuccess(c, http.StatusOK, gin.H{"bookmarks": listed})
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
