package config

import (
	"fmt"
	"strings"
)

// AuthzConfig holds the shape of a new account and of a learner's baseline
// (§5).
//
// It holds three keys and none of them is a rule. The two lists say *which*
// capabilities and surfaces a learner starts with; the floor underneath them —
// what no grant may take away — is a constant in authz/policy.go and is
// deliberately not here, because a floor read from a file is a floor one
// mistyped line removes, and its absence reads as "no restriction" rather than
// as an error. Configuration can only add to a learner's reach (§2.2).
//
// The values are validated for shape here and for *meaning* where the process
// is assembled: whether a capability name is one this build registers, and
// whether a surface name is one it has a route for, are questions about the
// build that a settings file cannot answer about itself.
type AuthzConfig struct {
	// DefaultPreset is what a newly registered account is given. New accounts
	// register as ordinary users, and which baseline an ordinary user starts
	// from is a deployment's choice rather than a property of the model.
	DefaultPreset string
	// LearnerCapabilities are the capabilities a learner's baseline contains
	// beyond the floor.
	LearnerCapabilities []string
	// LearnerSurfaces are the surfaces a learner's baseline contains beyond the
	// floor.
	LearnerSurfaces []string
}

// Validate checks each list is non-empty and free of blanks.
//
// A blank entry is refused rather than dropped, the same way MEMORY_SENSITIVE_
// KINDS refuses one: a list with a trailing comma is a list somebody was still
// editing, and reading `chat,` as `chat` would answer a question the file did
// not ask. An empty list is refused too — a learner whose baseline is the empty
// set is a learner who may reach nothing, which is §2.2's "the policy may not be
// emptied" arrived at from the other direction.
func (c AuthzConfig) Validate() error {
	if strings.TrimSpace(c.DefaultPreset) == "" {
		return fmt.Errorf("AUTHZ_DEFAULT_PRESET must not be blank")
	}
	for _, field := range []struct {
		key    string
		values []string
	}{
		{"AUTHZ_LEARNER_CAPABILITIES", c.LearnerCapabilities},
		{"AUTHZ_LEARNER_SURFACES", c.LearnerSurfaces},
	} {
		if len(field.values) == 0 {
			return fmt.Errorf("%s must name at least one entry", field.key)
		}
		if err := rejectBlanks(field.key, field.values); err != nil {
			return err
		}
	}
	return nil
}

func rejectBlanks(key string, values []string) error {
	for _, value := range values {
		if value == "" {
			return fmt.Errorf("%s must not contain a blank entry", key)
		}
	}
	return nil
}

func loadAuthzConfig(values map[string]string) (AuthzConfig, error) {
	return AuthzConfig{
		DefaultPreset:       strings.TrimSpace(values["AUTHZ_DEFAULT_PRESET"]),
		LearnerCapabilities: parseList(values["AUTHZ_LEARNER_CAPABILITIES"]),
		LearnerSurfaces:     parseList(values["AUTHZ_LEARNER_SURFACES"]),
	}, nil
}
