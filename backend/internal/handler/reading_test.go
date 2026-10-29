package handler

// The reading surface against a stub. Two things are pinned here.
//
// The first is what the route decides and what the body decides: the material
// comes from the URL rather than from a body that could disagree with it, a
// passage is resolved server-side before any extension sees it, and an extension
// that was skipped is still a 200 — a client renders the reason beside whatever
// the other extensions produced.
//
// The second, and the reason one function below is a table, is the mapping from a
// domain refusal to a status. The interesting failures of an HTTP surface are the
// ones a client has to act on differently, and two of these are conflicts: a
// coordinate the material does not have is the client's to fix and is a 400,
// while a coordinate that has moved underneath it is the reader's to decide about
// again and is a 409.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"backend/internal/authz"
	"backend/internal/middleware"
	"backend/internal/reading"
	"github.com/gin-gonic/gin"
)

var handlerReadingTime = time.Unix(1_800_000_000, 0).UTC()

// readingStub is the reading use case, recording what it was asked for.
type readingStub struct {
	materials []reading.Material
	material  reading.Material
	outline   reading.Outline
	created   reading.Material
	position  reading.Position
	saved     bool
	marks     []reading.Mark
	bookmarks []reading.Bookmark
	mark      reading.Mark
	passage   reading.Passage
	pairings  reading.Pairings
	pairing   reading.Pairing
	cancelled bool
	err       error

	workspaces []string
	creates    [][2]string
	saves      []reading.SavePositionRequest
	adds       []reading.MarkRequest
	resolved   []reading.Locator
	positions  [][2]string
	paired     []string
	confirms   []reading.PairingRequest
	cancels    []string
}

func (s *readingStub) Materials(_ context.Context, workspaceID string, _ authz.Caller) ([]reading.Material, error) {
	s.workspaces = append(s.workspaces, workspaceID)
	return s.materials, s.err
}

func (s *readingStub) Material(_ context.Context, materialID string, _ authz.Caller) (reading.Material, error) {
	if s.err != nil {
		return reading.Material{}, s.err
	}
	if s.material.MaterialID != "" && s.material.MaterialID != materialID {
		return reading.Material{}, reading.ErrNotFound
	}
	return s.material, nil
}

func (s *readingStub) Outline(_ context.Context, _ string, _ authz.Caller) (reading.Outline, error) {
	return s.outline, s.err
}

func (s *readingStub) Create(_ context.Context, fileID, sourceType string, _ authz.Caller) (reading.Material, error) {
	s.creates = append(s.creates, [2]string{fileID, sourceType})
	return s.created, s.err
}

func (s *readingStub) Position(_ context.Context, materialID, sessionID string, _ authz.Caller) (reading.Position, bool, error) {
	s.positions = append(s.positions, [2]string{materialID, sessionID})
	return s.position, s.saved, s.err
}

func (s *readingStub) SavePosition(_ context.Context, request reading.SavePositionRequest, _ authz.Caller) (reading.Position, error) {
	s.saves = append(s.saves, request)
	return s.position, s.err
}

func (s *readingStub) Marks(_ context.Context, _ string, _ authz.Caller) ([]reading.Mark, error) {
	return s.marks, s.err
}

func (s *readingStub) Bookmarks(_ context.Context, _ authz.Caller) ([]reading.Bookmark, error) {
	return s.bookmarks, s.err
}

func (s *readingStub) AddMark(_ context.Context, request reading.MarkRequest, _ authz.Caller) (reading.Mark, error) {
	s.adds = append(s.adds, request)
	return s.mark, s.err
}

func (s *readingStub) Resolve(_ context.Context, materialID string, locator reading.Locator, _ authz.Caller) (reading.Passage, error) {
	if s.err != nil {
		return reading.Passage{}, s.err
	}
	s.resolved = append(s.resolved, locator)
	passage := s.passage
	passage.MaterialID, passage.Locator = materialID, locator
	return passage, nil
}

