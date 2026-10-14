package memory

// L2's storage contract, checked against a real seekdb. The one that cannot be
// made anywhere else is `TestAFactWriteIsGuardedByTheVersionItWasReadAt`: a
// compare-and-set is a claim about what two concurrent writers do, and a test
// double can only restate it.

import (
	"context"
	"errors"
	"testing"
)

func TestOnlyAFactInForceIsTheOneAlreadyBelieved(t *testing.T) {
	repo, resource, owner := newDBRepository(t)
	other := createAccount(t, resource, "bob")
	write(t, repo, func(tx Repository) error {
		if err := tx.CreateFact(context.Background(), aFact("mf_retracted", owner, StatusRetracted, atNine)); err != nil {
			return err
		}
		if err := tx.CreateFact(context.Background(), aFact("mf_superseded", owner, StatusSuperseded, atTen)); err != nil {
			return err
		}
		if err := tx.CreateFact(context.Background(), aFact("mf_theirs", other, StatusActive, atEleven)); err != nil {
			return err
		}
		return tx.CreateFact(context.Background(), aFact("mf_active", owner, StatusActive, atNoon))
	})

	fact, err := repo.ActiveFact(context.Background(), owner, Fingerprint(statement))
	if err != nil {
		t.Fatalf("ActiveFact() error = %v", err)
	}
	// A fact the user took back is not one the system believes, and neither is a
	// fact that was merged into another. That is also why a fresh observation
	// after a retraction can admit the claim again, under a new id, with the
	// retraction of the old one still on the record.
	if fact.ID != "mf_active" {
		t.Errorf("ActiveFact() = %s, want the fact in force", fact.ID)
	}

	if _, err := repo.ActiveFact(context.Background(), owner, Fingerprint("从来没人说过这句话")); !errors.Is(err, ErrNotFound) {
		t.Errorf("ActiveFact() for an unheard statement = %v, want not found", err)
	}

	// The read that takes an id refuses to answer about somebody else's fact by
	// saying it is not there. The distinction is the whole of MEM-04's point.
	if _, err := repo.Fact(context.Background(), owner, "mf_theirs"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Fact() of another account's fact = %v, want not found", err)
	}
	if _, err := repo.Fact(context.Background(), other, "mf_theirs"); err != nil {
		t.Errorf("Fact() of the account's own fact = %v, want it", err)
	}
}

// TestTheListingPutsWhatChangedLastFirst checks the order a listing promises: a
// fact the user edited yesterday reads above one the gate admitted last month,
// and a fact that was never edited is placed by when it was created rather than
// sorted behind the edited ones as though it were older than it is.
func TestTheListingPutsWhatChangedLastFirst(t *testing.T) {
	repo, _, owner := newDBRepository(t)
	write(t, repo, func(tx Repository) error {
		for _, fact := range []Fact{
			aFact("mf_oldest", owner, StatusActive, atNine),
			aFact("mf_middle", owner, StatusActive, atTen),
			aFact("mf_newest", owner, StatusActive, atNoon),
		} {
			if err := tx.CreateFact(context.Background(), fact); err != nil {
				return err
			}
		}
		// The oldest fact is edited, which should bring it to the top — the read
		// is "what have I changed lately", not "what did the gate admit lately".
		held, err := tx.Fact(context.Background(), owner, "mf_oldest")
		if err != nil {
			return err
		}
		edited := held
		edited.Scope = "learning"
		_, err = tx.SaveFact(context.Background(), edited, held.Version, atNoon)
		return err
	})

	read, err := repo.Facts(context.Background(), FactQuery{OwnerID: owner})
	if err != nil {
		t.Fatalf("Facts() error = %v", err)
	}
	// mf_oldest and mf_newest were both changed in the same second — the columns
	// hold whole seconds — so the tie falls to the id, descending, the same rule
	// the trace listing uses. Descending on these two puts mf_oldest first: that is
	// the id order and not a claim about the facts, and it is exactly why the
	// tie-break has to be written down, because the names say the opposite.
	want := []string{"mf_oldest", "mf_newest", "mf_middle"}
	if len(read) != len(want) {
		t.Fatalf("Facts() = %+v, want three", read)
	}
	for index, id := range want {
		if read[index].ID != id {
			t.Fatalf("Facts() = %v, want %v", factIDs(read), want)
		}
	}

	// A status filter is a different question from the same table: what is in
	// force, against what the account has ever had.
	inForce, err := repo.Facts(context.Background(), FactQuery{OwnerID: owner, Statuses: []Status{StatusActive}})
	if err != nil {
		t.Fatalf("Facts() error = %v", err)
	}
	if len(inForce) != 3 {
		t.Errorf("Facts() in force = %v, want all three", factIDs(inForce))
	}
	none, err := repo.Facts(context.Background(), FactQuery{OwnerID: owner, Statuses: []Status{StatusRetracted}})
	if err != nil {
		t.Fatalf("Facts() error = %v", err)
	}
	if len(none) != 0 {
		t.Errorf("Facts() retracted = %v, want none", factIDs(none))
	}
}

