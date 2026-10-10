package models

import (
	"reflect"
	"sync"
	"testing"

	"gorm.io/gorm/schema"
)

func TestConversationModelMappings(t *testing.T) {
	tests := []struct {
		name       string
		model      any
		table      string
		primaryKey string
		columns    []string
	}{
		{
			name:       "event type",
			model:      &TurnEventType{},
			table:      "turn_event_types",
			primaryKey: "type_id",
			columns:    []string{"name", "description"},
		},
		{
			name:       "session",
			model:      &Session{},
			table:      "sessions",
			primaryKey: "session_id",
			columns:    []string{"title", "creat_time", "update_time", "preferences_json", "workspace_mode"},
		},
		{
			name:       "message",
			model:      &Message{},
			table:      "messages",
			primaryKey: "message_id",
			columns:    []string{"session_id", "content", "role", "extra", "creat_time", "metadata_json", "attachments_json", "event_json", "capability"},
		},
		{
			name:       "summary",
			model:      &Summary{},
			table:      "summaries",
			primaryKey: "summary_id",
			columns:    []string{"session_id", "compressed_summary", "summary_up_to_msg_id", "revision", "creat_time"},
		},
		{
			name:       "turn",
			model:      &Turn{},
			table:      "turns",
			primaryKey: "turn_id",
			columns: []string{
				"session_id", "extra", "creat_time", "update_time", "finish_time", "own_id",
				"fencing_token", "state_version", "status", "capability", "error", "retryable",
				"failure_code", "assistant_message_id",
			},
		},
		{
			name:       "turn event",
			model:      &TurnEvent{},
			table:      "turn_events",
			primaryKey: "turn_events_id",
			columns:    []string{"turn_id", "seq", "creat_time", "timestamp", "type", "extra", "metadata_json", "content", "source", "stage"},
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
			if len(parsed.PrimaryFields) != 1 || parsed.PrimaryFields[0].DBName != tt.primaryKey {
				t.Fatalf("primary fields = %v, want %q", primaryDBNames(parsed), tt.primaryKey)
			}
			for _, column := range tt.columns {
				if parsed.FieldsByDBName[column] == nil {
					t.Errorf("column %q is not mapped", column)
				}
			}
		})
	}
}

func TestConversationNullableFieldTypes(t *testing.T) {
	if reflect.TypeOf(Turn{}.AssistantMessageID).Kind() != reflect.Ptr {
		t.Fatal("Turn.AssistantMessageID must be nullable")
	}
	if reflect.TypeOf(Turn{}.Retryable).Kind() != reflect.Ptr {
		t.Fatal("Turn.Retryable must be nullable")
	}
	if reflect.TypeOf(Turn{}.UpdatedAt).Kind() != reflect.Ptr {
		t.Fatal("Turn.UpdatedAt must be nullable")
	}
}

func TestTurnEventTypesAreStableAndUnique(t *testing.T) {
	types := []struct {
		id   int16
		name string
	}{
		{TurnEventTypeStageStart, TurnEventTypeNameStageStart},
		{TurnEventTypeStageEnd, TurnEventTypeNameStageEnd},
		{TurnEventTypeThinking, TurnEventTypeNameThinking},
		{TurnEventTypeObservation, TurnEventTypeNameObservation},
		{TurnEventTypeContent, TurnEventTypeNameContent},
		{TurnEventTypeToolCall, TurnEventTypeNameToolCall},
		{TurnEventTypeToolResult, TurnEventTypeNameToolResult},
		{TurnEventTypeProgress, TurnEventTypeNameProgress},
		{TurnEventTypeSources, TurnEventTypeNameSources},
		{TurnEventTypeResult, TurnEventTypeNameResult},
		{TurnEventTypeError, TurnEventTypeNameError},
		{TurnEventTypeSession, TurnEventTypeNameSession},
		{TurnEventTypeSessionMeta, TurnEventTypeNameSessionMeta},
		{TurnEventTypeDone, TurnEventTypeNameDone},
		{TurnEventTypeWaitForInput, TurnEventTypeNameWaitForInput},
	}
	if len(types) != 15 {
		t.Fatalf("event type count = %d, want 15", len(types))
	}

	seenIDs := make(map[int16]struct{}, len(types))
	seenNames := make(map[string]struct{}, len(types))
	for want, eventType := range types {
		if eventType.id != int16(want+1) {
			t.Errorf("event type ID at index %d = %d, want %d", want, eventType.id, want+1)
		}
		if eventType.name == "" {
			t.Errorf("event type name at index %d is empty", want)
		}
		if _, exists := seenIDs[eventType.id]; exists {
			t.Errorf("duplicate event type ID %d", eventType.id)
		}
		if _, exists := seenNames[eventType.name]; exists {
			t.Errorf("duplicate event type name %q", eventType.name)
		}
		seenIDs[eventType.id] = struct{}{}
		seenNames[eventType.name] = struct{}{}
	}
}

var syncMap = sync.Map{}

func primaryDBNames(parsed *schema.Schema) []string {
	names := make([]string, 0, len(parsed.PrimaryFields))
	for _, field := range parsed.PrimaryFields {
		names = append(names, field.DBName)
	}
	return names
}