func (s *readingStub) Pairings(_ context.Context, materialID string, _ authz.Caller) (reading.Pairings, error) {
	s.paired = append(s.paired, materialID)
	return s.pairings, s.err
}

func (s *readingStub) ConfirmPairing(_ context.Context, request reading.PairingRequest, _ authz.Caller) (reading.Pairing, error) {
	s.confirms = append(s.confirms, request)
	return s.pairing, s.err
}

func (s *readingStub) CancelPairing(_ context.Context, pairingID string, _ authz.Caller) (bool, error) {
	s.cancels = append(s.cancels, pairingID)
	return s.cancelled, s.err
}

func newReadingRouter(readings Readings, extensions *reading.Registry) *gin.Engine {
	gin.SetMode(gin.TestMode)
	logger := testHandlerLogger()
	handler := NewReadingHandler(readings, extensions, logger)
	// Every reading route is behind the real bearer middleware: each of them reads
	// or writes something an account owns, so there is no version of one of these
	// requests that arrives without an account, and a test that reached a handler
	// without one would be exercising a path production cannot take.
	guarded := middleware.RequireAuth(handlerVerifierStub{}, logger)
	router := gin.New()
	router.GET("/api/v1/reading/materials", guarded, handler.List)
	router.POST("/api/v1/reading/materials", guarded, handler.Create)
	router.GET("/api/v1/reading/materials/:materialID", guarded, handler.Get)
	router.GET("/api/v1/reading/materials/:materialID/content", guarded, handler.Content)
	router.GET("/api/v1/reading/materials/:materialID/marks", guarded, handler.Marks)
	router.POST("/api/v1/reading/materials/:materialID/marks", guarded, handler.AddMark)
	router.GET("/api/v1/reading/bookmarks", guarded, handler.Bookmarks)
	router.POST("/api/v1/reading/materials/:materialID/position", guarded, handler.SavePosition)
	router.GET("/api/v1/reading/materials/:materialID/pairings", guarded, handler.Pairings)
	router.POST("/api/v1/reading/materials/:materialID/pairings", guarded, handler.ConfirmPairing)
	router.DELETE("/api/v1/reading/pairings/:pairingID", guarded, handler.CancelPairing)
	router.GET("/api/v1/reading/materials/:materialID/extensions", guarded, handler.Extensions)
	router.POST("/api/v1/reading/materials/:materialID/extensions/:name", guarded, handler.RunExtension)
	return router
}

