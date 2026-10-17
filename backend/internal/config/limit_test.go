package config

import (
	"strings"
	"testing"
)

// The rate limit section (§2.10). What the numbers mean is internal/limit's
// business; what is settled here is that a rate may be zero and the burst may
// not, and that the file names which of the five is wrong.

func TestLoadReadsTheRateLimits(t *testing.T) {
	values := validValues()
	values["RATE_LIMIT_MODEL_PER_MINUTE"] = "1"
	values["RATE_LIMIT_RETRIEVAL_PER_MINUTE"] = "0"
	values["RATE_LIMIT_UPLOAD_PER_MINUTE"] = "30"
	values["RATE_LIMIT_SEARCH_PER_MINUTE"] = "20"
	values["RATE_LIMIT_BURST"] = "1"

	cfg, err := Load(writeDotenv(t, values))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := LimitConfig{
		ModelPerMinute: 1, RetrievalPerMinute: 0, UploadPerMinute: 30,
		SearchPerMinute: 20, Burst: 1,
	}
	if cfg.Limit != want {
		t.Errorf("Limit = %+v, want %+v", cfg.Limit, want)
	}
}

// TestARateOfZeroIsAccepted: zero is a deployment saying "do not bound this",
// and it is the one value in this section that is not a mistake (the design's
// §23.5 第 25 条 threshold is unset). A burst of zero is the opposite — it is
// every one of the four refused, which is an outage written by hand.
func TestARateOfZeroIsAccepted(t *testing.T) {
	values := validValues()
	values["RATE_LIMIT_MODEL_PER_MINUTE"] = "0"
	values["RATE_LIMIT_RETRIEVAL_PER_MINUTE"] = "0"
	values["RATE_LIMIT_UPLOAD_PER_MINUTE"] = "0"
	values["RATE_LIMIT_SEARCH_PER_MINUTE"] = "0"
	values["RATE_LIMIT_BURST"] = "1"

	cfg, err := Load(writeDotenv(t, values))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Limit.ModelPerMinute != 0 {
		t.Errorf("model rate = %d, want 0", cfg.Limit.ModelPerMinute)
	}
}

func TestLoadRejectsRateLimitsItCouldNotBound(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "a negative model rate", key: "RATE_LIMIT_MODEL_PER_MINUTE", value: "-1"},
		{name: "a negative retrieval rate", key: "RATE_LIMIT_RETRIEVAL_PER_MINUTE", value: "-1"},
		{name: "a negative upload rate", key: "RATE_LIMIT_UPLOAD_PER_MINUTE", value: "-1"},
		{name: "a negative search rate", key: "RATE_LIMIT_SEARCH_PER_MINUTE", value: "-1"},
		{name: "a rate that is not a number", key: "RATE_LIMIT_MODEL_PER_MINUTE", value: "fast"},
		{name: "no burst at all", key: "RATE_LIMIT_BURST", value: "0"},
		{name: "a negative burst", key: "RATE_LIMIT_BURST", value: "-1"},
		{name: "a burst that is not a number", key: "RATE_LIMIT_BURST", value: "some"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := validValues()
			values[tt.key] = tt.value
			_, err := Load(writeDotenv(t, values))
			if err == nil {
				t.Fatalf("Load() error = nil, want an error for %s", tt.key)
			}
			if !strings.Contains(err.Error(), tt.key) {
				t.Errorf("error = %q, want it to name %s", err, tt.key)
			}
		})
	}
}

// TestAMissingRateLimitKeyIsNotADefault: the design's table gives these five
// defaults, and this build still refuses to start without them. The behaviour
// is the same — the value in .env.example is the design's own — and the
// difference is that a deployment which loses the key finds out at startup
// rather than quietly running at a number nobody wrote down. Every other
// section of this configuration works that way, and §0.2's "no silent
// defaults" is the reason.
func TestAMissingRateLimitKeyIsNotADefault(t *testing.T) {
	for _, key := range []string{
		"RATE_LIMIT_MODEL_PER_MINUTE",
		"RATE_LIMIT_RETRIEVAL_PER_MINUTE",
		"RATE_LIMIT_UPLOAD_PER_MINUTE",
		"RATE_LIMIT_SEARCH_PER_MINUTE",
		"RATE_LIMIT_BURST",
	} {
		t.Run(key, func(t *testing.T) {
			values := validValues()
			delete(values, key)
			_, err := Load(writeDotenv(t, values))
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("Load() error = %v, want an error naming %s", err, key)
			}
		})
	}
}
