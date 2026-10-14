package kb

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"backend/internal/authz"
	"backend/internal/embed"
)

// fakeRepository is an in-memory stand-in for the three tables.
//
// It is written to behave like the storage adapter in the ways this package
// depends on, and those are exactly the ways a test would otherwise have to
// reach a database to check: a chunk is active only for the revision its
// document points at, writing a revision whose predecessor has moved fails, one
// base is the default, and the same file is one document per collection. What
// it deliberately does not model is what the SQL is shaped for — which index
// the planner picks — because that is a property of the database and not of
// this code, and it is checked against a real one rather than here.
type fakeRepository struct {
	mu        sync.Mutex
	bases     map[string]KnowledgeBase
	documents map[string]Document
	texts     map[string]string
	chunks    map[string][]storedChunk
	order     []string
	// errors injects a failure per method name, for the paths where a storage
	// error has to reach the caller unchanged.
	errors map[string]error

	// vectorHits and textHits are what each retrieval path finds, best first.
	// Tests drive them directly: ranking is the database's job, and what this
	// package does with two ranked lists is what is under test.
	vectorHits []string
	textHits   []string
	// searched records the scope the last search was given.
	searched []string
	// switches counts the revision flips, so a test can tell a rebuild that
	// happened from one that was only attempted.
	switches int
	// holds is who has claimed which document for indexing, which is the part of
	// the claim the document row itself does not carry here.
	holds map[string]claim
}

// storedChunk is a chunk row: the chunk, whether its revision is the active
// one, and the vector column, which is empty until the index worker fills it.
type storedChunk struct {
	record    ChunkRecord
	active    bool
	embedding string
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{
		bases: map[string]KnowledgeBase{}, documents: map[string]Document{},
		texts: map[string]string{}, chunks: map[string][]storedChunk{},
		holds: map[string]claim{},
	}
}

func (f *fakeRepository) fail(method string) error {
	if f.errors == nil {
		return nil
	}
	return f.errors[method]
}

func (f *fakeRepository) CreateKnowledgeBase(_ context.Context, record KnowledgeBase) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("CreateKnowledgeBase"); err != nil {
		return err
	}
	if _, ok := f.bases[record.ID]; ok {
		return fmt.Errorf("%w: knowledge base %s", ErrConflict, record.ID)
	}
	isDefault := true
	for _, base := range f.bases {
		if base.IsDefault {
			isDefault = false
			break
		}
	}
	record.IsDefault = isDefault
	f.bases[record.ID] = record
	return nil
}

// visible mirrors what OwnerScope does in storage for the one row this fake
// models an owner on. It is a means and not evidence: what the predicate does to
// a real table is the contract tests' business, and this is a copy that can
// drift. The document side has no copy at all — a document has no owner column,
// its scope is derived from the collection and the file it came from, and that
// is a join this fake deliberately does not run. Cross-account document reads
// are pinned against a real database instead.
func (f *fakeRepository) visibleBase(record KnowledgeBase, caller authz.Caller) bool {
	if caller.UserID > 0 && record.OwnerID == caller.UserID {
		return true
	}
	// A base that belongs to nobody is the administrator's, and nobody else's.
	return caller.IsAdmin() && record.OwnerID == 0
}

func (f *fakeRepository) KnowledgeBase(_ context.Context, kbID string, caller authz.Caller) (KnowledgeBase, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("KnowledgeBase"); err != nil {
		return KnowledgeBase{}, err
	}
	base, ok := f.bases[kbID]
	if !ok || !f.visibleBase(base, caller) {
		return KnowledgeBase{}, fmt.Errorf("%w: knowledge base %s", ErrNotFound, kbID)
	}
	base.Documents, base.ReadyDocuments = 0, 0
	for _, document := range f.documents {
		if document.KBID != kbID {
			continue
		}
		base.Documents++
		if document.VectorState == VectorStateReady {
			base.ReadyDocuments++
		}
	}
	return base, nil
}

