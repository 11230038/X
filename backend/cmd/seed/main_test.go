package main

import (
	"encoding/json"
	"testing"

	"backend/internal/notebook"
)

// The question list reads options as objects. A seed written in another shape
// is not a harmless fixture: one row it cannot read turns every list request
// into a 500, so the seed's shape is pinned to the bank's own type here.
func TestSeedQuestionOptionsDecodeAsTheBankReadsThem(t *testing.T) {
	var options []notebook.Option
	if err := json.Unmarshal(seedQuestionOptions, &options); err != nil {
		t.Fatalf("unmarshal seed options: %v", err)
	}
	if len(options) != 4 {
		t.Fatalf("options = %d, want 4", len(options))
	}
	want := []notebook.Option{
		{Key: "A", Text: "PRIMARY KEY"},
		{Key: "B", Text: "UNIQUE"},
		{Key: "C", Text: "CHECK"},
		{Key: "D", Text: "DEFAULT"},
	}
	for i, option := range options {
		if option != want[i] {
			t.Errorf("option %d = %+v, want %+v", i, option, want[i])
		}
	}
}
