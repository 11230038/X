package parse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"backend/internal/outbound"
)

const testPDF = "%PDF-1.4\n%%EOF\n"

// answer is an httptest server that replies with one fixed conversion result.
func answer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}

// dialEngine is the boundary these cases run behind, and it is the deployment's
// own: the document engine is a service beside the product, so the LAN policy is
// what §2.6 gives it. Built as a value rather than written as a bare net.Dialer
// so that a change to the policy changes what these cases prove.
func dialEngine(t *testing.T) outbound.DialFunc {
	t.Helper()
	dial, err := outbound.DialContext(outbound.LANServicePolicy{})
	if err != nil {
		t.Fatalf("build the engine dialer: %v", err)
	}
	return dial
}

func askDocling(t *testing.T, baseURL string, timeout time.Duration) (Result, error) {
	t.Helper()
	return newDoclingParser(baseURL, timeout, dialEngine(t)).Parse(context.Background(), Request{
		Filename: "paper.pdf", MIME: "application/pdf", Category: CategoryPDF,
		Content: strings.NewReader(testPDF), MaxChars: 1000,
	})
}

// send is what the adapter does with a request, read back as the engine would
// see it.
type sentRequest struct {
	method      string
	path        string
	query       string
	contentType string
	accept      string
	fileName    string
	fileType    string
	fileBody    string
	fields      map[string]string
}

func record(t *testing.T) (*httptest.Server, *sentRequest) {
	t.Helper()
	seen := &sentRequest{fields: map[string]string{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.method, seen.path, seen.query = r.Method, r.URL.Path, r.URL.RawQuery
		seen.contentType, seen.accept = r.Header.Get("Content-Type"), r.Header.Get("Accept")
		body, _ := io.ReadAll(r.Body)
		_, parameters, _ := mime.ParseMediaType(seen.contentType)
		reader := multipart.NewReader(bytes.NewReader(body), parameters["boundary"])
		for {
			part, err := reader.NextPart()
			if err != nil {
				break
			}
			content, _ := io.ReadAll(part)
			if part.FileName() != "" {
				seen.fileName, seen.fileBody = part.FileName(), string(content)
				seen.fileType = part.Header.Get("Content-Type")
				continue
			}
			seen.fields[part.FormName()] = string(content)
		}
		_, _ = io.WriteString(w, `{"document":{"md_content":"# Title\n\nBody."},"status":"success","errors":[]}`)
	}))
	t.Cleanup(server.Close)
	return server, seen
}

func TestDoclingParserPostsTheFileAndReturnsTheMarkdown(t *testing.T) {
	server, seen := record(t)

	result, err := askDocling(t, server.URL, 5*time.Second)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if result.Text != "# Title\n\nBody." {
		t.Errorf("Text = %q, want the markdown from the answer", result.Text)
	}
	if result.Engine != EngineDocling {
		t.Errorf("Engine = %q, want %q", result.Engine, EngineDocling)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none for a successful conversion", result.Warnings)
	}
	// The engine's spans index the original document, not the markdown it
	// produced, so no page number is claimed from them.
	if len(result.Pages) != 0 {
		t.Errorf("Pages = %v, want none: the engine's spans are not offsets into this text", result.Pages)
	}

	if seen.method != http.MethodPost {
		t.Errorf("method = %q, want POST", seen.method)
	}
	if seen.path != "/v1/convert/file" {
		t.Errorf("path = %q, want /v1/convert/file", seen.path)
	}
	if seen.query != "" {
		t.Errorf("query = %q, want none: the options are form fields", seen.query)
	}
	if seen.accept != "application/json" {
		t.Errorf("Accept = %q, want application/json", seen.accept)
	}
	if seen.fileName != "paper.pdf" {
		t.Errorf("filename = %q, want paper.pdf", seen.fileName)
	}
	if seen.fileBody != testPDF {
		t.Errorf("file body = %q, want the bytes that were handed over", seen.fileBody)
	}
	if seen.fileType != "application/pdf" {
		t.Errorf("file content type = %q, want the request's own MIME type", seen.fileType)
	}
	// The format is asked for: the endpoint fills only the fields named in it.
	if seen.fields[convertFormatField] != convertMarkdown {
		t.Errorf("%s = %q, want %q", convertFormatField, seen.fields[convertFormatField], convertMarkdown)
	}
}

