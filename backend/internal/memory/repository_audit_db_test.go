package memory

// The audit trail's storage contract: that a line round-trips, that the listing
// is newest first and one account's, and that a line cannot be written twice.
//
// Nothing here tries to assert what the *table* will not do — there is nothing to
// assert, because the engine happily updates a row of it. What stands in for that
// is audit_test.go reading the interface.

import (
	"context"
	"errors"
	"testing"
)

func TestTheAuditTrailIsReadNewestFirst(t *testing.T) {
	repo, resource, owner := newDBRepository(t)
	other := createAccount(t, resource, "bob")
	write(t, repo, func(tx Repository) error {
		for _, row := range []Audit{
			{ID: "ma_1", OwnerID: owner, Actor: ActorUser, Action: ActionFactStated,
				TargetKind: TargetFact, TargetID: "mf_1", Result: ResultDone, CreatedAt: atNine},
			{ID: "ma_2", OwnerID: owner, Actor: ActorUser, Action: ActionFactRetracted,
				TargetKind: TargetFact, TargetID: "mf_1", Result: ResultDone, CreatedAt: atNoon},
			{ID: "ma_3", OwnerID: owner, Actor: ActorUser, Action: ActionMemoryExported,
				TargetKind: TargetMemory, Result: ResultDone, Detail: []byte(`{"facts":2}`), CreatedAt: atTen},
			{ID: "ma_theirs", OwnerID: other, Actor: ActorUser, Action: ActionFactStated,
				TargetKind: TargetFact, TargetID: "mf_9", Result: ResultDone, CreatedAt: atEleven},
		} {
			if err := tx.AppendAudit(context.Background(), row); err != nil {
				return err
			}
		}
		return nil
	})

	read, err := repo.Audits(context.Background(), AuditQuery{OwnerID: owner})
	if err != nil {
		t.Fatalf("Audits() error = %v", err)
	}
	if len(read) != 3 || read[0].ID != "ma_2" || read[1].ID != "ma_3" || read[2].ID != "ma_1" {
		t.Fatalf("Audits() = %+v, want this account's three newest first", read)
	}
	// An action about the account as a whole names no row, and a row that names
	// one keeps it: the same column with two different answers, and the round trip
	// has to keep both.
	if read[0].TargetID != "mf_1" {
		t.Errorf("the retraction names %q, want the fact", read[0].TargetID)
	}
	if read[1].TargetID != "" || !sameJSON(t, read[1].Detail, `{"facts":2}`) {
		t.Errorf("the export reads %+v, want no target and its detail", read[1])
	}
	if read[0].Result != ResultDone || read[2].Action != ActionFactStated {
		t.Errorf("the trail reads %+v, want the words that were written", read)
	}

	filtered, err := repo.Audits(context.Background(), AuditQuery{OwnerID: owner, Action: ActionFactStated})
	if err != nil {
		t.Fatalf("Audits() error = %v", err)
	}
	if len(filtered) != 1 || filtered[0].ID != "ma_1" {
		t.Errorf("Audits() filtered = %+v, want the one statement", filtered)
	}
	limited, err := repo.Audits(context.Background(), AuditQuery{OwnerID: owner, Limit: 1})
	if err != nil {
		t.Fatalf("Audits() error = %v", err)
	}
	if len(limited) != 1 || limited[0].ID != "ma_2" {
		t.Errorf("Audits() limited = %+v, want the newest one", limited)
	}
}

func TestAnAuditLineCannotBeWrittenTwice(t *testing.T) {
	repo, resource, owner := newDBRepository(t)
	line := Audit{
		ID: "ma_1", OwnerID: owner, Actor: ActorUser, Action: ActionFactStated,
		TargetKind: TargetFact, TargetID: "mf_1", Result: ResultDone, CreatedAt: atNoon,
	}

	// Two calls and not one, because that is what writing a line twice looks like
	// from outside: the first is the record of an action, the second is somebody
	// asking for that same record again. The trail keeps the first one, and the
	// second is refused by the primary key rather than merged into it.
	if err := repo.AppendAudit(context.Background(), line); err != nil {
		t.Fatalf("the first append = %v, want it written", err)
	}
	err := repo.AppendAudit(context.Background(), line)
	if !errors.Is(err, ErrDuplicate) {
		t.Errorf("writing one audit line twice = %v, want a duplicate", err)
	}
	if got := countRows(t, resource, "memory_audit"); got != 1 {
		t.Errorf("%d audit lines are stored, want one", got)
	}

	// The same thing inside a caller's transaction, which is how a fact and the
	// record of it are written together. Here the engine refuses the statement and
	// the caller's error rolls the whole transaction back, so the trail holds
	// nothing at all — and that is the right amount: MEM-02 asks for a record of
	// what was done, and a call that failed did nothing.
	second := line
	second.ID = "ma_2"
	err = repo.Within(context.Background(), func(tx Repository) error {
		if err := tx.AppendAudit(context.Background(), second); err != nil {
			return err
		}
		return tx.AppendAudit(context.Background(), second)
	})
	if !errors.Is(err, ErrDuplicate) {
		t.Errorf("a duplicate inside a transaction = %v, want a duplicate", err)
	}
	if got := countRows(t, resource, "memory_audit"); got != 1 {
		t.Errorf("%d audit lines are stored, want the one from the call that succeeded", got)
	}
}
