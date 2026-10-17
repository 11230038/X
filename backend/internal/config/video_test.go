package config

import (
	"strings"
	"testing"
	"time"
)

func validVideoConfig() VideoConfig {
	return VideoConfig{
		BaseURL:  "https://api.bilibili.com",
		Timeout:  10 * time.Second,
		CacheTTL: 10 * time.Minute,
	}
}

func TestVideoConfigValidateAcceptsTheShippedShape(t *testing.T) {
	if err := validVideoConfig().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestVideoConfigValidateRejectsInvalidSettings(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*VideoConfig)
		key    string
	}{
		{name: "a relative base URL", mutate: func(c *VideoConfig) { c.BaseURL = "api.bilibili.com" },
			key: "BILIBILI_BASE_URL"},
		{name: "a base URL without a host", mutate: func(c *VideoConfig) { c.BaseURL = "https://" },
			key: "BILIBILI_BASE_URL"},
		{name: "a non-HTTP scheme", mutate: func(c *VideoConfig) { c.BaseURL = "ftp://api.bilibili.com" },
			key: "BILIBILI_BASE_URL"},
		{name: "a zero timeout", mutate: func(c *VideoConfig) { c.Timeout = 0 }, key: "BILIBILI_TIMEOUT"},
		{name: "a negative timeout", mutate: func(c *VideoConfig) { c.Timeout = -time.Second },
			key: "BILIBILI_TIMEOUT"},
		{name: "a zero cache TTL", mutate: func(c *VideoConfig) { c.CacheTTL = 0 },
			key: "BILIBILI_CACHE_TTL"},
		{name: "a negative cache TTL", mutate: func(c *VideoConfig) { c.CacheTTL = -time.Minute },
			key: "BILIBILI_CACHE_TTL"},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			cfg := validVideoConfig()
			test.mutate(&cfg)

			err := cfg.Validate()
			if err == nil {
				t.Fatal("Validate() error = nil, want an error")
			}
			if !strings.Contains(err.Error(), test.key) {
				t.Fatalf("Validate() error = %q, want it to name %s", err, test.key)
			}
		})
	}
}

func TestLoadTrimsTheVideoBaseURLTrailingSlash(t *testing.T) {
	values := validValues()
	values["BILIBILI_BASE_URL"] = "https://api.bilibili.com/"

	cfg, err := Load(writeDotenv(t, values))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The client appends /x/web-interface..., which would otherwise become
	// //x/web-interface...
	if cfg.Video.BaseURL != "https://api.bilibili.com" {
		t.Fatalf("Video.BaseURL = %q", cfg.Video.BaseURL)
	}
}

func TestAVideoKeyIsNotADefault(t *testing.T) {
	for _, key := range []string{"BILIBILI_BASE_URL", "BILIBILI_TIMEOUT", "BILIBILI_CACHE_TTL"} {
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
