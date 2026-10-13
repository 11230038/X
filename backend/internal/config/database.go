package config

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// databaseIdentifier matches the database names this service will accept. seekdb
// runs with lower_case_table_names=1, so a mixed-case name would be folded anyway.
var databaseIdentifier = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// charsetIdentifier matches the character-set and collation names accepted in the
// connection string, keeping arbitrary text out of the DSN.
var charsetIdentifier = regexp.MustCompile(`^[a-z0-9_]+$`)

// utcZone is the only session time zone this service accepts.
const utcZone = "UTC"

// DatabaseConfig contains seekdb connection and pool settings. seekdb speaks the
// MySQL protocol, so the field set follows MySQL rather than PostgreSQL: there is
// no separate schema namespace (the database is the schema) and TLS is either on
// or off rather than a libpq sslmode ladder.
type DatabaseConfig struct {
	Host            string
	Port            uint16
	Name            string
	User            string
	Password        string
	Charset         string
	Collation       string
	TLS             bool
	TimeZone        string
	PingTimeout     time.Duration
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// Validate checks seekdb connection and pool settings.
func (c DatabaseConfig) Validate() error {
	if strings.TrimSpace(c.Host) == "" {
		return fmt.Errorf("DB_HOST must not be empty")
	}
	if c.Port == 0 {
		return fmt.Errorf("DB_PORT must be between 1 and 65535")
	}
	for key, value := range map[string]string{
		"DB_NAME":      c.Name,
		"DB_USER":      c.User,
		"DB_CHARSET":   c.Charset,
		"DB_COLLATION": c.Collation,
		"DB_TIMEZONE":  c.TimeZone,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s must not be empty", key)
		}
	}
	if !databaseIdentifier.MatchString(c.Name) {
		return fmt.Errorf("DB_NAME must be a lowercase identifier")
	}
	if !charsetIdentifier.MatchString(c.Charset) {
		return fmt.Errorf("DB_CHARSET must be a character-set name")
	}
	if !charsetIdentifier.MatchString(c.Collation) {
		return fmt.Errorf("DB_COLLATION must be a collation name")
	}
	if c.TimeZone != utcZone {
		return fmt.Errorf("DB_TIMEZONE must be %s", utcZone)
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
	tls, err := parseBool(values, "DB_TLS")
	if err != nil {
		return DatabaseConfig{}, err
	}
	return DatabaseConfig{
		Host: strings.TrimSpace(values["DB_HOST"]), Port: port,
		Name: strings.TrimSpace(values["DB_NAME"]), User: strings.TrimSpace(values["DB_USER"]),
		Password: values["DB_PASSWORD"], Charset: strings.TrimSpace(values["DB_CHARSET"]),
		Collation: strings.TrimSpace(values["DB_COLLATION"]), TLS: tls,
		TimeZone:    strings.TrimSpace(values["DB_TIMEZONE"]),
		PingTimeout: pingTimeout, MaxOpenConns: maxOpen, MaxIdleConns: maxIdle,
		ConnMaxLifetime: maxLifetime, ConnMaxIdleTime: maxIdleTime,
	}, nil
}