func factIDs(facts []Fact) []string {
	ids := make([]string, 0, len(facts))
	for _, fact := range facts {
		ids = append(ids, fact.ID)
	}
	return ids
}

func TestAFactWriteIsGuardedByTheVersionItWasReadAt(t *testing.T) {
	repo, resource, owner := newDBRepository(t)
	wrote := aFact("mf_1", owner, StatusActive, atNine)
	write(t, repo, func(tx Repository) error { return tx.CreateFact(context.Background(), wrote) })

	held, err := repo.Fact(context.Background(), owner, "mf_1")
	if err != nil {
		t.Fatalf("Fact() error = %v", err)
	}
	if held.Version != 1 || !held.UpdatedAt.IsZero() {
		t.Fatalf("a new fact is %+v, want version 1 and no update time", held)
	}

	edited := held
	edited.Statement = "对方正在学 Rust"
	edited.Fingerprint = Fingerprint(edited.Statement)
	reached, err := repo.SaveFact(context.Background(), edited, held.Version, atNoon)
	if err != nil {
		t.Fatalf("SaveFact() error = %v", err)
	}
	if reached != 2 {
		t.Errorf("SaveFact() reached version %d, want 2", reached)
	}
	stored, err := repo.Fact(context.Background(), owner, "mf_1")
	if err != nil {
		t.Fatalf("Fact() error = %v", err)
	}
	if stored.Statement != edited.Statement || stored.Fingerprint != edited.Fingerprint {
		t.Errorf("the fact reads %+v, want the edit", stored)
	}
	// An untouched fact has no update time; an edited one has the one it was
	// edited at, which is also the column the listing orders by.
	if !stored.UpdatedAt.Equal(atNoon) {
		t.Errorf("the fact was last changed at %s, want %s", stored.UpdatedAt, atNoon)
	}

	// The second writer is the one that loses: it read version 1 and the row is at
	// version 2. This is one statement with the version in its WHERE clause, not a
	// read followed by a write, and only the engine can say so.
	if _, err := repo.SaveFact(context.Background(), held, held.Version, atNoon); !errors.Is(err, ErrStaleState) {
		t.Errorf("a write over the old version = %v, want a stale state", err)
	}
	after, err := repo.Fact(context.Background(), owner, "mf_1")
	if err != nil {
		t.Fatalf("Fact() error = %v", err)
	}
	if after.Version != 2 || after.Statement != edited.Statement {
		t.Errorf("the failed write changed the row: %+v", after)
	}

	// A fact that is gone and a fact that moved on are different answers, and the
	// adapter tells them apart by reading again. The caller acts on them
	// differently: one is "start over", the other is "it is not yours".
	gone := aFact("mf_gone", owner, StatusActive, atNine)
	if _, err := repo.SaveFact(context.Background(), gone, 1, atNoon); !errors.Is(err, ErrNotFound) {
		t.Errorf("a write to a fact that is not there = %v, want not found", err)
	}
	if got := countRows(t, resource, "memory_facts"); got != 1 {
		t.Errorf("%d facts are stored, want the one", got)
	}
}

func TestAttachingTheSameEvidenceTwiceIsNotAnError(t *testing.T) {
	repo, resource, owner := newDBRepository(t)
	write(t, repo, func(tx Repository) error {
		if err := tx.AppendTrace(context.Background(), aTrace("mt_1", owner, atNine)); err != nil {
			return err
		}
		if err := tx.CreateFact(context.Background(), aFact("mf_1", owner, StatusActive, atNine)); err != nil {
			return err
		}
		for twice := 0; twice < 3; twice++ {
			if err := tx.AttachSource(context.Background(), "mf_1", "mt_1", atTen); err != nil {
				return err
			}
		}
		return nil
	})

	if got := countRows(t, resource, "memory_fact_sources"); got != 1 {
		t.Errorf("%d pieces of evidence are stored, want one", got)
	}
}

