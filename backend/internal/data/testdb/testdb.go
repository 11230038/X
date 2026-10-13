// Package testdb gives a test a real database.
//
// What this repository's storage adapters depend on lives in the SQL, not in
// Go: the index a query has to stay on, the ordering a listing promises, the
// check constraint that refuses half a write, the domain error a duplicate key
// turns into. A test double cannot attest to any of that — it can only restate
// the contract and then agree with itself — so the tests that make those claims
// are run here, against the same seekdb a deployment runs against.
//
// The database is not the development one. Each test package gets its own, named
// after the configured DB_NAME with the package appended (x_test_kb), created
// and migrated the first time a test asks for it. The application's database is
// never opened, so no test can empty it by accident.
//
// The cost of that guarantee is that a run needs a local seekdb. When there is
// none, these tests fail rather than skip: a suite that quietly passes without
// touching a database is the state this package exists to end.
package testdb

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"backend/internal/config"
	"backend/internal/data"
	"backend/internal/data/migration"
)

// maxIdentifierLength is seekdb's limit on a database name.
const maxIdentifierLength = 64

// defaultPingTimeout bounds the wait for a connection that never arrives, for
// the callers that hand in a config built by hand rather than read from a file.
const defaultPingTimeout = 5 * time.Second

// identifierPattern is what a database name may contain. data.EnsureDatabase
// interpolates the name into a CREATE DATABASE statement without escaping it,
// so the name is checked here rather than trusted.
var identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// envFile is the configuration file the application itself loads, found from
// this file's own location so that it does not matter which package's directory
// a test runs in.
var envFile = func() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ".env"
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".env"))
}()

// database is one attempt at one test database, remembered so that the second
// test of a package does not migrate again.
type database struct {
	resource *data.Resource
	err      error
}

var (
	mu     sync.Mutex
	opened = map[string]database{}
)

// Open returns the database this test package owns, migrating it if this is the
// first test to ask. It fails the test when there is no database to be had.
//
// The suffix names the caller: "kb" gives x_test_kb. Tests in one package share
// one database and must therefore not call t.Parallel — packages are separated
// by the suffix, tests inside one are not.
func Open(t *testing.T, suffix string) *data.Resource {
	t.Helper()
	resource, err := acquire(suffix)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return resource
}

// OpenDatabase is Open for a caller that has a config of its own — a test of
// this package, or one that wants to watch a connection fail. The suffix is
// checked here too.
func OpenDatabase(suffix string, cfg config.DatabaseConfig) (*data.Resource, error) {
	name, err := testName(cfg.Name, suffix)
	if err != nil {
		return nil, err
	}
	cfg.Name = name
	resource, err := prepare(cfg)
	if err != nil {
		return nil, fmt.Errorf(
			"the test database %s at %s:%d (user %s) is not usable: %w\n"+
				"tests run against a real seekdb: start the local container, check DB_HOST and DB_PORT "+
				"in backend/.env, then run `go run ./cmd/migrate up` in backend/",
			name, cfg.Host, cfg.Port, cfg.User, errors.New(redact(err.Error(), cfg.Password)))
	}
	return resource, nil
}

func acquire(suffix string) (*data.Resource, error) {
	mu.Lock()
	defer mu.Unlock()
	if entry, ok := opened[suffix]; ok {
		return entry.resource, entry.err
	}
	var entry database
	entry.resource, entry.err = open(suffix)
	opened[suffix] = entry
	return entry.resource, entry.err
}

func open(suffix string) (*data.Resource, error) {
	cfg, err := loadConfiguration()
	if err != nil {
		return nil, err
	}
	return OpenDatabase(suffix, cfg)
}

// loadConfiguration reads the settings the application runs on. A test changes
// one thing about them: which database they point at.
func loadConfiguration() (config.DatabaseConfig, error) {
	cfg, err := config.Load(envFile)
	if err != nil {
		return config.DatabaseConfig{}, fmt.Errorf("read the test configuration from %s: %w", envFile, err)
	}
	return cfg.Database, nil
}

