package memory

// The gate, case by case (§2.7). Every case here is a decision about rules and
// none of them touches storage, which is the point of the gate being a pure
// function: the three conditions and the fourth path can be pinned down one at a
// time, and what "corroborated" means is answered in the open rather than
// inferred from rows a database happened to have.

import (
	"testing"
)

// gateOptions are the conservative defaults §5 ships with. A case that wants a
// different deployment says so by changing one field.
func gateOptions() Options {
	return Options{
		ConfidenceThreshold: 60,
		MinCorroboration:    2,
		MinSurfaces:         2,
		SensitiveKinds:      []string{"preference", "health", "relationship", "credential"},
		SynthesisWindowDays: 30,
		MaxFactsPerSurface:  200,
	}
}

// said is an observation from one surface, in one turn.
func said(surface Surface, kind Kind, turn string, confidence int) Observation {
	return Observation{
		OwnerID:    7,
		Surface:    surface,
		Kind:       kind,
		TurnID:     turn,
		Statement:  "对方正在学 Go",
		Confidence: confidence,
	}
}

// heard is the L1 the gate reads: this observation's own row, which Observe
// writes before asking, and the rows that were already there.
func heard(observation Observation, earlier ...Trace) []Trace {
	rows := []Trace{{
		ID:          "mt_this",
		OwnerID:     observation.OwnerID,
		Surface:     observation.Surface,
		Kind:        observation.Kind,
		TurnID:      observation.TurnID,
		Statement:   observation.Statement,
		Fingerprint: Fingerprint(observation.Statement),
	}}
	return append(rows, earlier...)
}

// earlier is one observation that was already in L1.
func earlier(id string, surface Surface, turn string, statement string) Trace {
	return Trace{
		ID:          id,
		OwnerID:     7,
		Surface:     surface,
		TurnID:      turn,
		Statement:   statement,
		Fingerprint: Fingerprint(statement),
	}
}

func TestTheGateHearsAnObservationItDoesNotBelieve(t *testing.T) {
	statement := "对方正在学 Go"

	tests := []struct {
		name string
		// observation is the one in hand.
		observation Observation
		// stored is every L1 row for the fingerprint, this one included.
		stored []Trace
		// admitted is what the gate is expected to say.
		admitted bool
		// observations and surfaces are the counts the decision carries, which is
		// what a held observation is explained with.
		observations int
		surfaces     int
		// corroborationAlone is a deployment that set the confidence threshold to
		// zero, which §5 allows: the four knobs are a product decision and this is
		// one of the settings they can be given.
		corroborationAlone bool
		// because says which condition the case turns on.
		because string
	}{
		{
			name:         "below the confidence line",
			observation:  said(SurfaceMastery, "goal", "turn_1", 59),
			stored:       heard(said(SurfaceMastery, "goal", "turn_1", 59), earlier("mt_1", SurfaceReading, "turn_2", statement)),
			observations: 2, surfaces: 2,
			because: "two surfaces agreeing is not enough when the surface in hand is guessing",
		},
		{
			name:         "confident, but the only one who said it",
			observation:  said(SurfaceMastery, "goal", "turn_1", 90),
			stored:       heard(said(SurfaceMastery, "goal", "turn_1", 90)),
			observations: 1, surfaces: 1,
			because: "one model output on its own is exactly what MEM-03 forbids",
		},
		{
			name:         "said twice in one turn",
			observation:  said(SurfaceMastery, "goal", "turn_1", 90),
			stored:       heard(said(SurfaceMastery, "goal", "turn_1", 90), earlier("mt_1", SurfaceReading, "turn_1", statement)),
			observations: 1, surfaces: 2,
			because: "a model repeating itself in one turn is one voice, however many surfaces the turn wrote through",
		},
		{
			name:         "two turns, one surface",
			observation:  said(SurfaceMastery, "goal", "turn_1", 90),
			stored:       heard(said(SurfaceMastery, "goal", "turn_1", 90), earlier("mt_1", SurfaceMastery, "turn_2", statement)),
			observations: 2, surfaces: 1,
			because: "the same thing said again in the same place is repetition, not corroboration",
		},
		{
			name:         "two turns, two surfaces",
			observation:  said(SurfaceMastery, "goal", "turn_1", 90),
			stored:       heard(said(SurfaceMastery, "goal", "turn_1", 90), earlier("mt_1", SurfaceReading, "turn_2", statement)),
			admitted:     true,
			observations: 2, surfaces: 2,
			because: "crossing surfaces is what 证实 means here",
		},
		{
			name:         "a kind nobody may admit automatically",
			observation:  said(SurfaceMastery, "health", "turn_1", 100),
			stored:       heard(said(SurfaceMastery, "health", "turn_1", 100), earlier("mt_1", SurfaceReading, "turn_2", statement)),
			observations: 2, surfaces: 2,
			because: "a sensitive kind is not harder to admit, it is never admitted here",
		},
		{
			name:         "the user's own word, low confidence and said only once",
			observation:  said(SurfaceUser, KindUserStated, "", 10),
			stored:       heard(said(SurfaceUser, KindUserStated, "", 10)),
			admitted:     true,
			observations: 1, surfaces: 1,
			because: "the fourth path skips all three conditions, and what the user thinks of their own statement is not a model's guess",
		},
		{
			name:         "a surface claiming the user's word",
			observation:  said(SurfaceReading, KindUserStated, "turn_1", 100),
			stored:       heard(said(SurfaceReading, KindUserStated, "turn_1", 100)),
			observations: 1, surfaces: 1,
			because: "the fourth path is the surface and the kind together: a capability that reports the kind is not a user who wrote something",
		},
		{
			name:         "observations with no turn are their own voice",
			observation:  said(SurfaceUser, "goal", "", 90),
			stored:       heard(said(SurfaceUser, "goal", "", 90), earlier("mt_1", SurfaceReading, "", statement)),
			admitted:     true,
			observations: 2, surfaces: 2,
			because: "there is no turn for two turn-less observations to be folded into",
		},
		{
			name:               "a deployment that trusts corroboration alone",
			observation:        said(SurfaceMastery, "goal", "turn_1", 0),
			stored:             heard(said(SurfaceMastery, "goal", "turn_1", 0), earlier("mt_1", SurfaceReading, "turn_2", statement)),
			admitted:           true,
			observations:       2,
			surfaces:           2,
			corroborationAlone: true,
			because:            "a threshold of zero is a setting, not an omission",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := gateOptions()
			if test.corroborationAlone {
				options.ConfidenceThreshold = 0
			}
			decision := decide(test.observation, test.stored, options)
			if decision.admitted != test.admitted {
				t.Fatalf("decide() admitted = %v, want %v (%s)", decision.admitted, test.admitted, test.because)
			}
			if decision.observations != test.observations || decision.surfaces != test.surfaces {
				t.Errorf("decide() counted %d observations from %d surfaces, want %d from %d",
					decision.observations, decision.surfaces, test.observations, test.surfaces)
			}
			if test.admitted {
				if len(decision.sources) != len(test.stored) {
					t.Errorf("decide() rested the fact on %v, want every observation of the fingerprint %d",
						decision.sources, len(test.stored))
				}
				if decision.confidence != test.observation.Confidence {
					t.Errorf("decide() wrote confidence %d, want the admitting observation's %d",
						decision.confidence, test.observation.Confidence)
				}
			}
		})
	}
}