func TestAMergeUnionsTheEvidenceAndClearsTheLoser(t *testing.T) {
	repo, _, owner := newDBRepository(t)
	write(t, repo, func(tx Repository) error {
		for _, id := range []string{"mt_1", "mt_2", "mt_3"} {
			if err := tx.AppendTrace(context.Background(), aTrace(id, owner, atNine)); err != nil {
				return err
			}
		}
		if err := tx.CreateFact(context.Background(), aFact("mf_old", owner, StatusActive, atNine)); err != nil {
			return err
		}
		if err := tx.CreateFact(context.Background(), aFact("mf_new", owner, StatusActive, atTen)); err != nil {
			return err
		}
		// mt_2 is evidence for both, which is what two facts saying the same thing
		// are made of: the same pair of observations admitted each of them.
		for _, source := range [][2]string{
			{"mf_old", "mt_1"}, {"mf_old", "mt_2"}, {"mf_new", "mt_2"}, {"mf_new", "mt_3"},
		} {
			if err := tx.AttachSource(context.Background(), source[0], source[1], atNoon); err != nil {
				return err
			}
		}
		return tx.MoveSources(context.Background(), "mf_new", "mf_old")
	})

	// The survivor rests on everything both facts rested on, and the shared piece
	// is there once rather than twice: the key is the pair, so a wholesale move
	// would have collided on it.
	survivor, err := repo.TracesOf(context.Background(), "mf_old")
	if err != nil {
		t.Fatalf("TracesOf() error = %v", err)
	}
	ids := make([]string, 0, len(survivor))
	for _, trace := range survivor {
		ids = append(ids, trace.ID)
	}
	if len(ids) != 3 || ids[0] != "mt_1" || ids[1] != "mt_2" || ids[2] != "mt_3" {
		t.Errorf("the survivor rests on %v, want all three oldest first", ids)
	}
	loser, err := repo.TracesOf(context.Background(), "mf_new")
	if err != nil {
		t.Fatalf("TracesOf() error = %v", err)
	}
	if len(loser) != 0 {
		t.Errorf("the merged fact still owns %+v", loser)
	}
}

func TestDuplicatesAreFoundPerAccountAndInForceOnly(t *testing.T) {
	repo, resource, owner := newDBRepository(t)
	other := createAccount(t, resource, "bob")
	write(t, repo, func(tx Repository) error {
		for _, fact := range []Fact{
			aFact("mf_1", owner, StatusActive, atNine),
			aFact("mf_2", owner, StatusActive, atTen),
			// A third row for the same statement that is out of force: a merged or
			// retracted fact is not a duplicate to be merged again.
			aFact("mf_3", owner, StatusSuperseded, atEleven),
			aFact("mf_theirs", other, StatusActive, atNoon),
		} {
			if err := tx.CreateFact(context.Background(), fact); err != nil {
				return err
			}
		}
		return nil
	})

	groups, err := repo.DuplicateGroups(context.Background(), owner)
	if err != nil {
		t.Fatalf("DuplicateGroups() error = %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("DuplicateGroups() = %+v, want the one pair", groups)
	}
	group := groups[0]
	if group.Fingerprint != Fingerprint(statement) {
		t.Errorf("the group is for %q, want the fingerprint of what both facts say", group.Fingerprint)
	}
	// Oldest first is the order a merge reads its candidates in, and the first of
	// them is the fact that survives. A group in whatever order the engine chose
	// would merge into a different fact each time it ran.
	if len(group.Facts) != 2 || group.Facts[0].ID != "mf_1" || group.Facts[1].ID != "mf_2" {
		t.Errorf("the group holds %+v, want the two in force oldest first", group.Facts)
	}
	// A fact nobody else shares is not a group, and another account's fact is not
	// this account's business even when it says the same thing.
	theirs, err := repo.DuplicateGroups(context.Background(), other)
	if err != nil {
		t.Fatalf("DuplicateGroups() error = %v", err)
	}
	if len(theirs) != 0 {
		t.Errorf("the other account has %+v, want no duplicates of its own", theirs)
	}
}
