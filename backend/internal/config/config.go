package config

import (
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const multipartHeadroomBytes int64 = 64 * 1024

var postgresIdentifier = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

var requiredKeys = []string{
	"HTTP_ADDR",
	"APP_ENV",
	"SHUTDOWN_TIMEOUT",
	"HTTP_READ_HEADER_TIMEOUT",
	"HTTP_READ_TIMEOUT",
	"HTTP_WRITE_TIMEOUT",
	"HTTP_IDLE_TIMEOUT",
	"UPLOAD_ROOT",
	"UPLOAD_MAX_FILE_BYTES",
	"UPLOAD_MAX_REQUEST_BYTES",
	"DB_HOST",
	"DB_PORT",
	"DB_NAME",
	"DB_USER",
	"DB_PASSWORD",
	"DB_SCHEMA",
	"DB_SSLMODE",
	"DB_TIMEZONE",
	"DB_PING_TIMEOUT",
	"DB_MAX_OPEN_CONNS",
	"DB_MAX_IDLE_CONNS",
	"DB_CONN_MAX_LIFETIME",
	"DB_CONN_MAX_IDLE_TIME",
}

// Config contains all settings required to run the service.
type Config struct {
	HTTPAddr        string
	AppEnv          string
	ShutdownTimeout time.Duration
	HTTPTimeouts    HTTPTimeoutConfig
	Upload          UploadConfig
	Database        DatabaseConfig
}

// HTTPTimeoutConfig contains network deadlines for the HTTP server.
type HTTPTimeoutConfig struct {
	ReadHeader time.Duration
	Read       time.Duration
	Write      time.Duration
	Idle       time.Duration
}

// UploadConfig contains local file-library storage limits and location.
type UploadConfig struct {
	Root            string
	MaxFileBytes    int64
	MaxRequestBytes int64
}

// DatabaseConfig contains PostgreSQL connection and pool settings.
type DatabaseConfig struct {
	Host            string
	Port            uint16
	Name            string
	User            string
	Password        string
	Schema          string
	SSLMode         string
	TimeZone        string
	PingTimeout     time.Duration
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// Load reads configuration exclusively from the dotenv file at path.
func Load(path string) (Config, error) {
	values, err := godotenv.Read(path)
	if err != nil {
		return Config{}, fmt.Errorf("read configuration file %q", path)
	}
	if err := requireValues(values); err != nil {
		return Config{}, err
	}

	shutdownTimeout, err := parsePositiveDuration(values, "SHUTDOWN_TIMEOUT")
	if err != nil {
		return Config{}, err
	}
	httpTimeouts, err := loadHTTPTimeouts(values)
	if err != nil {
		return Config{}, err
	}
	upload, err := loadUploadConfig(path, values)
	if err != nil {
		return Config{}, err
	}
	database, err := loadDatabaseConfig(values)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		HTTPAddr:        strings.TrimSpace(values["HTTP_ADDR"]),
		AppEnv:          strings.TrimSpace(values["APP_ENV"]),
		ShutdownTimeout: shutdownTimeout,
		HTTPTimeouts:    httpTimeouts,
		Upload:          upload,
		Database:        database,
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
		return fmt.Errorf("SHUTDOWN_TIMEOUT must be a positive duration")
	}
	if err := c.HTTPTimeouts.Validate(); err != nil {
		return err
	}
	if err := c.Upload.Validate(); err != nil {
		return err
	}
	return c.Database.Validate()
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

// Validate checks upload storage and request limits.
func (c UploadConfig) Validate() error {
	if strings.TrimSpace(c.Root) == "" {
		return fmt.Errorf("UPLOAD_ROOT must not be empty")
	}
	if c.MaxFileBytes <= 0 {
		return fmt.Errorf("UPLOAD_MAX_FILE_BYTES must be a positive integer")
	}
	if c.MaxRequestBytes <= 0 {
		return fmt.Errorf("UPLOAD_MAX_REQUEST_BYTES must be a positive integer")
	}
	if c.MaxRequestBytes < multipartHeadroomBytes || c.MaxFileBytes > c.MaxRequestBytes-multipartHeadroomBytes {
		return fmt.Errorf("UPLOAD_MAX_REQUEST_BYTES must exceed UPLOAD_MAX_FILE_BYTES by at least %d bytes", multipartHeadroomBytes)
	}
	return nil
}

// Validate checks PostgreSQL connection and pool settings.
func (c DatabaseConfig) Validate() error {
	if strings.TrimSpace(c.Host) == "" {
		return fmt.Errorf("DB_HOST must not be empty")
	}
	if c.Port == 0 {
		return fmt.Errorf("DB_PORT must be between 1 and 65535")
	}
	for key, value := range map[string]string{
		"DB_NAME":     c.Name,
		"DB_USER":     c.User,
		"DB_PASSWORD": c.Password,
		"DB_SCHEMA":   c.Schema,
		"DB_TIMEZONE": c.TimeZone,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s must not be empty", key)
		}
	}
	if !postgresIdentifier.MatchString(c.Schema) {
		return fmt.Errorf("DB_SCHEMA must be a lowercase PostgreSQL identifier")
	}
	if !validSSLMode(c.SSLMode) {
		return fmt.Errorf("DB_SSLMODE must be one of disable, allow, prefer, require, verify-ca, verify-full")
	}
	if c.PingTimeout <= 0 {
		return fmt.Errorf("DB_PING_TIMEOUT must be a positive duration")
	}
	if c.MaxOpenConns <= 0 {
		return fmt.Errorf("DB_MAX_OPEN_CONNS must be positive")
	}
	if c.MaxIdleConns < 0 {
		return fmt.Errorf("DB_MAX_IDLE_CONNS must not be negative")
	}
	if c.MaxIdleConns > c.MaxOpenConns {
		return fmt.Errorf("DB_MAX_IDLE_CONNS must not exceed DB_MAX_OPEN_CONNS")
	}
	if c.ConnMaxLifetime < 0 {
		return fmt.Errorf("DB_CONN_MAX_LIFETIME must not be negative")
	}
	if c.ConnMaxIdleTime < 0 {
		return fmt.Errorf("DB_CONN_MAX_IDLE_TIME must not be negative")
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

func loadUploadConfig(path string, values map[string]string) (UploadConfig, error) {
	root := strings.TrimSpace(values["UPLOAD_ROOT"])
	if !filepath.IsAbs(root) {
		absoluteEnv, err := filepath.Abs(path)
		if err != nil {
			return UploadConfig{}, fmt.Errorf("resolve configuration file path")
		}
		root = filepath.Join(filepath.Dir(absoluteEnv), root)
	}
	root, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return UploadConfig{}, fmt.Errorf("resolve UPLOAD_ROOT")
	}
	maxFile, err := parsePositiveInt64(values, "UPLOAD_MAX_FILE_BYTES")
	if err != nil {
		return UploadConfig{}, err
	}
	maxRequest, err := parsePositiveInt64(values, "UPLOAD_MAX_REQUEST_BYTES")
	if err != nil {
		return UploadConfig{}, err
	}
	return UploadConfig{Root: root, MaxFileBytes: maxFile, MaxRequestBytes: maxRequest}, nil
}

func loadDatabaseConfig(values map[string]string) (DatabaseConfig, error) {
	port, err := parsePort(values, "DB_PORT")
	if err != nil {
		return DatabaseConfig{}, err
	}
	pingTimeout, err := parsePositiveDuration(values, "DB_PING_TIMEOUT")
	if err != nil {
		return DatabaseConfig{}, err
	}
	maxOpen, err := parseNonNegativeInt(values, "DB_MAX_OPEN_CONNS")
	if err != nil {
		return DatabaseConfig{}, err
	}
	maxIdle, err := parseNonNegativeInt(values, "DB_MAX_IDLE_CONNS")
	if err != nil {
		return DatabaseConfig{}, err
	}
	maxLifetime, err := parseNonNegativeDuration(values, "DB_CONN_MAX_LIFETIME")
	if err != nil {
		return DatabaseConfig{}, err
	}
	maxIdleTime, err := parseNonNegativeDuration(values, "DB_CONN_MAX_IDLE_TIME")
	if err != nil {
		return DatabaseConfig{}, err
	}
	return DatabaseConfig{
		Host: strings.TrimSpace(values["DB_HOST"]), Port: port,
		Name: strings.TrimSpace(values["DB_NAME"]), User: strings.TrimSpace(values["DB_USER"]),
		Password: values["DB_PASSWORD"], Schema: strings.TrimSpace(values["DB_SCHEMA"]),
		SSLMode: strings.TrimSpace(values["DB_SSLMODE"]), TimeZone: strings.TrimSpace(values["DB_TIMEZONE"]),
		PingTimeout: pingTimeout, MaxOpenConns: maxOpen, MaxIdleConns: maxIdle,
		ConnMaxLifetime: maxLifetime, ConnMaxIdleTime: maxIdleTime,
	}, nil
}

func requireValues(values map[string]string) error {
	for _, key := range requiredKeys {
		value, ok := values[key]
		if !ok || strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required in the configuration file", key)
		}
	}
	return nil
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

func validSSLMode(value string) bool {
	switch strings.TrimSpace(value) {
	case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
		return true
	default:
		return false
	}
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
