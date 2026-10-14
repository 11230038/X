package kb

// The fake storage adapter, for the half of it that is about documents: this
// file answers what a document is, where a file is parsed, and how a revision
// is written — the same split the real adapter has, and for the same reason,
// which is that a document and a chunk are two tables that are read together
// but written for different reasons.

import (
	"context"

	"backend/internal/authz"
	"fmt"
	"sort"
)

func (f *fakeRepository) Document(_ context.Context, documentID string, caller authz.Caller) (Document, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("Document"); err != nil {
		return Document{}, err
	}
	document, ok := f.documents[documentID]
	if !ok {
		return Document{}, fmt.Errorf("%w: document %s", ErrNotFound, documentID)
	}
	return document, nil
}

func (f *fakeRepository) DocumentForFile(_ context.Context, fileID, kbID string, caller authz.Caller) (Document, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("DocumentForFile"); err != nil {
		return Document{}, err
	}
	for _, document := range f.documents {
		if document.FileID == fileID && document.KBID == kbID {
			return document, nil
		}
	}
	return Document{}, fmt.Errorf("%w: file %s", ErrNotFound, fileID)
}

func (f *fakeRepository) DocumentOfFile(_ context.Context, fileID string, caller authz.Caller) (Document, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("DocumentOfFile"); err != nil {
		return Document{}, err
	}
	// The attachment first, then the oldest document of any base, in the order
	// the query would return them.
	var found Document
	for _, document := range f.documents {
		if document.FileID == fileID && (found.ID == "" || aheadOf(document, found)) {
			found = document
		}
	}
	if found.ID == "" {
		return Document{}, fmt.Errorf("%w: file %s is not parsed anywhere", ErrNotFound, fileID)
	}
	return found, nil
}

// aheadOf is the fake's copy of DocumentOfFile's ordering: an attachment before
// a document inside a base, and the older of two of the same kind. The id breaks
// a tie because the query does — two documents created in the same instant would
// otherwise be answered in map order, which is not an order at all.
func aheadOf(candidate, current Document) bool {
	if (candidate.KBID == "") != (current.KBID == "") {
		return candidate.KBID == ""
	}
	if !candidate.CreatedAt.Equal(current.CreatedAt) {
		return candidate.CreatedAt.Before(current.CreatedAt)
	}
	return candidate.ID < current.ID
}

func (f *fakeRepository) Documents(_ context.Context, kbID string, caller authz.Caller) ([]Document, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("Documents"); err != nil {
		return nil, err
	}
	documents := make([]Document, 0, len(f.documents))
	for _, document := range f.documents {
		if document.KBID == kbID {
			documents = append(documents, document)
		}
	}
	sort.Slice(documents, func(i, j int) bool {
		return documents[i].CreatedAt.Before(documents[j].CreatedAt)
	})
	return documents, nil
}

func (f *fakeRepository) CreateDocument(_ context.Context, write DocumentWrite) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("CreateDocument"); err != nil {
		return err
	}
	for _, document := range f.documents {
		if document.FileID == write.Document.FileID && document.KBID == write.Document.KBID {
			return fmt.Errorf("%w: file %s is already parsed here", ErrConflict, write.Document.FileID)
		}
	}
	f.documents[write.Document.ID] = write.Document
	f.texts[write.Document.ID] = write.Text
	f.order = append(f.order, write.Document.ID)
	f.writeChunksLocked(write)
	return nil
}

func (f *fakeRepository) ReplaceRevision(_ context.Context, write DocumentWrite, fromRevision int, caller authz.Caller) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ReplaceRevision"); err != nil {
		return err
	}
	document, ok := f.documents[write.Document.ID]
	if !ok {
		return fmt.Errorf("%w: document %s", ErrNotFound, write.Document.ID)
	}
	if document.ActiveRevision != fromRevision {
		return fmt.Errorf("%w: document %s is not at revision %d",
			ErrConflict, write.Document.ID, fromRevision)
	}
	f.switches++
	f.documents[write.Document.ID] = write.Document
	f.texts[write.Document.ID] = write.Text
	f.writeChunksLocked(write)
	return nil
}

// writeChunksLocked appends a revision's chunks and points the document's
// chunks at the revision the document now says is active. The caller holds the
// lock, which is how the two writes are one step here exactly as they are one
// transaction in storage.
func (f *fakeRepository) writeChunksLocked(write DocumentWrite) {
	stored := f.chunks[write.Document.ID]
	for index := range stored {
		stored[index].active = false
	}
	for _, chunk := range write.Chunks {
		stored = append(stored, storedChunk{
			record: chunk, active: chunk.Revision == write.Document.ActiveRevision,
			embedding: chunk.Embedding,
		})
	}
	f.chunks[write.Document.ID] = stored
}

func (f *fakeRepository) DeleteDocument(_ context.Context, documentID string, caller authz.Caller) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("DeleteDocument"); err != nil {
		return err
	}
	if _, ok := f.documents[documentID]; !ok {
		return fmt.Errorf("%w: document %s", ErrNotFound, documentID)
	}
	delete(f.documents, documentID)
	delete(f.texts, documentID)
	delete(f.chunks, documentID)
	return nil
}