func TestTheGateSaysWhichRouteAdmittedAFact(t *testing.T) {
	options := gateOptions()
	statement := "对方正在学 Go"

	byGate := decide(said(SurfaceMastery, "goal", "turn_1", 90),
		heard(said(SurfaceMastery, "goal", "turn_1", 90), earlier("mt_1", SurfaceReading, "turn_2", statement)), options)
	if byGate.confirmedBy != ConfirmerGate {
		t.Errorf("the gate admitted a corroborated observation as %q", byGate.confirmedBy)
	}
	if byGate.sensitive {
		t.Error("the gate marked a goal as sensitive")
	}

	user := said(SurfaceUser, KindUserStated, "", 100)
	byUser := decide(user, heard(user), options)
	if byUser.confirmedBy != ConfirmerUser {
		t.Errorf("the user's own word came in as %q", byUser.confirmedBy)
	}
	// Nothing the user writes through this path is marked sensitive, and that is
	// a gap rather than a decision: §2.7 fixes the fourth path's kind to
	// `user_stated`, so there is no topic kind left for the sensitive list to
	// match — the fact's `sensitive` column can only ever be false while that is
	// so. Recorded here so that the day a user-written fact carries a topic, this
	// assertion is what says what changed.
	if byUser.sensitive {
		t.Error("a fact written by the user came in marked sensitive; the kind it was written about cannot reach the gate yet")
	}
}

func TestASensitiveKindIsComparedLooselyEnoughToBeUsable(t *testing.T) {
	options := gateOptions()
	for _, kind := range []string{"preference", "Preference", " preference ", "PREFERENCE"} {
		if !options.IsSensitive(kind) {
			t.Errorf("IsSensitive(%q) = false, want true", kind)
		}
	}
	for _, kind := range []string{"goal", "preferences", "", "user_stated"} {
		if options.IsSensitive(kind) {
			t.Errorf("IsSensitive(%q) = true, want false", kind)
		}
	}
}
