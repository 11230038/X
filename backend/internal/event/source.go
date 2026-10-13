package event

// The metadata keys the protocol defines for sources.
const (
	// MetaSources carries the list of Source a sources event is about. The
	// payload is an object with one key rather than a bare array so that it can
	// grow — a count, a truncated flag — without breaking readers.
	MetaSources = "sources"
	// MetaUnresolvedCitations carries the numbers an answer cited that no
	// returned source had. They are recorded and nothing more: the answer is the
	// model's own words and is not rewritten, and no source is invented to make
	// a number add up.
	MetaUnresolvedCitations = "unresolved_citations"
	// MetaGrounding carries what a turn's material amounted to: on a result
	// event, how many sources the turn handed out and how much of its material
	// could not be read at all — which is what "the material does not answer
	// this" is told apart from "the material was never looked at" by (AC-06); on
	// an observation event, the survey's own summary and the same list.
	MetaGrounding = "grounding"
	// MetaQuestionPlan carries the set of questions a turn set out to write:
	// how many, of what kinds, at what difficulty, out of which material. It is
	// what an observation event says before the first question exists, so that
	// "我准备出 10 道" is on screen while the model is still being called — and
	// what the progress each question reports is then counted against.
	MetaQuestionPlan = "question_plan"
)

// Source is one piece of material an answer was built from.
//
// Every field is something the server read out of its own rows, and that is the
// point of it: a citation's page, its character span and its chunk id never pass
// through the model, so the model cannot invent one. It sees numbered sources
// and writes numbers.
type Source struct {
	// Index is the number the answer refers to this source by, counted from 1
	// within one list of sources.
	Index int `json:"index"`
	// DocumentID and FileID identify the parsed document and the stored file it
	// came from. FileID is half of what opens a citation: the other half is the
	// character span below.
	DocumentID string `json:"document_id"`
	FileID     string `json:"file_id"`
	// Title is the original filename, which is what a reader recognises.
	Title  string `json:"title"`
	Engine string `json:"engine,omitempty"`
	// Locator says where in the document this source is.
	Locator Locator `json:"locator"`
	// Snippet is the text itself: the chunk's content, or the window that was
	// read out of a document.
	Snippet string `json:"snippet"`
	// Score is the retrieval score, present when the source came from rank
	// fusion. It is not a similarity and is not comparable across queries; it is
	// what ordered the list.
	Score float64 `json:"score,omitempty"`
	// Revision is the document revision this text came from, so that a reader
	// looking at the file later can tell whether it has changed underneath.
	Revision int `json:"revision"`
}

// Locator is the coordinate a source can be opened at.
//
// The character span is in characters of the document's extracted text — the
// same unit as every other offset here — and it is what the preview endpoint
// takes. Page is optional because not every engine can place its text on a page,
// and a guessed page number is exactly what the citation rules forbid.
type Locator struct {
	// Kind names what the span refers to. "chunk" for a retrieved chunk,
	// "window" for a range read out of a document.
	Kind      string `json:"kind"`
	ChunkID   string `json:"chunk_id,omitempty"`
	Ordinal   int    `json:"ordinal,omitempty"`
	CharStart int64  `json:"char_start"`
	CharEnd   int64  `json:"char_end"`
	Page      *int   `json:"page,omitempty"`
}
