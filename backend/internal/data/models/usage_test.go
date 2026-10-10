package models

import (
	"reflect"
	"testing"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm/schema"
)

func TestLLMCallModelMapping(t *testing.T) {
	parsed, err := schema.Parse(&LLMCall{}, &syncMap, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}

	if parsed.Table != "llm_calls" {
		t.Errorf("table = %q, want llm_calls", parsed.Table)
	}
	if got := primaryDBNames(parsed); !reflect.DeepEqual(got, []string{"call_id"}) {
		t.Errorf("primary fields = %v, want [call_id]", got)
	}
	wantColumns := []string{"call_id", "started_at", "session_id", "turn_id", "source", "usage_json"}
	if got := sortedColumnNames(parsed); !reflect.DeepEqual(got, sortedCopy(wantColumns)) {
		t.Errorf("columns = %v, want %v", got, sortedCopy(wantColumns))
	}
	if field := parsed.FieldsByDBName["usage_json"]; field == nil || field.TagSettings["TYPE"] != "json" {
		t.Errorf("usage_json field missing or does not declare JSON storage")
	}
}

func TestLLMCallFieldTypes(t *testing.T) {
	model := LLMCall{}
	if reflect.TypeOf(model.StartedAt) != reflect.TypeOf(time.Time{}) {
		t.Errorf("StartedAt type = %T, want time.Time", model.StartedAt)
	}
	if reflect.TypeOf(model.Usage) != reflect.TypeOf(datatypes.JSON{}) {
		t.Errorf("Usage type = %T, want datatypes.JSON", model.Usage)
	}
}
