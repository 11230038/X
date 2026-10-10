package models

import (
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm/schema"
)

func TestReadingModelMappings(t *testing.T) {
	tests := []struct {
		name       string
		model      any
		table      string
		primaryKey []string
		columns    []string
	}{
		{"material", &ReadingMaterial{}, "reading_materials", []string{"material_id"}, []string{"material_id", "content_id", "filename", "source_type", "creat_time", "update_time"}},
		{"workspace", &ReadingWorkspace{}, "reading_workspaces", []string{"workspace_id"}, []string{"workspace_id", "active_material_id", "title", "description", "creat_time", "update_time"}},
		{"workspace session", &ReadingWorkspaceSession{}, "reading_workspace_sessions", []string{"workspace_id", "session_id"}, []string{"workspace_id", "session_id", "active_material_id", "creat_time", "update_time"}},
		{"workspace material", &ReadingWorkspaceMaterial{}, "reading_workspace_materials", []string{"workspace_id", "material_id"}, []string{"workspace_id", "material_id", "tab_order", "creat_time"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := schema.Parse(tt.model, &syncMap, schema.NamingStrategy{})
			if err != nil {
				t.Fatalf("parse schema: %v", err)
			}
			if parsed.Table != tt.table {
				t.Errorf("table = %q, want %q", parsed.Table, tt.table)
			}
			if got := primaryDBNames(parsed); !reflect.DeepEqual(got, tt.primaryKey) {
				t.Errorf("primary fields = %v, want %v", got, tt.primaryKey)
			}
			if got := sortedColumnNames(parsed); !reflect.DeepEqual(got, sortedCopy(tt.columns)) {
				t.Errorf("columns = %v, want %v", got, sortedCopy(tt.columns))
			}
			if field := parsed.FieldsByDBName["update_time"]; field != nil && field.AutoUpdateTime != 0 {
				t.Errorf("update_time AutoUpdateTime = %v, want disabled", field.AutoUpdateTime)
			}
		})
	}
}

func TestReadingNullableFields(t *testing.T) {
	material := ReadingMaterial{}
	workspace := ReadingWorkspace{}
	workspaceSession := ReadingWorkspaceSession{}
	workspaceMaterial := ReadingWorkspaceMaterial{}
	fields := []any{
		material.ContentID, material.Filename, material.SourceType, material.UpdatedAt,
		workspace.ActiveMaterialID, workspace.Title, workspace.Description, workspace.UpdatedAt,
		workspaceSession.ActiveMaterialID, workspaceSession.UpdatedAt,
		workspaceMaterial.TabOrder,
	}
	for index, field := range fields {
		if reflect.TypeOf(field).Kind() != reflect.Ptr {
			t.Errorf("nullable reading field %d has type %T, want pointer", index, field)
		}
	}
	if reflect.TypeOf(material.CreatedAt) != reflect.TypeOf(time.Time{}) {
		t.Fatal("ReadingMaterial.CreatedAt must be time.Time")
	}
}
