package data

import (
	"strings"
	"testing"
	"time"

	"backend/internal/config"
)

func TestPostgresDSNEscapesCredentials(t *testing.T) {
	dsn := postgresDSN(config.DatabaseConfig{
		Host:     "localhost",
		Port:     5432,
		Name:     "x",
		User:     "user@example.com",
		Password: "p@ss word&value",
		Schema:   "public",
		SSLMode:  "require",
		TimeZone: "UTC",
	})

	if !strings.Contains(dsn, "user%40example.com") {
		t.Fatalf("DSN does not escape username: %q", dsn)
	}
	if !strings.Contains(dsn, "p%40ss%20word&value@") {
		t.Fatalf("DSN does not safely delimit password: %q", dsn)
	}
	if !strings.Contains(dsn, "sslmode=require") || !strings.Contains(dsn, "timezone=UTC") || !strings.Contains(dsn, "search_path=%22public%22") {
		t.Fatalf("DSN is missing connection options: %q", dsn)
	}
}

func TestQuoteSearchPathPreservesIdentifierCase(t *testing.T) {
	if got := quoteSearchPath("app_data"); got != `"app_data"` {
		t.Fatalf("quoteSearchPath() = %q", got)
	}
}

func TestOpenConfiguresPoolWithoutPinging(t *testing.T) {
	resource, err := Open(config.DatabaseConfig{
		Host:            "invalid.invalid",
		Port:            5432,
		Name:            "x",
		User:            "user",
		Password:        "password",
		Schema:          "public",
		SSLMode:         "disable",
		TimeZone:        "UTC",
		MaxOpenConns:    7,
		MaxIdleConns:    3,
		ConnMaxLifetime: 2 * time.Minute,
		ConnMaxIdleTime: time.Minute,
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer resource.Close()

	stats := resource.SQLDB().Stats()
	if stats.MaxOpenConnections != 7 {
		t.Errorf("MaxOpenConnections = %d, want 7", stats.MaxOpenConnections)
	}
}
