package mastery

// What the real-database cases share: one empty database, the session and turn
// the seven tables hang off, and the clock the service reads.
//
// Nothing here calls t.Parallel. One database per package is testdb's whole
// design, so the cases inside this package run one at a time and each starts
// from empty tables.
//
// The mastery tables have carried no rows since migration 00004 — this version is
// their first writer — so until these cases there was no evidence anywhere that
// the version guard, the lease's compare-and-swap or any of the ORDER BYs whose
// tie breaker is the second half of the rule actually behave as the comments
// beside them say. A double restates a contract and then agrees with itself;
// what decides these answers is the primary key, the CHECK constraints and the
// ON DELETE clauses in the migration, and the SQL the adapters write.

import (
	"context"
	"testing"
	"time"

	"backend/internal/authz"
	"backend/internal/data"
	"backend/internal/data/testdb"
)

// The times are written out rather than read off a clock. The columns are
// DATETIMEs of whole seconds, so two rows written by a fast case land in the
// same second — which is a case worth having on purpose (see the ordering
// ones) and a nuisance everywhere else.
var (
	atNine   = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	atTen    = time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	atEleven = time.Date(2026, 10, 1, 11, 0, 0, 0, time.UTC)
	atNoon   = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
)

// dbTables are the tables these cases write, children first. turns and sessions
// come next-to-last because five of the seven mastery tables are foreign keys
// into them, and emptying the conversation is what the cascades are watched
// removing. users is last of all: sessions and mastery_paths both point at it
// (migration 00014 gave them owner_id), so the accounts cannot go before the rows
// that name them.
var dbTables = []string{
	"mastery_learning_evidences",
	"mastery_events",
	"mastery_interactions",
	"mastery_path_leases",
	"mastery_path_sessions",
	"mastery_topic_meta",
	"mastery_paths",
	"turns",
	"sessions",
	"users",
}

// testClock is the service's clock, and it is a field of the harness because a
// case about a lease's expiry has to decide what "now" is. A case that could not
// choose the instant could not pin the row it wrote.
type testClock struct{ now time.Time }

func (c *testClock) advance(d time.Duration) { c.now = c.now.Add(d) }
func (c *testClock) read() time.Time         { return c.now }

// harness is one empty database, the service over it, and the clock that service
// reads.
//
// The three callers are the accounts the cases are run for: caller owns the rows
// the helpers write, other is a second ordinary account that owns none of them
// and is what a cross-account read is asked as, and admin is the one account that
// sees rows belonging to nobody. Most cases need only the first, which is why the
// helpers below use it without being told.
type harness struct {
	service    *Service
	repository *GORMRepository
	resource   *data.Resource
	clock      *testClock
	caller     authz.Caller
	other      authz.Caller
	admin      authz.Caller
}

// newHarness opens the package's database, empties it, and builds a service whose
// thresholds are named here rather than taken from a deployment.
//
// Three consecutive right answers and two observations before the gate will call
// a knowledge point anything, and a three-path limit: small numbers, so that a
// case can reach a boundary without writing a page of rows.
func newHarness(t *testing.T) *harness {
	t.Helper()
	resource := testdb.Open(t, "mastery")
	testdb.Reset(t, resource, dbTables...)
	repository := NewGORMRepository(resource.GORM())
	service, err := NewService(repository, Options{
		LeaseTTL:    time.Hour,
		QuestionTTL: time.Hour,
		Gate:        GateOptions{Consecutive: 3, MinEvidence: 2},
	})
	if err != nil {
		t.Fatalf("build the mastery service: %v", err)
	}
	clock := &testClock{now: atNine}
	service.clock = clock.read
	// The three accounts every case has. They are written here rather than by the
	// cases because ownership is now load-bearing for every read in this package:
	// a path is written by some account, and the account that asks for it back is
	// what decides whether it is found. A case that had to create its own owner
	// would be a case whose subject is the schema, not the domain.
	built := &harness{service: service, repository: repository, resource: resource, clock: clock}
	built.caller = built.account(t, "owner", authz.RoleUser)
	built.other = built.account(t, "other", authz.RoleUser)
	built.admin = built.account(t, "administrator", authz.RoleAdmin)
	return built
}

// account writes one user row and returns it as the caller the domain now takes.
//
// The password and profile columns are not read by anything in this domain; they
// are filled because migration 00001 makes them NOT NULL, and a case that left
// them out would be testing the users table.
func (h *harness) account(t *testing.T, username, role string) authz.Caller {
	t.Helper()
	testdb.Exec(t, h.resource,
		"INSERT INTO users (username, password, learner_profile, extra) VALUES (?, 'hash', '{}', '{}')",
		username)
	var row struct {
		UserID int64 `gorm:"column:user_id"`
	}
	if err := h.resource.GORM().Raw("SELECT user_id FROM users WHERE username = ?", username).
		Scan(&row).Error; err != nil {
		t.Fatalf("read account %s: %v", username, err)
	}
	return authz.Caller{UserID: row.UserID, Role: role}
}

