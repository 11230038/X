package memory

// L1's storage contract, checked against a real seekdb: that a row round-trips
// as what it was, that a listing's order is decided even when two rows share a
// second, that the gate's read is one fingerprint of one account and nothing
// else, and that an evidence chain comes back oldest first.

import (
	"context"
	"testing"

	"backend/internal/data/testdb"
)

func TestATraceRoundTripsThroughTheTable(t *testing.T) {
	repo, resource, owner := newDBRepository(t)

	said := aTrace("mt_1", owner, atNine)
	said.SessionID, said.TurnID = "sess_1", "turn_1"
	said.Payload = []byte(`{"score":42}`)
	write(t, repo, func(tx Repository) error { return tx.AppendTrace(context.Background(), said) })

	read, err := repo.Traces(context.Background(), TraceQuery{OwnerID: owner})
	if err != nil {
		t.Fatalf("Traces() error = %v", err)
	}
	if len(read) != 1 {
		t.Fatalf("Traces() = %+v, want the one observation", read)
	}
	got := read[0]
	if got.ID != said.ID || got.Surface != said.Surface || got.Kind != said.Kind ||
		got.SessionID != "sess_1" || got.TurnID != "turn_1" || got.Statement != statement ||
		got.Fingerprint != said.Fingerprint {
		t.Errorf("Traces() = %+v, want what was written (%+v)", got, said)
	}
	if !sameJSON(t, got.Payload, `{"score":42}`) {
		t.Errorf("the payload reads %s, want the object that was written", got.Payload)
	}
	if !got.CreatedAt.Equal(atNine) {
		t.Errorf("the trace is dated %s, want %s", got.CreatedAt, atNine)
	}

	// An observation made outside any conversation, with nothing in its payload.
	// Both are stored as nothing and read back as the domain's own empty values,
	// which is the round trip this asserts: a NULL session is not the string ""
	// in the column, and a caller must not be able to tell the two apart.
	write(t, repo, func(tx Repository) error {
		return tx.AppendTrace(context.Background(), aTrace("mt_2", owner, atTen))
	})
	if stored := readStoredTrace(t, resource, "mt_2"); stored.SessionID != nil || stored.TurnID != nil {
		t.Errorf("a trace with no conversation stored %v/%v, want NULLs", stored.SessionID, stored.TurnID)
	}
	read, err = repo.Traces(context.Background(), TraceQuery{OwnerID: owner})
	if err != nil {
		t.Fatalf("Traces() error = %v", err)
	}
	if len(read) != 2 || read[0].ID != "mt_2" || read[0].SessionID != "" || read[0].TurnID != "" {
		t.Fatalf("Traces() = %+v, want the empty conversation read back as the empty string", read)
	}

	// The table is where "a fingerprint is a sha256" is a fact rather than a
	// convention: the CHECK refuses anything else, so a row written by some other
	// route cannot be one the gate silently fails to find.
	err = testdb.Try(resource, "INSERT INTO memory_traces "+
		"(trace_id, owner_id, surface, kind, statement, fingerprint) "+
		"VALUES ('mt_3', ?, 'mastery', 'goal', ?, 'not-a-hash')", owner, statement)
	if err == nil {
		t.Error("a trace with a fingerprint that is not 64 hex characters was stored")
	}
}

func TestTheTraceListingIsDecidedWhenTwoRowsShareASecond(t *testing.T) {
	repo, _, owner := newDBRepository(t)

	// Three observations in one second, written in an order that is not the order
	// they should be read in. A listing ordered by time alone would come back in
	// whatever order the engine chose, and the engine is free to choose
	// differently tomorrow.
	for _, id := range []string{"mt_a", "mt_c", "mt_b"} {
		trace := aTrace(id, owner, atNine)
		write(t, repo, func(tx Repository) error { return tx.AppendTrace(context.Background(), trace) })
	}

	read, err := repo.Traces(context.Background(), TraceQuery{OwnerID: owner})
	if err != nil {
		t.Fatalf("Traces() error = %v", err)
	}
	var ids []string
	for _, trace := range read {
		ids = append(ids, trace.ID)
	}
	// Newest first, and within one second descending by id — the tie is broken
	// rather than left to the engine.
	want := []string{"mt_c", "mt_b", "mt_a"}
	if len(ids) != len(want) {
		t.Fatalf("Traces() = %v, want %v", ids, want)
	}
	for index := range want {
		if ids[index] != want[index] {
			t.Fatalf("Traces() = %v, want %v", ids, want)
		}
	}
}

