package tool

import (
	"context"

	"backend/internal/event"
)

// Refusal is a source tool saying it will not do what was asked, in a sentence
// the model can act on.
//
// It is an error rather than a quiet result because the two things it says are
// both about the request and not about the material — the label names nothing
// this turn carries, the query is too short to search with — and a caller that
// did not know to expect one would render it as an empty read, which reads as
// "the material does not say this". As an error it becomes the loop's generic
// failure text instead: wrong, but never misleading. The source tools
// themselves do expect it and turn it into a failed result carrying Note, which
// is the only thing that tells the model how to ask differently.
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
	// does not carry is refused with ErrUnknownSource.
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
