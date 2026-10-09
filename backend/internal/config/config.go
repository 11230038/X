package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultHTTPAddr        = ":8080"
	defaultAppEnv          = "development"
	defaultShutdownTimeout = 10 * time.Second
)

// Config contains the settings required to run the HTTP server.
type Config struct {
	HTTPAddr        string
	AppEnv          string
	ShutdownTimeout time.Duration
}

// Load reads the server configuration from environment variables.
// It does not load dotenv files, so deployment configuration remains explicit.
func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:        envOrDefault("HTTP_ADDR", defaultHTTPAddr),
		AppEnv:          envOrDefault("APP_ENV", defaultAppEnv),
		ShutdownTimeout: defaultShutdownTimeout,
	}

	if raw, ok := os.LookupEnv("SHUTDOWN_TIMEOUT"); ok {
		duration, err := time.ParseDuration(strings.TrimSpace(raw))
		if err != nil || duration <= 0 {
			return Config{}, fmt.Errorf("SHUTDOWN_TIMEOUT must be a positive duration: %q", raw)
		}
		cfg.ShutdownTimeout = duration
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate checks values that would otherwise cause a late startup failure.
func (c Config) Validate() error {
	if err := validateHTTPAddr(c.HTTPAddr); err != nil {
		return fmt.Errorf("invalid HTTP_ADDR: %w", err)
	}
	if strings.TrimSpace(c.AppEnv) == "" {
		return fmt.Errorf("APP_ENV must not be empty")
	}
	if c.ShutdownTimeout <= 0 {
		return fmt.Errorf("shutdown timeout must be positive")
	}
	return nil
}

func envOrDefault(key, fallback string) string {
	value, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func validateHTTPAddr(addr string) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return fmt.Errorf("address must not be empty")
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("must be host:port: %w", err)
	}
	if host == "" {
		// An empty host means all interfaces and is valid for a server address.
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	return nil
}
