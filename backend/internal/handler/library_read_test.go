package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"backend/internal/kb"
	"backend/internal/library"
	"backend/internal/middleware"
	"github.com/gin-gonic/gin"
)

// fileStub answers the read side of the library, and remembers what it was asked
// to find.
type fileStub struct {
	file     library.UploadResult
	err      error
	askedFor string
}

func (s *fileStub) Find(_ context.Context, id string) (library.UploadResult, error) {
	s.askedFor = id
	return s.file, s.err
}

// materialStub answers the parsed side, and records the offsets a window was
// asked for: the window is the one place a client's citation coordinates pass
// through, so what arrived there is worth asserting on.
type materialStub struct {
	document kb.Document
	window   kb.Window
	spans    []kb.ChunkSpan
	err      error
	asked    struct {
		fileID   string
		docID    string
		offset   int64
		limit    int64
		spansFor string
	}
}

func (s *materialStub) DocumentOfFile(_ context.Context, fileID string) (kb.Document, error) {
	s.asked.fileID = fileID
	return s.document, s.err
}

func (s *materialStub) ReadWindow(_ context.Context, documentID string, offset, limit int64) (kb.Window, error) {
	s.asked.docID, s.asked.offset, s.asked.limit = documentID, offset, limit
	return s.window, s.err
}

func (s *materialStub) ChunkSpans(_ context.Context, documentID string) ([]kb.ChunkSpan, error) {
	s.asked.spansFor = documentID
	return s.spans, s.err
}

func testLibraryFile() library.UploadResult {
	return library.UploadResult{
		ID: "lib_1", SHA256: strings.Repeat("a", 64), Filename: "手册.pdf",
		MIMEType: "application/pdf", SizeBytes: 4211, Category: "pdf",
		CreatedAt: time.Unix(1, 0).UTC(),
	}
}

func newLibraryReadRouter(files *fileStub, materials *materialStub) *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := NewLibraryReadHandler(files, materials, testHandlerLogger())
	router := gin.New()
	router.Use(middleware.RequestID())
	router.GET("/api/v1/library/files/:fileID", handler.Get)
	router.GET("/api/v1/library/files/:fileID/text", handler.Text)
	return router
}

func TestGetFileReturnsItWithItsParseState(t *testing.T) {
	files := &fileStub{file: testLibraryFile()}
	materials := &materialStub{document: testDocument()}
	router := newLibraryReadRouter(files, materials)

	recorder := call(t, router, http.MethodGet, "/api/v1/library/files/lib_1", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		File     library.UploadResult `json:"file"`
		Document *documentView        `json:"document"`
	}
	kbData(t, recorder, &body)
	if body.File.ID != "lib_1" || body.File.Filename != "手册.pdf" {
		t.Errorf("file = %+v", body.File)
	}
	if body.Document == nil || body.Document.DocumentID != "doc_1" {
		t.Fatalf("document = %+v", body.Document)
	}
	if files.askedFor != "lib_1" || materials.asked.fileID != "lib_1" {
		t.Errorf("asked for file %q / %q", files.askedFor, materials.asked.fileID)
	}
}

