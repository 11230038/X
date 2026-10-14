package handler

// The extension half of the reading surface: what the catalogue says about each
// extension, and what an extension is handed when it runs.
//
// The rule these tests exist for is the confinement. The text is never on the
// wire — a body carries a coordinate, and the passage is resolved server-side —
// so what an extension can see is a material, a locator and the text that
// locator names, and there is nothing in its arguments that could carry the
// user's own words, another material, or the conversation.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"backend/internal/reading"
)

// aidStub is one reading extension, recording the context it was handed.
type aidStub struct {
	name    string
	ready   bool
	reason  string
	payload string
	seen    *reading.Context
}

func (a *aidStub) Name() string                       { return a.name }
func (a *aidStub) ResultSchema() reading.ResultSchema { return reading.ResultText }
func (a *aidStub) Available() (bool, string)          { return a.ready, a.reason }

func (a *aidStub) Run(_ context.Context, reading reading.Context) (json.RawMessage, error) {
	copied := reading
	a.seen = &copied
	return json.RawMessage(a.payload), nil
}
func TestReadingHandlerExtensionsAnswersForAMaterialThatIsThere(t *testing.T) {
	registry, err := reading.NewRegistry(time.Second,
		&aidStub{name: "explain", ready: true, payload: `"讲清楚了"`},
		&aidStub{name: "audio", reason: "这台部署没有语音合成"},
	)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	stub := &readingStub{material: reading.Material{MaterialID: "rm_1"}}
	router := newReadingRouter(stub, registry)

	recorder := callAs(t, router, http.MethodGet, "/api/v1/reading/materials/rm_1/extensions", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data struct {
			Extensions []reading.Availability `json:"extensions"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	// The catalogue comes back in the registry's own order, which is by name, so
	// that a client pairing it with anything else can rely on the position.
	if len(envelope.Data.Extensions) != 2 ||
		envelope.Data.Extensions[0].Name != "audio" ||
		envelope.Data.Extensions[1].Name != "explain" {
		t.Fatalf("the catalogue is %+v", envelope.Data.Extensions)
	}
	// An extension that cannot run says why, because a false with no reason is a
	// panel the reader can do nothing about.
	if envelope.Data.Extensions[0].Available || envelope.Data.Extensions[0].Reason == "" {
		t.Errorf("an unavailable extension came back as %+v", envelope.Data.Extensions[0])
	}
	if !envelope.Data.Extensions[1].Available || envelope.Data.Extensions[1].Reason != "" {
		t.Errorf("an available extension came back as %+v", envelope.Data.Extensions[1])
	}

	// A list of panels for a book that is not there is an answer to a question
	// nobody asked, so the material is checked even though the catalogue is a
	// property of the build.
	stub.material = reading.Material{MaterialID: "rm_other"}
	recorder = callAs(t, router, http.MethodGet, "/api/v1/reading/materials/rm_1/extensions", "")
	if recorder.Code != http.StatusNotFound {
		t.Errorf("listing extensions for a material that is not there: status = %d, want 404",
			recorder.Code)
	}
}

func TestReadingHandlerRunExtensionHandsOverTheResolvedPassage(t *testing.T) {
	aid := &aidStub{name: "explain", ready: true, payload: `"这一段说的是捕获"`}
	registry, err := reading.NewRegistry(time.Second, aid)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	stub := &readingStub{passage: reading.Passage{Text: "闭包捕获的是变量而不是值"}}
	recorder := callAs(t, newReadingRouter(stub, registry), http.MethodPost,
		"/api/v1/reading/materials/rm_1/extensions/explain",
		`{"locator":{"char_start":10,"char_end":20}}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if len(stub.resolved) != 1 || stub.resolved[0].CharStart != 10 {
		t.Fatalf("the passage was resolved as %v", stub.resolved)
	}
	// The text was never on the wire: the body carries a coordinate, and what the
	// extension is handed is what that coordinate resolves to server-side. That
	// is the whole of the confinement — an extension sees the passage and nothing
	// that could carry the user's own words.
	if aid.seen == nil {
		t.Fatal("the extension was never run")
	}
	if aid.seen.Text != "闭包捕获的是变量而不是值" {
		t.Errorf("the extension was handed the text %q", aid.seen.Text)
	}
	if aid.seen.MaterialID != "rm_1" || aid.seen.Locator.CharStart != 10 || aid.seen.Locator.CharEnd != 20 {
		t.Errorf("the extension was handed %+v", *aid.seen)
	}
	var envelope struct {
		Data struct {
			Extension reading.Outcome `json:"extension"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if envelope.Data.Extension.Skipped || len(envelope.Data.Extension.Result) == 0 {
		t.Errorf("the extension came back as %+v", envelope.Data.Extension)
	}
}

func TestReadingHandlerRunExtensionAnswersASKipWithTheReason(t *testing.T) {
	registry, err := reading.NewRegistry(time.Second,
		&aidStub{name: "audio", reason: "这台部署没有语音合成"})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	stub := &readingStub{}
	recorder := callAs(t, newReadingRouter(stub, registry), http.MethodPost,
		"/api/v1/reading/materials/rm_1/extensions/audio", `{"locator":{"char_start":0,"char_end":10}}`)

	// An extension that cannot run is not a failed request: the panel says why,
	// and whatever the other extensions produced is drawn beside it.
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data struct {
			Extension reading.Outcome `json:"extension"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if !envelope.Data.Extension.Skipped || envelope.Data.Extension.Reason == "" {
		t.Errorf("an unavailable extension came back as %+v", envelope.Data.Extension)
	}
	if len(envelope.Data.Extension.Result) != 0 {
		t.Errorf("a skipped extension carries a result as well: %s", envelope.Data.Extension.Result)
	}

	// An extension that does not exist is a different answer: there is no panel
	// to draw a reason in.
	recorder = callAs(t, newReadingRouter(stub, registry), http.MethodPost,
		"/api/v1/reading/materials/rm_1/extensions/translate", `{"locator":{"char_start":0,"char_end":10}}`)
	if recorder.Code != http.StatusNotFound {
		t.Errorf("running an extension that does not exist: status = %d, want 404", recorder.Code)
	}
}
