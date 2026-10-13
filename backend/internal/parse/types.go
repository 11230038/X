// Package parse turns a stored file into text.
//
// It is a port, not a set of integrations: an engine receives bytes and a
// filename and returns text. Nothing here knows about documents, chunks or
// knowledge bases — that is internal/kb's business — and nothing here decides at
// startup whether an optional service is up, because a dependency that may be
// absent is one that must be found missing when it is actually needed and not
// before (AC-14).
package parse

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// The categories this package reads. They are the strings internal/library
// derives from a filename extension, and a test in that package pins the two
// lists together: a category renamed on one side would otherwise turn a file
// that used to be readable into one nobody can read, with nothing failing.
const (
	CategoryMarkdown = "md"
	CategoryPDF      = "pdf"
	CategoryDocument = "doc"
)

// The engine names. They are also the values PARSER_DEFAULT_ENGINE accepts, and
// the registry is the authority on which of them this build has.
const (
	EngineText    = "text"
	EngineDocling = "docling"
)

// Code is the stable name of a parse outcome. It is what a client is told and
// what the logs group by, so a code never changes meaning: a new way to fail
// gets a new code, not a redefinition of an old one.
type Code string

const (
	// CodeParserUnavailable: no configured engine reads this category, or the
	// one that does is not answering. Both are fixable by whoever runs this
	// service, which is why they are one code and the message says how.
	CodeParserUnavailable Code = "parser_unavailable"
	// CodeUnsupportedFormat: no engine in this build reads this category at all.
	CodeUnsupportedFormat Code = "unsupported_format"
	// CodeParseFailed: an engine ran and failed — a broken file, an encrypted
	// one, a timeout, an answer that did not make sense.
	CodeParseFailed Code = "parse_failed"
	// CodeParseTruncated: the document is longer than the limit. Unlike the
	// other three, this one arrives with a usable result; see Parser.Parse.
	CodeParseTruncated Code = "parse_truncated"
)

// Error is a parse failure.
//
// Message is written for the person who uploaded the file: what happened, and
// what to do about it. Cause is the failure underneath — a path, an engine's own
// words, an operating system error — and it is kept for the log rather than
// shown to a client, which is the same split the rest of the service makes.
type Error struct {
	Code    Code
	Message string
	// Engine names the engine that failed. It is empty when the failure
	// happened before any engine was chosen.
	Engine string
	Cause  error
}

func (e *Error) Error() string {
	if e.Engine == "" {
		return fmt.Sprintf("parse: %s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("parse: %s (%s): %s", e.Code, e.Engine, e.Message)
}

// Unwrap exposes the underlying failure, so that cancellation and a transport
// error are still recognisable through the code.
func (e *Error) Unwrap() error {
	return e.Cause
}

// CodeOf reports the code of a parse failure, or "" when err is not one. It is
// how a caller tells the four outcomes apart without reaching into the error.
func CodeOf(err error) Code {
	var parseErr *Error
	if errors.As(err, &parseErr) {
		return parseErr.Code
	}
	return ""
}

// Request is one document to read.
type Request struct {
	Filename string
	MIME     string
	// Category is what the file library classified the file as. The engine is
	// chosen from it, not from the MIME type: a category is a kind of work, and
	// the MIME types that map to one category (md and markdown, doc and docx)
	// are not the same set the engines work in.
	Category string
	Content  io.Reader
	// MaxChars is the per-document limit in characters — Unicode code points,
	// not bytes, because that is the unit a citation's offsets count in. It is
	// required and positive: it is what bounds how much a single document can
	// put in memory, and what turns "too long" into a reported outcome instead
	// of a silent cut.
	MaxChars int
}

// PageRange maps a page of the original document to the span of extracted text
// it produced.
//
// The offsets count characters, matching the offsets on a chunk, so that a
// citation can be resolved either way. It is optional because an engine that
// cannot place its text on a page must not guess a page number: a wrong page is
// exactly the kind of invention the citation rules forbid.
type PageRange struct {
	Page  int
	Start int64
	End   int64
}

// Result is the text an engine produced.
type Result struct {
	Text string
	// Engine names the engine that produced the text. The registry fills it, so
	// that an engine cannot disagree with the one that was chosen.
	Engine string
	// Pages is empty when the engine cannot say where its text came from.
	Pages []PageRange
	// Warnings describes something the caller should know but that does not
	// invalidate the text — a partial conversion, a page that could not be read.
	Warnings []string
}

// Parser reads one kind of file into text.
type Parser interface {
	// Name returns the engine name, which is a value PARSER_DEFAULT_ENGINE
	// accepts.
	Name() string
	// Supports reports whether this engine reads documents of that category.
	Supports(category string) bool
	// Parse reads the document.
	//
	// Every failure is a *Error with one of the four codes, except that
	// cancellation surfaces as ctx.Err(): a caller that gave up is not a
	// document that failed to parse, and the two call for different responses.
	//
	// CodeParseTruncated is the one code that arrives with a usable result. A
	// document longer than Request.MaxChars is still a document, and the two
	// alternatives are both worse: storing nothing loses material that the user
	// uploaded, and storing the tail without saying so is the silent truncation
	// the requirements forbid. So the text that fits is returned alongside the
	// error, and the caller keeps it. For every other code the Result is the
	// zero value and must be ignored.
	Parse(ctx context.Context, req Request) (Result, error)
}
