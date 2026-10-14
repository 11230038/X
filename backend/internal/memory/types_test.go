package memory

// What a statement's identity is, and what an observation has to be before
// anything is done with it. Both are decisions about the domain's own values,
// which is why they are here and not beside a database: a fingerprint is the
// rule that decides what "the same thing" means, and it is checked against
// itself rather than against a row.

import (
	"strings"
	"testing"
)

func TestAFingerprintFoldsWhitespaceAndNothingElse(t *testing.T) {
	tests := []struct {
		name   string
		left   string
		right  string
		same   bool
		reason string
	}{
		{
			name: "the same sentence with a newline in it", left: "对方正在学 Go 语言",
			right: "对方正在学\nGo 语言", same: true,
			reason: "the case that actually happens: a model wraps a line where the last one had a space",
		},
		{
			name: "the same sentence with the spacing thrown away", left: "对方正在学 Go",
			right: "  对方正在学   Go  ", same: true,
			reason: "outer whitespace is not part of what was said",
		},
		{
			name: "a different capitalisation", left: "prefers Go", right: "Prefers Go", same: false,
			reason: "folding case would merge two statements that a reader would not",
		},
		{
			name: "a different punctuation", left: "prefers Go", right: "prefers Go.", same: false,
			reason: "the same trade as case: a duplicate is cheaper than a wrong merge",
		},
		{
			name: "a different sentence", left: "prefers Go", right: "prefers Rust", same: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			left, right := Fingerprint(test.left), Fingerprint(test.right)
			if (left == right) != test.same {
				t.Errorf("Fingerprint(%q) = %s and Fingerprint(%q) = %s; want same = %v (%s)",
					test.left, left, test.right, right, test.same, test.reason)
			}
			// The column is CHAR(64) with a REGEXP over lowercase hex, so the
			// shape is a storage contract as well as a domain one.
			if len(left) != 64 || strings.ToLower(left) != left {
				t.Errorf("Fingerprint(%q) = %q, which is not 64 lowercase hex characters", test.left, left)
			}
		})
	}
}

func TestAnObservationIsNotStoredUnlessItSaysWhatItIs(t *testing.T) {
	good := Observation{
		OwnerID: 7, Surface: SurfaceMastery, Kind: "goal",
		Statement: "对方正在学 Go", Confidence: 80,
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("a well-formed observation was refused: %v", err)
	}

	tests := []struct {
		name   string
		change func(Observation) Observation
	}{
		{"no owner", func(o Observation) Observation { o.OwnerID = 0; return o }},
		{"a surface nobody writes", func(o Observation) Observation { o.Surface = "telepathy"; return o }},
		{"an empty surface", func(o Observation) Observation { o.Surface = ""; return o }},
		{"a blank kind", func(o Observation) Observation { o.Kind = "  "; return o }},
		{"a blank statement", func(o Observation) Observation { o.Statement = " \n "; return o }},
		{"a confidence above a hundred", func(o Observation) Observation { o.Confidence = 101; return o }},
		{"a negative confidence", func(o Observation) Observation { o.Confidence = -1; return o }},
		{"a payload that is not JSON", func(o Observation) Observation { o.Payload = []byte("not json"); return o }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.change(good).Validate(); err == nil {
				t.Error("Validate() accepted an observation the domain cannot store")
			}
		})
	}
}

func TestTheClosedSetsStayClosed(t *testing.T) {
	for _, surface := range []Surface{SurfaceMastery, SurfaceReading, SurfacePractice, SurfaceUser} {
		if !surface.Valid() {
			t.Errorf("Surface(%q).Valid() = false", surface)
		}
	}
	if Surface("telepathy").Valid() {
		t.Error(`Surface("telepathy").Valid() = true`)
	}

	for _, status := range []Status{StatusActive, StatusRetracted, StatusSuperseded} {
		if !status.Valid() {
			t.Errorf("Status(%q).Valid() = false", status)
		}
	}
	if Status("deleted").Valid() {
		t.Error(`Status("deleted").Valid() = true: a fact is never deleted, it is retracted or merged`)
	}

	for _, confirmer := range []Confirmer{ConfirmerGate, ConfirmerUser} {
		if !confirmer.Valid() {
			t.Errorf("Confirmer(%q).Valid() = false", confirmer)
		}
	}
	if Confirmer("model").Valid() {
		t.Error(`Confirmer("model").Valid() = true: a model is not a route into L2`)
	}

	for _, action := range []Action{ActionFactStated, ActionFactEdited, ActionFactRetracted,
		ActionFactRestored, ActionFactMerged, ActionFactPurged, ActionMemoryExported} {
		if !action.Valid() {
			t.Errorf("Action(%q).Valid() = false", action)
		}
	}
	if Action("fact.deleted").Valid() {
		t.Error(`Action("fact.deleted").Valid() = true: MEM-02 asks for retracting and cleaning, not deleting`)
	}

	for _, result := range []Result{ResultDone, ResultPartial} {
		if !result.Valid() {
			t.Errorf("Result(%q).Valid() = false", result)
		}
	}

	for _, kind := range []TargetKind{TargetFact, TargetMemory} {
		if !kind.Valid() {
			t.Errorf("TargetKind(%q).Valid() = false", kind)
		}
	}
}

func TestOnlyAnActiveFactIsInForce(t *testing.T) {
	if !(Fact{Status: StatusActive}).InForce() {
		t.Error("an active fact is not in force")
	}
	for _, status := range []Status{StatusRetracted, StatusSuperseded} {
		if (Fact{Status: status}).InForce() {
			t.Errorf("a %s fact is in force", status)
		}
	}
}
