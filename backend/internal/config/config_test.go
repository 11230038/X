package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testPassword = "unit-test-password"

func TestLoadReadsCompleteDotenv(t *testing.T) {
	path := writeDotenv(t, validValues())

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.HTTPAddr != "127.0.0.1:9090" {
		t.Errorf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.AppEnv != "test" {
		t.Errorf("AppEnv = %q", cfg.AppEnv)
	}
	if cfg.ShutdownTimeout != 2*time.Second {
		t.Errorf("ShutdownTimeout = %s", cfg.ShutdownTimeout)
	}
	if cfg.Database.Port != 5432 || cfg.Database.Password != testPassword {
		t.Errorf("Database configuration was not loaded")
	}
	if cfg.Database.MaxOpenConns != 10 || cfg.Database.MaxIdleConns != 5 {
		t.Errorf("connection pool = %d/%d", cfg.Database.MaxOpenConns, cfg.Database.MaxIdleConns)
	}
}

func TestLoadRequiresDotenvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if _, err := Load(path); err == nil {
		t.Fatal("Load() error = nil, want missing file error")
	}
}

func TestLoadRejectsMissingAndEmptyValues(t *testing.T) {
	for _, tt := range []struct {
		name   string
		key    string
		remove bool
	}{
		{name: "missing database password", key: "DB_PASSWORD", remove: true},
		{name: "empty database name", key: "DB_NAME"},
		{name: "empty HTTP address", key: "HTTP_ADDR"},
	} {
		t.Run(tt.name, func(t *testing.T) {
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

func TestLoadRejectsInvalidValues(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
	}{
		{name: "HTTP address", key: "HTTP_ADDR", value: "localhost"},
		{name: "shutdown timeout", key: "SHUTDOWN_TIMEOUT", value: "0s"},
		{name: "database port", key: "DB_PORT", value: "70000"},
		{name: "SSL mode", key: "DB_SSLMODE", value: "unsafe"},
		{name: "schema identifier", key: "DB_SCHEMA", value: "public.schema"},
		{name: "uppercase schema identifier", key: "DB_SCHEMA", value: "AppData"},
		{name: "ping timeout", key: "DB_PING_TIMEOUT", value: "soon"},
		{name: "max open", key: "DB_MAX_OPEN_CONNS", value: "0"},
		{name: "max idle", key: "DB_MAX_IDLE_CONNS", value: "-1"},
		{name: "connection lifetime", key: "DB_CONN_MAX_LIFETIME", value: "-1s"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			values := validValues()
			values[tt.key] = tt.value
			if _, err := Load(writeDotenv(t, values)); err == nil {
				t.Fatalf("Load() error = nil, want error for %s", tt.key)
			}
		})
	}
}

func TestLoadRejectsIdleConnectionsAboveOpenConnections(t *testing.T) {
	values := validValues()
	values["DB_MAX_OPEN_CONNS"] = "2"
	values["DB_MAX_IDLE_CONNS"] = "3"

	if _, err := Load(writeDotenv(t, values)); err == nil {
		t.Fatal("Load() error = nil, want pool validation error")
	}
}

func TestLoadIgnoresProcessEnvironment(t *testing.T) {
	values := validValues()
	values["DB_HOST"] = "file-host"
	path := writeDotenv(t, values)
	t.Setenv("DB_HOST", "environment-host")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Database.Host != "file-host" {
		t.Fatalf("Database.Host = %q, want file-host", cfg.Database.Host)
	}

	delete(values, "DB_NAME")
	t.Setenv("DB_NAME", "environment-database")
	if _, err := Load(writeDotenv(t, values)); err == nil {
		t.Fatal("Load() used process environment to fill a missing file value")
	}
}

func TestLoadDoesNotExposeSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := "DB_PASSWORD=" + testPassword + "\nINVALID LINE WITH " + testPassword
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want dotenv parse error")
	}
	if strings.Contains(err.Error(), testPassword) {
		t.Fatal("configuration error exposed the database password")
	}
}

func validValues() map[string]string {
	return map[string]string{
		"HTTP_ADDR":             "127.0.0.1:9090",
		"APP_ENV":               "test",
		"SHUTDOWN_TIMEOUT":      "2s",
		"DB_HOST":               "localhost",
		"DB_PORT":               "5432",
		"DB_NAME":               "x_test",
		"DB_USER":               "x_test_user",
		"DB_PASSWORD":           testPassword,
		"DB_SCHEMA":             "public",
		"DB_SSLMODE":            "disable",
		"DB_TIMEZONE":           "UTC",
		"DB_PING_TIMEOUT":       "1s",
		"DB_MAX_OPEN_CONNS":     "10",
		"DB_MAX_IDLE_CONNS":     "5",
		"DB_CONN_MAX_LIFETIME":  "30m",
		"DB_CONN_MAX_IDLE_TIME": "5m",
	}
}

func writeDotenv(t *testing.T, values map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	var lines []string
	for _, key := range requiredKeys {
		if value, ok := values[key]; ok {
			lines = append(lines, key+"="+value)
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
