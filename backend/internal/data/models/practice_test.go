package models

import (
	"reflect"
	"testing"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm/schema"
)

func TestPracticeModelMappings(t *testing.T) {
	tests := []struct {
		name       string
		model      any
		table      string
		primaryKey []string
		columns    []string
	}{
		{
			name:       "review state",
			model:      &PracticeReviewState{},
			table:      "practice_review_state",
			primaryKey: []string{"notebook_entries_id"},
			columns: []string{
				"first_wrong_time", "due_time", "last_review_time", "is_mistake",
				"case", "streak", "review_count", "lapses", "extra",
			},
		},
		{
			name:       "review event",
			model:      &PracticeReviewEvent{},
			table:      "practice_review_events",
			primaryKey: []string{"request_id"},
			columns:    []string{"notebook_entries_id", "user_answer", "rating", "outcome_json", "review_time"},
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

func TestPracticeNullableFields(t *testing.T) {
	state := PracticeReviewState{}
	stateFields := []any{
		state.FirstWrongTime, state.DueTime, state.LastReviewTime, state.IsMistake,
		state.Case, state.Streak, state.ReviewCount, state.Lapses,
	}
	for index, field := range stateFields {
		if reflect.TypeOf(field).Kind() != reflect.Ptr {
			t.Errorf("nullable review state field %d has type %T, want pointer", index, field)
		}
	}
	if reflect.TypeOf(state.Extra) != reflect.TypeOf(datatypes.JSON{}) {
		t.Fatal("PracticeReviewState.Extra must use datatypes.JSON")
	}

	event := PracticeReviewEvent{}
	eventFields := []any{event.UserAnswer, event.Rating, event.ReviewTime}
	for index, field := range eventFields {
		if reflect.TypeOf(field).Kind() != reflect.Ptr {
			t.Errorf("nullable review event field %d has type %T, want pointer", index, field)
		}
	}
	if reflect.TypeOf(event.Outcome) != reflect.TypeOf(datatypes.JSON{}) {
		t.Fatal("PracticeReviewEvent.Outcome must use datatypes.JSON")
	}
	if reflect.TypeOf(event.ReviewTime) != reflect.TypeOf((*time.Time)(nil)) {
		t.Fatal("PracticeReviewEvent.ReviewTime must be *time.Time")
	}
}