// prepare creates the database, opens it, and brings the schema to the version
// this binary carries. Migration is not a step a caller can forget: a test that
// ran against the previous schema would fail for reasons that have nothing to
// do with what it is testing.
func prepare(cfg config.DatabaseConfig) (*data.Resource, error) {
	ctx := context.Background()
	if err := data.EnsureDatabase(ctx, cfg); err != nil {
		return nil, err
	}
	resource, err := data.Open(cfg)
	if err != nil {
		return nil, err
	}

	timeout := cfg.PingTimeout
	if timeout <= 0 {
		timeout = defaultPingTimeout
	}
	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := resource.Ping(pingCtx); err != nil {
		resource.Close()
		return nil, err
	}
	if err := migration.Up(ctx, resource.SQLDB()); err != nil {
		resource.Close()
		return nil, err
	}
	// Up applies what this binary knows about; CheckLatest is what says the
	// database is at that version and no other.
	if err := migration.CheckLatest(ctx, resource.SQLDB()); err != nil {
		resource.Close()
		return nil, err
	}
	return resource, nil
}

// testName is the one the tests use, derived rather than configured: a run must
// not be able to reach the database the application uses, and that has to hold
// even when somebody points the tests at a configuration they did not read.
func testName(configured, suffix string) (string, error) {
	if !identifierPattern.MatchString(suffix) {
		return "", fmt.Errorf(
			"the test database suffix %q must be lowercase letters, digits or underscores", suffix)
	}
	name := configured + "_test_" + suffix
	if !identifierPattern.MatchString(name) {
		return "", fmt.Errorf("the configured database name %q is not a lowercase identifier", configured)
	}
	if len(name) > maxIdentifierLength {
		return "", fmt.Errorf("the test database name %q is longer than seekdb allows", name)
	}
	return name, nil
}

// Reset empties the named tables, children first, so that a test starts from
// nothing and the next one starts where this one did.
//
// The rows are deleted and the deletes are committed, rather than written
// inside a transaction that is rolled back at the end. A rolled-back write is
// not visible to a full-text search — the index a MATCH reads only ever sees
// committed rows — so the tests about retrieval would pass by finding nothing.
func Reset(t *testing.T, resource *data.Resource, tables ...string) {
	t.Helper()
	for _, table := range tables {
		if !identifierPattern.MatchString(table) {
			t.Fatalf("Reset(%q): a table name is a lowercase identifier", table)
		}
		if err := Try(resource, "DELETE FROM `"+table+"`"); err != nil {
			t.Fatalf("empty %s (children first): %v", table, err)
		}
	}
}

// Exec runs one statement and fails the test if it does not go through.
func Exec(t *testing.T, resource *data.Resource, query string, args ...any) {
	t.Helper()
	if err := Try(resource, query, args...); err != nil {
		t.Fatalf("%s: %v", oneLine(query), err)
	}
}

// Try runs one statement and reports what it said, for the tests whose subject
// is a write the database is supposed to refuse.
func Try(resource *data.Resource, query string, args ...any) error {
	return resource.GORM().Exec(query, args...).Error
}

// redact takes the database password out of a message. Nothing here sets out to
// print one — the messages in this file are written by hand — but a driver error
// is not written by hand, and a local password that ends up in a test log is a
// worse outcome than a failure that reads a little less well.
func redact(message, secret string) string {
	if secret == "" {
		return message
	}
	return strings.ReplaceAll(message, secret, "***")
}

// oneLine keeps a statement readable in a failure without pasting a page of SQL
// into it.
func oneLine(query string) string {
	collapsed := strings.Join(strings.Fields(query), " ")
	if len(collapsed) > 120 {
		return collapsed[:120] + "…"
	}
	return collapsed
}