func (f *fakeRepository) KnowledgeBases(_ context.Context, caller authz.Caller) ([]KnowledgeBase, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("KnowledgeBases"); err != nil {
		return nil, err
	}
	bases := make([]KnowledgeBase, 0, len(f.bases))
	for _, base := range f.bases {
		if f.visibleBase(base, caller) {
			bases = append(bases, base)
		}
	}
	sort.Slice(bases, func(i, j int) bool {
		if bases[i].IsDefault != bases[j].IsDefault {
			return bases[i].IsDefault
		}
		return bases[i].CreatedAt.Before(bases[j].CreatedAt)
	})
	return bases, nil
}

func (f *fakeRepository) KnowledgeBaseCounts(_ context.Context) (map[string]Counts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	counts := map[string]Counts{}
	for _, document := range f.documents {
		if document.KBID == "" {
			continue
		}
		counted := counts[document.KBID]
		counted.Documents++
		if document.VectorState == VectorStateReady {
			counted.Ready++
		}
		counts[document.KBID] = counted
	}
	return counts, nil
}

func (f *fakeRepository) SetDefaultKnowledgeBase(_ context.Context, kbID string, caller authz.Caller) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("SetDefaultKnowledgeBase"); err != nil {
		return err
	}
	base, ok := f.bases[kbID]
	if !ok || !f.visibleBase(base, caller) {
		return fmt.Errorf("%w: knowledge base %s", ErrNotFound, kbID)
	}
	// The mark is instance-wide here as it is in storage, where the unique index
	// has no room for an account. The limitation is recorded rather than modelled
	// away in the fake, because a fake that hid it would hide the reason the
	// deviation is written down.
	for id, other := range f.bases {
		other.IsDefault = id == kbID
		f.bases[id] = other
	}
	return nil
}

func (f *fakeRepository) DeleteKnowledgeBase(_ context.Context, kbID string, caller authz.Caller) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("DeleteKnowledgeBase"); err != nil {
		return err
	}
	base, ok := f.bases[kbID]
	if !ok || !f.visibleBase(base, caller) {
		return fmt.Errorf("%w: knowledge base %s", ErrNotFound, kbID)
	}
	delete(f.bases, kbID)
	// The cascade the foreign keys do: documents, then their chunks.
	for id, document := range f.documents {
		if document.KBID != kbID {
			continue
		}
		delete(f.documents, id)
		delete(f.texts, id)
		delete(f.chunks, id)
	}
	if !base.IsDefault {
		return nil
	}
	oldest := ""
	for id := range f.bases {
		if oldest == "" || f.bases[id].CreatedAt.Before(f.bases[oldest].CreatedAt) {
			oldest = id
		}
	}
	if oldest != "" {
		base := f.bases[oldest]
		base.IsDefault = true
		f.bases[oldest] = base
	}
	return nil
}

func (f *fakeRepository) Chunks(_ context.Context, documentID string, revision int) ([]ChunkRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("Chunks"); err != nil {
		return nil, err
	}
	chunks := make([]ChunkRecord, 0, len(f.chunks[documentID]))
	for _, stored := range f.chunks[documentID] {
		if stored.record.Revision == revision {
			chunks = append(chunks, stored.record)
		}
	}
	sort.Slice(chunks, func(i, j int) bool { return chunks[i].ChunkID < chunks[j].ChunkID })
	return chunks, nil
}

func (f *fakeRepository) ChunkSpans(_ context.Context, documentID string, revision int) ([]ChunkSpan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ChunkSpans"); err != nil {
		return nil, err
	}
	spans := make([]ChunkSpan, 0, len(f.chunks[documentID]))
	for _, stored := range f.chunks[documentID] {
		if stored.record.Revision != revision {
			continue
		}
		spans = append(spans, ChunkSpan{
			ChunkID: stored.record.ChunkID, Ordinal: stored.record.Ordinal,
			CharStart: stored.record.CharStart, CharEnd: stored.record.CharEnd,
			Page: stored.record.Page,
		})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].ChunkID < spans[j].ChunkID })
	return spans, nil
}

