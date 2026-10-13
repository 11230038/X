package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// HTTPTimeoutConfig contains network deadlines for the HTTP server.
type HTTPTimeoutConfig struct {
	ReadHeader time.Duration
	Read       time.Duration
	Write      time.Duration
	Idle       time.Duration
}

// Validate checks HTTP server timeout settings.
func (c HTTPTimeoutConfig) Validate() error {
	for key, value := range map[string]time.Duration{
		"HTTP_READ_HEADER_TIMEOUT": c.ReadHeader,
		"HTTP_READ_TIMEOUT":        c.Read,
		"HTTP_WRITE_TIMEOUT":       c.Write,
		"HTTP_IDLE_TIMEOUT":        c.Idle,
	} {
		if value <= 0 {
			return fmt.Errorf("%s must be a positive duration", key)
		}
	}
	return nil
}

func loadHTTPTimeouts(values map[string]string) (HTTPTimeoutConfig, error) {
	readHeader, err := parsePositiveDuration(values, "HTTP_READ_HEADER_TIMEOUT")
	if err != nil {
		return HTTPTimeoutConfig{}, err
	}
	read, err := parsePositiveDuration(values, "HTTP_READ_TIMEOUT")
	if err != nil {
		return HTTPTimeoutConfig{}, err
	}
	write, err := parsePositiveDuration(values, "HTTP_WRITE_TIMEOUT")
	if err != nil {
		return HTTPTimeoutConfig{}, err
	}
	idle, err := parsePositiveDuration(values, "HTTP_IDLE_TIMEOUT")
	if err != nil {
		return HTTPTimeoutConfig{}, err
	}
	return HTTPTimeoutConfig{ReadHeader: readHeader, Read: read, Write: write, Idle: idle}, nil
}

func validateHTTPAddr(addr string) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return fmt.Errorf("address must not be empty")
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("must be host:port: %w", err)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	return nil
}
