package kb

// The index worker's half of the stand-in: the claim, the queue read, and where
// a released claim leaves the document.
//
// What is modelled here, rather than in the real adapter, is exactly what the
// worker leans on: a claim is exclusive, a document that belongs to no base is
// never claimed, a document the provider refused is not offered again, and a
// write by a worker that no longer holds the claim is refused.

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// claim is one worker's hold on one document.
type claim struct {
	owner     string
	claimedAt time.Time
}

func (f *fakeRepository) ClaimDocument(_ context.Context, owner string, lease time.Duration, now time.Time) (Document, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ClaimDocument"); err != nil {
		return Document{}, err
	}
	cutoff := now.Add(-lease)
	for _, documentID := range f.order {
		document, ok := f.documents[documentID]
		if !ok || !f.claimableLocked(documentID, document, cutoff) {
			continue
		}
		document.VectorState = VectorStateRunning
		document.VectorError = ""
		f.documents[documentID] = document
		f.holds[documentID] = claim{owner: owner, claimedAt: now}
		return document, nil
	}
	return Document{}, fmt.Errorf("%w: no document is waiting to be indexed", ErrNotFound)
}

// claimableLocked is the stand-in's version of the WHERE clause: a document in a
// base that is either waiting or left claimed by a worker that has gone quiet.
func (f *fakeRepository) claimableLocked(documentID string, document Document, cutoff time.Time) bool {
	if document.KBID == "" {
		return false
	}
	switch document.VectorState {
	case VectorStatePending:
		return true
	case VectorStateRunning:
		held, ok := f.holds[documentID]
		return !ok || held.claimedAt.Before(cutoff)
	default:
		// ready and failed are both terminal: the first has nothing left to do
		// and the second needs a rebuild, not another attempt.
		return false
	}
}

func (f *fakeRepository) PendingChunks(_ context.Context, documentID string, revision int) ([]ChunkRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("PendingChunks"); err != nil {
		return nil, err
	}
	chunks := make([]ChunkRecord, 0, len(f.chunks[documentID]))
	for _, stored := range f.chunks[documentID] {
		if stored.record.Revision == revision && stored.embedding == "" {
			chunks = append(chunks, stored.record)
		}
	}
	sort.Slice(chunks, func(i, j int) bool { return chunks[i].ChunkID < chunks[j].ChunkID })
	return chunks, nil
}

// SaveEmbeddings stores vectors against chunk ids, the way the real statement
// does: the chunk is found by its id, and an id that is not there is an error
// rather than a quietly dropped vector.
func (f *fakeRepository) SaveEmbeddings(_ context.Context, chunks []ChunkRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("SaveEmbeddings"); err != nil {
		return err
	}
	for _, chunk := range chunks {
		location, index, ok := f.findChunkLocked(chunk.ChunkID)
		if !ok {
			return fmt.Errorf("%w: chunk %s", ErrNotFound, chunk.ChunkID)
		}
		location.chunks[index].embedding = chunk.Embedding
		f.chunks[location.documentID] = location.chunks
	}
	return nil
}

// chunkLocation is where a chunk id was found.
type chunkLocation struct {
	documentID string
	chunks     []storedChunk
}

func (f *fakeRepository) findChunkLocked(chunkID string) (chunkLocation, int, bool) {
	for documentID, chunks := range f.chunks {
		for index, stored := range chunks {
			if stored.record.ChunkID == chunkID {
				return chunkLocation{documentID: documentID, chunks: chunks}, index, true
			}
		}
	}
	return chunkLocation{}, 0, false
}

func (f *fakeRepository) CompleteDocument(_ context.Context, documentID, owner string) error {
	return f.release("CompleteDocument", documentID, owner, VectorStateReady, "")
}

func (f *fakeRepository) FailDocument(_ context.Context, documentID, owner, message string) error {
	return f.release("FailDocument", documentID, owner, VectorStateFailed, message)
}

func (f *fakeRepository) RetryDocument(_ context.Context, documentID, owner, message string) error {
	return f.release("RetryDocument", documentID, owner, VectorStatePending, message)
}

// release ends a claim, refusing a worker that no longer holds it — the same
// guard the real statement carries in its WHERE clause.
func (f *fakeRepository) release(method, documentID, owner, state, message string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(method); err != nil {
		return err
	}
	held, ok := f.holds[documentID]
	if !ok || held.owner != owner {
		return fmt.Errorf("%w: document %s is no longer claimed by this worker", ErrConflict, documentID)
	}
	delete(f.holds, documentID)
	document := f.documents[documentID]
	document.VectorState = state
	document.VectorError = message
	f.documents[documentID] = document
	return nil
}

// embeddingOf reads one chunk's vector, empty when there is none.
func (f *fakeRepository) embeddingOf(chunkID string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	location, index, ok := f.findChunkLocked(chunkID)
	if !ok {
		return ""
	}
	return location.chunks[index].embedding
}

// embeddingsOf lists a document's active chunks with the vector each one has,
// empty where the worker has not been there yet.
func (f *fakeRepository) embeddingsOf(documentID string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	stored := f.chunks[documentID]
	vectors := make([]string, 0, len(stored))
	for _, chunk := range stored {
		if chunk.active {
			vectors = append(vectors, chunk.embedding)
		}
	}
	sort.Slice(vectors, func(i, j int) bool { return vectors[i] < vectors[j] })
	return vectors
}
