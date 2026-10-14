package config

import (
	"fmt"
	"strconv"
	"strings"
)

// MemoryConfig holds the six knobs the memory layers have, and four of them are
// the numbers §2.7 calls a product question rather than a technical one.
//
// That is why this file is longer than its share of keys would suggest. The gate
// itself — confidence, corroboration across surfaces, a sensitive set that is
// never admitted automatically — is a mechanism, and the mechanism can be
// written and tested whatever the numbers are. What cannot be decided in code is
// how sure a model has to claim to be, how many independent observations count
// as corroboration, how many surfaces those have to come from, and which kinds
// of fact are never admitted without the user saying so. The defaults here are
// the conservative ones §2.7 proposes; they are defaults and not answers.
type MemoryConfig struct {
	// ConfidenceThreshold is the lowest confidence, 0–100, that passes the first
	// gate. The gate is deliberately the weakest of the three, because a
	// confidence the model reports is the model talking about itself — and a
	// threshold of 0 is a legitimate setting for a deployment that wants to rely
	// on corroboration alone. That is why the bound is a range and not
	// "positive": zero is a choice here, not an omission.
	ConfidenceThreshold int
	// MinCorroboration is how many independent observations one assertion needs
	// before it becomes a fact. Two traces from one turn count once, which is
	// what makes "independent" mean something.
	MinCorroboration int
	// MinSurfaces is how many *different* surfaces those observations must come
	// from. This is the condition MEM-03's word "corroborated" actually rests on:
	// the same thing said again in the same place is repetition, and said in two
	// places it is evidence.
	MinSurfaces int
	// SensitiveKinds is the set of fact kinds that never pass the gate
	// automatically, whatever their confidence and whatever corroborates them.
	// A kind is in the set or it is not; the set is compared case-insensitively
	// so that a deployment writing `Preference` and one writing `preference`
	// mean the same thing.
	SensitiveKinds []string
	// SynthesisWindowDays is how far back L3's "recent" reaches. It is a window
	// over observation time rather than a cap on rows, because what an interface
	// shows as recent is a question about when, not about how many.
	SynthesisWindowDays int
	// MaxFactsPerSurface is how many facts one surface contributes to a
	// synthesis. It is the bound that keeps one talkative surface from being the
	// whole picture.
	MaxFactsPerSurface int
}

// Validate checks the six knobs.
//
// The confidence threshold is the one that has a range rather than a floor, and
// the message says so: a value outside 0–100 is not a stricter or looser
// setting, it is a number that cannot be a percentage. The sensitive set is
// checked for blank entries because a list with one in it is a list somebody was
// still editing, and left alone it would make the empty kind name sensitive —
// a rule about nothing.
func (c MemoryConfig) Validate() error {
	if c.ConfidenceThreshold < 0 || c.ConfidenceThreshold > 100 {
		return fmt.Errorf("MEMORY_CONFIDENCE_THRESHOLD must be between 0 and 100")
	}
	for _, field := range []struct {
		key   string
		value int
	}{
		{"MEMORY_MIN_CORROBORATION", c.MinCorroboration},
		{"MEMORY_MIN_SURFACES", c.MinSurfaces},
		{"MEMORY_SYNTHESIS_WINDOW_DAYS", c.SynthesisWindowDays},
		{"MEMORY_MAX_FACTS_PER_SURFACE", c.MaxFactsPerSurface},
	} {
		if field.value <= 0 {
			return fmt.Errorf("%s must be a positive integer", field.key)
		}
	}
	for _, kind := range c.SensitiveKinds {
		if strings.TrimSpace(kind) == "" {
			return fmt.Errorf("MEMORY_SENSITIVE_KINDS must not carry an empty kind")
		}
	}
	return nil
}

func loadMemoryConfig(values map[string]string) (MemoryConfig, error) {
	threshold, err := parsePercent(values, "MEMORY_CONFIDENCE_THRESHOLD")
	if err != nil {
		return MemoryConfig{}, err
	}
	minCorroboration, err := parsePositiveInt(values, "MEMORY_MIN_CORROBORATION")
	if err != nil {
		return MemoryConfig{}, err
	}
	minSurfaces, err := parsePositiveInt(values, "MEMORY_MIN_SURFACES")
	if err != nil {
		return MemoryConfig{}, err
	}
	windowDays, err := parsePositiveInt(values, "MEMORY_SYNTHESIS_WINDOW_DAYS")
	if err != nil {
		return MemoryConfig{}, err
	}
	maxFacts, err := parsePositiveInt(values, "MEMORY_MAX_FACTS_PER_SURFACE")
	if err != nil {
		return MemoryConfig{}, err
	}
	return MemoryConfig{
		ConfidenceThreshold: threshold,
		MinCorroboration:    minCorroboration,
		MinSurfaces:         minSurfaces,
		SensitiveKinds:      parseKinds(values["MEMORY_SENSITIVE_KINDS"]),
		SynthesisWindowDays: windowDays,
		MaxFactsPerSurface:  maxFacts,
	}, nil
}

// parsePercent reads a key that is a percentage rather than a count, so that the
// refusal names the range it wanted. It is beside the memory section rather than
// in values.go because memory is the only section with such a key, and a helper
// used once is not a rule yet.
func parsePercent(values map[string]string, key string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(values[key]))
	if err != nil || value < 0 || value > 100 {
		return 0, fmt.Errorf("%s must be between 0 and 100", key)
	}
	return value, nil
}

// parseKinds reads a comma-separated set of memory kinds. It is parseList, and
// it is named for its key here because that is the only thing the call site is
// about; the reading itself is shared so that two keys of the same shape cannot
// come to disagree about a trailing comma.
func parseKinds(raw string) []string { return parseList(raw) }
