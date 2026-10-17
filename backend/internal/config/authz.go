package config

import (
	"fmt"
	"strconv"
	"strings"
)

// AuthzConfig holds the shape of a new account and of a learner's baseline
// (§5).
//
// It holds four keys and none of them is a rule. The two lists say *which*
// capabilities and surfaces a learner starts with; the floor underneath them —
// what no grant may take away — is a constant in authz/policy.go and is
// deliberately not here, because a floor read from a file is a floor one
// mistyped line removes, and its absence reads as "no restriction" rather than
// as an error. Configuration can only add to a learner's reach (§2.2).
//
// SUPER_ADMIN_USER_IDS is not a fourth authorization layer: it names the
// accounts that may perform the four account-lifecycle operations, and it is
// read by a route gate of its own rather than by authz.Decide. Roles stay the
// two this build knows.
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
	// SuperAdminUserIDs are the accounts allowed to create, delete, disable and
	// reset the password of other accounts. Empty means no account is one, and
	// an empty list is a deployment that meant it: fail closed rather than
	// guessing which administrator should have the extra power.
	SuperAdminUserIDs []int64
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
	superAdmins, err := parseIDList("SUPER_ADMIN_USER_IDS", values["SUPER_ADMIN_USER_IDS"])
	if err != nil {
		return AuthzConfig{}, err
	}
	return AuthzConfig{
		DefaultPreset:       strings.TrimSpace(values["AUTHZ_DEFAULT_PRESET"]),
		LearnerCapabilities: parseList(values["AUTHZ_LEARNER_CAPABILITIES"]),
		LearnerSurfaces:     parseList(values["AUTHZ_LEARNER_SURFACES"]),
		SuperAdminUserIDs:   superAdmins,
	}, nil
}

// parseIDList reads a list of account ids. An empty value is no accounts.
//
// The three refusals are one rule with three faces: a value here is a grant of
// power over every other account, so a list that is being edited (`7,`), is not
// numeric (`admin`), or names an account that cannot exist (`0`, `-1`) is a
// sentence the deployment did not mean to write. A repeated id is refused for
// the same reason a blank entry is: it says nothing a single id does not, and
// accepting it would make the file say two things where its reader expects one.
func parseIDList(key, raw string) ([]int64, error) {
	entries := parseList(raw)
	if len(entries) == 0 {
		return nil, nil
	}
	ids := make([]int64, 0, len(entries))
	seen := make(map[int64]struct{}, len(entries))
	for _, entry := range entries {
		if entry == "" {
			return nil, fmt.Errorf("%s must not contain a blank entry", key)
		}
		id, err := strconv.ParseInt(entry, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("%s must be a comma-separated list of positive account ids", key)
		}
		if _, repeated := seen[id]; repeated {
			return nil, fmt.Errorf("%s names account %d more than once", key, id)
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
}
