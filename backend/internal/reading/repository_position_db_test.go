package reading

// Where a reader stopped, against a real seekdb: the composite key one position
// per material and conversation is, and the compare and swap that makes two tabs
// a conflict rather than a silent overwrite.
//
// The write is what makes this a database's business rather than the service's.
// `revision = revision + 1` inside a WHERE clause is an answer only the storage
// engine can give, and the service's translation of it — a false into
// ErrStalePosition — is only as good as the row count it is given.

import (
	"context"
	"errors"
	"testing"

	"backend/internal/data/testdb"
)

func TestDBTheFirstPositionForAPairIsOneRowAndTheSecondIsACollision(t *testing.T) {
	repo, resource := newDBRepository(t)
	storeMaterialRow(t, resource, "rm_1", "doc_1", atSecond(1), nil)
	storeSessionRow(t, resource, "s_1", atSecond(1))
	ctx := context.Background()

	first := Position{
		MaterialID: "rm_1", SessionID: "s_1", Revision: 1,
		Locator:   Locator{MaterialID: "rm_1", Ordinal: 2, CharStart: 10, CharEnd: 10},
		UpdatedAt: atSecond(2),
	}
	if err := repo.CreatePosition(ctx, first); err != nil {
		t.Fatalf("create the first position: %v", err)
	}
	// The key is the pair, so a second first-write is the same row said twice —
	// which is exactly the race the service turns into a conflict.
	second := first
	second.Locator = Locator{MaterialID: "rm_1", CharStart: 99, CharEnd: 99}
	if err := repo.CreatePosition(ctx, second); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("creating the same pair twice: %v, want %v", err, ErrDuplicate)
	}
	if got := countRows(t, resource, "reading_positions"); got != 1 {
		t.Errorf("%d positions are stored, want the one", got)
	}

	// What was stored is what was asked for, coordinate and all, decoded out of
	// the JSON column rather than read back from the caller's value.
	stored, err := repo.Position(ctx, "rm_1", "s_1")
	if err != nil {
		t.Fatalf("read the position back: %v", err)
	}
	if stored.Revision != 1 || stored.Locator.CharStart != 10 || stored.Locator.Ordinal != 2 {
		t.Errorf("the stored position is %+v", stored)
	}
	if !stored.UpdatedAt.Equal(atSecond(2)) {
		t.Errorf("the stored position was written at %v, want the moment it was saved", stored.UpdatedAt)
	}
	// A different conversation is a different key and its own place.
	if err := repo.CreatePosition(ctx, Position{
		MaterialID: "rm_1", SessionID: "s_2", Revision: 1,
		Locator: Locator{MaterialID: "rm_1", CharStart: 4, CharEnd: 4},
	}); err != nil {
		t.Fatalf("create a second reader's position: %v", err)
	}
	if _, err := repo.Position(ctx, "rm_1", "s_missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a reader who has not scrolled: %v, want %v", err, ErrNotFound)
	}
}

func TestDBAPositionMovesOnlyFromTheRevisionItWasReadAt(t *testing.T) {
	repo, resource := newDBRepository(t)
	storeMaterialRow(t, resource, "rm_1", "doc_1", atSecond(1), nil)
	storeSessionRow(t, resource, "s_1", atSecond(1))
	ctx := context.Background()

	if err := repo.CreatePosition(ctx, Position{
		MaterialID: "rm_1", SessionID: "s_1", Revision: 1,
		Locator: Locator{MaterialID: "rm_1", CharStart: 10, CharEnd: 10},
	}); err != nil {
		t.Fatalf("create the position: %v", err)
	}

	moved, err := repo.SavePosition(ctx, "rm_1", "s_1", 1,
		Locator{MaterialID: "rm_1", CharStart: 40, CharEnd: 40}, atSecond(3))
	if err != nil {
		t.Fatalf("move the position: %v", err)
	}
	if !moved {
		t.Fatal("moving a position from the revision it was read at reported nothing moved")
	}
	stored, err := repo.Position(ctx, "rm_1", "s_1")
	if err != nil {
		t.Fatalf("read the position back: %v", err)
	}
	// `revision = revision + 1` and not a value: the revision that lands is the
	// one the row had plus one, which is what makes the guard a comparison rather
	// than a write.
	if stored.Revision != 2 || stored.Locator.CharStart != 40 {
		t.Errorf("the moved position is %+v", stored)
	}
	if !stored.UpdatedAt.Equal(atSecond(3)) {
		t.Errorf("the moved position was written at %v", stored.UpdatedAt)
	}

	// The same revision again is a row that has moved on: the answer is the
	// false this whole path exists to be able to give, and nothing is written.
	moved, err = repo.SavePosition(ctx, "rm_1", "s_1", 1,
		Locator{MaterialID: "rm_1", CharStart: 80, CharEnd: 80}, atSecond(4))
	if err != nil {
		t.Fatalf("move the position from a stale revision: %v", err)
	}
	if moved {
		t.Error("a write against a revision that is no longer current reported a move")
	}
	// A revision nobody ever had is the same answer, and a pair with no row at
	// all is the same answer again: what the caller learns is "it did not move".
	for _, scenario := range []struct {
		name, material, session string
		expected                int64
	}{
		{"a revision ahead of the row", "rm_1", "s_1", 7},
		{"a pair with no position", "rm_1", "s_2", 1},
	} {
		moved, err = repo.SavePosition(ctx, scenario.material, scenario.session, scenario.expected,
			Locator{MaterialID: scenario.material, CharStart: 1, CharEnd: 1}, atSecond(5))
		if err != nil {
			t.Fatalf("%s: %v", scenario.name, err)
		}
		if moved {
			t.Errorf("%s reported a move", scenario.name)
		}
	}
	stored, err = repo.Position(ctx, "rm_1", "s_1")
	if err != nil {
		t.Fatalf("read the position back: %v", err)
	}
	if stored.Revision != 2 || stored.Locator.CharStart != 40 {
		t.Errorf("a refused write changed the row to %+v", stored)
	}
}

