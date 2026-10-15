package parse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"strings"
	"time"

	"backend/internal/outbound"
)

// convertFilePath is Docling Serve's file conversion endpoint, appended to the
// configured base URL.
const convertFilePath = "/v1/convert/file"

// The form field names the endpoint takes. to_formats is sent because only the
// formats asked for come back: without it the answer's shape is the server's
// choice.
const (
	convertFileField   = "files"
	convertFormatField = "to_formats"
	convertMarkdown    = "md"
)

// maxConversionBytes bounds the answer this adapter will read. A conversion
// result is text, so it is smaller than the document that produced it, and a
// ceiling here keeps a service that answers with something enormous from
// becoming this process's memory problem.
const maxConversionBytes = 32 << 20

// maxErrorBodyBytes bounds how much of a refusal is kept. It goes into a log
// line as the cause, never into a message a client sees.
const maxErrorBodyBytes = 8 << 10

// doclingParser reads a file by sending it to a Docling Serve instance, which
// turns PDFs and office documents into Markdown.
//
// It is deliberately the only thing in this package that talks to another
// process, and it knows nothing about what the text is for: protocol conversion
// and error normalisation, nothing more.
type doclingParser struct {
	baseURL string
	http    *http.Client
}

var _ Parser = doclingParser{}

// newDoclingParser builds the adapter. dial is where this engine's requests are
// allowed to go, produced by internal/outbound from a policy — and the policy
// is the LAN one, because this service sits beside the product rather than out
// on the internet (§2.6).
//
// A nil dial is not a state this function handles: NewRegistry refuses it, the
// way it already refuses a timeout that is not positive, and this constructor
// is not reachable from anywhere else in the build. It is a parameter and not a
// default because the default would be silence — the client this replaces had
// no transport at all, which is precisely how the three clients in this
// repository ended up with no boundary between them.
func newDoclingParser(baseURL string, timeout time.Duration, dial outbound.DialFunc) doclingParser {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// The one hook every connection this engine makes has to pass through.
	transport.DialContext = dial
	// The whole call is bounded, not just the wait for headers: the answer is a
	// single body, and a conversion that never finishes holding a worker is the
	// failure this timeout exists to prevent.
	return doclingParser{
		baseURL: strings.TrimSuffix(strings.TrimSpace(baseURL), "/"),
		http:    &http.Client{Timeout: timeout, Transport: transport},
	}
}

func (doclingParser) Name() string { return EngineDocling }

func (doclingParser) Supports(category string) bool {
	return category == CategoryPDF || category == CategoryDocument
}

func (p doclingParser) Parse(ctx context.Context, req Request) (Result, error) {
	body, contentType, err := p.buildRequest(req)
	if err != nil {
		return Result{}, &Error{
			Code:    CodeParseFailed,
			Engine:  p.Name(),
			Message: "the file could not be prepared for the document engine",
			Cause:   err,
		}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+convertFilePath, body)
	if err != nil {
		return Result{}, &Error{
			Code:    CodeParseFailed,
			Engine:  p.Name(),
			Message: "the request to the document engine could not be built",
			Cause:   err,
		}
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := p.http.Do(httpReq)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Result{}, ctxErr
		}
		return Result{}, p.describeTransportFailure(err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Result{}, p.describeRefusal(resp)
	}
	return p.decode(ctx, resp.Body)
}

// describeTransportFailure tells "the service is not there" apart from "the
// service is too slow", because they call for different things: the first is a
// deployment that has to be fixed, and the second is a document that may need
// more time or a smaller page range.
func (p doclingParser) describeTransportFailure(err error) error {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &Error{
			Code:    CodeParseFailed,
			Engine:  p.Name(),
			Message: "the document engine did not finish in time",
			Cause:   err,
		}
	}
	// This is the only place the unreachable service is discovered, and that is
	// by design: pinging it at startup would make the whole service depend on a
	// component it can live without.
	return &Error{
		Code:   CodeParserUnavailable,
		Engine: p.Name(),
		Message: fmt.Sprintf(
			"the document engine at %s did not answer; check that the service is running and that PARSER_DOCLING_BASE_URL points at it",
			p.baseURL),
		Cause: err,
	}
}

func (p doclingParser) describeRefusal(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	return &Error{
		Code:   CodeParseFailed,
		Engine: p.Name(),
		Message: fmt.Sprintf(
			"the document engine refused the file with status %d", resp.StatusCode),
		Cause: fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body))),
	}
}

