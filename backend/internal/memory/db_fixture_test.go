package memory

// What the real-database tests share: one empty database, one account, and the
// builders that keep a case about a rule from also being a case about a struct
// literal.
//
// Nothing here calls t.Parallel. One database per package is the fixture's whole
// design (testdb's own comment says why), so the tests inside a package run one
// at a time and each starts from an empty set of tables.

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"backend/internal/data"
	"backend/internal/data/testdb"
)

// The times are written out rather than taken from a clock. The columns are
// DATETIMEs of whole seconds, so two rows written by a fast test land in the same
// second — which is a case worth testing on purpose (see the ordering tests) and
// a nuisance everywhere else.
var (
	atNine   = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	atTen    = time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	atEleven = time.Date(2026, 10, 1, 11, 0, 0, 0, time.UTC)
	atNoon   = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	statement = "对方正在学 Go"
)

func newDBRepository(t *testing.T) (*GORMRepository, *data.Resource, int64) {
	t.Helper()
	resource := testdb.Open(t, "memory")
	// Children first: the evidence, then the facts and traces it points at, the
	// audit, and the accounts last.
	testdb.Reset(t, resource,
		"memory_fact_sources", "memory_facts", "memory_traces", "memory_audit", "users")
	owner := createAccount(t, resource, "alice")
	return NewGORMRepository(resource.GORM()), resource, owner
}

// createAccount inserts the account every row here belongs to. All four tables
// carry a foreign key to `users`, so a test that wrote memory for nobody would be
// testing the constraint instead of the adapter.
func createAccount(t *testing.T, resource *data.Resource, username string) int64 {
	t.Helper()
	testdb.Exec(t, resource,
		"INSERT INTO users (username, password, learner_profile, extra) VALUES (?, 'hash', '{}', '{}')",
		username)
	var account struct {
		UserID int64 `gorm:"column:user_id"`
	}
	if err := resource.GORM().Raw("SELECT user_id FROM users WHERE username = ?", username).
		Scan(&account).Error; err != nil {
		t.Fatalf("read the account %q: %v", username, err)
	}
	return account.UserID
}

func countRows(t *testing.T, resource *data.Resource, table string) int64 {
	t.Helper()
	var count int64
	if err := resource.GORM().Raw("SELECT COUNT(*) FROM `" + table + "`").Scan(&count).Error; err != nil {
		t.Fatalf("count the rows of %s: %v", table, err)
	}
	return count
}

// sameJSON compares two JSON documents by what they say rather than by their
// spelling: the column is a JSON type, and the engine writes it back in its own
// shape — with a space after each colon, for one thing — so a test comparing the
// bytes would be testing the engine's printer.
func sameJSON(t *testing.T, got []byte, want string) bool {
	t.Helper()
	var left, right any
	if err := json.Unmarshal(got, &left); err != nil {
		t.Errorf("%s is not JSON: %v", got, err)
		return false
	}
	if err := json.Unmarshal([]byte(want), &right); err != nil {
		t.Fatalf("%s is not JSON: %v", want, err)
	}
	return reflect.DeepEqual(left, right)
}

func aTrace(id string, owner int64, created time.Time) Trace {
	return Trace{
		ID: id, OwnerID: owner, Surface: SurfaceMastery, Kind: "goal",
		Statement: statement, Fingerprint: Fingerprint(statement), CreatedAt: created,
	}
}

func aFact(id string, owner int64, status Status, created time.Time) Fact {
	return Fact{
		ID: id, OwnerID: owner, Surface: SurfaceMastery, Statement: statement,
		Fingerprint: Fingerprint(statement), Confidence: 80, Status: status,
		ConfirmedBy: ConfirmerGate, Version: 1, CreatedAt: created,
	}
}

func write(t *testing.T, repo *GORMRepository, work func(Repository) error) {
	t.Helper()
	if err := repo.Within(context.Background(), work); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// storedTrace reads a row as the table holds it, rather than as the adapter says
// it wrote it. The difference is where NULL lives: the domain has one way to say
// "no conversation" and the column has two.
type storedTrace struct {
	SessionID *string `gorm:"column:session_id"`
	TurnID    *string `gorm:"column:turn_id"`
	Payload   string  `gorm:"column:payload_json"`
}

func readStoredTrace(t *testing.T, resource *data.Resource, id string) storedTrace {
	t.Helper()
	var row storedTrace
	err := resource.GORM().
		Raw("SELECT session_id, turn_id, payload_json FROM memory_traces WHERE trace_id = ?", id).
		Scan(&row).Error
	if err != nil {
		t.Fatalf("read the stored trace %s: %v", id, err)
	}
	return row
}
