package kb

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"backend/internal/event"
)

// Locate finds the parts of one document that answer a query.
//
// It exists because an attachment is material a model may only read through a
// tool. A document at the character limit is two hundred thousand characters
// and cannot go into a prompt, so the model asks a question and gets back the
// passages that answer it, numbered like any other source.
//
// The search stays inside this one document — there is no scope to leave, which
// is why an attachment cannot be searched for anything but itself. It runs in
// this process over the document's own chunks rather than through the
// full-text index, because that index spans the whole library and has no
// document filter to give it: nothing indexes document_id (see the schema
// notes). One document's chunks are a few hundred rows, read by primary-key
// range.
//
// The unit is the character bigram, the same one this build's full-text index
// tokenises with (ngram_token_size = 2), so an attachment and a library answer
// by the same rule. A chunk's score is how much of the question's bigram set it
// holds: a chunk with the question's characters scattered through it does not
// outrank the one that holds the phrase, and the number means something a
// reader can check — "two thirds of what you asked is in this passage".
func (s *Service) Locate(ctx context.Context, documentID, query string, limit int) ([]event.Source, error) {
	documentID = strings.TrimSpace(documentID)
	query = strings.TrimSpace(query)
	if utf8.RuneCountInString(query) < minQueryChars {
		return nil, fmt.Errorf("%w: it needs at least %d characters", ErrQueryTooShort, minQueryChars)
	}
	if limit <= 0 {
		limit = s.options.RetrievalTopK
	}
	document, err := s.repository.Document(ctx, documentID)
	if err != nil {
		return nil, err
	}
	// The active revision's chunks, which is what the document currently says.
	// An earlier revision is still on disk and is not the document.
	chunks, err := s.repository.Chunks(ctx, documentID, document.ActiveRevision)
	if err != nil {
		return nil, err
	}

	ranked := rankChunks(chunks, query, limit)
	sources := make([]event.Source, 0, len(ranked))
	for _, entry := range ranked {
		sources = append(sources, sourceOf(chunkSourceOf(entry.chunk, document), len(sources)+1, entry.score, 0))
	}
	return sources, nil
}

// scoredChunk is one chunk with the score that placed it.
type scoredChunk struct {
	chunk ChunkRecord
	// score is how much of the query's bigram set the chunk holds, from 0 to 1.
	score float64
}

// rankChunks orders a document's chunks by how much of the query they hold,
// best first, and returns at most limit of them.
//
// A query that matches nothing falls back to the document's opening chunks with
// a score of zero. Returning nothing would tell a model that asked about an
// attachment that the attachment holds nothing, which is a much stronger claim
// than "these words are not in it" — and the material it was given is the
// document, whose beginning is where a reader would start anyway.
func rankChunks(chunks []ChunkRecord, query string, limit int) []scoredChunk {
	if limit <= 0 {
		return nil
	}
	bigrams := queryBigrams(query)
	scored := make([]scoredChunk, 0, len(chunks))
	if len(bigrams) > 0 {
		for _, chunk := range chunks {
			held := 0
			lowered := strings.ToLower(chunk.Content)
			for bigram := range bigrams {
				if strings.Contains(lowered, bigram) {
					held++
				}
			}
			if held == 0 {
				continue
			}
			scored = append(scored, scoredChunk{
				chunk: chunk,
				score: float64(held) / float64(len(bigrams)),
			})
		}
	}
	if len(scored) == 0 {
		return leadingChunks(chunks, limit)
	}
	// Ties go to the earlier chunk, so two identical questions about one
	// document return the same passages in the same order.
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].chunk.Ordinal < scored[j].chunk.Ordinal
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}
	return scored
}

func leadingChunks(chunks []ChunkRecord, limit int) []scoredChunk {
	if len(chunks) < limit {
		limit = len(chunks)
	}
	leading := make([]scoredChunk, 0, limit)
	for _, chunk := range chunks[:limit] {
		leading = append(leading, scoredChunk{chunk: chunk})
	}
	return leading
}

// queryBigrams reduces a query to the set of character pairs it asks about.
//
// Spaces are dropped rather than kept: they separate the words of a question,
// and the text that answers it does not have to separate them at the same
// points. Case is folded for the same reason — a reader asking about "closure"
// means the document's "Closure".
func queryBigrams(query string) map[string]bool {
	letters := make([]rune, 0, len(query))
	for _, r := range strings.ToLower(query) {
		if unicode.IsSpace(r) {
			continue
		}
		letters = append(letters, r)
	}
	bigrams := make(map[string]bool, len(letters))
	for index := 0; index+1 < len(letters); index++ {
		bigrams[string(letters[index:index+2])] = true
	}
	return bigrams
}

// chunkSourceOf joins a chunk to its document, which is cheap here because the
// document was already read: the two are apart in storage only because a chunk
// carries what retrieval filters on, not because a caller ever wants one
// without the other.
func chunkSourceOf(chunk ChunkRecord, document Document) ChunkSource {
	return ChunkSource{
		ChunkID: chunk.ChunkID, DocumentID: chunk.DocumentID, KBID: chunk.KBID,
		FileID: document.FileID, Filename: document.Filename, Engine: document.Engine,
		Revision: chunk.Revision, Ordinal: chunk.Ordinal, Content: chunk.Content,
		CharStart: chunk.CharStart, CharEnd: chunk.CharEnd, Page: chunk.Page,
	}
}
