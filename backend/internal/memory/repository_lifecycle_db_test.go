package memory

// What the one deletion in this package does to a real seekdb: which rows it takes
// with the fact, which ones it leaves, and what the cutoff a cleanup reads by
// actually compares.
//
// Two of the three claims here cannot be made anywhere else. That the evidence rows
// go with the fact is a foreign key's `ON DELETE CASCADE`, which no fake has; and
// that a purge with an empty list removes nothing is about a statement — `IN ()` —
// which is one of the two ways a delete turns into a delete-everything.

import (
	"context"
	"testing"
)

func TestPurgingAFactTakesItsEvidenceAndLeavesTheObservations(t *testing.T) {
	repo, resource, owner := newDBRepository(t)
	write(t, repo, func(tx Repository) error {
		for _, id := range []string{"mt_1", "mt_2"} {
			if err := tx.AppendTrace(context.Background(), aTrace(id, owner, atNine)); err != nil {
				return err
			}
		}
		for _, id := range []string{"mf_doomed", "mf_staying"} {
			if err := tx.CreateFact(context.Background(), aFact(id, owner, StatusActive, atNine)); err != nil {
				return err
			}
		}
		for _, source := range [][2]string{
			{"mf_doomed", "mt_1"}, {"mf_doomed", "mt_2"}, {"mf_staying", "mt_2"},
		} {
			if err := tx.AttachSource(context.Background(), source[0], source[1], atNoon); err != nil {
				return err
			}
		}
		return nil
	})

	var purged int
	write(t, repo, func(tx Repository) error {
		var err error
		purged, err = tx.PurgeFacts(context.Background(), owner, []string{"mf_doomed"})
		return err
	})
	if purged != 1 {
		t.Errorf("the purge removed %d rows, want the one it named", purged)
	}

	// The fact is gone and its evidence went with it, by the foreign key rather than
	// by a second statement — which is why this has to be checked here: a purge that
	// left the evidence behind would leave rows pointing at a fact that is not there.
	if got := countRows(t, resource, "memory_facts"); got != 1 {
		t.Errorf("%d facts are stored, want the one that stayed", got)
	}
	if got := countRows(t, resource, "memory_fact_sources"); got != 1 {
		t.Errorf("%d pieces of evidence are stored, want the one the surviving fact owns", got)
	}
	// And L1 is untouched: what a surface said stays said, which is the same rule a
	// retraction is held to (§7.1) and the sentence PRD §11.3 writes about L1.
	if got := countRows(t, resource, "memory_traces"); got != 2 {
		t.Errorf("%d observations are stored, want both", got)
	}
	if _, err := repo.Fact(context.Background(), owner, "mf_doomed"); err == nil {
		t.Error("the purged fact is still readable")
	}
	if _, err := repo.TracesOf(context.Background(), "mf_staying"); err != nil {
		t.Errorf("the surviving fact's evidence could not be read: %v", err)
	}
}

// TestAPurgeWithNothingToPurgeRemovesNothing checks the guard that keeps the one
// deletion in this package from being a deletion of everything.
//
// `fact_id IN ()` is not a statement with no matches — it is a statement with no
// WHERE clause, and GORM's own protection against a global delete is the only thing
// standing between that and an empty table. The guard is the early return, and this
// is the case that says so.
func TestAPurgeWithNothingToPurgeRemovesNothing(t *testing.T) {
	repo, resource, owner := newDBRepository(t)
	write(t, repo, func(tx Repository) error {
		return tx.CreateFact(context.Background(), aFact("mf_1", owner, StatusActive, atNine))
	})

	for _, ids := range [][]string{nil, {}} {
		purged, err := repo.PurgeFacts(context.Background(), owner, ids)
		if err != nil {
			t.Fatalf("PurgeFacts(%v) error = %v", ids, err)
		}
		if purged != 0 {
			t.Errorf("a purge of %v removed %d rows", ids, purged)
		}
	}
	if got := countRows(t, resource, "memory_facts"); got != 1 {
		t.Errorf("%d facts are stored, want the one", got)
	}

	// And another account's fact is not reached by naming its id: the owner is in
	// the WHERE clause, so the same list against the wrong account removes nothing.
	other := createAccount(t, resource, "bob")
	purged, err := repo.PurgeFacts(context.Background(), other, []string{"mf_1"})
	if err != nil {
		t.Fatalf("PurgeFacts() error = %v", err)
	}
	if purged != 0 {
		t.Errorf("another account's purge removed %d of this account's rows", purged)
	}
	if got := countRows(t, resource, "memory_facts"); got != 1 {
		t.Errorf("%d facts are stored, want the one", got)
	}
}

// TestACleanupCutoffReadsTheAdmissionAndNotTheLastEdit pins which column the cutoff
// compares, because the two columns are both on the row and only one of them is the
// answer a cleanup means.
//
// A fact admitted in September whose wording was fixed in October is a September
// fact: a cleanup "up to September" that a later edit draws a row into would be a
// preview that named a batch and a commit that removed a different one.
func TestACleanupCutoffReadsTheAdmissionAndNotTheLastEdit(t *testing.T) {
	repo, _, owner := newDBRepository(t)
	write(t, repo, func(tx Repository) error {
		for _, fact := range []Fact{
			aFact("mf_september", owner, StatusActive, atNine),
			aFact("mf_october", owner, StatusActive, atNoon),
		} {
			if err := tx.CreateFact(context.Background(), fact); err != nil {
				return err
			}
		}
		// The September fact is edited in October, which moves the column the listing
		// orders by and must not move the one the cutoff reads.
		held, err := tx.Fact(context.Background(), owner, "mf_september")
		if err != nil {
			return err
		}
		edited := held
		edited.Scope = "learning"
		_, err = tx.SaveFact(context.Background(), edited, held.Version, atNoon)
		return err
	})

	read, err := repo.Facts(context.Background(), FactQuery{OwnerID: owner, Before: atNoon})
	if err != nil {
		t.Fatalf("Facts() error = %v", err)
	}
	// `creat_time < atNoon` keeps the September fact and drops the October one, and
	// the September fact's edit does not move it out of the window it was admitted in
	// nor into anything.
	if got := factIDs(read); len(got) != 1 || got[0] != "mf_september" {
		t.Errorf("the cutoff named %v, want the fact admitted before it", got)
	}
	// The same read with no cutoff is the whole memory, which is what the export
	// asks for.
	all, err := repo.Facts(context.Background(), FactQuery{OwnerID: owner})
	if err != nil {
		t.Fatalf("Facts() error = %v", err)
	}
	if len(all) != 2 {
		t.Errorf("the unbounded read named %v, want both", factIDs(all))
	}
}
