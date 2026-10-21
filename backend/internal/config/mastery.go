package config

import (
	"fmt"
	"time"
)

// MasteryConfig holds the four knobs a mastery path has, and there are four
// because everything else about a path is an algorithm rather than a setting
// (§5, V3 §5's rule): how many right answers promote a point, what a level means
// with little evidence, how long a claim stands, and how long a question may
// wait are the questions whose answers are genuinely choices.
//
// How many paths a person may hold at once used to be a fourth. It is gone
// rather than answered: the key was required, validated and wired, and it
// refused nothing — the count it was compared against was read only when the
// conversation was on no path at all (V4设计方案 §0.3 第 10 条). A limit that
// fires has to say what it counts and what it refuses, and nothing here has said
// so.
//
// The other half of the split is worth naming too, because it is what keeps this
// file short: the *closed set* of an observation's result and the arithmetic of
// the review curve are not here. A deployment that could change what "correct"
// means could quietly make every stored evidence mean something else.
type MasteryConfig struct {
	// LeaseTTL is how long a turn's claim on a path stands before another turn
	// may take it over. It is the one key here whose value is about people rather
	// than about the domain: too short and two tabs fight over a path a turn is
	// still working on, too long and a turn that died holds it while a user
	// waits.
	LeaseTTL time.Duration
	// QuestionTTL is how long one question may stay open on a path. A path
	// allows one open question, so an abandoned one blocks every question after
	// it until this runs out; it is the bound that keeps a learner walking away
	// from costing the rest of the path.
	QuestionTTL time.Duration
	// GateConsecutive is how many right answers in a row promote a knowledge
	// point to mastered.
	GateConsecutive int
	// GateMinEvidence is how many observations a knowledge point needs before
	// the gate will call it anything but unknown — the threshold that keeps "not
	// tested yet" from being reported as "weak".
	GateMinEvidence int
}

// Validate checks the mastery thresholds.
//
// All three are positive rules and none of them is a range, which is the shape
// the domain imposes: a zero lease would be a lease that expires before it is
// written, a zero consecutive threshold would promote on no evidence, and a zero
// evidence floor would make every untested knowledge point weak.
func (c MasteryConfig) Validate() error {
	if c.LeaseTTL <= 0 {
		return fmt.Errorf("MASTERY_LEASE_TTL must be a positive duration")
	}
	if c.QuestionTTL <= 0 {
		return fmt.Errorf("MASTERY_QUESTION_TTL must be a positive duration")
	}
	for _, field := range []struct {
		key   string
		value int
	}{
		{"MASTERY_GATE_CONSECUTIVE", c.GateConsecutive},
		{"MASTERY_GATE_MIN_EVIDENCE", c.GateMinEvidence},
	} {
		if field.value <= 0 {
			return fmt.Errorf("%s must be a positive integer", field.key)
		}
	}
	return nil
}

func loadMasteryConfig(values map[string]string) (MasteryConfig, error) {
	leaseTTL, err := parsePositiveDuration(values, "MASTERY_LEASE_TTL")
	if err != nil {
		return MasteryConfig{}, err
	}
	questionTTL, err := parsePositiveDuration(values, "MASTERY_QUESTION_TTL")
	if err != nil {
		return MasteryConfig{}, err
	}
	gateConsecutive, err := parsePositiveInt(values, "MASTERY_GATE_CONSECUTIVE")
	if err != nil {
		return MasteryConfig{}, err
	}
	gateMinEvidence, err := parsePositiveInt(values, "MASTERY_GATE_MIN_EVIDENCE")
	if err != nil {
		return MasteryConfig{}, err
	}
	return MasteryConfig{
		LeaseTTL:        leaseTTL,
		QuestionTTL:     questionTTL,
		GateConsecutive: gateConsecutive,
		GateMinEvidence: gateMinEvidence,
	}, nil
}