func TestDoclingParserAcceptsABaseURLWithATrailingSlash(t *testing.T) {
	server, seen := record(t)

	if _, err := askDocling(t, server.URL+"/", 5*time.Second); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if seen.path != "/v1/convert/file" {
		t.Fatalf("path = %q, want /v1/convert/file", seen.path)
	}
}

// A file with no MIME type gets no Content-Type part header: an empty one would
// be a claim about the file that nobody made.
func TestDoclingParserOmitsAnEmptyMIMEType(t *testing.T) {
	server, seen := record(t)

	_, err := newDoclingParser(server.URL, 5*time.Second, dialEngine(t)).Parse(context.Background(), Request{
		Filename: "paper.pdf", Category: CategoryPDF,
		Content: strings.NewReader(testPDF), MaxChars: 1000,
	})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if seen.fileType != "" {
		t.Fatalf("file content type = %q, want none", seen.fileType)
	}
}

// sanitizeFilename guards a seam: everything reaching this adapter has already
// passed the file library's checks, but this is the boundary where a name goes
// into a protocol header.
func TestSanitizeFilenameRemovesWhatWouldEndTheHeader(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{name: "paper.pdf", want: "paper.pdf"},
		{name: "论知识库的设计.pdf", want: "论知识库的设计.pdf"},
		{name: `a".pdf`, want: "a.pdf"},
		{name: `a\b.pdf`, want: "ab.pdf"},
		{name: "a\r\nX-Evil: 1.pdf", want: "aX-Evil: 1.pdf"},
		{name: "  ", want: "document"},
		{name: "", want: "document"},
	}

	for _, testCase := range cases {
		if got := sanitizeFilename(testCase.name); got != testCase.want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", testCase.name, got, testCase.want)
		}
	}
}

// A partial conversion is text the user can use. Throwing it away would lose
// most of a document over one page, and keeping it silently would hide that
// something is missing.
func TestDoclingParserKeepsAPartialConversionAndSaysSo(t *testing.T) {
	server := answer(t, http.StatusOK, `{
		"document": {"md_content": "the part that was read"},
		"status": "partial_success",
		"errors": [{"error_message": "page 3 could not be read"}]
	}`)

	result, err := askDocling(t, server.URL, 5*time.Second)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if result.Text != "the part that was read" {
		t.Errorf("Text = %q, want the text that was returned", result.Text)
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("Warnings = %v, want one", result.Warnings)
	}
	for _, fragment := range []string{"partial_success", "page 3 could not be read"} {
		if !strings.Contains(result.Warnings[0], fragment) {
			t.Errorf("warning %q, want it to mention %q", result.Warnings[0], fragment)
		}
	}
}

// The boundary is not an ornament here either. A parser built with a dialer and
// installing nothing would send every document wherever PARSER_DOCLING_BASE_URL
// pointed, which is the state §2.5 replaces — and every other case in this file
// would still pass, because they all reach a server that is already allowed.
func TestDoclingRequestsGoThroughTheDialerItWasBuiltWith(t *testing.T) {
	var mu sync.Mutex
	var reached []string
	server := answer(t, http.StatusOK, `{"document":{"md_content":"read"},"status":"success","errors":[]}`)

	through := dialEngine(t)
	parser := newDoclingParser(server.URL, 5*time.Second,
		func(ctx context.Context, network, address string) (net.Conn, error) {
			mu.Lock()
			reached = append(reached, address)
			mu.Unlock()
			return through(ctx, network, address)
		})

	if _, err := parser.Parse(context.Background(), Request{
		Filename: "paper.pdf", Category: CategoryPDF,
		Content: strings.NewReader(testPDF), MaxChars: 1000,
	}); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(reached) == 0 {
		t.Fatal("the request never passed through the dialer the parser was built with")
	}
}