func TestDBAPositionCannotExistHalfWritten(t *testing.T) {
	_, resource := newDBRepository(t)
	storeMaterialRow(t, resource, "rm_1", "doc_1", atSecond(1), nil)
	storeSessionRow(t, resource, "s_1", atSecond(1))

	// The schema states the two things the adapter also states, and it states
	// them for every writer rather than for this one.
	for _, scenario := range []struct{ name, query string }{
		{"a position at a revision before the first one",
			"INSERT INTO reading_positions (material_id, session_id, locator_json, revision) " +
				"VALUES ('rm_1', 's_1', '{}', 0)"},
		{"a position with no material named",
			"INSERT INTO reading_positions (material_id, session_id, locator_json, revision) " +
				"VALUES ('', 's_1', '{}', 1)"},
		{"a position over a material that is not there",
			"INSERT INTO reading_positions (material_id, session_id, locator_json, revision) " +
				"VALUES ('rm_missing', 's_1', '{}', 1)"},
		{"a position over a conversation that is not there",
			"INSERT INTO reading_positions (material_id, session_id, locator_json, revision) " +
				"VALUES ('rm_1', 's_missing', '{}', 1)"},
	} {
		if err := testdb.Try(resource, scenario.query); err == nil {
			t.Errorf("%s was accepted, want the schema to refuse it", scenario.name)
		}
	}
	if got := countRows(t, resource, "reading_positions"); got != 0 {
		t.Errorf("%d positions are stored, want none", got)
	}
}

func TestDBDeletingAMaterialTakesItsPositionsWithIt(t *testing.T) {
	repo, resource := newDBRepository(t)
	storeMaterialRow(t, resource, "rm_1", "doc_1", atSecond(1), nil)
	storeMaterialRow(t, resource, "rm_2", "doc_2", atSecond(1), nil)
	storeSessionRow(t, resource, "s_1", atSecond(1))
	ctx := context.Background()
	for _, materialID := range []string{"rm_1", "rm_2"} {
		if err := repo.CreatePosition(ctx, Position{
			MaterialID: materialID, SessionID: "s_1", Revision: 1,
			Locator: Locator{MaterialID: materialID, CharStart: 1, CharEnd: 1},
		}); err != nil {
			t.Fatalf("create the position of %s: %v", materialID, err)
		}
	}

	// A place in a book that is gone is not a place, and the key says so: the
	// cascade is what makes deleting a material a whole delete rather than a
	// delete that leaves rows pointing at nothing.
	testdb.Exec(t, resource, "DELETE FROM reading_materials WHERE material_id = ?", "rm_1")
	if got := countRows(t, resource, "reading_positions"); got != 1 {
		t.Errorf("%d positions are left, want the other material's one", got)
	}
	if _, err := repo.Position(ctx, "rm_1", "s_1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("the deleted material's position: %v, want %v", err, ErrNotFound)
	}
}

func TestDBDeletingAConversationTakesItsPositionsWithIt(t *testing.T) {
	repo, resource := newDBRepository(t)
	storeMaterialRow(t, resource, "rm_1", "doc_1", atSecond(1), nil)
	storeSessionRow(t, resource, "s_1", atSecond(1))
	storeSessionRow(t, resource, "s_2", atSecond(1))
	ctx := context.Background()
	for _, sessionID := range []string{"s_1", "s_2"} {
		if err := repo.CreatePosition(ctx, Position{
			MaterialID: "rm_1", SessionID: sessionID, Revision: 1,
			Locator: Locator{MaterialID: "rm_1", CharStart: 1, CharEnd: 1},
		}); err != nil {
			t.Fatalf("create the position of %s: %v", sessionID, err)
		}
	}

	// Where a conversation had got to is part of the conversation, and tidying
	// the list of conversations tidies this with it. The marks on the same
	// material do the opposite, and that is the next file's subject.
	testdb.Exec(t, resource, "DELETE FROM sessions WHERE session_id = ?", "s_1")
	if got := countRows(t, resource, "reading_positions"); got != 1 {
		t.Errorf("%d positions are left, want the other conversation's one", got)
	}
	if _, err := repo.Position(ctx, "rm_1", "s_1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("the deleted conversation's position: %v, want %v", err, ErrNotFound)
	}
}
