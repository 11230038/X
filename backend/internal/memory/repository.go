package memory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Repository is the storage the memory domain needs.
//
// Every method is one statement except the two that cannot be — the grouping a
// dedupe reads, and the move that re-points a merged fact's evidence — and the
// decisions live in the service: what the gate's conditions mean, which fact
// survives a merge, what an edit costs. A repository that decided any of that
// would be a repository no fake could replace for a test about the rules.
//
// Two absences are deliberate and are the interface's shape rather than an
// oversight:
//
//   - there is no method that updates or deletes an audit row. Append-only is
//     enforced here because the engine has no table property that would enforce
//     it, and a test asserts that this interface cannot be given one quietly.
//   - there is no method that reads a fact without an owner. Another account's
//     row is not found, and the only way to guarantee that is to never write the
//     query that could find it.
type Repository interface {
	// Within runs work against one transaction. The repository it hands over is
	// bound to that transaction and is the only one the work may use: a write
	// made through the outer repository while a transaction is open would be
	// committed on its own and would survive the rollback that was supposed to
	// take it back.
	//
	// The transaction is how an observation and the fact it produced land
	// together — an L1 row with no fact is an ordinary outcome, but a fact with
	// no evidence is not.
	Within(ctx context.Context, work func(Repository) error) error

	// AppendTrace writes one observation (L1). It is written whatever the gate
	// decides: hearing something and not believing it is still hearing it.
	// ErrDuplicate when the id is taken.
	AppendTrace(ctx context.Context, trace Trace) error
	// Traces reads L1, newest first. The fingerprint filter is what the gate
	// reads by; the rest are the listing's (§2.14). An account that has nothing
	// matching is an empty slice, not an error: "you have never been told this"
	// is an answer.
	Traces(ctx context.Context, query TraceQuery) ([]Trace, error)
	// TracesOf reads the observations one fact rests on, oldest first (MEM-01).
	TracesOf(ctx context.Context, factID string) ([]Trace, error)

	// ActiveFact reads the fact in force that already says this, if there is one.
	// ErrNotFound when there is not, which is the ordinary state of a fingerprint
	// the account has just been told for the first time.
	ActiveFact(ctx context.Context, ownerID int64, fingerprint string) (Fact, error)
	// Fact reads one fact of one account's. ErrNotFound when there is no such
	// fact, including when there is one belonging to somebody else.
	Fact(ctx context.Context, ownerID int64, factID string) (Fact, error)
	// Facts reads L2, most recently changed first.
	Facts(ctx context.Context, query FactQuery) ([]Fact, error)
	// DuplicateGroups reads the facts in force that say the same thing, grouped
	// by fingerprint and each group oldest first.
	//
	// The grouping is a rule and it lives in SQL anyway, because the alternative
	// is reading every fact an account has in order to find the handful of pairs
	// — and the number of groups is by nature small: it is the number of times
	// the system has been told the same thing twice.
	DuplicateGroups(ctx context.Context, ownerID int64) ([]FactGroup, error)

	// CreateFact writes a fact. ErrDuplicate when the id is taken.
	CreateFact(ctx context.Context, fact Fact) error
	// SaveFact writes a fact over the version the caller read it at, and reports
	// the version the write reached. The fact's own Version field is ignored:
	// what guards the write is the version the caller read, passed separately, so
	// that a caller cannot forget which one it holds.
	// ErrNotFound when there is no such fact; ErrStaleState when the row moved on.
	SaveFact(ctx context.Context, fact Fact, expected int32, now time.Time) (int32, error)
	// AttachSource records one observation as evidence for one fact. Attaching
	// the same pair twice is not an error: it is already recorded, which is the
	// answer the caller wanted.
	AttachSource(ctx context.Context, factID, traceID string, now time.Time) error
	// PurgeFacts removes facts outright, and the evidence under them with them.
	//
	// It is the only deletion this package has, and it exists because cleanup is a
	// batch a user previews and then runs (cleanup.go) — everything else MEM-02
	// asks for is a state change with a row left behind, which is why nothing else
	// here removes anything. The observations stay: L1 is append-only (PRD §11.3),
	// and a fact going away says nothing about whether a surface said it. What the
	// evidence rows point at is therefore still there to read; it is the fact that
	// is gone.
	//
	// The owner is part of the WHERE clause, as everywhere else here: a caller
	// cannot name another account's rows into a deletion any more than it can read
	// them. An empty list removes nothing and is not an error — it is a cleanup
	// that found nothing to clean.
	PurgeFacts(ctx context.Context, ownerID int64, factIDs []string) (int, error)
	// MoveSources re-points every piece of evidence from one fact to another,
	// which is what a merge is on the evidence side.
	MoveSources(ctx context.Context, fromFactID, toFactID string) error

	// AppendAudit writes one line of the audit trail (MEM-02). There is no
	// counterpart that changes or removes one.
	AppendAudit(ctx context.Context, audit Audit) error
	// Audits reads the audit trail, newest first (§2.14's `GET /memory/audit`).
	// This is the second and last method over this table — an append and a read,
	// and the test that pins the shape reads the interface to say so.
	Audits(ctx context.Context, query AuditQuery) ([]Audit, error)
}

// GORMRepository reads and writes the memory tables through GORM (seekdb over
// the MySQL protocol).
type GORMRepository struct {
	db *gorm.DB
}

// NewGORMRepository creates a storage adapter over an existing GORM handle.
func NewGORMRepository(db *gorm.DB) *GORMRepository {
	return &GORMRepository{db: db}
}

var _ Repository = (*GORMRepository)(nil)

// Within runs work in one transaction.
func (r *GORMRepository) Within(ctx context.Context, work func(Repository) error) error {
	if err := r.ready(); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return work(&GORMRepository{db: tx})
	})
}

func (r *GORMRepository) ready() error {
	if r == nil || r.db == nil {
		return fmt.Errorf("memory repository is not initialized")
	}
	return nil
}

// payloadOf is the JSON a nullable JSON column is given. A column with nothing
// in it gets an empty object rather than a NULL: the two read the same way
// through this package, and an object is the one that keeps the column's type
// honest for anything reading the table directly.
func payloadOf(payload []byte) []byte {
	if len(payload) == 0 {
		return []byte("{}")
	}
	return payload
}

// moment is a time as a column that may have none, with the zero time said as
// nothing rather than as 1970.
func moment(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}
	return &at
}

func statusesOf(statuses []Status) []string {
	values := make([]string, 0, len(statuses))
	for _, status := range statuses {
		values = append(values, string(status))
	}
	return values
}

// duplicateOr turns a collision on a key into ErrDuplicate, and says which
// statement collided — which matters because the two callers mean different
// things by it.
func duplicateOr(err error, what string, args ...any) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return fmt.Errorf("%w: %s", ErrDuplicate, fmt.Sprintf(what, args...))
	}
	return fmt.Errorf("%s: %w", fmt.Sprintf(what, args...), err)
}

func nullable(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func text(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
