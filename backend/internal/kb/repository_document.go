package kb

// The document half of the storage adapter. The SQL shapes worth knowing about
// are explained on GORMRepository in repository.go.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"backend/internal/authz"
	"backend/internal/data"
	"backend/internal/data/models"
	"gorm.io/gorm"
)

// documentScope narrows a documents query to the rows one caller may read.
//
// A document carries no owner column — it is not a thing a person makes, it is
// what a parser produced from something they do have — so its ownership is
// derived twice over, once from the collection it sits in and once from the file
// it was read out of. A row reaches the caller when *either* holds: the base is
// theirs, or there is no base at all and the file is theirs. Attachments are the
// second half and are not an afterthought: a document with kb_id NULL is what a
// turn's uploaded file becomes, and it has no collection to be owned through.
//
// The whole predicate is one parenthesised WHERE, because GORM ANDs it against
// whatever the caller has already built. Left unparenthesised the OR would
// swallow the caller's own conditions and widen the query instead of narrowing
// it — the failure mode that returns more rows than asked for while every test
// that reads one row passes.
//
// Each subquery is built on a session of its own, and that is not tidiness. A
// GORM handle carries one statement, and every chained call after the first
// writes to it: building both subqueries on the handle handed in here would
// leave the second one's table on the outer statement, turn the caller's read
// into a read of `library_files`, and — because a statement passed as an
// argument is rendered as a subquery — inline the outer query into its own
// WHERE. A session with NewDB starts a statement that keeps this handle's
// connection and context, which is what a subquery of a transaction needs.
func documentScope(db *gorm.DB, caller authz.Caller) *gorm.DB {
	bases := data.OwnerScope(subquery(db, &models.KnowledgeBase{}, "kb_id"),
		caller.UserID, caller.IsAdmin(), "owner_id")
	files := data.OwnerScope(subquery(db, &models.LibraryFile{}, "file_id"),
		caller.UserID, caller.IsAdmin(), "owner_id")
	return db.Where("(kb_id IN (?) OR (kb_id IS NULL AND file_id IN (?)))", bases, files)
}

// subquery is one column of one table as a statement of its own, for the scopes
// that are written as a subquery rather than as a join.
func subquery(db *gorm.DB, model any, column string) *gorm.DB {
	return db.Session(&gorm.Session{NewDB: true}).Model(model).Select(column)
}

func (r *GORMRepository) Document(ctx context.Context, documentID string, caller authz.Caller) (Document, error) {
	if err := r.ready(); err != nil {
		return Document{}, err
	}
	var model models.Document
	query := documentScope(r.db.WithContext(ctx), caller)
	if err := query.First(&model, "document_id = ?", documentID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Document{}, fmt.Errorf("%w: document %s", ErrNotFound, documentID)
		}
		return Document{}, fmt.Errorf("read document: %w", err)
	}
	return toDocument(model), nil
}

// DocumentForFile returns the document one file produced, either inside a base
// or as an attachment.
//
// `<=>` is MySQL's null-safe equality, and it is doing real work here: the
// attachment case passes NULL, where `= NULL` matches nothing at all and would
// make every attachment look like a file that was never parsed.
func (r *GORMRepository) DocumentForFile(ctx context.Context, fileID, kbID string, caller authz.Caller) (Document, error) {
	if err := r.ready(); err != nil {
		return Document{}, err
	}
	var model models.Document
	query := documentScope(r.db.WithContext(ctx), caller)
	err := query.
		Where("file_id = ? AND kb_id <=> ?", fileID, nullableString(kbID)).
		First(&model).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Document{}, fmt.Errorf("%w: file %s is not parsed in %s", ErrNotFound, fileID, scopeName(kbID))
		}
		return Document{}, fmt.Errorf("read document for file: %w", err)
	}
	return toDocument(model), nil
}

