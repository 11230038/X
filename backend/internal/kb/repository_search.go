package kb

// The retrieval half of the storage adapter, and the one where the SQL is
// shaped by what this seekdb build plans rather than by what reads naturally.
// The findings behind each shape are explained where they apply.

import (
	"context"
	"database/sql"
	"fmt"

	"backend/internal/data/models"
	"backend/internal/embed"
)

func (r *GORMRepository) Chunks(ctx context.Context, documentID string, revision int) ([]ChunkRecord, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	// A range on the primary key rather than `chunk_id LIKE '...%'`: the ids
	// contain underscores, and `_` is a single-character wildcard in LIKE, so the
	// pattern would not be a prefix the optimiser can range over. The upper bound
	// is the prefix with its last byte raised.
	low, high := chunkRange(ChunkIDPrefix(documentID, revision))
	var records []models.DocumentChunk
	err := r.db.WithContext(ctx).
		Where("chunk_id >= ? AND chunk_id < ?", low, high).
		Order("chunk_id ASC").
		Find(&records).Error
	if err != nil {
		return nil, fmt.Errorf("read document chunks: %w", err)
	}
	chunks := make([]ChunkRecord, 0, len(records))
	for _, model := range records {
		chunks = append(chunks, toChunkRecord(model))
	}
	return chunks, nil
}

// chunkSourceRow is one chunk joined to its document, as the query returns it.
type chunkSourceRow struct {
	ChunkID    string `gorm:"column:chunk_id"`
	DocumentID string `gorm:"column:document_id"`
	KBID       string `gorm:"column:kb_id"`
	Revision   int    `gorm:"column:revision"`
	Ordinal    int    `gorm:"column:ordinal"`
	Content    string `gorm:"column:content"`
	CharStart  int64  `gorm:"column:char_start"`
	CharEnd    int64  `gorm:"column:char_end"`
	Page       *int   `gorm:"column:page"`
	FileID     string `gorm:"column:file_id"`
	Filename   string `gorm:"column:filename"`
	Engine     string `gorm:"column:engine"`
}

// ChunkSources reads the chunks behind a list of ids, with the document
// metadata a citation needs.
//
// `is_active = TRUE` is part of the read, not a filter applied afterwards: a
// rebuild that landed between the search and this read leaves the chunk the
// search found inactive, and it is then simply absent — the caller skips it,
// which is the right answer. Citing text from a revision that is no longer the
// document's would point a reader at a document that no longer says that.
func (r *GORMRepository) ChunkSources(ctx context.Context, chunkIDs []string) ([]ChunkSource, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(chunkIDs) == 0 {
		return nil, nil
	}
	var rows []chunkSourceRow
	err := r.db.WithContext(ctx).Raw(`
		SELECT c.chunk_id, c.document_id, c.kb_id, c.revision, c.ordinal,
		       c.content, c.char_start, c.char_end, c.page,
		       d.file_id, d.filename, d.engine
		FROM document_chunks c
		JOIN documents d ON d.document_id = c.document_id
		WHERE c.chunk_id IN ? AND c.is_active = TRUE`, chunkIDs).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read chunk sources: %w", err)
	}
	sources := make([]ChunkSource, 0, len(rows))
	for _, row := range rows {
		sources = append(sources, ChunkSource{
			ChunkID: row.ChunkID, DocumentID: row.DocumentID, KBID: row.KBID,
			FileID: row.FileID, Filename: row.Filename, Engine: row.Engine,
			Revision: row.Revision, Ordinal: row.Ordinal, Content: row.Content,
			CharStart: row.CharStart, CharEnd: row.CharEnd, Page: row.Page,
		})
	}
	return sources, nil
}

// DocumentText reads a window of a document's extracted text.
//
// SUBSTRING counts characters in utf8mb4 and offsets are 1-based, which is the
// same unit and the same off-by-one as everything else in this package: the
// char_start of a chunk is the 0-based character offset, so the window request
// for it is offset+1 here.
func (r *GORMRepository) DocumentText(ctx context.Context, documentID string, offset, length int64) (string, error) {
	if err := r.ready(); err != nil {
		return "", err
	}
	var text sql.NullString
	err := r.db.WithContext(ctx).Raw(
		"SELECT SUBSTRING(extracted_text, ?, ?) FROM documents WHERE document_id = ?",
		offset+1, length, documentID).Scan(&text).Error
	if err != nil {
		return "", fmt.Errorf("read document text: %w", err)
	}
	if !text.Valid {
		return "", nil
	}
	return text.String, nil
}

// SearchVector finds the chunks of these bases nearest to a query vector.
//
// Two spellings here are load-bearing and neither fails loudly when dropped:
//
//   - `APPROXIMATE` is required syntax, not a hint. Without it this build plans
//     a full scan with an exact top-n sort — right answers, hundreds of times
//     the cost.
//   - the scalar filters stay in the same table as the vector index, which is
//     why kb_id and is_active are denormalised onto the chunk rows at all: the
//     planner then filters first and searches the vector index afterwards.
func (r *GORMRepository) SearchVector(ctx context.Context, kbIDs []string, vector embed.Vector, limit int) ([]string, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	var rows []struct {
		ChunkID string `gorm:"column:chunk_id"`
	}
	err := r.db.WithContext(ctx).Raw(`
		SELECT chunk_id
		FROM document_chunks
		WHERE kb_id IN ? AND is_active = TRUE AND embedding IS NOT NULL
		ORDER BY cosine_distance(embedding, ?) ASC APPROXIMATE
		LIMIT ?`, kbIDs, vector.String(), limit).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("search chunks by vector: %w", err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ChunkID)
	}
	return ids, nil
}

// SearchFullText finds the chunks of these bases that match a query.
//
// There is no ORDER BY and that is deliberate. `MATCH ... AGAINST` has to stay
// a bare predicate in the WHERE clause: as soon as it is wrapped in `> 0`, or
// moved into ORDER BY, this build stops using the full-text index and scans the
// table instead. The relevance order the design leans on is the one the planner
// pushes into the index search, and it arrives with the rows.
func (r *GORMRepository) SearchFullText(ctx context.Context, kbIDs []string, query string, limit int) ([]string, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	var rows []struct {
		ChunkID string `gorm:"column:chunk_id"`
	}
	err := r.db.WithContext(ctx).Raw(`
		SELECT chunk_id
		FROM document_chunks
		WHERE kb_id IN ? AND is_active = TRUE AND MATCH(content) AGAINST(?)
		LIMIT ?`, kbIDs, query, limit).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("search chunks by text: %w", err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ChunkID)
	}
	return ids, nil
}

func chunkRange(prefix string) (low, high string) {
	if prefix == "" {
		return "", ""
	}
	raised := []byte(prefix)
	raised[len(raised)-1]++
	return prefix, string(raised)
}

func toChunkRecord(model models.DocumentChunk) ChunkRecord {
	record := ChunkRecord{
		ChunkID: model.ChunkID, DocumentID: model.DocumentID, Revision: model.Revision,
		Ordinal: model.Ordinal, Content: model.Content,
		CharStart: model.CharStart, CharEnd: model.CharEnd, Page: model.Page,
	}
	if model.KbID != nil {
		record.KBID = *model.KbID
	}
	if model.Embedding != nil {
		record.Embedding = *model.Embedding
	}
	return record
}

// scopeName names where a document was looked for, in the words the caller
// asked in.
