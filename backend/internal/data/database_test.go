package data

import (
	"testing"
	"time"

	"backend/internal/config"
	mysqldriver "github.com/go-sql-driver/mysql"
)

func testDatabaseConfig() config.DatabaseConfig {
	return config.DatabaseConfig{
		Host:            "seekdb.internal",
		Port:            2881,
		Name:            "x",
		User:            "user@example.com",
		Password:        "p@ss word&value",
		Charset:         "utf8mb4",
		Collation:       "utf8mb4_bin",
		TimeZone:        "UTC",
		MaxOpenConns:    7,
		MaxIdleConns:    3,
		ConnMaxLifetime: 2 * time.Minute,
		ConnMaxIdleTime: time.Minute,
	}
}

// TestMySQLDSNRoundTripsCredentials asserts the escaped DSN is parsed back to the
// exact credentials, which is what stops a password containing '@' or ':' from
// being read as part of the host.
func TestMySQLDSNRoundTripsCredentials(t *testing.T) {
	cfg := testDatabaseConfig()

	parsed, err := mysqldriver.ParseDSN(mysqlDSN(cfg))
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}
	if parsed.User != cfg.User || parsed.Passwd != cfg.Password {
		t.Errorf("credentials = %q/%q, want %q/%q", parsed.User, parsed.Passwd, cfg.User, cfg.Password)
	}
	if parsed.Addr != "seekdb.internal:2881" {
		t.Errorf("Addr = %q, want seekdb.internal:2881", parsed.Addr)
	}
	if parsed.DBName != "x" {
		t.Errorf("DBName = %q, want x", parsed.DBName)
	}
	if parsed.Net != "tcp" {
		t.Errorf("Net = %q, want tcp", parsed.Net)
	}
	if !parsed.ParseTime {
		t.Error("ParseTime = false, want true so DATETIME scans into time.Time")
	}
}

// TestMySQLDSNPinsSessionTimeZone guards the +08:00 trap: a seekdb session
// defaults to the host offset, and every creat_time/update_time default is
// evaluated against the session zone, not the connection's Loc.
func TestMySQLDSNPinsSessionTimeZone(t *testing.T) {
	parsed, err := mysqldriver.ParseDSN(mysqlDSN(testDatabaseConfig()))
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}
	if got := parsed.Params["time_zone"]; got != sessionTimeZone {
		t.Errorf("time_zone = %q, want %q", got, sessionTimeZone)
	}
	if parsed.Loc != time.UTC {
		t.Errorf("Loc = %v, want UTC", parsed.Loc)
	}
}

func TestMySQLDSNUsesConfiguredCharacterSet(t *testing.T) {
	parsed, err := mysqldriver.ParseDSN(mysqlDSN(testDatabaseConfig()))
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}
	if parsed.Collation != "utf8mb4_bin" {
		t.Errorf("Collation = %q, want utf8mb4_bin", parsed.Collation)
	}
	if got := parsed.Params["charset"]; got != "utf8mb4" {
		t.Errorf("charset = %q, want utf8mb4", got)
	}
}

func TestMySQLDSNEnablesTLSOnlyWhenAsked(t *testing.T) {
	cfg := testDatabaseConfig()
	if parsed, err := mysqldriver.ParseDSN(mysqlDSN(cfg)); err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	} else if parsed.TLSConfig != "" {
		t.Errorf("TLSConfig = %q, want empty", parsed.TLSConfig)
	}

	cfg.TLS = true
	if parsed, err := mysqldriver.ParseDSN(mysqlDSN(cfg)); err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	} else if parsed.TLSConfig != "true" {
		t.Errorf("TLSConfig = %q, want true", parsed.TLSConfig)
	}
}

func TestOpenConfiguresPoolWithoutPinging(t *testing.T) {
	resource, err := Open(testDatabaseConfig())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer resource.Close()

	stats := resource.SQLDB().Stats()
	if stats.MaxOpenConnections != 7 {
		t.Errorf("MaxOpenConnections = %d, want 7", stats.MaxOpenConnections)
	}
}
