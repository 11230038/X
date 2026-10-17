package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"backend/internal/authz"
	"backend/internal/httpx"
	"backend/internal/persona"
	"github.com/gin-gonic/gin"
)

// Personas is the persona use case consumed by the HTTP handler.
type Personas interface {
	Get(context.Context, string, authz.Caller) (persona.Persona, error)
	List(context.Context, authz.Caller) ([]persona.Persona, error)
	Create(context.Context, persona.Input, authz.Caller) (persona.Persona, error)
	Update(context.Context, string, persona.Input, authz.Caller) (persona.Persona, error)
	Delete(context.Context, string, authz.Caller) error
}

// PersonaHandler handles a person's saved prompts: the list the composer shows,
// one persona, and the writes that maintain them.
type PersonaHandler struct {
	personas Personas
	logger   *slog.Logger
}

// NewPersonaHandler constructs the persona HTTP handler.
func NewPersonaHandler(personas Personas, logger *slog.Logger) *PersonaHandler {
	return &PersonaHandler{personas: personas, logger: logger}
}

// personaView is one persona as a client reads it.
//
// The instruction is part of the view. It is the account's own text, the page
// exists to edit it, and hiding it behind a second request would make the edit
// form fetch the same row twice.
type personaView struct {
	PersonaID   string    `json:"persona_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Content     string    `json:"content"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type personaListView struct {
	Personas []personaView `json:"personas"`
}

type personaRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
}

// List returns every persona the caller has.
func (h *PersonaHandler) List(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	found, err := h.personas.List(c.Request.Context(), account)
	if err != nil {
		h.fail(c, "list_personas_failed", err)
		return
	}
	listed := make([]personaView, 0, len(found))
	for _, record := range found {
		listed = append(listed, toPersonaView(record))
	}
	httpx.WriteSuccess(c, http.StatusOK, personaListView{Personas: listed})
}

// Get returns one persona.
func (h *PersonaHandler) Get(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	record, err := h.personas.Get(c.Request.Context(), c.Param("personaID"), account)
	if err != nil {
		h.fail(c, "get_persona_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, toPersonaView(record))
}

// Create stores a persona and answers with it.
func (h *PersonaHandler) Create(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	var request personaRequest
	if !readJSON(c, maxPersonaRequestBytes, &request) {
		return
	}
	created, err := h.personas.Create(c.Request.Context(), request.input(), account)
	if err != nil {
		h.fail(c, "create_persona_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusCreated, toPersonaView(created))
}

// Update replaces a persona's name, description and instruction.
func (h *PersonaHandler) Update(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	var request personaRequest
	if !readJSON(c, maxPersonaRequestBytes, &request) {
		return
	}
	updated, err := h.personas.Update(c.Request.Context(), c.Param("personaID"), request.input(), account)
	if err != nil {
		h.fail(c, "update_persona_failed", err)
		return
	}
	httpx.WriteSuccess(c, http.StatusOK, toPersonaView(updated))
}

// Delete removes a persona.
func (h *PersonaHandler) Delete(c *gin.Context) {
	if !h.ready(c) {
		return
	}
	account, ok := caller(c)
	if !ok {
		return
	}
	if err := h.personas.Delete(c.Request.Context(), c.Param("personaID"), account); err != nil {
		h.fail(c, "delete_persona_failed", err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *PersonaHandler) ready(c *gin.Context) bool {
	if h != nil && h.personas != nil {
		return true
	}
	httpx.WriteError(c, http.StatusInternalServerError, "personas_unavailable", "personas are not configured")
	return false
}

// fail reports a persona failure, distinguishing what the client can act on from
// what only an operator can.
func (h *PersonaHandler) fail(c *gin.Context, code string, err error) {
	switch {
	case errors.Is(err, persona.ErrNotFound):
		httpx.WriteError(c, http.StatusNotFound, "not_found", "no such persona")
	case errors.Is(err, persona.ErrNameRequired):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_name", detail(err, persona.ErrNameRequired))
	case errors.Is(err, persona.ErrNameTooLong):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_name", detail(err, persona.ErrNameTooLong))
	case errors.Is(err, persona.ErrDescriptionTooLong):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_description", detail(err, persona.ErrDescriptionTooLong))
	case errors.Is(err, persona.ErrContentRequired):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_content", detail(err, persona.ErrContentRequired))
	case errors.Is(err, persona.ErrContentTooLong):
		httpx.WriteError(c, http.StatusBadRequest, "invalid_content", detail(err, persona.ErrContentTooLong))
	case errors.Is(err, persona.ErrNameTaken):
		httpx.WriteError(c, http.StatusConflict, "name_taken", err.Error())
	default:
		if h.logger != nil {
			h.logger.Error("persona request failed", "error", err, "request_id", httpx.RequestID(c))
		}
		httpx.WriteError(c, http.StatusInternalServerError, code, "the persona request failed")
	}
}

func (r personaRequest) input() persona.Input {
	return persona.Input{Name: r.Name, Description: r.Description, Content: r.Content}
}

func toPersonaView(record persona.Persona) personaView {
	return personaView{
		PersonaID:   record.ID,
		Name:        record.Name,
		Description: record.Description,
		Content:     record.Content,
		CreatedAt:   record.CreatedAt,
		UpdatedAt:   record.UpdatedAt,
	}
}
