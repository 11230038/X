package kb

import (
	"fmt"
	"sort"
)

// Branch is one retrieval path's answer, best first.
//
// The name travels with the result because "why is this chunk here" is answered
// by which paths found it and where each of them put it — the same question the
// sources payload answers for a reader.
type Branch struct {
	Name     string
	ChunkIDs []string
}

// BranchRank is where one path placed a chunk: 1 is the best it had.
type BranchRank struct {
	Branch string
	Rank   int
}

// Fused is one chunk's place in the merged ranking.
type Fused struct {
	ChunkID string
	// Score is the fusion score described on Fuse. It orders the results; it is
	// not a similarity, and it is not comparable across queries.
	Score float64
	// Ranks is every path that found this chunk, in the order the branches were
	// given. A path that missed it is absent rather than ranked last.
	Ranks []BranchRank
}

// Fuse merges retrieved branches into one ranking by reciprocal rank fusion:
// every path that found a chunk contributes 1/(k + rank), and the chunks are
// ordered by what they collected.
//
// Ranks rather than scores, because the two paths' numbers are not on speaking
// terms: a cosine distance and a full-text relevance have no common scale, and
// any attempt to weight them against each other is a guess with a number's
// clothes on. Rank is the one thing both paths can honestly report.
//
// The formula degenerates instead of failing when a path has nothing to
// contribute. With one branch the scores are simply 1/(k + rank), so the order
// is that branch's order — which is why a document whose embeddings have not
// been computed yet is still retrievable, and why an embedding provider that is
// down degrades retrieval rather than breaking it.
//
// The same chunk found by both paths appears once: it is one span of text, and
// showing it twice would spend two of topK's places on it and take two citation
// numbers.
func Fuse(branches []Branch, k, topK int) ([]Fused, error) {
	if k < 0 {
		return nil, fmt.Errorf("kb: the reciprocal rank constant must not be negative")
	}
	if topK <= 0 {
		return nil, fmt.Errorf("kb: top k must be a positive number of chunks")
	}

	type scored struct {
		fused Fused
		best  int
	}
	byChunk := make(map[string]*scored)
	for _, branch := range branches {
		// A path that lists the same chunk twice is listing one span twice; the
		// better of the two ranks is the one that describes where it placed it.
		seen := make(map[string]bool, len(branch.ChunkIDs))
		for index, chunkID := range branch.ChunkIDs {
			if chunkID == "" || seen[chunkID] {
				continue
			}
			seen[chunkID] = true

			rank := index + 1
			entry, ok := byChunk[chunkID]
			if !ok {
				entry = &scored{fused: Fused{ChunkID: chunkID}, best: rank}
				byChunk[chunkID] = entry
			}
			entry.fused.Score += 1 / float64(k+rank)
			entry.fused.Ranks = append(entry.fused.Ranks, BranchRank{Branch: branch.Name, Rank: rank})
			if rank < entry.best {
				entry.best = rank
			}
		}
	}

	results := make([]scored, 0, len(byChunk))
	for _, entry := range byChunk {
		results = append(results, *entry)
	}
	// Ties are broken by the better rank and then by id, so that the same two
	// branches always produce the same list: a ranking that reshuffles itself
	// between two identical queries is a citation that moves under the reader.
	sort.Slice(results, func(i, j int) bool {
		if results[i].fused.Score != results[j].fused.Score {
			return results[i].fused.Score > results[j].fused.Score
		}
		if results[i].best != results[j].best {
			return results[i].best < results[j].best
		}
		return results[i].fused.ChunkID < results[j].fused.ChunkID
	})

	if len(results) > topK {
		results = results[:topK]
	}
	fused := make([]Fused, len(results))
	for index, entry := range results {
		fused[index] = entry.fused
	}
	return fused, nil
}
