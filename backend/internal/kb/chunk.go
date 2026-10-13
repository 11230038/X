// Package kb is the knowledge base domain: a file becomes text, text becomes
// chunks, chunks are retrieved two ways, and the two rankings are fused into one
// list of sources.
//
// It is the only package that knows what a chunk is. internal/parse returns text
// and knows nothing about chunks; the tools call in through a port declared
// where the tools live, so this package does not depend on them; the database
// does the distance and the full-text arithmetic, and the rank fusion happens
// here, in Go, because ranking is the one part of retrieval that has to be
// explainable when a user asks why they were shown that.
package kb

import (
	"fmt"
	"strconv"
)

// Chunking is the window a document's text is cut into, in characters.
//
// It is recorded on the document row rather than read from configuration at
// retrieval time: a document is chunked once and rebuilt later, and a rebuild
// after the window changed would produce chunks whose offsets no longer describe
// the citations that were already handed out.
type Chunking struct {
	Size    int
	Overlap int
}

// Validate reports whether this window can cut anything.
//
// The overlap rule is the one that matters: at or above the size no text ever
// advances, and a cutter that does not advance does not terminate.
func (c Chunking) Validate() error {
	if c.Size <= 0 {
		return fmt.Errorf("kb: chunk size must be a positive number of characters")
	}
	if c.Overlap < 0 {
		return fmt.Errorf("kb: chunk overlap must not be negative")
	}
	if c.Overlap >= c.Size {
		return fmt.Errorf("kb: chunk overlap (%d) must be smaller than the chunk size (%d)", c.Overlap, c.Size)
	}
	return nil
}

// Chunk is one retrievable span of a document's text.
//
// CharStart and CharEnd are offsets in characters — code points, like every
// other offset in this service — into the document's extracted text, and the
// content is exactly the text between them.
type Chunk struct {
	Ordinal   int
	CharStart int64
	CharEnd   int64
	Content   string
}

// Split cuts text into overlapping chunks.
//
// The cuts are placed so that the chunks laid back down in order, with the
// overlap removed, are the original text character for character. Everything
// downstream leans on that: a citation points at a document's text by offset, so
// a chunk that dropped, reordered or trimmed a character would send a reader to
// text that is not the text the answer came from.
//
// Within each window the cut is placed as far right as it can be while still
// landing on a boundary — a paragraph break first, then the end of a sentence,
// and a hard cut only when neither is there. It is not a strict outline of the
// document: a paragraph shorter than what is left of the window is carried into
// the next chunk rather than becoming a chunk of its own. Filling the window is
// the point of having one — a one-line chunk per list item spends the model's
// attention on overhead and retrieves worse.
func Split(text string, chunking Chunking) ([]Chunk, error) {
	if err := chunking.Validate(); err != nil {
		return nil, err
	}
	if text == "" {
		return nil, nil
	}

	// Work in characters, not bytes: the offsets are handed to a citation and
	// shown to a reader, and a byte offset means nothing to either.
	runes := []rune(text)
	if chunking.Size >= len(runes) {
		return []Chunk{{
			Ordinal: 0, CharStart: 0, CharEnd: int64(len(runes)), Content: text,
		}}, nil
	}

	chunks := make([]Chunk, 0, len(runes)/chunking.Size+1)
	for start := 0; start < len(runes); {
		end := start + chunking.Size
		if end >= len(runes) {
			end = len(runes)
		} else {
			end = cutPoint(runes, start, end, chunking.Overlap)
		}
		chunks = append(chunks, Chunk{
			Ordinal:   len(chunks),
			CharStart: int64(start),
			CharEnd:   int64(end),
			Content:   string(runes[start:end]),
		})
		if end >= len(runes) {
			break
		}
		// The overlap is taken from the text, not from the chunk before: the next
		// chunk starts earlier in the document than this one ended, and nothing is
		// copied or held twice.
		start = end - chunking.Overlap
	}
	return chunks, nil
}

// cutPoint chooses where a chunk that starts at start ends, given that it may
// run to limit.
//
// The floor is start+overlap+1: any earlier and the next chunk would begin
// before this one did, so the text would not advance and the chunks would not
// tile the document.
func cutPoint(runes []rune, start, limit, overlap int) int {
	floor := start + overlap + 1
	if point := lastBreak(runes, floor, limit, isParagraphEnd); point > 0 {
		return point
	}
	if point := lastBreak(runes, floor, limit, endsSentence); point > 0 {
		return point
	}
	return limit
}

// lastBreak walks from limit back to floor and returns the last place the text
// breaks, or 0 when it does not break anywhere in that range.
func lastBreak(runes []rune, floor, limit int, breaks func([]rune, int) bool) int {
	for point := limit; point >= floor; point-- {
		if breaks(runes, point) {
			return point
		}
	}
	return 0
}

// isParagraphEnd reports whether the text up to point ends a paragraph, which is
// a blank line. The blank line stays with the chunk it ends, so the chunk after
// it begins at the first character of the next paragraph.
func isParagraphEnd(runes []rune, point int) bool {
	return point >= 2 && runes[point-1] == '\n' && runes[point-2] == '\n'
}

// endsSentence reports whether the text up to point ends a sentence.
//
// A comma is not here on purpose: it separates clauses, and cutting at one would
// put half a sentence at the top of the next chunk. Closing quotes and brackets
// belong to the sentence they close, and trailing spaces belong to neither.
func endsSentence(runes []rune, point int) bool {
	index := point - 1
	for index >= 0 && (isCloser(runes[index]) || runes[index] == ' ' || runes[index] == '\t') {
		index--
	}
	return index >= 0 && isSentenceEnd(runes[index])
}

// isSentenceEnd is the punctuation that ends a sentence in the languages this
// service reads, ASCII and CJK.
func isSentenceEnd(r rune) bool {
	switch r {
	case '。', '！', '？', '；', '…', '.', '!', '?', ';':
		return true
	}
	return false
}

func isCloser(r rune) bool {
	switch r {
	case '"', '\'', '”', '’', '」', '』', '）', '》', '】', ')', ']', '}':
		return true
	}
	return false
}

// ordinalDigits is how wide the ordinal is written into a chunk id.
//
// Padded so that a primary-key range scan over one document's chunks comes back
// in the order the text is in, rather than in the order a string comparison puts
// 10 and 2. Six digits is a thousand times the chunks a document at the
// configured character limit can have.
const ordinalDigits = 6

// ChunkID builds the identifier a chunk row is stored under.
//
// It is deterministic rather than random because of two findings recorded in the
// design document's deviation log: the primary key is the uniqueness that a
// UNIQUE (document_id, revision, ordinal) constraint used to provide, and this
// build's storage engine cannot index document_id at all on a table carrying a
// vector index — so a chunk id that begins with its document id is what leaves a
// primary-key range scan for reading one document's chunks.
func ChunkID(documentID string, revision, ordinal int) string {
	return ChunkIDPrefix(documentID, revision) + ordinalString(ordinal)
}

// ChunkIDPrefix is the part of a chunk id shared by every chunk of one revision
// of one document. It is what a prefix scan ranges over.
func ChunkIDPrefix(documentID string, revision int) string {
	return "chk_" + documentID + "_" + strconv.Itoa(revision) + "_"
}

func ordinalString(ordinal int) string {
	digits := strconv.Itoa(ordinal)
	if len(digits) >= ordinalDigits {
		return digits
	}
	return zeros[:ordinalDigits-len(digits)] + digits
}

const zeros = "000000"