func TestTheGateReadsOneFingerprintOfOneAccount(t *testing.T) {
	repo, resource, owner := newDBRepository(t)
	other := createAccount(t, resource, "bob")

	write(t, repo, func(tx Repository) error {
		if err := tx.AppendTrace(context.Background(), aTrace("mt_mine", owner, atNine)); err != nil {
			return err
		}
		if err := tx.AppendTrace(context.Background(), aTrace("mt_theirs", other, atTen)); err != nil {
			return err
		}
		// The same words about something else: the fingerprint is the filter, so
		// this row is not the gate's business either.
		elsewhere := aTrace("mt_elsewhere", owner, atEleven)
		elsewhere.Statement = "对方正在学 Rust"
		elsewhere.Fingerprint = Fingerprint(elsewhere.Statement)
		return tx.AppendTrace(context.Background(), elsewhere)
	})

	read, err := repo.Traces(context.Background(), TraceQuery{
		OwnerID: owner, Fingerprint: Fingerprint(statement),
	})
	if err != nil {
		t.Fatalf("Traces() error = %v", err)
	}
	if len(read) != 1 || read[0].ID != "mt_mine" {
		t.Errorf("Traces() by fingerprint = %+v, want only this account's row for this statement", read)
	}
	// An account with nothing on record for a fingerprint is an empty slice, which
	// is the answer "you have never been told this" — not an error, and not the
	// other account's rows.
	empty, err := repo.Traces(context.Background(), TraceQuery{
		OwnerID: owner, Fingerprint: Fingerprint("从来没人说过这句话"),
	})
	if err != nil {
		t.Fatalf("Traces() error = %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("Traces() for an unheard statement = %+v, want nothing", empty)
	}
}

func TestEvidenceReadsOldestFirstAndOnlyForItsFact(t *testing.T) {
	repo, _, owner := newDBRepository(t)
	write(t, repo, func(tx Repository) error {
		for _, trace := range []Trace{aTrace("mt_late", owner, atNoon), aTrace("mt_early", owner, atNine)} {
			if err := tx.AppendTrace(context.Background(), trace); err != nil {
				return err
			}
		}
		if err := tx.CreateFact(context.Background(), aFact("mf_1", owner, StatusActive, atNine)); err != nil {
			return err
		}
		if err := tx.CreateFact(context.Background(), aFact("mf_2", owner, StatusActive, atTen)); err != nil {
			return err
		}
		for _, source := range [][2]string{{"mf_1", "mt_late"}, {"mf_1", "mt_early"}, {"mf_2", "mt_late"}} {
			if err := tx.AttachSource(context.Background(), source[0], source[1], atNoon); err != nil {
				return err
			}
		}
		return nil
	})

	read, err := repo.TracesOf(context.Background(), "mf_1")
	if err != nil {
		t.Fatalf("TracesOf() error = %v", err)
	}
	if len(read) != 2 || read[0].ID != "mt_early" || read[1].ID != "mt_late" {
		t.Errorf("TracesOf() = %+v, want the chain oldest first", read)
	}
	// The join is what keeps one fact's evidence from being another's: the same
	// observation supports both facts, and the read has to say which one it is
	// answering for.
	other, err := repo.TracesOf(context.Background(), "mf_2")
	if err != nil {
		t.Fatalf("TracesOf() error = %v", err)
	}
	if len(other) != 1 || other[0].ID != "mt_late" {
		t.Errorf("TracesOf() for the second fact = %+v, want only its own evidence", other)
	}
}
