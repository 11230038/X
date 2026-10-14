package reading

import (
	"context"
	"fmt"
	"strings"
)

// Selection is the passage a reader had chosen when a turn started (§2.8).
//
// It is not stored, and that is the design rather than an omission: a selection
// is browser state, and one saved would show up in the second tab as somebody
// else's. What survives a tab is the *position* (reading_positions) and what the
// reader wrote (reading_marks); what they had highlighted while asking a
// question is part of that question.
//
// Text is what the client says it had selected, and the server does not trust
// it: the passage a model is given is the one the coordinates resolve to, read
// out of the document this service already has. A client that sends text the
// locator does not cover — a stale tab, a document re-parsed underneath it — is
// answered about the text that is actually there.
type Selection struct {
	MaterialID string
	Locator    Locator
	Text       string
}

// Validate checks a selection before anything is read.
func (s Selection) Validate() error {
	if strings.TrimSpace(s.MaterialID) == "" {
		return fmt.Errorf("%w: the selection names no material", ErrInvalidRequest)
	}
	if err := s.Locator.Validate(); err != nil {
		return err
	}
	// The material is said twice, and a selection that says it differently is one
	// selection about two materials.
	if s.Locator.MaterialID != s.MaterialID {
		return fmt.Errorf("%w: the selection's locator belongs to another material", ErrInvalidRequest)
	}
	return nil
}

// Passage is the text a locator names, with the coordinates that were actually
// read.
type Passage struct {
	MaterialID string
	Locator    Locator
	Text       string
}

// Resolve reads the text one coordinate names.
//
// It reads through the port rather than through anything of its own, which is
// what keeps the reading layer from becoming a second way to read a document:
// the same extractor, the same chunker, the same offsets (§2.8).
//
// Two rules are worth stating because they are choices:
//
//   - **the span is the coordinate and the ordinal is a hint.** A locator whose
//     ordinal disagrees with its character offsets resolves by the offsets —
//     they are what the text is read by, and the ordinal only says which unit
//     the client thought it was in. (§7.1 asks for one of the two, and pins it
//     with a case.)
//   - **a span past the end of the document is refused rather than clamped.**
//     Storage clamps, because opening a citation whose document was replaced
//     should not fail; a locator arriving at this layer is a client asking for a
//     passage, and a client asking for characters the document does not have has
//     a stale view of it. Answering about a shorter span than the one asked for
//     would hide that.
func (s *Service) Resolve(ctx context.Context, materialID string, locator Locator) (Passage, error) {
	materialID = strings.TrimSpace(materialID)
	if materialID == "" {
		return Passage{}, fmt.Errorf("%w: no material was named", ErrInvalidRequest)
	}
	if err := locator.Validate(); err != nil {
		return Passage{}, err
	}
	if locator.MaterialID != materialID {
		return Passage{}, fmt.Errorf("%w: the locator belongs to another material", ErrInvalidRequest)
	}
	if locator.CharEnd == locator.CharStart {
		// A zero-width locator is a place, which is exactly what a saved position
		// is (Locator's own comment), and a place is not a passage: there is no
		// text to hand a model and none to show a panel. The two are refused
		// differently rather than by one rule, because the reader's next move
		// differs: one is "select something", the other is "this is where I am".
		return Passage{}, fmt.Errorf("%w: the locator names no text", ErrInvalidRequest)
	}
	material, err := s.repository.Material(ctx, materialID)
	if err != nil {
		return Passage{}, err
	}
	if material.ContentID == "" {
		return Passage{}, fmt.Errorf("%w: this material has no text behind it", ErrInvalidRequest)
	}
	document, err := s.documents.Document(ctx, material.ContentID)
	if err != nil {
		return Passage{}, err
	}
	if locator.CharEnd > document.TextChars {
		return Passage{}, fmt.Errorf("%w: the locator ends at %d, past the %d characters this material has",
			ErrInvalidRequest, locator.CharEnd, document.TextChars)
	}
	if locator.CharEnd-locator.CharStart > int64(s.maxPassageChars) {
		return Passage{}, fmt.Errorf("%w: the locator spans %d characters, more than the %d one passage may hold",
			ErrInvalidRequest, locator.CharEnd-locator.CharStart, s.maxPassageChars)
	}
	text, err := s.documents.Text(ctx, material.ContentID, locator.CharStart, locator.CharEnd)
	if err != nil {
		return Passage{}, err
	}
	return Passage{MaterialID: materialID, Locator: locator, Text: text}, nil
}

// unitsOf cuts a document's chunks into the units a catalogue lists.
//
// A unit is a run of the document's own chunks rather than a second segmentation
// of the text: the chunk boundaries are where V2 already decided a passage ends,
// and cutting anywhere else would be a second opinion about the same text with
// nothing to settle the disagreement. What this adds is the merge, because a
// chunk is sized for retrieval and a unit is sized for reading — what a reader
// scrolls through and what one extension is handed.
//
// A single chunk wider than the bound stays a unit of its own. Splitting it
// would invent an offset in the middle of a passage that V2 chose as one, and
// the bound is a preference about size rather than a rule the text has to obey.
func unitsOf(spans []ChunkSpan, maxChars int) []Unit {
	units := make([]Unit, 0, len(spans))
	var (
		current     ChunkSpan
		have        bool
		pageKnown   bool
		currentPage int
	)
	for _, span := range spans {
		if !have {
			current, have = span, true
			pageKnown, currentPage = span.Page != nil, pageOf(span.Page)
			continue
		}
		if span.CharEnd-current.CharStart > int64(maxChars) {
			units = append(units, unitOf(current, len(units), pageKnown, currentPage))
			current, pageKnown, currentPage = span, span.Page != nil, pageOf(span.Page)
			continue
		}
		current.CharEnd = span.CharEnd
		// A page is carried only while every chunk in the unit agrees on one.
		// A unit that straddles a page break has no single page, and a page
		// number invented for it is exactly the guess the citation rules forbid.
		if !pageKnown || span.Page == nil || *span.Page != currentPage {
			pageKnown = false
		}
	}
	if have {
		units = append(units, unitOf(current, len(units), pageKnown, currentPage))
	}
	return units
}

func unitOf(span ChunkSpan, ordinal int, pageKnown bool, page int) Unit {
	unit := Unit{Ordinal: ordinal, CharStart: span.CharStart, CharEnd: span.CharEnd}
	if pageKnown {
		known := page
		unit.Page = &known
	}
	return unit
}

func pageOf(page *int) int {
	if page == nil {
		return 0
	}
	return *page
}