func TestGetFileWithNothingParsedOutOfItIsNotAFailure(t *testing.T) {
	// A file that was uploaded and used for nothing yet has no document. That is
	// the state the client has to be told about before it offers to open text
	// that does not exist, and it is not a 404: the file is here.
	files := &fileStub{file: testLibraryFile()}
	materials := &materialStub{err: fmt.Errorf("%w: no document", kb.ErrNotFound)}
	router := newLibraryReadRouter(files, materials)

	recorder := call(t, router, http.MethodGet, "/api/v1/library/files/lib_1", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	// null rather than absent: a client distinguishing "not parsed" from "this
	// build does not answer that" reads the field, and has to find it.
	if !strings.Contains(recorder.Body.String(), `"document":null`) {
		t.Errorf("body = %s", recorder.Body.String())
	}
}

func TestGetMissingFileIsNotFound(t *testing.T) {
	files := &fileStub{err: fmt.Errorf("%w: lib_9", library.ErrNotFound)}
	router := newLibraryReadRouter(files, &materialStub{})

	recorder := call(t, router, http.MethodGet, "/api/v1/library/files/lib_9", "")

	if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), `"code":"not_found"`) {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestTextReturnsTheWindowAndTheChunkBoundaries(t *testing.T) {
	// AC-06: a citation carries a locator, and this is the endpoint the locator
	// opens. The offsets travel through untouched — a handler that adjusted them
	// would be a second place that has to agree about what a character is.
	document := testDocument()
	files := &fileStub{file: testLibraryFile()}
	materials := &materialStub{
		document: document,
		window: kb.Window{
			Document: document, Text: "保修期是两年。",
			CharStart: 100, CharEnd: 107, TotalChars: 4200,
		},
		spans: []kb.ChunkSpan{
			{ChunkID: "chk_0", Ordinal: 0, CharStart: 0, CharEnd: 512},
			{ChunkID: "chk_1", Ordinal: 1, CharStart: 462, CharEnd: 974, Page: ptrTo(3)},
		},
	}
	router := newLibraryReadRouter(files, materials)

	recorder := call(t, router, http.MethodGet, "/api/v1/library/files/lib_1/text?offset=100&limit=7", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var body fileTextView
	kbData(t, recorder, &body)
	if body.Text != "保修期是两年。" || body.CharStart != 100 || body.TotalChars != 4200 {
		t.Errorf("window = %+v", body)
	}
	if len(body.Chunks) != 2 || body.Chunks[1].Ordinal != 1 || *body.Chunks[1].Page != 3 {
		t.Fatalf("chunks = %+v", body.Chunks)
	}
	if materials.asked.offset != 100 || materials.asked.limit != 7 {
		t.Errorf("asked for a window at %d, %d", materials.asked.offset, materials.asked.limit)
	}
	if materials.asked.spansFor != "doc_1" {
		t.Errorf("boundaries read for %q", materials.asked.spansFor)
	}
	// The boundaries are coordinates, not content: a chunk's text is already in
	// the window beside it, and a document is two hundred thousand characters.
	// The wire shape is pinned here, spelling out that none of it is text.
	if !strings.Contains(recorder.Body.String(), `"chunks":[{"ordinal":0,"char_start":0,"char_end":512}`) {
		t.Errorf("body = %s", recorder.Body.String())
	}
}

func TestTextWithoutOffsetsAsksForTheStartOfTheDocument(t *testing.T) {
	document := testDocument()
	materials := &materialStub{document: document, window: kb.Window{Document: document, TotalChars: 4200}}
	router := newLibraryReadRouter(&fileStub{file: testLibraryFile()}, materials)

	recorder := call(t, router, http.MethodGet, "/api/v1/library/files/lib_1/text", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if materials.asked.offset != 0 || materials.asked.limit != 0 {
		t.Errorf("asked for a window at %d, %d; the domain decides the default length", materials.asked.offset, materials.asked.limit)
	}
	if !strings.Contains(recorder.Body.String(), `"chunks":[]`) {
		t.Errorf("body = %s", recorder.Body.String())
	}
}

func TestTextRefusesAnOffsetThatIsNotACount(t *testing.T) {
	materials := &materialStub{document: testDocument()}
	router := newLibraryReadRouter(&fileStub{file: testLibraryFile()}, materials)

	for _, query := range []string{"offset=-1", "offset=abc", "limit=x"} {
		t.Run(query, func(t *testing.T) {
			recorder := call(t, router, http.MethodGet, "/api/v1/library/files/lib_1/text?"+query, "")
			if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"code":"invalid_query"`) {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
		})
	}
	if materials.asked.docID != "" {
		t.Error("a refused request still read a window")
	}
}

func TestTextOfAFileThatWasNeverReadSaysSo(t *testing.T) {
	// 404 with its own code rather than the file's own not_found: the file
	// exists and the client may well be holding a valid link to it — what is
	// missing is the text, and the two send a client to different places.
	materials := &materialStub{err: fmt.Errorf("%w: no document", kb.ErrNotFound)}
	router := newLibraryReadRouter(&fileStub{file: testLibraryFile()}, materials)

	recorder := call(t, router, http.MethodGet, "/api/v1/library/files/lib_1/text", "")

	if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), `"code":"not_parsed"`) {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestTheReadEndpointsAnswerRatherThanPanicWhenUnconfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewLibraryReadHandler(nil, nil, testHandlerLogger())
	router.GET("/api/v1/library/files/:fileID", handler.Get)
	router.GET("/api/v1/library/files/:fileID/text", handler.Text)

	for _, path := range []string{"/api/v1/library/files/lib_1", "/api/v1/library/files/lib_1/text"} {
		t.Run(path, func(t *testing.T) {
			recorder := call(t, router, http.MethodGet, path, "")
			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &map[string]any{}); err != nil {
				t.Fatalf("the answer is not JSON: %v", err)
			}
		})
	}
}

func ptrTo[T any](value T) *T { return &value }
