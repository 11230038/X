package models

import (
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm/schema"
)

func TestLibraryFileModelMapping(t *testing.T) {
	parsed, err := schema.Parse(&LibraryFile{}, &syncMap, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}

	if parsed.Table != "library_files" {
		t.Errorf("table = %q, want library_files", parsed.Table)
	}
	if got := primaryDBNames(parsed); !reflect.DeepEqual(got, []string{"id"}) {
		t.Errorf("primary fields = %v, want [id]", got)
	}
	wantColumns := []string{
		"id", "sha256", "filename", "mime_type", "size_bytes", "library_path",
		"created_at", "updated_at", "is_deleted", "deleted_at",
	}
	if got := sortedColumnNames(parsed); !reflect.DeepEqual(got, sortedCopy(wantColumns)) {
		t.Errorf("columns = %v, want %v", got, sortedCopy(wantColumns))
	}
}

func TestLibraryFileFieldTypes(t *testing.T) {
	model := LibraryFile{}
	if reflect.TypeOf(model.SizeBytes).Kind() != reflect.Int64 {
		t.Errorf("SizeBytes type = %T, want int64", model.SizeBytes)
	}
	if reflect.TypeOf(model.IsDeleted).Kind() != reflect.Bool {
		t.Errorf("IsDeleted type = %T, want bool", model.IsDeleted)
	}
	if reflect.TypeOf(model.DeletedAt).Kind() != reflect.Ptr {
		t.Errorf("DeletedAt type = %T, want pointer", model.DeletedAt)
	}
	if reflect.TypeOf(model.CreatedAt) != reflect.TypeOf(time.Time{}) {
		t.Errorf("CreatedAt type = %T, want time.Time", model.CreatedAt)
	}
	if reflect.TypeOf(model.UpdatedAt) != reflect.TypeOf(time.Time{}) {
		t.Errorf("UpdatedAt type = %T, want time.Time", model.UpdatedAt)
	}
}
