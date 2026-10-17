package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"backend/internal/authz"
	"backend/internal/persona"
	"github.com/gin-gonic/gin"
)

// personaStub is the persona use case, recording what it was asked for. The
// account it records is the point of half these tests: it has to have come
// from the token, not from anything in the request.
type personaStub struct {
	listing []persona.Persona
	record  persona.Persona
	err     error

	calls   []string
	account authz.Caller
	askedID string
	input   persona.Input
}

func (s *personaStub) List(_ context.Context, caller authz.Caller) ([]persona.Persona, error) {
	s.calls, s.account = append(s.calls, "list"), caller
	return s.listing, s.err
}

func (s *personaStub) Get(_ context.Context, personaID string, caller authz.Caller) (persona.Persona, error) {
	s.calls, s.account, s.askedID = append(s.calls, "get"), caller, personaID
	return s.record, s.err
}

func (s *personaStub) Create(_ context.Context, input persona.Input, caller authz.Caller) (persona.Persona, error) {
	s.calls, s.account, s.input = append(s.calls, "create"), caller, input
	return s.record, s.err
}

func (s *personaStub) Update(_ context.Context, personaID string, input persona.Input, caller authz.Caller) (persona.Persona, error) {
	s.calls, s.account, s.askedID, s.input = append(s.calls, "update"), caller, personaID, input
	return s.record, s.err
}

func (s *personaStub) Delete(_ context.Context, personaID string, caller authz.Caller) error {
	s.calls, s.account, s.askedID = append(s.calls, "delete"), caller, personaID
	return s.err
}

var samplePersonaTime = time.Unix(1_800_000_000, 0).UTC()

// aStoredPersona is one persona as the domain would hand it back, owner
// included — the handler is what has to keep that out of the response.
func aStoredPersona() persona.Persona {
	return persona.Persona{
		ID: "per_1", OwnerID: handlerAccountID, Name: "耐心的导师",
		Description: "解释概念时先讲直觉", Content: "先给直觉，再给定义。",
		CreatedAt: samplePersonaTime, UpdatedAt: samplePersonaTime,
	}
}

func newPersonaRouter(personas Personas) *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := NewPersonaHandler(personas, testHandlerLogger())
	router := gin.New()
	router.GET("/api/v1/personas", guard(), handler.List)
	router.POST("/api/v1/personas", guard(), handler.Create)
	router.GET("/api/v1/personas/:personaID", guard(), handler.Get)
	router.PUT("/api/v1/personas/:personaID", guard(), handler.Update)
	router.DELETE("/api/v1/personas/:personaID", guard(), handler.Delete)
	return router
}

