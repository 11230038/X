package config

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The helpers in this file are what every section file reads its keys with. They
// are deliberately shallow: a key that is absent, blank, or not the shape the key
// names all fail the same way, naming the key and never echoing the value — the
// file may hold secrets, and a failure message is read in places they should not
// reach.

// blankAllowedKeys are the required keys whose empty value is a deployment
// somebody meant. There is one, and it is the exception for one reason: it is a
// list of accounts, and "no account" is a complete answer. Every other key's
// blank is a line the operator meant to finish, and refusing it at startup is
// how the message can name the key rather than the symptom.
var blankAllowedKeys = map[string]bool{
	"SUPER_ADMIN_USER_IDS": true,
}

// requireValues checks that the file carries every key at all. It is breadth, not
// depth: a key that is present but malformed is caught by its own section.
func requireValues(values map[string]string) error {
	for _, key := range requiredKeys {
		value, ok := values[key]
		if !ok {
			return fmt.Errorf("%s is required in the configuration file", key)
		}
		if strings.TrimSpace(value) == "" && !blankAllowedKeys[key] {
			return fmt.Errorf("%s is required in the configuration file", key)
		}
	}
	return nil
}

func parseBool(values map[string]string, key string) (bool, error) {
	value := strings.TrimSpace(values[key])
	if value != "true" && value != "false" {
		return false, fmt.Errorf("%s must be true or false", key)
	}
	return value == "true", nil
}

func parsePort(values map[string]string, key string) (uint16, error) {
	value, err := strconv.ParseUint(strings.TrimSpace(values[key]), 10, 16)
	if err != nil || value == 0 {
		return 0, fmt.Errorf("%s must be between 1 and 65535", key)
	}
	return uint16(value), nil
}

func parsePositiveInt64(values map[string]string, key string) (int64, error) {
	value, err := strconv.ParseInt(strings.TrimSpace(values[key]), 10, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return value, nil
}

func parseNonNegativeInt(values map[string]string, key string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(values[key]))
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", key)
	}
	return value, nil
}

func parsePositiveInt(values map[string]string, key string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(values[key]))
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return value, nil
}

func parsePositiveDuration(values map[string]string, key string) (time.Duration, error) {
	value, err := time.ParseDuration(strings.TrimSpace(values[key]))
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return value, nil
}

func parseNonNegativeDuration(values map[string]string, key string) (time.Duration, error) {
	value, err := time.ParseDuration(strings.TrimSpace(values[key]))
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s must be a non-negative duration", key)
	}
	return value, nil
}

// resolveConfigPath turns a path written in the dotenv file into an absolute
// one, reading a relative value against the file it was written in rather than
// against the process's working directory.
//
// Those two are not the same directory: the server is run from `backend/`, tests
// are run from wherever the developer is, and a deployment may start the binary
// from anywhere at all. A storage root that meant three different directories
// depending on who started the process is a storage root nobody can describe —
// and for the archive root it would also be a second place packages land.
//
// It is one function because there are two such keys now (UPLOAD_ROOT and
// WORKSPACE_ARCHIVE_ROOT) and the rule is subtle enough that two copies of it
// would drift.
func resolveConfigPath(configPath, value string) (string, error) {
	resolved := strings.TrimSpace(value)
	if !filepath.IsAbs(resolved) {
		absoluteConfig, err := filepath.Abs(configPath)
		if err != nil {
			return "", fmt.Errorf("resolve configuration file path")
		}
		resolved = filepath.Join(filepath.Dir(absoluteConfig), resolved)
	}
	resolved, err := filepath.Abs(filepath.Clean(resolved))
	if err != nil {
		return "", fmt.Errorf("resolve %q", value)
	}
	return resolved, nil
}

// parseList reads a comma-separated list.
//
// Entries are trimmed and none is dropped: a list with a trailing comma is a
// list somebody was still editing, and reading `preference,` as `preference`
// would be answering a question the file did not ask — the same rule the query
// parameters follow. Each section's Validate refuses the blank entry by name, so
// the mistake is a startup failure naming the key rather than a value that
// quietly is not one. A blank input is no entries at all rather than one blank
// one, so that a section handed a list it never got says so in those words.
func parseList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	pieces := strings.Split(raw, ",")
	entries := make([]string, 0, len(pieces))
	for _, piece := range pieces {
		entries = append(entries, strings.TrimSpace(piece))
	}
	return entries
}
