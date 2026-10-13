package kb

// The index worker's half of the storage adapter: the claim, the queue read, the
// vectors going in, and the three ways a claim is given back. The SQL shapes
// worth knowing about are explained on GORMRepository in repository.go.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"backend/internal/data/models"
	"gorm.io/gorm"
)

// claimAttempts bounds how many times a worker re-reads the candidate list after
// losing a claim to another worker. Every loss means somebody else took the row
// this worker would have taken, so a handful of rounds is enough to find one
// that is still free; giving up after them costs one poll interval, not a
// document.
const claimAttempts = 4

// claimable is the condition a document must meet to be claimed: it belongs to a
// knowledge base — an attachment is never embedded, and that rule lives here
// rather than in a state that would have to mean two things (§0.3 第 4 条 f) —
// and it is either waiting, or left claimed by a worker that has since gone
// quiet for longer than the lease.
const claimable = `kb_id IS NOT NULL AND (vector_state = ? OR (vector_state = ? AND vector_claimed_at < ?))`

// ClaimDocument takes one document for one owner.
func (r *GORMRepository) ClaimDocument(ctx context.Context, owner string, lease time.Duration, now time.Time) (Document, error) {
	if err := r.ready(); err != nil {
		return Document{}, err
	}
	stale := now.Add(-lease)
	db := r.db.WithContext(ctx)
	for attempt := 0; attempt < claimAttempts; attempt++ {
		// Oldest first, which is the order a user expects their uploads to
		// become searchable in. The filter is what idx_documents_vector_state
		// exists for and the planner uses it — the scan is over that index's
		// (pending, …) and (running, …) ranges, which a document that is ready or
		// failed is outside of — so the ordering is a top-n sort of a handful of
		// rows and creat_time needs no index of its own.
		var candidate models.Document
		err := db.Where(claimable, VectorStatePending, VectorStateRunning, stale).
			Order("creat_time ASC, document_id ASC").
			First(&candidate).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Document{}, fmt.Errorf("%w: no document is waiting to be indexed", ErrNotFound)
		}
		if err != nil {
			return Document{}, fmt.Errorf("find a document to index: %w", err)
		}

		// The read above picked a candidate; this is what makes the claim
		// exclusive. Repeating the condition in the WHERE clause is the point:
		// between the two statements another worker may have claimed the same
		// row, and a claim that is taken on a row somebody else won is exactly
		// how one document gets embedded twice.
		result := db.Model(&models.Document{}).
			Where("document_id = ?", candidate.DocumentID).
			Where(claimable, VectorStatePending, VectorStateRunning, stale).
			Updates(map[string]any{
				"vector_state":      VectorStateRunning,
				"vector_owner":      owner,
				"vector_claimed_at": now,
				// A previous failure's message is cleared by the claim, so that
				// what a document shows is never a complaint about an attempt
				// that is over.
				"vector_error": nil,
			})
		if result.Error != nil {
			return Document{}, fmt.Errorf("claim document %s: %w", candidate.DocumentID, result.Error)
		}
		if result.RowsAffected == 0 {
			continue
		}
		candidate.VectorState = VectorStateRunning
		candidate.VectorOwner = &owner
		candidate.VectorClaimedAt = &now
		candidate.VectorError = nil
		return toDocument(candidate), nil
	}
	return Document{}, fmt.Errorf("%w: every candidate was claimed by another worker", ErrNotFound)
}

// PendingChunks reads one revision's chunks that have no vector yet.
func (r *GORMRepository) PendingChunks(ctx context.Context, documentID string, revision int) ([]ChunkRecord, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	// The same primary-key range scan as Chunks, for the same reason: an idle
	// revision's chunks are never wanted, so the revision is part of the filter
	// rather than of the prefix.
	low, high := chunkRange("chk_" + documentID + "_")
	var records []models.DocumentChunk
	err := r.db.WithContext(ctx).
		Where("chunk_id >= ? AND chunk_id < ? AND revision = ? AND embedding IS NULL", low, high, revision).
		Order("chunk_id ASC").
		Find(&records).Error
	if err != nil {
		return nil, fmt.Errorf("read chunks waiting for vectors: %w", err)
	}
	chunks := make([]ChunkRecord, 0, len(records))
	for _, record := range records {
		chunks = append(chunks, toChunkRecord(record))
	}
	return chunks, nil
}

// SaveEmbeddings stores one group of vectors.
//
// One statement per chunk inside one transaction, rather than one clever
// statement for the group: the group is the provider's batch size (tens), each
// statement is a primary-key lookup, and the transaction is what makes a group
// arrive whole or not at all. A `CASE WHEN` form would make a diff of the SQL
// unreadable for a saving that is only visible at a batch size nobody uses.
func (r *GORMRepository) SaveEmbeddings(ctx context.Context, chunks []ChunkRecord) error {
	if err := r.ready(); err != nil {
		return err
	}
	if len(chunks) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, chunk := range chunks {
			err := tx.Model(&models.DocumentChunk{}).
				Where("chunk_id = ?", chunk.ChunkID).
				Update("embedding", chunk.Embedding).Error
			if err != nil {
				return fmt.Errorf("store the vector of chunk %s: %w", chunk.ChunkID, err)
			}
		}
		return nil
	})
}

// CompleteDocument marks a document's vectors as done.
func (r *GORMRepository) CompleteDocument(ctx context.Context, documentID, owner string) error {
	return r.release(ctx, documentID, owner, VectorStateReady, "")
}

// FailDocument marks a document as one the provider refused.
func (r *GORMRepository) FailDocument(ctx context.Context, documentID, owner, message string) error {
	return r.release(ctx, documentID, owner, VectorStateFailed, message)
}

// RetryDocument hands a claimed document back to the queue.
func (r *GORMRepository) RetryDocument(ctx context.Context, documentID, owner, message string) error {
	return r.release(ctx, documentID, owner, VectorStatePending, message)
}

// release ends a claim and writes where the document goes next.
//
// The owner is in the WHERE clause and that is the whole safety of it: a rebuild
// clears the claim, a worker whose lease ran out had its claim taken, and in
// both cases whoever holds the document now is not this caller. Writing anyway
// would let a worker that no longer owns a document mark it ready with vectors
// for a revision nobody is serving.
func (r *GORMRepository) release(ctx context.Context, documentID, owner, state, message string) error {
	if err := r.ready(); err != nil {
		return err
	}
	var reason *string
	if message != "" {
		reason = &message
	}
	// The three columns move together because the schema's check constraint
	// requires them to: only a running document carries an owner and a claim
	// time, and only a document with a reason carries a reason.
	result := r.db.WithContext(ctx).Model(&models.Document{}).
		Where("document_id = ? AND vector_owner = ?", documentID, owner).
		Updates(map[string]any{
			"vector_state":      state,
			"vector_owner":      nil,
			"vector_claimed_at": nil,
			"vector_error":      reason,
		})
	if result.Error != nil {
		return fmt.Errorf("finish document %s: %w", documentID, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: document %s is no longer claimed by this worker",
			ErrConflict, documentID)
	}
	return nil
}
