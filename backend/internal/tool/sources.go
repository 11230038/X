package tool

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"backend/internal/event"
)

// Refusal is a tool saying it will not do what was asked, in a sentence the
// model can act on.
//
// It is an error rather than a quiet result because what it says is always about
// the request and never about the material — the label names nothing this turn
// carries, the query is too short to search with, the correct answer names an
// option the question does not offer — and a caller that did not know to expect
// one would render it as a result that succeeded, which for a read means "the
// material does not say this" and for a question means a question that was
// stored. As an error it becomes the loop's generic failure text instead: wrong,
// but never misleading. The tools themselves do expect it and turn it into a
// failed result carrying Note, which is the only thing that tells the model how
// to ask differently.
type Refusal struct {
	// Note is what the model is told. It is written in the language the tools
	// answer in, because it is read by the model and repeated to the user.
	Note string
}

func (r *Refusal) Error() string { return "tool: refused: " + r.Note }

// Sources is the turn runtime's side of the source tools: what this turn may
// read, and how to read it.
//
// The tools are registered once for the process while the material is one
// turn's, so the scope is found in the context the call arrives on — the same
// seam Asker uses. Neither method takes a file id or a collection id, because a
// parameter would be the model's to choose and the scope has to be the turn's:
// read_source names one of the turn's attachments by the label the turn gave it,
// and retrieve searches the turn's collections and nothing else.
type Sources interface {
	// ReadSource reads part of one of this turn's attachments. A label the turn
	// does not carry is refused with Refusal.
	ReadSource(ctx context.Context, request ReadRequest) (Reading, error)
	// Retrieve searches the knowledge bases this turn carries.
	Retrieve(ctx context.Context, query string) (Reading, error)
}

// ReadRequest is one read of one attachment.
type ReadRequest struct {
	// Label names the attachment in the turn's own numbering, which counts from
	// 1 in the order the turn declared its attachments.
	Label string
	// Query, when set, locates the passages of that one attachment that answer
	// it, instead of reading a window of it.
	Query string
	// Offset and Limit read a window of that attachment's text, in characters.
	// Offset zero and Limit zero is the start of the document and the reader's
	// default window.
	Offset int64
	Limit  int64
}

// Reading is what one read of this turn's material produced.
type Reading struct {
	// Sources are the pieces the read found, in the order the model should read
	// them. Each carries the number an answer cites it by, and that number is the
	// turn's rather than the call's: two reads that each numbered from 1 would
	// leave one number meaning two passages.
	Sources []event.Source
	// Unavailable says what this read could not give the model, and why.
	//
	// It is not an error. An attachment that is a picture, a collection that has
	// been deleted, a query the index cannot match at all: each is a sentence the
	// model has to be able to pass on (ATT-04, KB-07), and a note with no source
	// beside it is the honest answer to a read that found nothing. Dropping them
	// would leave the model to say "the material does not mention this" when what
	// happened is that the material could not be looked at.
	Unavailable []string
}

// SourcesText renders a list of sources the way the model is shown them: the
// number an answer cites, the document's title, and the text.
//
// It is one function rather than two because the two callers — a read the model
// asked for, and the pre-survey the runtime ran before the first call — are the
// same list seen at different moments, and a model that learned one shape and
// then met another would have to guess which numbers it may cite (AIQ-02). The
// locator, the page and the character span are deliberately absent: they are
// this service's bookkeeping, and coordinates the model never saw are
// coordinates it cannot invent.
func SourcesText(sources []event.Source) string {
	var content strings.Builder
	for _, source := range sources {
		fmt.Fprintf(&content, "[%d] %s\n%s\n\n", source.Index, source.Title, source.Snippet)
	}
	return content.String()
}

// renderReading turns what a read found into what the model is given.
//
// A read that found nothing still answers: it says so, and it says what it could
// not look at, because "the material does not mention this" and "the material
// could not be read" are different sentences and only one of them is true.
func renderReading(reading Reading) Result {
	var content strings.Builder
	content.WriteString(SourcesText(reading.Sources))
	if len(reading.Sources) == 0 {
		content.WriteString("这一轮携带的材料里没有找到相关的片段。\n")
	}
	for _, note := range reading.Unavailable {
		content.WriteString(note)
		content.WriteString("\n")
	}
	// A read that ran is a successful call whatever it found: the alternative is
	// a "failure" whose content the model would have to be told to trust anyway.
	return Result{Content: strings.TrimSpace(content.String()), Success: true}
}

// refused turns a source tool's refusal into the result the model reads.
//
// The sentence is the whole point: the loop's generic failure text would tell
// the model the tool could not be run, and the model would try the same thing
// again. A refusal it can act on says what it asked for that this turn has not
// got, or how to ask in a way the index can answer.
func refused(err error) (Result, error) {
	var refusal *Refusal
	if errors.As(err, &refusal) {
		return Result{Content: refusal.Note, Success: false}, nil
	}
	return Result{}, err
}
