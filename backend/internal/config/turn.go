package config

import (
	"fmt"
	"time"
)

// TurnConfig bounds how long a single turn may run and how much work it can do.
type TurnConfig struct {
	// InputTimeout caps how long a turn may sit in waiting_input before it fails
	// with a structured error instead of holding a goroutine indefinitely.
	InputTimeout time.Duration
	// MaxRounds caps agent-loop iterations so a tool-calling model cannot spin.
	MaxRounds int
}

// Validate checks the turn execution bounds.
func (c TurnConfig) Validate() error {
	if c.InputTimeout <= 0 {
		return fmt.Errorf("TURN_INPUT_TIMEOUT must be a positive duration")
	}
	if c.MaxRounds <= 0 {
		return fmt.Errorf("TURN_MAX_ROUNDS must be a positive integer")
	}
	return nil
}

func loadTurnConfig(values map[string]string) (TurnConfig, error) {
	inputTimeout, err := parsePositiveDuration(values, "TURN_INPUT_TIMEOUT")
	if err != nil {
		return TurnConfig{}, err
	}
	maxRounds, err := parsePositiveInt(values, "TURN_MAX_ROUNDS")
	if err != nil {
		return TurnConfig{}, err
	}
	return TurnConfig{InputTimeout: inputTimeout, MaxRounds: maxRounds}, nil
}
