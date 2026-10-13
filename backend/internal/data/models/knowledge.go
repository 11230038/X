package models

import "time"

// KnowledgeBase is the persistence mapping for one retrievable collection.
type KnowledgeBase struct {
	KBID           string    `gorm:"column:kb_id;primaryKey;autoIncrement:false"`
	Name           string    `gorm:"column:name;not null"`
	Description    *string   `gorm:"column:description"`
	Provider       string    `gorm:"column:provider;not null"`
	EmbeddingModel string    `gorm:"column:embedding_model;not null"`
	// IsDefault is what the schema derives its marker from, so that at most one
	// base can carry the mark (migration 00008). Nothing here writes the marker
	// itself: it is computed, and a write that tried would be refused.
	IsDefault      bool      `gorm:"column:is_default;not null;default:false"`
	CreatedAt      time.Time `gorm:"column:creat_time;autoCreateTime"`
	UpdatedAt      time.Time `gorm:"column:update_time;autoCreateTime;autoUpdateTime"`
}

// TableName fixes the knowledge_bases table name.
func (KnowledgeBase) TableName() string {
	return "knowledge_bases"
}

// Document is the persistence mapping for one parsed file.
//
// A document with a NULL KbID is an attachment: it exists for the turns that
// carried it and belongs to no collection.
type Document struct {
	DocumentID string `gorm:"column:document_id;primaryKey;autoIncrement:false"`
	// KbID and FileID together are one document per file per place, enforced by
	// the unique index on (file_id, file_scope) from migration 00008, where the
	// scope is this column's NULL folded to the empty string.
	KbID *string `gorm:"column:kb_id"`
	FileID     string  `gorm:"column:file_id;not null"`
	Filename   string  `gorm:"column:filename;not null"`
	Engine     string  `gorm:"column:engine;not null"`
	// ExtractedText is the parser's output, and the text char_start/char_end on
	// each chunk index into. MEDIUMTEXT rather than TEXT: ATT-03 allows 200k
	// characters in one document.
	ExtractedText   *string    `gorm:"column:extracted_text"`
	TextChars       int64      `gorm:"column:text_chars;not null"`
	Truncated       bool       `gorm:"column:truncated;not null;default:false"`
	ChunkSize       int        `gorm:"column:chunk_size;not null"`
	ChunkOverlap    int        `gorm:"column:chunk_overlap;not null"`
	ActiveRevision  int        `gorm:"column:active_revision;not null"`
	VectorState     string     `gorm:"column:vector_state;not null"`
	VectorOwner     *string    `gorm:"column:vector_owner"`
	VectorClaimedAt *time.Time `gorm:"column:vector_claimed_at"`
	VectorError     *string    `gorm:"column:vector_error"`
	CreatedAt       time.Time  `gorm:"column:creat_time;autoCreateTime"`
	UpdatedAt       time.Time  `gorm:"column:update_time;autoCreateTime;autoUpdateTime"`
}

// TableName fixes the documents table name.
func (Document) TableName() string {
	return "documents"
}

// DocumentChunk is the persistence mapping for one retrievable span of text.
type DocumentChunk struct {
	ChunkID    string  `gorm:"column:chunk_id;primaryKey;autoIncrement:false"`
	DocumentID string  `gorm:"column:document_id;not null"`
	KbID       *string `gorm:"column:kb_id"`
	Revision   int     `gorm:"column:revision;not null"`
	IsActive   bool    `gorm:"column:is_active;not null;default:false"`
	Ordinal    int     `gorm:"column:ordinal;not null"`
	Content    string  `gorm:"column:content;not null"`
	CharStart  int64   `gorm:"column:char_start;not null"`
	CharEnd    int64   `gorm:"column:char_end;not null"`
	Page       *int    `gorm:"column:page"`
	// Embedding holds the model's vector in its text form, the shape seekdb's
	// cosine_distance accepts. It is a string rather than a float slice because
	// nothing in this process ever reads it back out: the database does the
	// arithmetic, and retrieval returns distances, not vectors.
	Embedding *string   `gorm:"column:embedding;type:vector(1024)"`
	CreatedAt time.Time `gorm:"column:creat_time;autoCreateTime"`
}

// TableName fixes the document_chunks table name.
func (DocumentChunk) TableName() string {
	return "document_chunks"
}
