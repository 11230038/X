package config

import (
	"strings"
	"testing"
)

func validAuditConfig() AuditConfig {
	return AuditConfig{RetentionDays: 180, MaxPageSize: 200}
}

func TestAuditConfigValidateAcceptsTheShippedShape(t *testing.T) {
	if err := validAuditConfig().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestAuditConfigValidateRejectsInvalidSettings(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*AuditConfig)
		wantKey string
	}{
		{name: "zero retention window", mutate: func(c *AuditConfig) { c.RetentionDays = 0 },
			wantKey: "AUDIT_RETENTION_DAYS"},
		{name: "negative retention window", mutate: func(c *AuditConfig) { c.RetentionDays = -1 },
			wantKey: "AUDIT_RETENTION_DAYS"},
		{name: "zero page cap", mutate: func(c *AuditConfig) { c.MaxPageSize = 0 },
			wantKey: "AUDIT_MAX_PAGE_SIZE"},
		{name: "negative page cap", mutate: func(c *AuditConfig) { c.MaxPageSize = -1 },
			wantKey: "AUDIT_MAX_PAGE_SIZE"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validAuditConfig()
			tt.mutate(&cfg)

			err := cfg.Validate()
			if err == nil {
				t.Fatal("Validate() error = nil, want an error")
			}
			if !strings.Contains(err.Error(), tt.wantKey) {
				t.Fatalf("Validate() error = %v, want one naming %s", err, tt.wantKey)
			}
		})
	}
}

// A window of one day is a deployment that keeps almost nothing, and that is a
// decision an operator is allowed to make: the check is that the number is a
// window, not that it is a long one.
func TestAuditConfigValidateAcceptsAWindowOfOneDay(t *testing.T) {
	cfg := validAuditConfig()
	cfg.RetentionDays = 1

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want a one-day window accepted", err)
	}
}

func TestLoadRejectsMissingAndBlankAuditValues(t *testing.T) {
	for _, tt := range []struct {
		name   string
		key    string
		remove bool
	}{
		{name: "missing retention window", key: "AUDIT_RETENTION_DAYS", remove: true},
		{name: "missing page cap", key: "AUDIT_MAX_PAGE_SIZE", remove: true},
		{name: "blank retention window", key: "AUDIT_RETENTION_DAYS"},
		{name: "blank page cap", key: "AUDIT_MAX_PAGE_SIZE"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			values := validValues()
			if tt.remove {
				delete(values, tt.key)
			} else {
				values[tt.key] = " "
			}

			_, err := Load(writeDotenv(t, values))
			if err == nil || !strings.Contains(err.Error(), tt.key) {
				t.Fatalf("Load() error = %v, want error naming %s", err, tt.key)
			}
		})
	}
}

func TestLoadRejectsInvalidAuditValues(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
	}{
		{name: "retention window as zero", key: "AUDIT_RETENTION_DAYS", value: "0"},
		{name: "retention window as negative", key: "AUDIT_RETENTION_DAYS", value: "-30"},
		{name: "retention window as a word", key: "AUDIT_RETENTION_DAYS", value: "forever"},
		{name: "retention window as a duration", key: "AUDIT_RETENTION_DAYS", value: "180d"},
		{name: "retention window as a fraction", key: "AUDIT_RETENTION_DAYS", value: "180.5"},
		{name: "page cap as zero", key: "AUDIT_MAX_PAGE_SIZE", value: "0"},
		{name: "page cap as negative", key: "AUDIT_MAX_PAGE_SIZE", value: "-1"},
		{name: "page cap as a word", key: "AUDIT_MAX_PAGE_SIZE", value: "two hundred"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			values := validValues()
			values[tt.key] = tt.value

			if _, err := Load(writeDotenv(t, values)); err == nil {
				t.Fatalf("Load() error = nil, want an error for %s", tt.key)
			}
		})
	}
}

// Two positive integers in one file, read by two calls that differ only in
// their key. Swapping them changes nothing about whether the service starts —
// a 180-entry page and a 200-day window are both plausible — so nothing but
// this test notices.
func TestLoadReadsEachAuditBoundFromItsOwnKey(t *testing.T) {
	values := validValues()
	values["AUDIT_RETENTION_DAYS"] = "30"
	values["AUDIT_MAX_PAGE_SIZE"] = "50"

	cfg, err := Load(writeDotenv(t, values))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := AuditConfig{RetentionDays: 30, MaxPageSize: 50}
	if cfg.Audit != want {
		t.Errorf("Audit = %+v, want %+v", cfg.Audit, want)
	}
}

// Neither key is a secret, and this is not a secret test. It is the same rule
// for the same reason as the practice bounds: a message that carries the value
// is a message somebody has to read carefully before pasting it into an issue,
// and the operator needs the key, not what they wrote for it.
func TestLoadNeverEchoesAuditValues(t *testing.T) {
	values := validValues()
	values["AUDIT_MAX_PAGE_SIZE"] = "-424242"

	_, err := Load(writeDotenv(t, values))
	if err == nil {
		t.Fatal("Load() error = nil, want an error")
	}
	if strings.Contains(err.Error(), "424242") {
		t.Fatalf("configuration error echoed the value: %v", err)
	}
}
