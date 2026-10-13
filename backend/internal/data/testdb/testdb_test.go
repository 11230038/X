package testdb

import (
	"strings"
	"testing"
	"time"

	"backend/internal/config"
)

// The harness itself is exercised by every package that uses it, so what is
// checked here is only the part that a passing run never shows: the names it
// derives, the message it gives when there is no database, and the one thing it
// must never print.

func TestTheDerivedNameCannotBeTheApplicationOne(t *testing.T) {
	for _, scenario := range []struct {
		configured string
		suffix     string
		want       string
	}{
		{configured: "x", suffix: "kb", want: "x_test_kb"},
		{configured: "x", suffix: "session", want: "x_test_session"},
		{configured: "app", suffix: "turn_v2", want: "app_test_turn_v2"},
	} {
		name, err := testName(scenario.configured, scenario.suffix)
		if err != nil {
			t.Errorf("testName(%q, %q) error = %v", scenario.configured, scenario.suffix, err)
			continue
		}
		if name != scenario.want {
			t.Errorf("testName(%q, %q) = %q, want %q", scenario.configured, scenario.suffix, name, scenario.want)
		}
		if !strings.Contains(name, "_test_") || name == scenario.configured {
			t.Errorf("%q is not distinguishable from the configured database %q", name, scenario.configured)
		}
	}
}

func TestASuffixThatCouldNotBeADatabaseIsRefused(t *testing.T) {
	// data.EnsureDatabase interpolates this name into a CREATE DATABASE
	// statement, so a suffix that is not a lowercase identifier is refused before
	// it can reach one.
	for _, suffix := range []string{"", "KB", "kb-2", "kb;k", "kb'", "9kb", "../x", "kb test"} {
		if name, err := testName("x", suffix); err == nil {
			t.Errorf("testName(%q) = %q, want a refusal", suffix, name)
		}
	}
}

func TestANameLongerThanSeekdbAllowsIsRefused(t *testing.T) {
	configured := strings.Repeat("d", 60)
	if name, err := testName(configured, "kb"); err == nil {
		t.Errorf("testName() = %q, want a refusal: it is longer than seekdb allows", name)
	}
	// 56 characters, plus "_test_" and "kb", is exactly the 64 seekdb allows.
	if name, err := testName(strings.Repeat("d", 56), "kb"); err != nil {
		t.Errorf("testName() error = %v, want a name of %d characters to be accepted", err, 64)
	} else if len(name) != 64 {
		t.Errorf("len(testName()) = %d, want 64", len(name))
	}
}

func TestNoDatabaseIsReportedWithTheNextStepAndWithoutThePassword(t *testing.T) {
	// Port 1 refuses at once on every platform this runs on, so the test costs a
	// connection attempt rather than a timeout. What it is really checking is the
	// message: a machine without seekdb has to be told what to do, and whoever
	// reads the log must not be handed the database password on the way.
	cfg := config.DatabaseConfig{
		Host: "127.0.0.1", Port: 1, Name: "x",
		User: "x_app", Password: "not-a-real-password",
		Charset: "utf8mb4", Collation: "utf8mb4_bin", TimeZone: "UTC",
		PingTimeout: 2 * time.Second,
	}

	resource, err := OpenDatabase("kb", cfg)
	if err == nil {
		resource.Close()
		t.Fatal("OpenDatabase() opened a database on a port nothing listens on")
	}
	message := err.Error()
	for _, wanted := range []string{"x_test_kb", "127.0.0.1:1", "x_app", "go run ./cmd/migrate up"} {
		if !strings.Contains(message, wanted) {
			t.Errorf("the failure says nothing about %q:\n%s", wanted, message)
		}
	}
	if strings.Contains(message, cfg.Password) {
		t.Errorf("the failure prints the database password:\n%s", message)
	}
}

func TestRedactTakesTheSecretOut(t *testing.T) {
	secret := "hunter2"
	message := "open " + secret + "@127.0.0.1:2881 with " + secret + ": refused"
	cleaned := redact(message, secret)
	if strings.Contains(cleaned, secret) {
		t.Errorf("redact() = %q, the secret is still in it", cleaned)
	}
	if !strings.Contains(cleaned, "***") {
		t.Errorf("redact() = %q, want the secret replaced", cleaned)
	}
	// An empty password is a password that cannot leak, and replacing "" would
	// put a marker between every character.
	if redact(message, "") != message {
		t.Errorf("redact(message, \"\") = %q, want the message unchanged", redact(message, ""))
	}
}
