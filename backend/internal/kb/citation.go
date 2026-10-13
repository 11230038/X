package kb

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"backend/internal/event"
)

// The two kinds of locator this package produces. A chunk is a span that was
// retrieved from a library; a window is a range that was read out of one
// document. They are distinguishable afterwards because they are opened
// differently, and a reader should be able to tell which one a citation is.
const (
	locatorChunk  = "chunk"
	locatorWindow = "window"
)

// citationPattern is a numbered reference in an answer.
//
// Up to two digits: a list of sources is never a hundred long, so a longer
// number in brackets is a year, an array index or a footnote, and reading one
// as a citation would report a reference that was never made.
var citationPattern = regexp.MustCompile(`\[([0-9]{1,2})\]`)

// Citations is what the numbered references in an answer amount to.
type Citations struct {
	// Cited is every number the answer used, in the order they first appear,
	// each once.
	Cited []int
	// Unresolved is the cited numbers that no returned source has. They are
	// recorded so a reader can be told the answer pointed at something it was
	// not given, and nothing else happens to them: no source is invented to
	// make the number add up, and the answer is not edited. It is the model's
	// own words, and deleting [7] from them is rewriting them.
	Unresolved []int
}

// ParseCitations reads an answer's references against the sources it was given.
//
// Full-width brackets are read as the same thing as ASCII ones, because an
// answer written in Chinese uses them and a citation that resolves only in one
// script would be a citation that half the time does not exist.
func ParseCitations(answer string, sourceCount int) Citations {
	scanned := strings.NewReplacer("【", "[", "】", "]").Replace(answer)
	seen := make(map[int]bool)
	var citations Citations
	for _, match := range citationPattern.FindAllStringSubmatch(scanned, -1) {
		number, err := strconv.Atoi(match[1])
		if err != nil || seen[number] {
			continue
		}
		seen[number] = true
		citations.Cited = append(citations.Cited, number)
		if number < 1 || number > sourceCount {
			citations.Unresolved = append(citations.Unresolved, number)
		}
	}
	return citations
}

// sourceOf describes one stored chunk as a numbered source, and it is the only
// place that does.
//
// Every field comes out of the row: the page, the character span, the chunk id
// and the revision are this service's own bookkeeping, and none of them ever
// passes through the model. That is what makes a citation's coordinates
// impossible to invent — the model is handed the numbers and writes numbers
// (AIQ-02). A page it cannot have seen is a page it cannot get wrong.
func sourceOf(chunk ChunkSource, index int, score float64, snippetChars int) event.Source {
	return event.Source{
		Index:      index,
		DocumentID: chunk.DocumentID,
		FileID:     chunk.FileID,
		Title:      chunk.Filename,
		Engine:     chunk.Engine,
		Locator: event.Locator{
			Kind: locatorChunk, ChunkID: chunk.ChunkID, Ordinal: chunk.Ordinal,
			CharStart: chunk.CharStart, CharEnd: chunk.CharEnd, Page: chunk.Page,
		},
		Snippet:  snippet(chunk.Content, snippetChars),
		Score:    score,
		Revision: chunk.Revision,
	}
}

// Source describes a window as a numbered source. It is how read_source hands
// an attachment's passage to a model: the locator is the window's own span and
// says so, so a reader can tell a passage that was read from a chunk that was
// retrieved.
func (w Window) Source(index, snippetChars int) event.Source {
	return event.Source{
		Index:      index,
		DocumentID: w.Document.ID,
		FileID:     w.Document.FileID,
		Title:      w.Document.Filename,
		Engine:     w.Document.Engine,
		Locator: event.Locator{
			Kind: locatorWindow, CharStart: w.CharStart, CharEnd: w.CharEnd,
		},
		Snippet:  snippet(w.Text, snippetChars),
		Revision: w.Document.ActiveRevision,
	}
}

// snippet cuts text to at most maxChars characters, on a character boundary,
// with an ellipsis saying that it was cut. Zero or less returns it whole.
//
// The ellipsis is one of the characters: maxChars is a cap, and a cap that
// returns its own limit plus one is not one.
func snippet(text string, maxChars int) string {
	if maxChars <= 0 || utf8.RuneCountInString(text) <= maxChars {
		return text
	}
	if maxChars == 1 {
		return "…"
	}
	cut := strings.TrimRight(string([]rune(text)[:maxChars-1]), " \t\n\r")
	return cut + "…"
}
