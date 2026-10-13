package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The helpers in this file are what every section file reads its keys with. They
// are deliberately shallow: a key that is absent, blank, or not the shape the key
// names all fail the same way, naming the key and never echoing the value — the
// file may hold secrets, and a failure message is read in places they should not
// reach.

// requireValues checks that the file carries every key at all. It is breadth, not
// depth: a key that is present but malformed is caught by its own section.
func requireValues(values map[string]string) error {
	for _, key := range requiredKeys {
		value, ok := values[key]
		if !ok || strings.TrimSpace(value) == "" {
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