// DocumentOfFile returns a document parsed from one file, for a caller that is
// asking about the file rather than about where it is used.
//
// The attachment wins when there is one, and the ordering says so rather than a
// second query: `kb_id IS NULL` sorts true first under DESC. After that the
// oldest document is taken, which is the one every rebuild of the same bytes is
// a newer revision of.
func (r *GORMRepository) DocumentOfFile(ctx context.Context, fileID string, caller authz.Caller) (Document, error) {
	if err := r.ready(); err != nil {
		return Document{}, err
	}
	var model models.Document
	query := documentScope(r.db.WithContext(ctx), caller)
	err := query.
		Where("file_id = ?", fileID).
		Order("kb_id IS NULL DESC, creat_time ASC, document_id ASC").
		First(&model).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Document{}, fmt.Errorf("%w: file %s is not parsed anywhere", ErrNotFound, fileID)
		}
		return Document{}, fmt.Errorf("read document of file: %w", err)
	}
	return toDocument(model), nil
}

// Documents lists a base's documents in the order they were added, or an empty
// base's nothing when kbID is empty.
func (r *GORMRepository) Documents(ctx context.Context, kbID string, caller authz.Caller) ([]Document, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	query := documentScope(r.db.WithContext(ctx), caller).Model(&models.Document{})
	if kbID == "" {
		query = query.Where("kb_id IS NULL")
	} else {
		query = query.Where("kb_id = ?", kbID)
	}
	var records []models.Document
	if err := query.Order("creat_time ASC, document_id ASC").Find(&records).Error; err != nil {
		return nil, fmt.Errorf("read documents: %w", err)
	}
	documents := make([]Document, 0, len(records))
	for _, model := range records {
		documents = append(documents, toDocument(model))
	}
	return documents, nil
}

// CreateDocument stores a document and the first revision of its chunks.
func (r *GORMRepository) CreateDocument(ctx context.Context, write DocumentWrite) error {
	if err := r.ready(); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(documentModel(write)).Error; err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return fmt.Errorf("%w: file %s is already parsed here", ErrConflict, write.Document.FileID)
			}
			return fmt.Errorf("create document: %w", err)
		}
		return writeChunks(tx, write)
	})
}

// ReplaceRevision stores a document's next revision and moves the active
// pointer onto it.
//
// The guard is the revision the caller read: two rebuilds of one document
// started at the same moment would otherwise both write revision n+1 (the chunk
// ids would collide) or both move the pointer, and the second would win with
// chunks whose text nobody chose.
func (r *GORMRepository) ReplaceRevision(ctx context.Context, write DocumentWrite, fromRevision int, caller authz.Caller) error {
	if err := r.ready(); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := documentScope(tx.Model(&models.Document{}), caller).
			Where("document_id = ? AND active_revision = ?", write.Document.ID, fromRevision).
			Updates(map[string]any{
				"engine":          write.Document.Engine,
				"extracted_text":  write.Text,
				"text_chars":      write.Document.TextChars,
				"truncated":       write.Document.Truncated,
				"chunk_size":      write.Document.ChunkSize,
				"chunk_overlap":   write.Document.ChunkOverlap,
				"active_revision": write.Document.ActiveRevision,
				// The new chunks have no vectors yet, and the claim has to be
				// cleared with the state: a row left in 'running' with a stale
				// owner is one the lease would eventually hand to a second
				// worker while the first still believes it owns it.
				"vector_state":      VectorStatePending,
				"vector_owner":      nil,
				"vector_claimed_at": nil,
				"vector_error":      nil,
				"update_time":       write.Document.UpdatedAt,
			})
		if result.Error != nil {
			return fmt.Errorf("move the active revision: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			// The re-read tells the two ways this can happen apart, and it is the
			// scoped read: a document the caller cannot see is reported as missing
			// rather than as a lost race, so that a rebuild aimed at somebody
			// else's document says nothing about whether that document exists.
			if _, err := r.Document(ctx, write.Document.ID, caller); err != nil {
				return err
			}
			return fmt.Errorf("%w: document %s is not at revision %d",
				ErrConflict, write.Document.ID, fromRevision)
		}
		return writeChunks(tx, write)
	})
}

