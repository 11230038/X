package memory

// What the engine does to these tables whichever method asked: the errors a
// violated key produces, what a transaction takes back, and what deleting an
// account takes with it.
//
// The three are here rather than beside the methods they run through because none
// of them is about one method: the duplicate-key mapping is the same claim for a
// trace, a fact and an audit line, the rollback is about the four tables
// together, and the cascade is a foreign key declared once per table.

import (
	"context"
	"errors"
	"testing"

	"backend/internal/data/testdb"
)

func TestAKeyThatIsTakenIsReportedAsADuplicate(t *testing.T) {
	repo, resource, owner := newDBRepository(t)
	write(t, repo, func(tx Repository) error {
		if err := tx.AppendTrace(context.Background(), aTrace("mt_1", owner, atNine)); err != nil {
			return err
		}
		return tx.CreateFact(context.Background(), aFact("mf_1", owner, StatusActive, atNine))
	})

	// The engine says 1062, GORM turns it into one of its own errors, and the
	// adapter turns that into ErrDuplicate. The middle step is the one that cannot
	// be asserted without a database, and all three tables' writes go through it —
	// which is why the three are checked together rather than once.
	err := repo.Within(context.Background(), func(tx Repository) error {
		return tx.AppendTrace(context.Background(), aTrace("mt_1", owner, atTen))
	})
	if !errors.Is(err, ErrDuplicate) {
		t.Errorf("a trace id that is taken = %v, want a duplicate", err)
	}
	err = repo.Within(context.Background(), func(tx Repository) error {
		return tx.CreateFact(context.Background(), aFact("mf_1", owner, StatusActive, atTen))
	})
	if !errors.Is(err, ErrDuplicate) {
		t.Errorf("a fact id that is taken = %v, want a duplicate", err)
	}
	err = repo.Within(context.Background(), func(tx Repository) error {
		audit := Audit{ID: "ma_1", OwnerID: owner, Actor: ActorUser, Action: ActionFactStated,
			TargetKind: TargetFact, TargetID: "mf_1", Result: ResultDone, CreatedAt: atNoon}
		if err := tx.AppendAudit(context.Background(), audit); err != nil {
			return err
		}
		return tx.AppendAudit(context.Background(), audit)
	})
	if !errors.Is(err, ErrDuplicate) {
		t.Errorf("an audit id that is taken = %v, want a duplicate", err)
	}

	// ErrDuplicate is not a storage failure wearing a costume: the tables still
	// hold exactly what the first successful writes put there.
	if got := countRows(t, resource, "memory_traces"); got != 1 {
		t.Errorf("%d traces are stored, want the one", got)
	}
}

func TestAFailedTransactionLeavesNothingBehind(t *testing.T) {
	repo, resource, owner := newDBRepository(t)

	// The observation is written and then the work fails. A fact with no evidence
	// is the state a transaction exists to prevent, and the trace is half of the
	// write that has to go back with it.
	err := repo.Within(context.Background(), func(tx Repository) error {
		if err := tx.AppendTrace(context.Background(), aTrace("mt_1", owner, atNine)); err != nil {
			return err
		}
		if err := tx.AppendAudit(context.Background(), Audit{
			ID: "ma_1", OwnerID: owner, Actor: ActorUser, Action: ActionFactStated,
			TargetKind: TargetFact, Result: ResultDone, CreatedAt: atNoon,
		}); err != nil {
			return err
		}
		return errors.New("the caller changed its mind")
	})
	if err == nil {
		t.Fatal("Within() reported success over a failing step")
	}
	if got := countRows(t, resource, "memory_traces"); got != 0 {
		t.Errorf("%d traces survived the rollback", got)
	}
	if got := countRows(t, resource, "memory_audit"); got != 0 {
		t.Errorf("%d audit lines survived the rollback", got)
	}
	// And the ids are free again, which is the same claim from the outside: a
	// rolled-back row does not hold a key.
	write(t, repo, func(tx Repository) error {
		return tx.AppendTrace(context.Background(), aTrace("mt_1", owner, atNine))
	})
	write(t, repo, func(tx Repository) error {
		return tx.AppendAudit(context.Background(), Audit{
			ID: "ma_1", OwnerID: owner, Actor: ActorUser, Action: ActionFactStated,
			TargetKind: TargetFact, Result: ResultDone, CreatedAt: atNoon,
		})
	})
}

func TestDeletingAnAccountTakesItsMemoryWithIt(t *testing.T) {
	repo, resource, owner := newDBRepository(t)
	write(t, repo, func(tx Repository) error {
		if err := tx.AppendTrace(context.Background(), aTrace("mt_1", owner, atNine)); err != nil {
			return err
		}
		if err := tx.CreateFact(context.Background(), aFact("mf_1", owner, StatusActive, atNine)); err != nil {
			return err
		}
		if err := tx.AttachSource(context.Background(), "mf_1", "mt_1", atNine); err != nil {
			return err
		}
		return tx.AppendAudit(context.Background(), Audit{
			ID: "ma_1", OwnerID: owner, Actor: ActorUser, Action: ActionFactStated,
			TargetKind: TargetFact, TargetID: "mf_1", Result: ResultDone, CreatedAt: atNoon,
		})
	})

	testdb.Exec(t, resource, "DELETE FROM users WHERE user_id = ?", owner)

	// Every one of the four tables is emptied by the account going away, the
	// evidence included — it is reachable only through the fact it supports. This
	// is PRD §11.3's "the user must be able to delete Memory" taken to its end, and
	// it is a foreign key rather than a service method: an account deleted by any
	// route leaves nothing behind.
	for _, table := range []string{"memory_traces", "memory_facts", "memory_fact_sources", "memory_audit"} {
		if got := countRows(t, resource, table); got != 0 {
			t.Errorf("%s still holds %d rows after the account was deleted", table, got)
		}
	}
}

// TestAWritesEvidenceCannotOutliveWhatItNames checks the two foreign keys under
// `memory_fact_sources` from the direction nothing else here does: the repository
// has no delete path at all, so the only way to see them is to delete a row the
// way the schema would.
func TestAWritesEvidenceCannotOutliveWhatItNames(t *testing.T) {
	repo, resource, owner := newDBRepository(t)
	write(t, repo, func(tx Repository) error {
		if err := tx.AppendTrace(context.Background(), aTrace("mt_1", owner, atNine)); err != nil {
			return err
		}
		if err := tx.CreateFact(context.Background(), aFact("mf_1", owner, StatusActive, atNine)); err != nil {
			return err
		}
		return tx.AttachSource(context.Background(), "mf_1", "mt_1", atNine)
	})

	// Withdrawing the observation withdraws the support it gave. A fact whose last
	// piece of evidence is gone is exactly what the gate's second condition is
	// about, so this is not tidiness — it is the chain staying honest.
	testdb.Exec(t, resource, "DELETE FROM memory_traces WHERE trace_id = 'mt_1'")
	if got := countRows(t, resource, "memory_fact_sources"); got != 0 {
		t.Errorf("%d pieces of evidence outlived the observation they point at", got)
	}
	if got := countRows(t, resource, "memory_facts"); got != 1 {
		t.Errorf("%d facts are stored, want the fact to stay", got)
	}

	// And an evidence row naming something that does not exist cannot be written
	// in the first place.
	if err := testdb.Try(resource,
		"INSERT INTO memory_fact_sources (fact_id, trace_id) VALUES ('mf_nobody', 'mt_nobody')"); err == nil {
		t.Error("evidence was recorded for a fact and an observation that do not exist")
	}
}
