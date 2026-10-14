package config

import (
	"strings"
	"testing"
)

func validUsageConfig() UsageConfig {
	// The shipped shape: a year of retention, and the quota pair as .env.example
	// writes it — the daily limit left at zero, because §23.5 第 25 条 has not
	// chosen one, and the monthly one set, which is what a deployment that has
	// chosen one of the two looks like.
	return UsageConfig{RetentionDays: 365, MonthlyTokensPerAccount: 5000000}
}

func TestUsageConfigValidateAcceptsTheShippedShape(t *testing.T) {
	if err := validUsageConfig().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	// And the shape nothing is set in: a deployment that bounds neither period
	// is a deployment the design says must start.
	if err := (UsageConfig{RetentionDays: 365}).Validate(); err != nil {
		t.Errorf("Validate() error = %v, want a section with no quotas accepted", err)
	}
}

func TestUsageConfigValidateRejectsInvalidSettings(t *testing.T) {
	for _, tt := range []struct {
		name string
		cfg  UsageConfig
		key  string
	}{
		{name: "zero retention window", cfg: UsageConfig{}, key: "USAGE_RETENTION_DAYS"},
		{name: "negative retention window", cfg: UsageConfig{RetentionDays: -1}, key: "USAGE_RETENTION_DAYS"},
		{
			name: "negative daily calls",
			cfg:  UsageConfig{RetentionDays: 365, DailyCallsPerAccount: -1},
			key:  "USAGE_DAILY_CALLS_PER_ACCOUNT",
		},
		{
			name: "negative monthly tokens",
			cfg:  UsageConfig{RetentionDays: 365, MonthlyTokensPerAccount: -1},
			key:  "USAGE_MONTHLY_TOKENS_PER_ACCOUNT",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if err == nil {
				t.Fatal("Validate() error = nil, want an error")
			}
			if !strings.Contains(err.Error(), tt.key) {
				t.Fatalf("Validate() error = %v, want one naming %s", err, tt.key)
			}
		})
	}
}

func TestLoadRejectsMissingAndBlankUsageValues(t *testing.T) {
	for _, tt := range []struct {
		key    string
		remove bool
	}{
		{key: "USAGE_RETENTION_DAYS", remove: true},
		{key: "USAGE_RETENTION_DAYS"},
		{key: "USAGE_DAILY_CALLS_PER_ACCOUNT", remove: true},
		{key: "USAGE_DAILY_CALLS_PER_ACCOUNT"},
		{key: "USAGE_MONTHLY_TOKENS_PER_ACCOUNT", remove: true},
		{key: "USAGE_MONTHLY_TOKENS_PER_ACCOUNT"},
	} {
		t.Run(tt.key+" missing or blank", func(t *testing.T) {
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

// The two quota keys are two integers of the same type, so the mistake worth a
// case is not that one of them is unreadable but that they are read into each
// other — which no failure would report: a deployment that meant to allow a
// million tokens a month and allowed a million calls a day would work.
func TestLoadReadsTheQuotasFromTheirOwnKeys(t *testing.T) {
	values := validValues()
	values["USAGE_DAILY_CALLS_PER_ACCOUNT"] = "7"
	values["USAGE_MONTHLY_TOKENS_PER_ACCOUNT"] = "11"

	cfg, err := Load(writeDotenv(t, values))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Usage.DailyCallsPerAccount != 7 || cfg.Usage.MonthlyTokensPerAccount != 11 {
		t.Errorf("Usage = %+v, want 7 calls a day and 11 tokens a month",
			cfg.Usage)
	}
}

// §2.9: zero is no limit at all, and it is what a deployment that has not
// decided starts from. The two are read as zeros and the process starts, which
// is the difference between this section and every other one in this package.
func TestLoadAcceptsNoQuotaAtAll(t *testing.T) {
	values := validValues()
	values["USAGE_DAILY_CALLS_PER_ACCOUNT"] = "0"
	values["USAGE_MONTHLY_TOKENS_PER_ACCOUNT"] = "0"

	cfg, err := Load(writeDotenv(t, values))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Usage.DailyCallsPerAccount != 0 || cfg.Usage.MonthlyTokensPerAccount != 0 {
		t.Errorf("Usage = %+v, want both limits at zero", cfg.Usage)
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
		{name: "daily quota as negative", value: "-1"},
		{name: "daily quota as a word", value: "many"},
		{name: "daily quota as a fraction", value: "2.5"},
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
