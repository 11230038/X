package config

import (
	"fmt"
	"time"
)

// MasteryConfig holds the four knobs a mastery path has, and there are four
// because everything else about a path is an algorithm rather than a setting
// (§5, V3 §5's rule): how many right answers promote a point, what a level means
// with little evidence, how long a claim stands, and how many paths a person may
// hold at once are the four questions whose answers are genuinely choices.
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
	// GateConsecutive is how many right answers in a row promote a knowledge
	// point to mastered.
	GateConsecutive int
	// GateMinEvidence is how many observations a knowledge point needs before
	// the gate will call it anything but unknown — the threshold that keeps "not
	// tested yet" from being reported as "weak".
	GateMinEvidence int
	// MaxActivePaths is how many paths one conversation may be in the middle of
	// at once. It bounds *starting* a path, not having one: a turn arriving on a
	// path it already has is never refused, however many it has.
	MaxActivePaths int
}

// Validate checks the mastery thresholds.
//
// All four are positive-integer rules and none of them is a range, which is the
// shape the domain imposes: a zero lease would be a lease that expires before it
// is written, a zero consecutive threshold would promote on no evidence, and a
// zero evidence floor would make every untested knowledge point weak.
func (c MasteryConfig) Validate() error {
	if c.LeaseTTL <= 0 {
		return fmt.Errorf("MASTERY_LEASE_TTL must be a positive duration")
	}
	for _, field := range []struct {
		key   string
		value int
	}{
		{"MASTERY_GATE_CONSECUTIVE", c.GateConsecutive},
		{"MASTERY_GATE_MIN_EVIDENCE", c.GateMinEvidence},
		{"MASTERY_MAX_ACTIVE_PATHS", c.MaxActivePaths},
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
	gateConsecutive, err := parsePositiveInt(values, "MASTERY_GATE_CONSECUTIVE")
	if err != nil {
		return MasteryConfig{}, err
	}
	gateMinEvidence, err := parsePositiveInt(values, "MASTERY_GATE_MIN_EVIDENCE")
	if err != nil {
		return MasteryConfig{}, err
	}
	maxActivePaths, err := parsePositiveInt(values, "MASTERY_MAX_ACTIVE_PATHS")
	if err != nil {
		return MasteryConfig{}, err
	}
	return MasteryConfig{
		LeaseTTL:        leaseTTL,
		GateConsecutive: gateConsecutive,
		GateMinEvidence: gateMinEvidence,
		MaxActivePaths:  maxActivePaths,
	}, nil
}