func TestReadingHandlerListAsksForTheWorkspaceItWasGiven(t *testing.T) {
	pinned, opened := true, false
	stub := &readingStub{materials: []reading.Material{
		{
			MaterialID: "rm_1", ContentID: "doc_1", Filename: "算法笔记.md",
			SourceType: "library", CreatedAt: handlerReadingTime,
			Pinned: &pinned, Opened: &opened,
		},
		{MaterialID: "rm_2", ContentID: "doc_2", Filename: "另一本.md", CreatedAt: handlerReadingTime},
	}}
	recorder := callAs(t, newReadingRouter(stub, nil), http.MethodGet,
		"/api/v1/reading/materials?workspace_id=rw_1", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if len(stub.workspaces) != 1 || stub.workspaces[0] != "rw_1" {
		t.Errorf("the catalogue was read for the workspaces %v", stub.workspaces)
	}
	var envelope struct {
		Data struct {
			Materials []materialView `json:"materials"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if len(envelope.Data.Materials) != 2 {
		t.Fatalf("the catalogue came back as %+v", envelope.Data.Materials)
	}

	// A tab nobody has said anything about carries no flag at all, which is what
	// lets a client tell "not pinned" from "nobody has said" — a false would
	// collapse the two.
	body := recorder.Body.String()
	if envelope.Data.Materials[0].Pinned == nil || !*envelope.Data.Materials[0].Pinned {
		t.Errorf("a pinned tab came back as %+v", envelope.Data.Materials[0])
	}
	// A flag that is false is a thing somebody said, so it has to survive the
	// wire: an omitempty pointer drops a nil and keeps a false, which is the
	// whole of how the client tells the two apart.
	if opened := envelope.Data.Materials[0].Opened; opened == nil || *opened {
		t.Errorf("an unopened tab came back as %+v", envelope.Data.Materials[0])
	}
	if envelope.Data.Materials[1].Pinned != nil || envelope.Data.Materials[1].Opened != nil {
		t.Errorf("a tab nobody has said anything about came back as %+v", envelope.Data.Materials[1])
	}
	// One material has tab state and one does not, so each flag is on the wire
	// exactly once — and it is the same material's that is missing from neither.
	if countOf(body, `"pinned"`) != 1 || countOf(body, `"opened"`) != 1 {
		t.Errorf("the flags on the wire do not match the tab state: %s", body)
	}
}

func TestReadingHandlerGetCarriesThePositionOnlyWhenASessionIsNamed(t *testing.T) {
	stub := &readingStub{
		outline: reading.Outline{
			Material:  reading.Material{MaterialID: "rm_1", Filename: "算法笔记.md"},
			Units:     []reading.Unit{{Ordinal: 0, CharStart: 0, CharEnd: 40}},
			TextChars: 400,
		},
		position: reading.Position{
			MaterialID: "rm_1", SessionID: "s_1", Revision: 3,
			Locator: reading.Locator{MaterialID: "rm_1", CharStart: 40, CharEnd: 40},
		},
		saved: true,
	}
	router := newReadingRouter(stub, nil)

	// Without a session this is a catalogue read: "what is this", and nobody's
	// place in it.
	recorder := callAs(t, router, http.MethodGet, "/api/v1/reading/materials/rm_1", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var plain struct {
		Data struct {
			Outline outlineView `json:"outline"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &plain)
	if plain.Data.Outline.Position != nil {
		t.Errorf("a catalogue read came back with a position: %+v", plain.Data.Outline.Position)
	}
	if len(stub.positions) != 0 {
		t.Errorf("a catalogue read asked for a position anyway: %v", stub.positions)
	}
	if plain.Data.Outline.TextChars != 400 || len(plain.Data.Outline.Units) != 1 {
		t.Errorf("the outline is %+v", plain.Data.Outline)
	}

	// Naming a session is one question — "what is this and where was I" — and one
	// round trip is enough to draw the first screen.
	recorder = callAs(t, router, http.MethodGet, "/api/v1/reading/materials/rm_1?session_id=s_1", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var withPosition struct {
		Data struct {
			Outline outlineView `json:"outline"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &withPosition)
	if withPosition.Data.Outline.Position == nil {
		t.Fatal("a read that named a session came back without the position it saved")
	}
	// The revision rides along because it is what a client sends back to write a
	// position, and a client that had to guess it would be sending a conflict.
	if withPosition.Data.Outline.Position.Revision != 3 ||
		withPosition.Data.Outline.Position.Locator.CharStart != 40 {
		t.Errorf("the position came back as %+v", withPosition.Data.Outline.Position)
	}
	if len(stub.positions) != 1 || stub.positions[0] != [2]string{"rm_1", "s_1"} {
		t.Errorf("the position was asked for as %v", stub.positions)
	}
}

func TestReadingHandlerContentReadsTheCoordinateOffTheQueryString(t *testing.T) {
	stub := &readingStub{
		passage: reading.Passage{
			Text: "闭包捕获的是变量而不是值", DocumentID: "doc_1", Revision: 2,
		},
	}
	router := newReadingRouter(stub, nil)

	recorder := callAs(t, router, http.MethodGet,
		"/api/v1/reading/materials/rm_1/content?char_start=10&char_end=20&ordinal=3", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if len(stub.resolved) != 1 {
		t.Fatalf("the passage was resolved %d times, want once", len(stub.resolved))
	}
	// The material is the route's, and the ordinal is a hint the answer does not
	// depend on: the two offsets are what the text is read by.
	resolved := stub.resolved[0]
	if resolved.MaterialID != "rm_1" || resolved.CharStart != 10 || resolved.CharEnd != 20 ||
		resolved.Ordinal != 3 {
		t.Errorf("the passage was resolved at %+v", resolved)
	}
	var envelope struct {
		Data struct {
			Passage passageView `json:"passage"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if envelope.Data.Passage.Text != "闭包捕获的是变量而不是值" {
		t.Errorf("the passage reads %q", envelope.Data.Passage.Text)
	}
	// What comes back is the coordinate that was resolved rather than the one
	// that was asked for, so a client that sent a stale one is shown where the
	// text it got actually lives.
	if envelope.Data.Passage.Locator.CharStart != 10 {
		t.Errorf("the passage came back at %+v", envelope.Data.Passage.Locator)
	}
	if envelope.Data.Passage.MaterialID != "rm_1" {
		t.Errorf("the passage names the material %q", envelope.Data.Passage.MaterialID)
	}
}

func TestReadingHandlerContentNeedsTheCoordinatesItReadsBy(t *testing.T) {
	for _, test := range []struct{ name, query, want string }{
		{name: "no coordinates", query: "", want: "char_start and char_end are required"},
		{name: "only the start", query: "?char_start=10", want: "char_start and char_end are required"},
		{name: "a start that is not a number", query: "?char_start=ten&char_end=20",
			want: "char_start and char_end are required"},
		{name: "an ordinal that is not a number", query: "?char_start=10&char_end=20&ordinal=third",
			want: "ordinal is not a number"},
	} {
		t.Run(test.name, func(t *testing.T) {
			stub := &readingStub{}
			recorder := callAs(t, newReadingRouter(stub, nil), http.MethodGet,
				"/api/v1/reading/materials/rm_1/content"+test.query, "")

			// A read that would have to guess which characters to hand back is
			// refused whole: half a coordinate is not a coordinate.
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
			if !contains(recorder.Body.String(), test.want) {
				t.Errorf("the refusal reads %s, want it to mention %q", recorder.Body.String(), test.want)
			}
			if len(stub.resolved) != 0 {
				t.Errorf("a passage was resolved from %q", test.query)
			}
		})
	}
}

func TestReadingHandlerAddMarkTakesTheMaterialFromTheRoute(t *testing.T) {
	stub := &readingStub{mark: reading.Mark{
		MarkID: "rmk_1", MaterialID: "rm_1", Kind: reading.MarkNote,
		Revision: 1, CreatedAt: handlerReadingTime,
	}}
	router := newReadingRouter(stub, nil)

	// The body leaves the material out, which is not missing anything: the route
	// already said which one.
	recorder := callAs(t, router, http.MethodPost, "/api/v1/reading/materials/rm_1/marks",
		`{"session_id":"s_1","kind":"note","body":"这里说的是捕获",
		  "locator":{"char_start":10,"char_end":20,"ordinal":2}}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	if len(stub.adds) != 1 {
		t.Fatalf("the mark was written %d times", len(stub.adds))
	}
	added := stub.adds[0]
	if added.MaterialID != "rm_1" || added.Locator.MaterialID != "rm_1" {
		t.Errorf("the mark was written as %+v", added)
	}
	if added.Kind != reading.MarkNote || added.SessionID != "s_1" || added.Body != "这里说的是捕获" {
		t.Errorf("the mark was written as %+v", added)
	}
	if added.Locator.CharStart != 10 || added.Locator.CharEnd != 20 || added.Locator.Ordinal != 2 {
		t.Errorf("the mark carries the locator %+v", added.Locator)
	}

	// A body that fills the material in is held to what it said rather than
	// quietly overwritten: the offsets were measured in the material the client
	// named, and the domain is the one that refuses a mismatch.
	recorder = callAs(t, router, http.MethodPost, "/api/v1/reading/materials/rm_1/marks",
		`{"session_id":"s_1","kind":"bookmark","locator":{"material_id":"rm_2","char_start":1,"char_end":1}}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	if got := stub.adds[1].Locator.MaterialID; got != "rm_2" {
		t.Errorf("a body that named its own material was rewritten to %q", got)
	}
}

func TestReadingHandlerSavePositionCarriesTheRevisionThrough(t *testing.T) {
	stub := &readingStub{position: reading.Position{
		MaterialID: "rm_1", SessionID: "s_1", Revision: 5, UpdatedAt: handlerReadingTime,
	}}
	recorder := callAs(t, newReadingRouter(stub, nil), http.MethodPost,
		"/api/v1/reading/materials/rm_1/position",
		`{"session_id":"s_1","revision":4,"locator":{"char_start":40,"char_end":40}}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if len(stub.saves) != 1 {
		t.Fatalf("the position was saved %d times", len(stub.saves))
	}
	saved := stub.saves[0]
	// The revision is the caller's promise about what the row held, and a handler
	// that dropped it would turn every save into a last-writer-wins write.
	if saved.Revision != 4 || saved.MaterialID != "rm_1" || saved.SessionID != "s_1" {
		t.Errorf("the position was saved as %+v", saved)
	}
	if saved.Locator.MaterialID != "rm_1" || saved.Locator.CharStart != 40 {
		t.Errorf("the position carries the locator %+v", saved.Locator)
	}
	var envelope struct {
		Data struct {
			Position positionView `json:"position"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &envelope)
	if envelope.Data.Position.Revision != 5 {
		t.Errorf("the saved position came back as %+v", envelope.Data.Position)
	}
}

func TestReadingHandlerMapsARefusalToTheStatusAClientActsOn(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "a material that is not there", err: reading.ErrNotFound,
			status: http.StatusNotFound, code: "not_found"},
		{name: "a coordinate the material does not have", err: reading.ErrInvalidRequest,
			status: http.StatusBadRequest, code: "invalid_request"},
		// A position somebody else moved is not a failure: the reader reads the
		// material again and decides, which is what a conflict is for.
		{name: "a position that has moved", err: reading.ErrStalePosition,
			status: http.StatusConflict, code: "stale_position"},
		{name: "a row that is already there", err: reading.ErrDuplicate,
			status: http.StatusConflict, code: "conflict"},
		{name: "storage being unwell", err: errors.New("the database is unreachable"),
			status: http.StatusInternalServerError, code: "list_materials_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			stub := &readingStub{err: test.err}
			recorder := callAs(t, newReadingRouter(stub, nil), http.MethodGet,
				"/api/v1/reading/materials", "")

			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.status, recorder.Body.String())
			}
			var envelope struct {
				Error *struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			decodeBody(t, recorder, &envelope)
			if envelope.Error == nil || envelope.Error.Code != test.code {
				t.Fatalf("the refusal is %+v, want the code %q", envelope.Error, test.code)
			}
			// Whatever a client is told has to be something it can act on, and an
			// error only an operator can read is not: a failure from below is a
			// sentence, and one from above names the thing that was wrong.
			if envelope.Error.Message == "" {
				t.Error("a refusal came back with no message at all")
			}
			if test.status == http.StatusInternalServerError &&
				contains(envelope.Error.Message, "unreachable") {
				t.Errorf("the message leaks what went wrong below: %q", envelope.Error.Message)
			}
		})
	}
}

func TestReadingHandlerWithoutTheReadingLayerSaysSo(t *testing.T) {
	handler := NewReadingHandler(nil, nil, testHandlerLogger())
	router := gin.New()
	router.GET("/api/v1/reading/materials", handler.List)

	recorder := callAs(t, router, http.MethodGet, "/api/v1/reading/materials", "")
	// A deployment that did not assemble the reading layer is a mistake on the
	// operator's side of the line, and it is answered as one rather than as a
	// crash in a goroutine.
	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", recorder.Code)
	}
	if !contains(recorder.Body.String(), "reading_unavailable") {
		t.Errorf("the refusal is %s", recorder.Body.String())
	}
}

// contains and countOf keep the two assertions about what reached the wire
// readable: one asks whether a word is on the wire at all, and the other how
// often it is.
func contains(body, want string) bool { return strings.Contains(body, want) }

func countOf(body, want string) int { return strings.Count(body, want) }
