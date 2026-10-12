package event

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// seededTypes reads the event type rows out of the migration that creates them,
// so this test compares the Go constants against the schema rather than against
// a second copy of the same list.
func seededTypes(t *testing.T) map[string]int16 {
	t.Helper()
	const path = "../data/migration/sql/00002_create_conversation_tables.sql"
	sql, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	_, tail, found := strings.Cut(string(sql), "INSERT INTO turn_event_types")
	if !found {
		t.Fatalf("%s no longer seeds turn_event_types; this test needs updating", path)
	}
	values, _, found := strings.Cut(tail, ";")
	if !found {
		t.Fatalf("the turn_event_types insert in %s is unterminated", path)
	}

	rows := regexp.MustCompile(`\(\s*(\d+)\s*,\s*'([a-z_]+)'`).FindAllStringSubmatch(values, -1)
	if len(rows) == 0 {
		t.Fatalf("no rows parsed from the turn_event_types insert in %s", path)
	}

	seeded := make(map[string]int16, len(rows))
	for _, row := range rows {
		id, err := strconv.ParseInt(row[1], 10, 16)
		if err != nil {
			t.Fatalf("parse id %q: %v", row[1], err)
		}
		seeded[row[2]] = int16(id)
	}
	return seeded
}

func TestTypesMatchTheSeededTable(t *testing.T) {
	seeded := seededTypes(t)

	if len(seeded) != len(typeIDs) {
		t.Errorf("schema seeds %d event types, code defines %d", len(seeded), len(typeIDs))
	}
	for name, id := range seeded {
		eventType := Type(name)
		gotID, ok := eventType.ID()
		if !ok {
			t.Errorf("schema seeds %q but the code has no such type", name)
			continue
		}
		if gotID != id {
			t.Errorf("%s: code id = %d, schema id = %d", name, gotID, id)
		}
	}
	for eventType, id := range typeIDs {
		if _, ok := seeded[string(eventType)]; !ok {
			t.Errorf("code defines %q (id %d) but the schema does not seed it", eventType, id)
		}
	}
}

func TestTypeIDsRoundTrip(t *testing.T) {
	for _, eventType := range Types() {
		id, ok := eventType.ID()
		if !ok {
			t.Fatalf("%s has no id", eventType)
		}
		got, ok := TypeByID(id)
		if !ok || got != eventType {
			t.Errorf("TypeByID(%d) = %q, %v; want %q", id, got, ok, eventType)
		}
	}
}

func TestTypesAreOrderedByID(t *testing.T) {
	types := Types()
	if len(types) != 15 {
		t.Fatalf("Types() returned %d types, want 15", len(types))
	}
	// The first and last are the ones a reader checks against the schema most
	// often; both are pinned by migration 00002.
	if types[0] != TypeStageStart || types[14] != TypeWaitForInput {
		t.Errorf("Types() spans %q..%q, want stage_start..wait_for_input", types[0], types[14])
	}
	for i := 1; i < len(types); i++ {
		previous, _ := types[i-1].ID()
		current, _ := types[i].ID()
		if current <= previous {
			t.Errorf("Types() is not in id order at %q (id %d after %d)", types[i], current, previous)
		}
	}
}

func TestTypeValidity(t *testing.T) {
	if !TypeContent.Valid() {
		t.Error("content should be valid")
	}
	if Type("").Valid() {
		t.Error("the empty type should not be valid")
	}
	if Type("content_delta").Valid() {
		t.Error("an unlisted type should not be valid")
	}
	if _, ok := TypeByID(0); ok {
		t.Error("id 0 is reserved for unassigned and should not resolve")
	}
	if _, ok := TypeByID(16); ok {
		t.Error("id 16 is past the seeded range and should not resolve")
	}
}
