package main

// The count that guards an import, and the library row a collection's document
// needs before it can exist at all.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"

	"backend/internal/data"
	"backend/internal/data/testdb"
	"backend/internal/session"
	"backend/internal/turn"
)

// TestDBTheBusyPortCountsTheTurnsInFlight is AC-09's half of §2.13's second
// discipline, and it is a claim about a SQL predicate: the three statuses that
// mean "something is happening" are strings in a column, and a count that named
// the wrong ones would let an import run over a turn that is mid-sentence — or
// refuse every import for ever because it counted finished turns too.
func TestDBTheBusyPortCountsTheTurnsInFlight(t *testing.T) {
	fixture := newSinkFixture(t)
	store := session.NewGORMRepository(fixture.resource.GORM())
	// One conversation per turn, and that is 00009 rather than tidiness: an
	// unfinished turn takes its conversation's one slot in a unique index over a
	// generated column, so a second unfinished turn in the same conversation is
	// refused by the database — which is the rule the count below is about. A turn
	// also hangs off a conversation by a foreign key (00002), so the conversation
	// has to be written first either way.
	for index, status := range []turn.Status{
		turn.StatusQueued, turn.StatusRunning, turn.StatusWaitingInput,
		turn.StatusCompleted, turn.StatusFailed, turn.StatusCancelled,
	} {
		sessionID := fmt.Sprintf("sess_busy_%d", index)
		if err := store.Create(context.Background(), session.Session{ID: sessionID, OwnerID: fixture.owner}); err != nil {
			t.Fatalf("write the conversation turn %d hangs off: %v", index, err)
		}
		testdb.Exec(t, fixture.resource, "INSERT INTO turns "+
			"(turn_id, session_id, status, state_version, creat_time) VALUES (?, ?, ?, 1, ?)",
			fmt.Sprintf("turn_%d", index), sessionID, string(status), atSecond(index))
	}

	running, err := runningTurns{db: fixture.resource.GORM()}.Running(context.Background())
	if err != nil {
		t.Fatalf("Running() error = %v", err)
	}
	if running != 3 {
		t.Errorf("Running() = %d, want the three turns that have not finished", running)
	}
}

// addSinkLibraryFile writes the row a document's file foreign key needs.
//
// The path and the digest are not decoration: migration 00005 checks that the
// path is md/lib_<32 lowercase hex digits>.md with the id as the name before the
// extension, and that the digest is sixty-four lowercase hex digits — so a
// document fixture cannot invent a file id, it has to be one the library itself
// would have stored.
func addSinkLibraryFile(t *testing.T, resource *data.Resource, owner int64) string {
	t.Helper()
	id := fmt.Sprintf("lib_%032x", 1)
	sum := sha256.Sum256([]byte(id))
	testdb.Exec(t, resource, "INSERT INTO library_files "+
		"(id, sha256, filename, mime_type, size_bytes, library_path, is_deleted, owner_id) "+
		"VALUES (?, ?, ?, 'text/markdown', 0, ?, FALSE, ?)",
		id, hex.EncodeToString(sum[:]), id+".md", "md/"+id+".md", owner)
	return id
}
