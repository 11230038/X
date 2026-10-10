package models

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm/schema"
)

func TestMasteryModelMappings(t *testing.T) {
	tests := []struct {
		name       string
		model      any
		table      string
		primaryKey []string
		columns    []string
	}{
		{"path", &MasteryPath{}, "mastery_paths", []string{"mastery_path_id"}, []string{"mastery_path_id", "owner_session_id", "state_json", "version", "creat_time", "update_time"}},
		{"evidence", &MasteryLearningEvidence{}, "mastery_learning_evidences", []string{"mle_id"}, []string{"mle_id", "session_id", "turn_id", "path_id", "extra", "result", "creat_time", "source", "quality", "assessment_type"}},
		{"lease", &MasteryPathLease{}, "mastery_path_leases", []string{"path_id"}, []string{"path_id", "session_id", "turn_id", "creat_time"}},
		{"interaction", &MasteryInteraction{}, "mastery_interactions", []string{"interaction_id"}, []string{"interaction_id", "path_id", "turn_id", "session_id", "user_answer", "result_json", "question_json", "creat_time", "update_time"}},
		{"event", &MasteryEvent{}, "mastery_events", []string{"mastery_events_id"}, []string{"mastery_events_id", "path_id", "turn_id", "session_id", "version", "payload_json", "event_type", "creat_time"}},
		{"path session", &MasteryPathSession{}, "mastery_path_sessions", []string{"path_id", "session_id"}, []string{"path_id", "session_id", "creat_time", "last_seen_time"}},
		{"topic meta", &MasteryTopicMeta{}, "mastery_topic_meta", []string{"path_id"}, []string{"path_id", "goal", "description", "emoji", "status", "creat_time", "update_time"}},
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

func TestMasteryNullableAndJSONFields(t *testing.T) {
	path := MasteryPath{}
	pointerFields := []any{path.Version, path.UpdatedAt}
	evidence := MasteryLearningEvidence{}
	pointerFields = append(pointerFields, evidence.Result, evidence.Source, evidence.Quality, evidence.AssessmentType)
	interaction := MasteryInteraction{}
	pointerFields = append(pointerFields, interaction.UserAnswer, interaction.UpdatedAt)
	event := MasteryEvent{}
	pointerFields = append(pointerFields, event.Version, event.EventType)
	pathSession := MasteryPathSession{}
	pointerFields = append(pointerFields, pathSession.LastSeenTime)
	meta := MasteryTopicMeta{}
	pointerFields = append(pointerFields, meta.Goal, meta.Description, meta.Emoji, meta.Status, meta.UpdatedAt)
	for index, field := range pointerFields {
		if reflect.TypeOf(field).Kind() != reflect.Ptr {
			t.Errorf("nullable mastery field %d has type %T, want pointer", index, field)
		}
	}

	jsonFields := []any{path.State, evidence.Extra, interaction.Result, interaction.Question, event.Payload}
	for index, field := range jsonFields {
		if reflect.TypeOf(field) != reflect.TypeOf(datatypes.JSON{}) {
			t.Errorf("mastery JSON field %d has type %T", index, field)
		}
	}
	if reflect.TypeOf(path.CreatedAt) != reflect.TypeOf(time.Time{}) {
		t.Fatal("MasteryPath.CreatedAt must be time.Time")
	}
}

func sortedColumnNames(parsed *schema.Schema) []string {
	names := make([]string, 0, len(parsed.DBNames))
	names = append(names, parsed.DBNames...)
	sort.Strings(names)
	return names
}

func sortedCopy(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}
