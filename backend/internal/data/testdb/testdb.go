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
// One kind of test asks for the other entry point, OpenAt, which creates the
// database and stops there. Those tests are the ones whose subject is the
// migrations: they drive the schema with the migration package, so they need a
// database that nothing has already brought to the current version.
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
	migrated bool
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
	resource, err := acquire(suffix, true)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return resource
}

// OpenAt returns the database this test package owns, holding no tables at all.
//
// A test that asks for it is testing the migrations themselves: it drives the
// schema with the migration package and is responsible for where it ends up. The
// entry point is here because every other one migrates before handing the
// database over, and a migration is only ever tested by running it over a
// database that is behind — with rows in the tables it alters and a version table
// that already has something in it.
//
// Emptied, not just created. A test database outlives the run that made it, so
// "created when missing" would mean the second run of these tests met the version
// the first run left behind: the upgrade would have nothing to do, the rollback
// would have nothing to roll back, and both would pass while testing neither. The
// tables are dropped through the same connection so that the foreign-key checks
// can be turned off around them; only the database named after the configured one
// with "_test_" and this suffix appended is ever touched.
//
// The suffix names the caller exactly as Open's does, and the two are kept apart:
// asking for a suffix the other one already opened fails rather than handing back
// a database in the state the caller did not ask for. A suffix cannot be shared
// with a test that has already built something on it either, because this empties
// the database every time it is called.
func OpenAt(t *testing.T, suffix string) *data.Resource {
	t.Helper()
	resource, err := acquire(suffix, false)
	if err != nil {
		t.Fatalf("%v", err)
	}
	emptyDatabase(t, resource)
	return resource
}

// emptyDatabase drops every table the test database holds.
func emptyDatabase(t *testing.T, resource *data.Resource) {
	t.Helper()
	ctx := context.Background()
	conn, err := resource.SQLDB().Conn(ctx)
	if err != nil {
		t.Fatalf("take a connection to empty the test database: %v", err)
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx,
		"SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE()")
	if err != nil {
		t.Fatalf("list the tables of the test database: %v", err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			rows.Close()
			t.Fatalf("read a table name back: %v", err)
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("list the tables of the test database: %v", err)
	}
	rows.Close()

	if len(tables) == 0 {
		return
	}
	// Dropped over one connection, and with the checks off, because the order the
	// tables come back in is not a dependency order and there is no reason to make
	// it one.
	if _, err := conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0"); err != nil {
		t.Fatalf("turn the foreign-key checks off: %v", err)
	}
	for _, table := range tables {
		if !identifierPattern.MatchString(table) {
			t.Fatalf("the test database holds a table named %q, which is not a lowercase identifier", table)
		}
		if _, err := conn.ExecContext(ctx, "DROP TABLE `"+table+"`"); err != nil {
			t.Fatalf("drop the table %s: %v", table, err)
		}
	}
	if _, err := conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 1"); err != nil {
		t.Fatalf("turn the foreign-key checks back on: %v", err)
	}
}

// OpenDatabase is Open for a caller that has a config of its own — a test of
// this package, or one that wants to watch a connection fail. The suffix is
// checked here too.
func OpenDatabase(suffix string, cfg config.DatabaseConfig) (*data.Resource, error) {
	return openDatabase(suffix, cfg, true)
}

func openDatabase(suffix string, cfg config.DatabaseConfig, migrate bool) (*data.Resource, error) {
	name, err := testName(cfg.Name, suffix)
	if err != nil {
		return nil, err
	}
	cfg.Name = name
	resource, err := prepare(cfg, migrate)
	if err != nil {
		return nil, fmt.Errorf(
			"the test database %s at %s:%d (user %s) is not usable: %w\n"+
				"tests run against a real seekdb: start the local container, check DB_HOST and DB_PORT "+
				"in backend/.env, then run `go run ./cmd/migrate up` in backend/",
			name, cfg.Host, cfg.Port, cfg.User, errors.New(redact(err.Error(), cfg.Password)))
	}
	return resource, nil
}

func acquire(suffix string, migrate bool) (*data.Resource, error) {
	mu.Lock()
	defer mu.Unlock()
	if entry, ok := opened[suffix]; ok {
		if entry.migrated != migrate {
			return nil, fmt.Errorf(
				"the test database suffix %q is already open %s",
				suffix, openedBy(entry.migrated))
		}
		return entry.resource, entry.err
	}
	var entry database
	entry.migrated = migrate
	entry.resource, entry.err = open(suffix, migrate)
	opened[suffix] = entry
	return entry.resource, entry.err
}

// openedBy names the entry point that took a suffix, for the message above.
func openedBy(migrated bool) string {
	if migrated {
		return "by Open, which migrates it to the schema this binary carries, so it cannot also be " +
			"the database a test drives the migrations on: ask for a suffix of your own"
	}
	return "by OpenAt, which leaves the schema where the test puts it, so it cannot also be a " +
		"database that has to be at the current schema: ask for a suffix of your own"
}

func open(suffix string, migrate bool) (*data.Resource, error) {
	cfg, err := loadConfiguration()
	if err != nil {
		return nil, err
	}
	return openDatabase(suffix, cfg, migrate)
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

// prepare creates the database, opens it, and — unless the caller is testing the
// migrations themselves — brings the schema to the version this binary carries.
// Migration is not a step a caller can forget: a test that ran against the
// previous schema would fail for reasons that have nothing to do with what it is
// testing.
func prepare(cfg config.DatabaseConfig, migrate bool) (*data.Resource, error) {
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
	if !migrate {
		return resource, nil
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

// Account writes one account row and answers with its id.
//
// It is here rather than in each package because V5 gave eight tables an owner
// column pointing at this row (migration 00014), so a fixture that writes an
// owner and does not write the account is refused by the database before the
// test under it runs — which reads as a broken query rather than a fixture that
// forgot a row. The id is the caller's to choose because the fixtures name it in
// their caller constants, and the two have to agree.
//
// The credentials are placeholders: nothing here reads them, and a real
// deployment's are hashes no test may hold. The profile columns are filled
// because migration 00001 makes them NOT NULL.
func Account(t *testing.T, resource *data.Resource, userID int64, username string) int64 {
	t.Helper()
	if userID <= 0 {
		t.Fatalf("Account(%q): the id %d must be positive, because the fixtures that own rows "+
			"name the account they own them for", username, userID)
	}
	Exec(t, resource,
		"INSERT INTO users (user_id, username, password, learner_profile, extra) "+
			"VALUES (?, ?, 'hash', '{}', '{}')", userID, username)
	return userID
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
