package models

import (
	"reflect"
	"testing"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm/schema"
)

func TestNotebookModelMappings(t *testing.T) {
	tests := []struct {
		name       string
		model      any
		table      string
		primaryKey []string
		columns    []string
	}{
		{
			name:       "notebook entry",
			model:      &NotebookEntry{},
			table:      "notebook_entries",
			primaryKey: []string{"notebook_entries_id"},
			columns: []string{
				"question_id", "question", "question_type", "question_illustration", "extra",
				"creat_time", "update_time", "difficulty", "user_answer", "user_answer_image",
				"options", "correct_answer", "explanation", "quality", "assessment_type", "is_correct",
				"result", "resolved", "attempt_count", "bookmarked", "source", "mastery_path_id",
				"knowledge_point_id", "session_id", "turn_id",
			},
		},
		{
			name:       "reading quiz pending",
			model:      &ReadingQuizPending{},
			table:      "reading_quiz_pending",
			primaryKey: []string{"question_id"},
			columns:    []string{"question", "creat_time"},
		},
		{
			name:       "notebook category",
			model:      &NotebookCategory{},
			table:      "notebook_categories",
			primaryKey: []string{"notebook_categories_id"},
			columns:    []string{"name", "creat_time"},
		},
		{
			name:       "entry category",
			model:      &NotebookEntryCategory{},
			table:      "notebook_entry_categories",
			primaryKey: []string{"notebook_entries_id", "notebook_categories_id"},
			columns:    []string{"notebook_entries_id", "notebook_categories_id"},
		},
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
			for _, column := range tt.columns {
				if parsed.FieldsByDBName[column] == nil {
					t.Errorf("column %q is not mapped", column)
				}
			}
		})
	}
}

func TestNotebookNullableFields(t *testing.T) {
	entry := NotebookEntry{}
	fields := []any{
		entry.QuestionIllustration, entry.UpdatedAt, entry.Difficulty, entry.UserAnswer,
		entry.CorrectAnswer, entry.Explanation, entry.Quality, entry.AssessmentType,
		entry.IsCorrect, entry.Result, entry.Resolved, entry.AttemptCount, entry.Bookmarked,
		entry.Source, entry.MasteryPathID, entry.KnowledgePointID, entry.SessionID, entry.TurnID,
	}
	for index, field := range fields {
		if reflect.TypeOf(field).Kind() != reflect.Ptr {
			t.Errorf("nullable notebook field %d has type %T, want pointer", index, field)
		}
	}
	if reflect.TypeOf(entry.CreatedAt) != reflect.TypeOf(time.Time{}) {
		t.Fatal("NotebookEntry.CreatedAt must be time.Time")
	}
	if reflect.TypeOf(entry.Extra) != reflect.TypeOf(datatypes.JSON{}) {
		t.Fatal("NotebookEntry.Extra must use datatypes.JSON")
	}
}
