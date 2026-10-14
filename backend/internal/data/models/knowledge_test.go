package models

import (
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm/schema"
)

// TestKnowledgeModelMappings pins each mapping to the table migration 00007
// creates. The two are written twice — once in SQL, once here — and a column
// that exists in only one of them is a compile-time-clean runtime failure.
func TestKnowledgeModelMappings(t *testing.T) {
	cases := []struct {
		model   any
		table   string
		primary []string
		columns []string
	}{
		{
			model:   &KnowledgeBase{},
			table:   "knowledge_bases",
			primary: []string{"kb_id"},
			columns: []string{
				"kb_id", "name", "description", "provider", "embedding_model",
				"is_default", "owner_id", "creat_time", "update_time",
			},
		},
		{
			model:   &Document{},
			table:   "documents",
			primary: []string{"document_id"},
			columns: []string{
				"document_id", "kb_id", "file_id", "filename", "engine",
				"extracted_text", "text_chars", "truncated", "chunk_size",
				"chunk_overlap", "active_revision", "vector_state", "vector_owner",
				"vector_claimed_at", "vector_error", "creat_time", "update_time",
			},
		},
		{
			model:   &DocumentChunk{},
			table:   "document_chunks",
			primary: []string{"chunk_id"},
			columns: []string{
				"chunk_id", "document_id", "kb_id", "revision", "is_active",
				"ordinal", "content", "char_start", "char_end", "page",
				"embedding", "creat_time",
			},
		},
	}

	for _, testCase := range cases {
		parsed, err := schema.Parse(testCase.model, &syncMap, schema.NamingStrategy{})
		if err != nil {
			t.Fatalf("parse %s schema: %v", testCase.table, err)
		}
		if parsed.Table != testCase.table {
			t.Errorf("table = %q, want %q", parsed.Table, testCase.table)
		}
		if got := primaryDBNames(parsed); !reflect.DeepEqual(got, testCase.primary) {
			t.Errorf("%s primary fields = %v, want %v", testCase.table, got, testCase.primary)
		}
		if got := sortedColumnNames(parsed); !reflect.DeepEqual(got, sortedCopy(testCase.columns)) {
			t.Errorf("%s columns = %v, want %v", testCase.table, got, sortedCopy(testCase.columns))
		}
	}
}

// TestDocumentNullableColumns pins which columns carry NULL, because NULL is
// the meaning in each case rather than an absence of data: a document with no
// kb_id is an attachment, and a chunk with no embedding is one the worker has
// not reached yet.
func TestDocumentNullableColumns(t *testing.T) {
	if reflect.TypeOf(Document{}.KbID).Kind() != reflect.Ptr {
		t.Error("Document.KbID must be a pointer so an attachment can be stored")
	}
	if reflect.TypeOf(DocumentChunk{}.Embedding).Kind() != reflect.Ptr {
		t.Error("DocumentChunk.Embedding must be a pointer so an unembedded chunk can be stored")
	}
	if reflect.TypeOf(Document{}.VectorClaimedAt).Kind() != reflect.Ptr {
		t.Error("Document.VectorClaimedAt must be a pointer: an unclaimed document has no lease")
	}
	if reflect.TypeOf(Document{}.CreatedAt) != reflect.TypeOf(time.Time{}) {
		t.Error("Document.CreatedAt must be time.Time")
	}
	if reflect.TypeOf(DocumentChunk{}.CharEnd).Kind() != reflect.Int64 {
		t.Error("DocumentChunk.CharEnd must be int64: offsets index into a MEDIUMTEXT column")
	}
	if reflect.TypeOf(DocumentChunk{}.Page).Kind() != reflect.Ptr {
		t.Error("DocumentChunk.Page must be a pointer: text has no page number")
	}
}