func TestDoclingParserReportsWhyTheFileWasRefused(t *testing.T) {
	server := answer(t, http.StatusInternalServerError, "the engine is unwell")

	_, err := askDocling(t, server.URL, 5*time.Second)
	if CodeOf(err) != CodeParseFailed {
		t.Fatalf("code = %q, want %q (error: %v)", CodeOf(err), CodeParseFailed, err)
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %v, want it to name the status", err)
	}
	// What the engine said is kept for the log and not shown as the message.
	var parseErr *Error
	if !errors.As(err, &parseErr) {
		t.Fatalf("error = %v, want a *parse.Error", err)
	}
	if parseErr.Cause == nil || !strings.Contains(parseErr.Cause.Error(), "the engine is unwell") {
		t.Errorf("cause = %v, want the engine's own words kept for the log", parseErr.Cause)
	}
	if strings.Contains(parseErr.Message, "the engine is unwell") {
		t.Errorf("message = %q, want the engine's words out of what a client is shown", parseErr.Message)
	}
}

// A body that is not a conversion result is reported as that, and not as a
// document with no text in it.
func TestDoclingParserRefusesAnAnswerItCannotMakeSenseOf(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "html from a proxy", body: "<html><body>502 Bad Gateway</body></html>"},
		{name: "json of another shape", body: `{"message":"ok"}`},
		{name: "empty body", body: ""},
		{name: "json that is not a conversion", body: `[]`},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := answer(t, http.StatusOK, testCase.body)

			_, err := askDocling(t, server.URL, 5*time.Second)
			if CodeOf(err) != CodeParseFailed {
				t.Fatalf("code = %q, want %q (error: %v)", CodeOf(err), CodeParseFailed, err)
			}
			if !strings.Contains(err.Error(), "conversion result") {
				t.Errorf("error = %v, want it to say what was wrong with the answer", err)
			}
		})
	}
}

func TestDoclingParserSaysWhenThereWasNoTextToExtract(t *testing.T) {
	t.Run("the engine reported nothing at all", func(t *testing.T) {
		server := answer(t, http.StatusOK, `{"document":{"md_content":"   "},"status":"success","errors":[]}`)

		_, err := askDocling(t, server.URL, 5*time.Second)
		if CodeOf(err) != CodeParseFailed {
			t.Fatalf("code = %q, want %q (error: %v)", CodeOf(err), CodeParseFailed, err)
		}
		// A scanned PDF is the common cause, and the fix is on the engine, so
		// the message says where to look.
		if !strings.Contains(err.Error(), "OCR") {
			t.Errorf("error = %v, want it to name OCR", err)
		}
	})

	t.Run("the engine said why", func(t *testing.T) {
		server := answer(t, http.StatusOK, `{
			"document": {"md_content": ""},
			"status": "failure",
			"errors": [{"message": "the file is password protected"}]
		}`)

		_, err := askDocling(t, server.URL, 5*time.Second)
		if CodeOf(err) != CodeParseFailed {
			t.Fatalf("code = %q, want %q (error: %v)", CodeOf(err), CodeParseFailed, err)
		}
		var parseErr *Error
		if !errors.As(err, &parseErr) {
			t.Fatalf("error = %v, want a *parse.Error", err)
		}
		if parseErr.Cause == nil || !strings.Contains(parseErr.Cause.Error(), "password protected") {
			t.Errorf("cause = %v, want the engine's reason", parseErr.Cause)
		}
	})
}