func (f *fakeRepository) ChunkSources(_ context.Context, chunkIDs []string) ([]ChunkSource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ChunkSources"); err != nil {
		return nil, err
	}
	wanted := make(map[string]bool, len(chunkIDs))
	for _, chunkID := range chunkIDs {
		wanted[chunkID] = true
	}
	sources := make([]ChunkSource, 0, len(chunkIDs))
	for documentID, stored := range f.chunks {
		document := f.documents[documentID]
		for _, chunk := range stored {
			if !chunk.active || !wanted[chunk.record.ChunkID] {
				continue
			}
			sources = append(sources, chunkSourceOf(chunk.record, document))
		}
	}
	return sources, nil
}

func (f *fakeRepository) DocumentText(_ context.Context, documentID string, offset, length int64) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("DocumentText"); err != nil {
		return "", err
	}
	runes := []rune(f.texts[documentID])
	if offset >= int64(len(runes)) {
		return "", nil
	}
	end := offset + length
	if end > int64(len(runes)) {
		end = int64(len(runes))
	}
	return string(runes[offset:end]), nil
}

func (f *fakeRepository) SearchVector(_ context.Context, kbIDs []string, _ embed.Vector, limit int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("SearchVector"); err != nil {
		return nil, err
	}
	f.searched = append([]string(nil), kbIDs...)
	return f.visible(f.vectorHits, kbIDs, limit, true), nil
}

func (f *fakeRepository) SearchFullText(_ context.Context, kbIDs []string, _ string, limit int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("SearchFullText"); err != nil {
		return nil, err
	}
	f.searched = append([]string(nil), kbIDs...)
	return f.visible(f.textHits, kbIDs, limit, false), nil
}

// visible is the part of a branch's answer that is actually there: the chunks
// of those bases that are active and, for the vector path, embedded. A branch
// that lists something the storage no longer serves is the database's business
// to filter, and the stand-in filters it the same way.
func (f *fakeRepository) visible(hits, kbIDs []string, limit int, needsVector bool) []string {
	inScope := make(map[string]bool, len(kbIDs))
	for _, kbID := range kbIDs {
		inScope[kbID] = true
	}
	ids := make([]string, 0, len(hits))
	for _, hit := range hits {
		for documentID, stored := range f.chunks {
			document := f.documents[documentID]
			for _, chunk := range stored {
				if chunk.record.ChunkID != hit || !chunk.active || !inScope[document.KBID] {
					continue
				}
				if needsVector && chunk.embedding == "" {
					continue
				}
				ids = append(ids, hit)
			}
		}
	}
	if len(ids) > limit {
		ids = ids[:limit]
	}
	return ids
}

// embedChunks marks a document's active chunks as embedded, which is how a test
// says "the index worker has caught up with this one".
func (f *fakeRepository) embedChunks(documentID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	stored := f.chunks[documentID]
	for index := range stored {
		if stored[index].active {
			stored[index].embedding = "[1,0]"
		}
	}
	f.chunks[documentID] = stored
	document := f.documents[documentID]
	document.VectorState = VectorStateReady
	f.documents[documentID] = document
}

func (f *fakeRepository) setError(method string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.errors == nil {
		f.errors = map[string]error{}
	}
	f.errors[method] = err
}

func (f *fakeRepository) documentCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.documents)
}

func (f *fakeRepository) activeChunks(documentID string) []ChunkRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	chunks := make([]ChunkRecord, 0, len(f.chunks[documentID]))
	for _, stored := range f.chunks[documentID] {
		if stored.active {
			chunks = append(chunks, stored.record)
		}
	}
	sort.Slice(chunks, func(i, j int) bool { return chunks[i].ChunkID < chunks[j].ChunkID })
	return chunks
}

func baseRecord(name string, createdAt time.Time) KnowledgeBase {
	return KnowledgeBase{
		ID: "kb_" + name, Name: name, Provider: ProviderNative,
		EmbeddingModel: "test-embed", CreatedAt: createdAt, UpdatedAt: createdAt,
	}
}