// DeleteDocument removes a document and its chunks.
func (r *GORMRepository) DeleteDocument(ctx context.Context, documentID string, caller authz.Caller) error {
	if err := r.ready(); err != nil {
		return err
	}
	query := documentScope(r.db.WithContext(ctx), caller)
	result := query.Delete(&models.Document{}, "document_id = ?", documentID)
	if result.Error != nil {
		return fmt.Errorf("delete document: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: document %s", ErrNotFound, documentID)
	}
	return nil
}

// writeChunks stores one revision's chunks and points the document's chunks at
// that revision.
//
// Both statements are here, and only here, so that the flag on a chunk and the
// pointer on its document cannot be written by two callers who disagree about
// which revision is current: the flag is computed from the pointer inside one
// transaction.
func writeChunks(tx *gorm.DB, write DocumentWrite) error {
	if len(write.Chunks) > 0 {
		records := make([]models.DocumentChunk, 0, len(write.Chunks))
		for _, chunk := range write.Chunks {
			records = append(records, models.DocumentChunk{
				ChunkID: chunk.ChunkID, DocumentID: chunk.DocumentID,
				KbID: nullableString(chunk.KBID), Revision: chunk.Revision,
				Ordinal: chunk.Ordinal, Content: chunk.Content,
				CharStart: chunk.CharStart, CharEnd: chunk.CharEnd, Page: chunk.Page,
				Embedding: nullableString(chunk.Embedding),
			})
		}
		if err := tx.CreateInBatches(records, chunkBatchSize).Error; err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return fmt.Errorf("%w: revision %d of document %s is already written",
					ErrConflict, write.Document.ActiveRevision, write.Document.ID)
			}
			return fmt.Errorf("create document chunks: %w", err)
		}
	}
	low, high := chunkRange("chk_" + write.Document.ID + "_")
	if err := tx.Model(&models.DocumentChunk{}).
		Where("chunk_id >= ? AND chunk_id < ?", low, high).
		Update("is_active", gorm.Expr("revision = ?", write.Document.ActiveRevision)).Error; err != nil {
		return fmt.Errorf("move the active chunks: %w", err)
	}
	return nil
}

// documentModel is one document row as it is written.
func documentModel(write DocumentWrite) *models.Document {
	return &models.Document{
		DocumentID: write.Document.ID,
		KbID:       nullableString(write.Document.KBID),
		FileID:     write.Document.FileID, Filename: write.Document.Filename,
		Engine: write.Document.Engine, ExtractedText: &write.Text,
		TextChars: write.Document.TextChars, Truncated: write.Document.Truncated,
		ChunkSize: write.Document.ChunkSize, ChunkOverlap: write.Document.ChunkOverlap,
		ActiveRevision: write.Document.ActiveRevision,
		VectorState:    write.Document.VectorState,
		CreatedAt:      write.Document.CreatedAt, UpdatedAt: write.Document.UpdatedAt,
	}
}

func toDocument(model models.Document) Document {
	record := Document{
		ID: model.DocumentID, FileID: model.FileID, Filename: model.Filename,
		Engine: model.Engine, TextChars: model.TextChars, Truncated: model.Truncated,
		ChunkSize: model.ChunkSize, ChunkOverlap: model.ChunkOverlap,
		ActiveRevision: model.ActiveRevision, VectorState: model.VectorState,
		CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt,
	}
	if model.KbID != nil {
		record.KBID = *model.KbID
	}
	if model.VectorError != nil {
		record.VectorError = *model.VectorError
	}
	return record
}

func scopeName(kbID string) string {
	if kbID == "" {
		return "any attachment"
	}
	return "knowledge base " + strings.TrimSpace(kbID)
}