func (p doclingParser) buildRequest(req Request) (io.Reader, string, error) {
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)

	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`,
		convertFileField, sanitizeFilename(req.Filename)))
	if req.MIME != "" {
		header.Set("Content-Type", req.MIME)
	}
	part, err := writer.CreatePart(header)
	if err != nil {
		return nil, "", err
	}
	if _, err := io.Copy(part, req.Content); err != nil {
		return nil, "", err
	}
	// The format is requested, not assumed: the endpoint populates only the
	// fields named here, so a server that defaulted to another format would
	// answer with an empty Markdown field.
	if err := writer.WriteField(convertFormatField, convertMarkdown); err != nil {
		return nil, "", err
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return &buffer, writer.FormDataContentType(), nil
}

// sanitizeFilename keeps a filename from ending the header it sits in.
//
// The file library already refuses control characters and path separators, but
// this adapter is a public seam and a quote or a newline here would let a
// filename append headers of its own choosing. What is left is what the engine
// needs: something to name the temporary file after.
func sanitizeFilename(name string) string {
	name = strings.Map(func(r rune) rune {
		switch r {
		case '"', '\\', '\r', '\n':
			return -1
		}
		return r
	}, name)
	if strings.TrimSpace(name) == "" {
		return "document"
	}
	return name
}

// convertResponse is Docling Serve's answer. Only the fields this service uses
// are decoded; status and errors are read as a witness that the body really is a
// conversion result, so that a changed or unexpected shape is reported as such
// instead of as a document with no text in it.
type convertResponse struct {
	Document struct {
		MDContent string `json:"md_content"`
	} `json:"document"`
	Status string            `json:"status"`
	Errors []json.RawMessage `json:"errors"`
}

func (p doclingParser) decode(ctx context.Context, body io.Reader) (Result, error) {
	raw, err := io.ReadAll(io.LimitReader(body, maxConversionBytes+1))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Result{}, ctxErr
		}
		return Result{}, &Error{
			Code:    CodeParseFailed,
			Engine:  p.Name(),
			Message: "the document engine's answer could not be read",
			Cause:   err,
		}
	}
	if len(raw) > maxConversionBytes {
		return Result{}, &Error{
			Code:    CodeParseFailed,
			Engine:  p.Name(),
			Message: "the document engine's answer is larger than this service accepts",
		}
	}

	var payload convertResponse
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Status == "" {
		cause := err
		if cause == nil {
			cause = fmt.Errorf("no status field in the answer")
		}
		return Result{}, &Error{
			Code:    CodeParseFailed,
			Engine:  p.Name(),
			Message: "the document engine returned something other than a conversion result",
			Cause:   cause,
		}
	}

	text := payload.Document.MDContent
	engineSaid := firstErrorMessage(payload.Errors)
	if strings.TrimSpace(text) == "" {
		message := "the document engine extracted no text; if this is a scanned document it needs OCR enabled on the engine"
		var cause error
		if engineSaid != "" {
			message = "the document engine extracted no text from the file"
			cause = fmt.Errorf("engine reported %q: %s", payload.Status, engineSaid)
		}
		return Result{}, &Error{Code: CodeParseFailed, Engine: p.Name(), Message: message, Cause: cause}
	}

	result := Result{Text: text, Engine: p.Name()}
	// A partial conversion is text the user can use, so it is kept and the
	// reason it is partial is carried alongside rather than thrown away with it.
	if payload.Status != convertStatusSuccess {
		result.Warnings = append(result.Warnings, describePartialStatus(payload.Status, engineSaid))
	}
	return result, nil
}

// convertStatusSuccess is the only status that means "this is all of it".
const convertStatusSuccess = "success"

func describePartialStatus(status, engineSaid string) string {
	if engineSaid == "" {
		return fmt.Sprintf("the document engine reported %s", status)
	}
	return fmt.Sprintf("the document engine reported %s: %s", status, engineSaid)
}

// firstErrorMessage digs a sentence out of the engine's error list. The list's
// element shape is the engine's business, so the known keys are tried and what
// is found is passed on verbatim; nothing is invented when nothing matches.
func firstErrorMessage(errors []json.RawMessage) string {
	for _, raw := range errors {
		var fields map[string]any
		if err := json.Unmarshal(raw, &fields); err != nil {
			continue
		}
		for _, key := range []string{"error_message", "message", "error"} {
			if text, ok := fields[key].(string); ok && strings.TrimSpace(text) != "" {
				return truncateForLog(text)
			}
		}
	}
	return ""
}

// maxEngineMessageBytes bounds how much of the engine's own words are kept.
// They end up in a log line, and an engine that answers with a stack trace
// should not be able to fill it.
const maxEngineMessageBytes = 512

func truncateForLog(text string) string {
	text = strings.TrimSpace(text)
	if len(text) <= maxEngineMessageBytes {
		return text
	}
	return text[:maxEngineMessageBytes] + "..."
}