// session writes the conversation a path is created from. Four of these tables
// carry a foreign key into sessions, so a case that skipped this would be
// testing the constraint instead of the domain.
func (h *harness) session(t *testing.T, id string) {
	t.Helper()
	testdb.Exec(t, h.resource, "INSERT INTO sessions "+
		"(session_id, title, creat_time, update_time) VALUES (?, ?, ?, ?)",
		id, id, atNine, atNine)
}

// turn writes a turn and returns it as the actor the domain names. The fence
// column starts NULL, which is the state FencingToken reports as 0 — the state
// every turn in this build is in until a lease writes one.
//
// The status is a terminal one, and that is not decoration: migration 00009 gives
// `turns` a unique index over a generated column that is the session id for every
// unfinished turn, so a session may hold exactly one turn that has not ended. The
// mastery domain reads no turn's status — a lease names a turn, not its state —
// while two turns in one conversation is the ordinary case here (two tabs, a
// takeover), which is exactly what that index would refuse.
func (h *harness) turn(t *testing.T, id, sessionID string) Actor {
	t.Helper()
	testdb.Exec(t, h.resource, "INSERT INTO turns "+
		"(turn_id, session_id, status, state_version, creat_time) VALUES (?, ?, 'completed', 1, ?)",
		id, sessionID, atNine)
	return Actor{SessionID: sessionID, TurnID: id}
}

// conversation is the ordinary opening of a case: one session, one turn on it,
// and the actor to write with.
func (h *harness) conversation(t *testing.T, sessionID, turnID string) Actor {
	t.Helper()
	h.session(t, sessionID)
	return h.turn(t, turnID, sessionID)
}

// storedPath reads a row as the table holds it, rather than as the adapter says
// it wrote it. The difference is where NULL lives: version is nullable in the
// schema and the domain has one value for "no tree yet".
type storedPath struct {
	State   *string    `gorm:"column:state_json"`
	Version *int32     `gorm:"column:version"`
	Updated *time.Time `gorm:"column:update_time"`
}

func (h *harness) storedPath(t *testing.T, pathID string) storedPath {
	t.Helper()
	var row storedPath
	err := h.resource.GORM().Raw(
		"SELECT state_json, version, update_time FROM mastery_paths WHERE mastery_path_id = ?", pathID).
		Scan(&row).Error
	if err != nil {
		t.Fatalf("read the stored path %s: %v", pathID, err)
	}
	return row
}

// try runs one statement that the schema is expected to refuse. It is the way to
// watch a CHECK constraint or a foreign key fire, which is something no adapter
// in this package can be asked to do.
func (h *harness) try(query string) error {
	return testdb.Try(h.resource, query)
}

func (h *harness) countRows(t *testing.T, table string) int64 {
	t.Helper()
	var count int64
	if err := h.resource.GORM().Raw("SELECT COUNT(*) FROM `" + table + "`").Scan(&count).Error; err != nil {
		t.Fatalf("count the rows of %s: %v", table, err)
	}
	return count
}

// write runs work in one transaction, which is the only way into this
// repository by design: a state written outside a transaction would be a state
// with no event explaining it.
func (h *harness) within(t *testing.T, work func(Repository) error) {
	t.Helper()
	if err := h.repository.Within(context.Background(), work); err != nil {
		t.Fatalf("within: %v", err)
	}
}

// aTree is a path with two modules and three knowledge points, which is enough
// for every case here: one point to be wrong about, one to be right about, and
// one nobody has touched, so that "unknown" and "weak" can be told apart in the
// same response.
func aTree() Tree {
	return Tree{Modules: []Module{
		{ID: "m_basics", Name: "基础", KnowledgePoints: []KnowledgePoint{
			{ID: "kp_capture", Name: "闭包捕获的是变量", Type: "concept", ModuleID: "m_basics"},
			{ID: "kp_loop", Name: "循环里的闭包", Type: "concept", ModuleID: "m_basics"},
		}},
		{ID: "m_advanced", Name: "进阶", KnowledgePoints: []KnowledgePoint{
			{ID: "kp_escape", Name: "逃逸分析", ModuleID: "m_advanced"},
		}},
	}}
}

// create writes a path through the domain, which is what puts the row, its
// event, its metadata and its link to the conversation there in one transaction.
// The path is written for h.caller, which is what every read in the domain will
// later take it as belonging to.
func (h *harness) create(t *testing.T, pathID, goal string, actor Actor) Path {
	t.Helper()
	path, err := h.service.Create(context.Background(), Create{
		PathID: pathID, Goal: goal, Tree: aTree(), Actor: actor,
	}, h.caller)
	if err != nil {
		t.Fatalf("create path %s: %v", pathID, err)
	}
	return path
}

// storeEvidence writes an observation straight through the repository, with the
// time the caller chooses. It is the only way an ordering can be asserted rather
// than observed.
func (h *harness) storeEvidence(t *testing.T, id, pathID, point string, result Result,
	actor Actor, at time.Time) {
	t.Helper()
	h.within(t, func(repository Repository) error {
		return repository.AppendEvidence(context.Background(), Evidence{
			ID: id, PathID: pathID, KnowledgePoint: point, Result: result,
			Source: SourceAssessment, Actor: actor, CreatedAt: at,
		}, h.caller)
	})
}
