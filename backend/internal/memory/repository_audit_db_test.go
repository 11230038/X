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

	err := repo.Within(context.Background(), func(tx Repository) error {
		if err := tx.AppendAudit(context.Background(), line); err != nil {
			return err
		}
		return tx.AppendAudit(context.Background(), line)
	})
	if !errors.Is(err, ErrDuplicate) {
		t.Errorf("writing one audit line twice = %v, want a duplicate", err)
	}
	// The second write took the transaction with it, which is the same statement
	// read from the other end: the trail holds the line once.
	if got := countRows(t, resource, "memory_audit"); got != 1 {
		t.Errorf("%d audit lines are stored, want one", got)
	}
}
