package models

import (
	"reflect"
	"testing"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm/schema"
)

// The memory tables' mapping (migration 00013).
//
// The column lists are the migration's own, spelled out here a second time on
// purpose: a mapping that grew a column the table does not have fails on the
// first write in production, and a table column no mapping knows about is a
// value nothing can read. Neither is visible in a test that reads the list back
// off the model.

func TestMemoryModelMappings(t *testing.T) {
	tests := []struct {
		name       string
		model      any
		table      string
		primaryKey []string
		columns    []string
	}{
		{"trace", &MemoryTrace{}, "memory_traces", []string{"trace_id"},
			[]string{"trace_id", "owner_id", "surface", "kind", "session_id", "turn_id",
				"statement", "fingerprint", "payload_json", "creat_time"}},
		{"fact", &MemoryFact{}, "memory_facts", []string{"fact_id"},
			[]string{"fact_id", "owner_id", "surface", "scope", "statement", "fingerprint",
				"confidence", "status", "sensitive", "confirmed_by", "version",
				"creat_time", "update_time", "retract_time"}},
		{"fact source", &MemoryFactSource{}, "memory_fact_sources", []string{"fact_id", "trace_id"},
			[]string{"fact_id", "trace_id", "creat_time"}},
		{"audit", &MemoryAudit{}, "memory_audit", []string{"audit_id"},
			[]string{"audit_id", "owner_id", "actor", "action", "target_kind", "target_id",
				"result", "detail_json", "creat_time"}},
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
			// update_time is written by the lifecycle and never by GORM: a fact's
			// update time is when the fact changed, and an edit that changed
			// nothing is not a change.
			if field := parsed.FieldsByDBName["update_time"]; field != nil && field.AutoUpdateTime != 0 {
				t.Errorf("update_time AutoUpdateTime = %v, want disabled", field.AutoUpdateTime)
			}
		})
	}
}

// TestMemoryNullableFields pins which columns mean three things rather than two.
// Each of these is nil for a reason the value cannot express: a trace with no
// conversation, a fact with no scope, an audit row about a batch instead of a row.
func TestMemoryNullableFields(t *testing.T) {
	trace := MemoryTrace{}
	fact := MemoryFact{}
	audit := MemoryAudit{}
	fields := []any{
		trace.SessionID, trace.TurnID,
		fact.Scope, fact.UpdatedAt, fact.RetractTime,
		audit.TargetID,
	}
	for index, field := range fields {
		if reflect.TypeOf(field).Kind() != reflect.Ptr {
			t.Errorf("nullable memory field %d has type %T, want pointer", index, field)
		}
	}
	if reflect.TypeOf(trace.CreatedAt) != reflect.TypeOf(time.Time{}) {
		t.Fatal("MemoryTrace.CreatedAt must be time.Time")
	}
}

func TestMemoryJSONFields(t *testing.T) {
	jsonFields := []any{
		MemoryTrace{}.Payload,
		MemoryAudit{}.Detail,
	}
	for index, field := range jsonFields {
		if reflect.TypeOf(field) != reflect.TypeOf(datatypes.JSON{}) {
			t.Errorf("memory JSON field %d has type %T", index, field)
		}
	}
}
