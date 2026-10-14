package config

import (
	"strings"
	"testing"
)

func validUsageConfig() UsageConfig {
	return UsageConfig{RetentionDays: 365}
}

func TestUsageConfigValidateAcceptsTheShippedShape(t *testing.T) {
	if err := validUsageConfig().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestUsageConfigValidateRejectsInvalidSettings(t *testing.T) {
	for _, tt := range []struct {
		name         string
		retentionDay int
	}{
		{name: "zero retention window", retentionDay: 0},
		{name: "negative retention window", retentionDay: -1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := UsageConfig{RetentionDays: tt.retentionDay}

			err := cfg.Validate()
			if err == nil {
				t.Fatal("Validate() error = nil, want an error")
			}
			if !strings.Contains(err.Error(), "USAGE_RETENTION_DAYS") {
				t.Fatalf("Validate() error = %v, want one naming USAGE_RETENTION_DAYS", err)
			}
		})
	}
}

func TestLoadRejectsMissingAndBlankUsageValues(t *testing.T) {
	for _, tt := range []struct {
		name   string
		remove bool
	}{
		{name: "missing retention window", remove: true},
		{name: "blank retention window"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			values := validValues()
			if tt.remove {
				delete(values, "USAGE_RETENTION_DAYS")
			} else {
				values["USAGE_RETENTION_DAYS"] = " "
			}

			_, err := Load(writeDotenv(t, values))
			if err == nil || !strings.Contains(err.Error(), "USAGE_RETENTION_DAYS") {
				t.Fatalf("Load() error = %v, want error naming USAGE_RETENTION_DAYS", err)
			}
		})
	}
}

func TestLoadRejectsInvalidUsageValues(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value string
	}{
		{name: "retention window as zero", value: "0"},
		{name: "retention window as negative", value: "-365"},
		{name: "retention window as a word", value: "forever"},
		{name: "retention window as a duration", value: "365d"},
		{name: "retention window as a fraction", value: "365.5"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			values := validValues()
			values["USAGE_RETENTION_DAYS"] = tt.value

			if _, err := Load(writeDotenv(t, values)); err == nil {
				t.Fatal("Load() error = nil, want an error")
			}
		})
	}
}

// The window is read from its own key and reaches its own section. A missing
// wiring line would leave the field at zero, which is a retention window of
// "delete everything" — and Validate refuses zero, so the only way this test
// can fail is if the value was read into the wrong place.
func TestLoadReadsTheUsageWindowFromItsOwnKey(t *testing.T) {
	values := validValues()
	values["USAGE_RETENTION_DAYS"] = "90"

	cfg, err := Load(writeDotenv(t, values))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Usage.RetentionDays != 90 {
		t.Errorf("Usage.RetentionDays = %d, want 90", cfg.Usage.RetentionDays)
	}
	if cfg.Audit.RetentionDays == 90 {
		t.Error("the usage window was written into the audit's")
	}
}

// The same rule as the audit's window: the message names the key and not what
// the operator wrote for it, because the operator needs the key.
func TestLoadNeverEchoesTheUsageWindow(t *testing.T) {
	values := validValues()
	values["USAGE_RETENTION_DAYS"] = "-424242"

	_, err := Load(writeDotenv(t, values))
	if err == nil {
		t.Fatal("Load() error = nil, want an error")
	}
	if strings.Contains(err.Error(), "424242") {
		t.Fatalf("configuration error echoed the value: %v", err)
	}
}