func TestDoclingParserTellsTooSlowFromNotThere(t *testing.T) {
	t.Run("too slow", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(300 * time.Millisecond)
		}))
		t.Cleanup(server.Close)

		_, err := askDocling(t, server.URL, 50*time.Millisecond)
		if CodeOf(err) != CodeParseFailed {
			t.Fatalf("code = %q, want %q (error: %v)", CodeOf(err), CodeParseFailed, err)
		}
		if !strings.Contains(err.Error(), "in time") {
			t.Errorf("error = %v, want it to say the engine was too slow", err)
		}
	})

	t.Run("not there", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		address := server.URL
		server.Close()

		_, err := askDocling(t, address, 5*time.Second)
		if CodeOf(err) != CodeParserUnavailable {
			t.Fatalf("code = %q, want %q (error: %v)", CodeOf(err), CodeParserUnavailable, err)
		}
		// Whoever has to fix this needs both facts: where it looked, and which
		// setting points it there.
		for _, fragment := range []string{address, "PARSER_DOCLING_BASE_URL"} {
			if !strings.Contains(err.Error(), fragment) {
				t.Errorf("error = %v, want it to mention %q", err, fragment)
			}
		}
	})
}

func TestDoclingParserReturnsCancellationAsCancellation(t *testing.T) {
	server, _ := record(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := newDoclingParser(server.URL, 5*time.Second, dialEngine(t)).Parse(ctx, Request{
		Filename: "paper.pdf", Category: CategoryPDF,
		Content: strings.NewReader(testPDF), MaxChars: 1000,
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if CodeOf(err) != "" {
		t.Fatalf("code = %q, want none: a caller that gave up is not a failed document", CodeOf(err))
	}
}

// A file that cannot be read cannot be sent either, and that is a failure of
// this document rather than of the engine — so the engine is never called.
func TestDoclingParserReportsAFileItCannotRead(t *testing.T) {
	server, seen := record(t)

	_, err := newDoclingParser(server.URL, 5*time.Second, dialEngine(t)).Parse(context.Background(), Request{
		Filename: "paper.pdf", Category: CategoryPDF, MaxChars: 1000,
		Content: errorReader{errors.New("the disk went away")},
	})
	if CodeOf(err) != CodeParseFailed {
		t.Fatalf("code = %q, want %q (error: %v)", CodeOf(err), CodeParseFailed, err)
	}
	if seen.path != "" {
		t.Fatalf("the engine was called at %q, want no call at all", seen.path)
	}
}

// firstErrorMessage digs a sentence out of a list whose element shape is the
// engine's business. What it finds is passed on; what it does not find is not
// invented.
func TestFirstErrorMessage(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{name: "no errors", raw: `[]`, want: ""},
		{name: "the documented key", raw: `[{"error_message":"bad page"}]`, want: "bad page"},
		{name: "a shorter key", raw: `[{"message":"bad page"}]`, want: "bad page"},
		{name: "the shortest key", raw: `[{"error":"bad page"}]`, want: "bad page"},
		{name: "a plain string", raw: `["bad page"]`, want: ""},
		{name: "a key this service does not know", raw: `[{"detail":"bad page"}]`, want: ""},
		{name: "an empty message", raw: `[{"error_message":""}]`, want: ""},
		{name: "the second one has it", raw: `[{"detail":"x"},{"message":"bad page"}]`, want: "bad page"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var raw []json.RawMessage
			if err := json.Unmarshal([]byte(testCase.raw), &raw); err != nil {
				t.Fatalf("the test's own fixture is not valid json: %v", err)
			}
			if got := firstErrorMessage(raw); got != testCase.want {
				t.Errorf("firstErrorMessage(%s) = %q, want %q", testCase.raw, got, testCase.want)
			}
		})
	}
}

func TestTruncateForLogBoundsWhatTheEngineSaid(t *testing.T) {
	long := strings.Repeat("x", maxEngineMessageBytes+100)

	got := truncateForLog(long)
	if len(got) != maxEngineMessageBytes+3 {
		t.Fatalf("length = %d, want %d plus the ellipsis", len(got), maxEngineMessageBytes)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("truncateForLog() = %q, want it to end in an ellipsis", got)
	}
	if got := truncateForLog("  short  "); got != "short" {
		t.Errorf("truncateForLog() = %q, want %q", got, "short")
	}
}