type aPersonaFailure struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func TestPersonaHandlerListsOnlyWhatTheCallerHas(t *testing.T) {
	stub := &personaStub{listing: []persona.Persona{aStoredPersona()}}
	recorder := callAs(t, newPersonaRouter(stub), http.MethodGet, "/api/v1/personas", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data personaListView `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if len(envelope.Data.Personas) != 1 {
		t.Fatalf("personas = %+v, want the one persona", envelope.Data.Personas)
	}
	listed := envelope.Data.Personas[0]
	if listed.PersonaID != "per_1" || listed.Name != "耐心的导师" || listed.Content != "先给直觉，再给定义。" {
		t.Errorf("persona = %+v, want it as it was stored", listed)
	}
	if stub.account != handlerCaller() {
		t.Errorf("the use case was asked as %+v, want the account on the token", stub.account)
	}
	// Whose persona it is stays on this side: the page is one person's list, so
	// the owner is context rather than content.
	if strings.Contains(recorder.Body.String(), "owner_id") {
		t.Errorf("the response carries the owner: %s", recorder.Body.String())
	}
}

func TestPersonaHandlerWithNoPersonasAnswersAnEmptyList(t *testing.T) {
	recorder := callAs(t, newPersonaRouter(&personaStub{}), http.MethodGet, "/api/v1/personas", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	// An empty list rather than null, for the same reason every other list here
	// does: the client should not have to tell the two apart.
	if body := recorder.Body.String(); !strings.Contains(body, `"personas":[]`) {
		t.Errorf("body = %s, want an empty list", body)
	}
}

func TestPersonaHandlerCreateAnswers201WithTheStoredPersona(t *testing.T) {
	stub := &personaStub{record: aStoredPersona()}
	recorder := callAs(t, newPersonaRouter(stub), http.MethodPost, "/api/v1/personas",
		`{"name":"耐心的导师","description":"解释概念时先讲直觉","content":"先给直觉，再给定义。"}`)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.askedID != "" || stub.input.Name != "耐心的导师" || stub.input.Content != "先给直觉，再给定义。" {
		t.Errorf("Create() was given %+v, want the request body", stub.input)
	}
	if stub.account != handlerCaller() {
		t.Errorf("the use case was asked as %+v, want the account on the token", stub.account)
	}
	var envelope struct {
		Data personaView `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if envelope.Data.PersonaID != "per_1" {
		t.Errorf("data = %+v, want the persona that was stored", envelope.Data)
	}
}

func TestPersonaHandlerGetAndUpdateUseThePathID(t *testing.T) {
	stub := &personaStub{record: aStoredPersona()}
	router := newPersonaRouter(stub)

	recorder := callAs(t, router, http.MethodGet, "/api/v1/personas/per_1", "")
	if recorder.Code != http.StatusOK || stub.askedID != "per_1" {
		t.Errorf("Get() status = %d, asked for %q; want 200 and the path id", recorder.Code, stub.askedID)
	}

	recorder = callAs(t, router, http.MethodPut, "/api/v1/personas/per_1",
		`{"name":"严格的审稿人","description":"","content":"指出每一处含糊。"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("Update() status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.askedID != "per_1" {
		t.Errorf("Update() was asked for %q, want the path id", stub.askedID)
	}
	if stub.input.Name != "严格的审稿人" || stub.input.Description != "" || stub.input.Content != "指出每一处含糊。" {
		t.Errorf("Update() was given %+v, want the whole replacement", stub.input)
	}
}

func TestPersonaHandlerDeleteAnswersNoContent(t *testing.T) {
	stub := &personaStub{}
	recorder := callAs(t, newPersonaRouter(stub), http.MethodDelete, "/api/v1/personas/per_1", "")

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", recorder.Code, recorder.Body.String())
	}
	if body := recorder.Body.String(); body != "" {
		t.Errorf("body = %q, want none", body)
	}
	if stub.askedID != "per_1" {
		t.Errorf("Delete() was asked for %q, want the path id", stub.askedID)
	}
}

func TestPersonaHandlerMapsTheProblemsAClientCanFix(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		want   int
		code   string
		detail string
	}{
		{name: "a missing name", err: persona.ErrNameRequired, want: 400, code: "invalid_name", detail: "name"},
		{name: "a long name", err: persona.ErrNameTooLong, want: 400, code: "invalid_name", detail: "name"},
		{name: "a long description", err: persona.ErrDescriptionTooLong, want: 400, code: "invalid_description", detail: "description"},
		{name: "a missing instruction", err: persona.ErrContentRequired, want: 400, code: "invalid_content", detail: "instruction"},
		{name: "a long instruction", err: persona.ErrContentTooLong, want: 400, code: "invalid_content", detail: "instruction"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &personaStub{err: test.err}
			recorder := callAs(t, newPersonaRouter(stub), http.MethodPost, "/api/v1/personas",
				`{"name":"x","description":"","content":"y"}`)

			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.want, recorder.Body.String())
			}
			var envelope aPersonaFailure
			decodeBody(t, recorder, &envelope)
			if envelope.Error.Code != test.code {
				t.Errorf("code = %q, want %q", envelope.Error.Code, test.code)
			}
			// The message says which bound was broken; the code says which field.
			if !strings.Contains(strings.ToLower(envelope.Error.Message), test.detail) {
				t.Errorf("message = %q, want it to mention the %s", envelope.Error.Message, test.detail)
			}
		})
	}
}

func TestPersonaHandlerRefusesASecondNameForOneAccount(t *testing.T) {
	stub := &personaStub{err: persona.ErrNameTaken}
	recorder := callAs(t, newPersonaRouter(stub), http.MethodPut, "/api/v1/personas/per_1",
		`{"name":"耐心的导师","description":"","content":"先给直觉。"}`)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope aPersonaFailure
	decodeBody(t, recorder, &envelope)
	if envelope.Error.Code != "name_taken" {
		t.Errorf("code = %q, want name_taken", envelope.Error.Code)
	}
}

func TestPersonaHandlerAnswersNotFoundForSomebodyElsesPersona(t *testing.T) {
	stub := &personaStub{err: persona.ErrNotFound}
	recorder := callAs(t, newPersonaRouter(stub), http.MethodGet, "/api/v1/personas/per_elsewhere", "")

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", recorder.Code, recorder.Body.String())
	}
	// Another account's persona and a missing one are one answer: whether an id
	// exists is not a fact this account gets to learn.
	if body := recorder.Body.String(); strings.Contains(body, "per_elsewhere") {
		t.Errorf("the response repeats the id: %s", body)
	}
}

func TestPersonaHandlerReportsAStorageFailureAsAFailure(t *testing.T) {
	stub := &personaStub{err: errors.New("the database went away")}
	recorder := callAs(t, newPersonaRouter(stub), http.MethodPost, "/api/v1/personas",
		`{"name":"x","description":"","content":"y"}`)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope aPersonaFailure
	decodeBody(t, recorder, &envelope)
	if envelope.Error.Code != "create_persona_failed" {
		t.Errorf("code = %q, want create_persona_failed", envelope.Error.Code)
	}
	// The driver's words are for the log, not the client.
	if strings.Contains(recorder.Body.String(), "database went away") {
		t.Errorf("the response carries the storage failure: %s", recorder.Body.String())
	}
}

func TestPersonaHandlerWithoutAServiceFailsRatherThanPanics(t *testing.T) {
	var handler *PersonaHandler
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/personas", guard(), handler.List)

	recorder := callAs(t, router, http.MethodGet, "/api/v1/personas", "")

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope aPersonaFailure
	decodeBody(t, recorder, &envelope)
	if envelope.Error.Code != "personas_unavailable" {
		t.Errorf("code = %q, want personas_unavailable", envelope.Error.Code)
	}
}
