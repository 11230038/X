package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("APP_ENV", "")
	if err := os.Unsetenv("SHUTDOWN_TIMEOUT"); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.HTTPAddr != defaultHTTPAddr {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, defaultHTTPAddr)
	}
	if cfg.AppEnv != defaultAppEnv {
		t.Errorf("AppEnv = %q, want %q", cfg.AppEnv, defaultAppEnv)
	}
	if cfg.ShutdownTimeout != defaultShutdownTimeout {
		t.Errorf("ShutdownTimeout = %s, want %s", cfg.ShutdownTimeout, defaultShutdownTimeout)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("HTTP_ADDR", "127.0.0.1:9090")
	t.Setenv("APP_ENV", "production")
	t.Setenv("SHUTDOWN_TIMEOUT", "2s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.HTTPAddr != "127.0.0.1:9090" || cfg.AppEnv != "production" || cfg.ShutdownTimeout != 2*time.Second {
		t.Fatalf("Load() = %+v, want configured values", cfg)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
	}{
		{name: "address", key: "HTTP_ADDR", value: "localhost"},
		{name: "timeout", key: "SHUTDOWN_TIMEOUT", value: "0s"},
		{name: "timeout syntax", key: "SHUTDOWN_TIMEOUT", value: "soon"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			for _, key := range []string{"HTTP_ADDR", "APP_ENV", "SHUTDOWN_TIMEOUT"} {
				if key == tt.key {
					_ = os.Setenv(key, tt.value)
				} else {
					_ = os.Unsetenv(key)
				}
			}
			if _, err := Load(); err == nil {
				t.Fatalf("Load() error = nil, want error for %s=%q", tt.key, tt.value)
			}
		})
	}
}

func TestConfigValidateRejectsEmptyEnvironment(t *testing.T) {
	cfg := Config{HTTPAddr: ":8080", AppEnv: " ", ShutdownTimeout: time.Second}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want error")
	}
}
