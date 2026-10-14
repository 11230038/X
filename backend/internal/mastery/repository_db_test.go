package mastery

// The path row's storage contract, checked against a real seekdb: the version
// guard, the NULL a row this package has not written yet carries, the four writes
// that surround the tree, and the schema that refuses a path written half way.
//
// None of it is knowable from a double. The version guard is one SQL statement
// (`COALESCE(version, 0) + 1` under a `COALESCE(version, 0) = ?`), whether a
// rollback takes an event with it is a property of the transaction and not of any
// Go type, and a CHECK constraint is not something Go sees at all.
//
// The link table — which conversations have touched a path, and the listing read
// off it — is next door in repository_link_db_test.go, because its questions are
// orderings and none of them are about this row.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestDBCreatingAPathWritesTheWholeOfIt(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	path := h.create(t, "mp_1", "学会闭包", actor)

	// Version 1 and not 0: the schema's CHECK refuses a version below one, and a
	// row at 0 is a tree nobody has changed.
	if path.Version != 1 {
		t.Errorf("the created path is at version %d, want 1", path.Version)
	}
	stored := h.storedPath(t, "mp_1")
	if stored.Version == nil || *stored.Version != 1 {
		t.Errorf("the stored version is %v, want 1", stored.Version)
	}
	if stored.State == nil || !strings.Contains(*stored.State, "kp_capture") {
		t.Errorf("the stored state is %v, want the tree that was created with it", stored.State)
	}

	// Four tables, one transaction. A path whose creation event was lost is a path
	// that cannot say where it came from, and the event log is the only place that
	// answer lives.
	if got := h.countRows(t, "mastery_paths"); got != 1 {
		t.Errorf("%d paths are stored, want one", got)
	}
	if got := h.countRows(t, "mastery_events"); got != 1 {
		t.Errorf("%d events are stored, want the creation", got)
	}
	if got := h.countRows(t, "mastery_topic_meta"); got != 1 {
		t.Errorf("%d metadata rows are stored, want one", got)
	}
	if got := h.countRows(t, "mastery_path_sessions"); got != 1 {
		t.Errorf("%d links are stored, want the conversation that made it", got)
	}

	events, err := h.repository.Events(context.Background(), "mp_1", h.caller)
	if err != nil {
		t.Fatalf("read the history: %v", err)
	}
	if len(events) != 1 || events[0].Type != EventPathCreated || events[0].Version != 1 {
		t.Fatalf("the history is %+v, want one creation at version 1", events)
	}
	if events[0].Actor != actor {
		t.Errorf("the creation is attributed to %+v, want %+v", events[0].Actor, actor)
	}

	meta, err := h.repository.Meta(context.Background(), "mp_1", h.caller)
	if err != nil {
		t.Fatalf("read the metadata: %v", err)
	}
	if meta.Goal != "学会闭包" || meta.Status != StatusActive || meta.Emoji == "" {
		t.Errorf("the metadata is %+v", meta)
	}
	// An emoji nobody chose is the default rather than a blank, which is the same
	// substitution a listing makes when it draws one.
	if meta.Emoji != defaultEmoji {
		t.Errorf("a path made without an emoji carries %q, want %q", meta.Emoji, defaultEmoji)
	}

	// And the path reads back whole: the same tree, byte for byte.
	read, err := h.repository.Path(context.Background(), "mp_1", h.caller)
	if err != nil {
		t.Fatalf("read the path back: %v", err)
	}
	first, err := EncodeTree(path.Tree)
	if err != nil {
		t.Fatalf("encode the created tree: %v", err)
	}
	second, err := EncodeTree(read.Tree)
	if err != nil {
		t.Fatalf("encode the tree that came back: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("the tree came back as\n%s\nwant\n%s", second, first)
	}
}

func TestDBAStateThatEncodesDifferentlyOnASecondPassCouldNeverBeComparedWithItself(t *testing.T) {
	h := newHarness(t)
	h.conversation(t, "s_1", "t_1")
	// The property the state round trip rests on: the bytes are a function of the
	// tree and nothing else, so encoding a tree that was decoded from a column
	// reproduces that column's bytes. A map anywhere inside the tree would break
	// this, and it would break nothing else — it would show up as the state column
	// changing on every read, and as a CAS that fails for no reason a caller can
	// see.
	tree := aTree()
	once, err := EncodeTree(tree)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := DecodeTree(once)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	twice, err := EncodeTree(decoded)
	if err != nil {
		t.Fatalf("re-encode: %v", err)
	}
	if string(once) != string(twice) {
		t.Errorf("the tree encodes as\n%s\nand then as\n%s", once, twice)
	}
	// Three times, because a single pass cannot tell a stable function from one
	// that happened to agree with itself once.
	third, err := EncodeTree(decoded)
	if err != nil {
		t.Fatalf("re-encode again: %v", err)
	}
	if string(twice) != string(third) {
		t.Errorf("the second and third encodings differ:\n%s\n%s", twice, third)
	}
}

func TestDBTheVersionGuardIsWhatStandsBetweenTwoWriters(t *testing.T) {
	h := newHarness(t)
	first := h.conversation(t, "s_1", "t_1")
	h.create(t, "mp_1", "学会闭包", first)
	second := h.turn(t, "t_2", "s_1")
	ctx := context.Background()

	// The first writer read version 1 and says so. It lands at 2.
	one := int64(1)
	updated, err := h.service.AddKnowledgePoint(ctx, AddKnowledgePointRequest{
		Change: Change{PathID: "mp_1", Actor: first, Expected: &one},
		ID:     "kp_new", Name: "值接收者", ModuleID: "m_basics",
	}, h.caller)
	if err != nil {
		t.Fatalf("add a knowledge point: %v", err)
	}
	if updated.Version != 2 {
		t.Errorf("the write reached version %d, want 2", updated.Version)
	}

	// The second writer read the same version and thought about it for a while.
	// Its write must not land: without the guard this is a lost update, which is
	// the one failure a reader never sees.
	_, err = h.service.AddKnowledgePoint(ctx, AddKnowledgePointRequest{
		Change: Change{PathID: "mp_1", Actor: second, Expected: &one},
		ID:     "kp_late", Name: "指针接收者", ModuleID: "m_basics",
	}, h.caller)
	if !errors.Is(err, ErrStaleState) {
		t.Fatalf("a write over a version that moved on: %v, want %v", err, ErrStaleState)
	}

	stored := h.storedPath(t, "mp_1")
	if stored.Version == nil || *stored.Version != 2 {
		t.Errorf("the row is at version %v, want the winner's 2", stored.Version)
	}
	if stored.State == nil || strings.Contains(*stored.State, "kp_late") {
		t.Errorf("the loser's tree was stored: %s", *stored.State)
	}
	// Nothing of the loser's is left anywhere: no second event, and no link for a
	// turn that never wrote.
	if got := h.countRows(t, "mastery_events"); got != 2 {
		t.Errorf("%d events are stored, want the creation and the winner's edit", got)
	}
	if got := h.countRows(t, "mastery_path_sessions"); got != 1 {
		t.Errorf("%d links are stored, want only the winning turn's conversation", got)
	}
}

func TestDBAWriteThatLosesTheGuardTakesItsEventWithIt(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	h.create(t, "mp_1", "学会闭包", actor)
	before := h.countRows(t, "mastery_events")

	// A change that fails inside the transaction — here because the knowledge point
	// it names is not in the tree — must leave neither a state nor an event. The
	// event is written before the state (so that a state which cannot be explained
	// is not a state that can be stored), and the transaction is what makes that
	// ordering safe: written first, rolled back by the same rollback.
	_, err := h.service.RemoveKnowledgePoint(context.Background(), RemoveKnowledgePointRequest{
		Change: Change{PathID: "mp_1", Actor: actor},
		ID:     "kp_nobody",
	}, h.caller)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("removing a knowledge point that is not there: %v, want %v", err, ErrNotFound)
	}
	if got := h.countRows(t, "mastery_events"); got != before {
		t.Errorf("%d events are stored after a failed change, want %d", got, before)
	}
	stored := h.storedPath(t, "mp_1")
	if stored.Version == nil || *stored.Version != 1 {
		t.Errorf("a failed change moved the version to %v, want it left at 1", stored.Version)
	}
}

func TestDBAChangeThatFailsNeverLeavesAnEventBehind(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	h.create(t, "mp_1", "学会闭包", actor)

	// The same claim one level down, where the case can say exactly which write
	// failed: the event lands inside the transaction and the transaction is then
	// abandoned. A repository that committed each statement on its own would leave
	// this event standing, and the history would describe a tree that never moved.
	sentinel := errors.New("the caller changed its mind")
	err := h.repository.Within(context.Background(), func(repository Repository) error {
		if err := repository.AppendEvent(context.Background(), Event{
			ID: "mev_orphan", PathID: "mp_1", Type: EventPathMapped, Version: 2, Actor: actor,
			CreatedAt: atTen,
		}, h.caller); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Within() error = %v, want the caller's own", err)
	}
	events, err := h.repository.Events(context.Background(), "mp_1", h.caller)
	if err != nil {
		t.Fatalf("read the history: %v", err)
	}
	for _, event := range events {
		if event.ID == "mev_orphan" {
			t.Fatalf("the abandoned transaction's event is in the history: %+v", event)
		}
	}
}

func TestDBAVersionNobodyHasWrittenReadsAsZeroAndCanBeWritten(t *testing.T) {
	h := newHarness(t)
	h.conversation(t, "s_1", "t_1")
	// A row left behind by a deployment that never wrote a tree: the columns are
	// nullable, and the two NULLs mean the same thing the domain does — nothing has
	// been changed here yet. The read coalesces them so that the first write is the
	// same statement as every other write, which is what keeps compare-and-swap
	// from having a special case, and a special case in a CAS is where lost updates
	// come from.
	//
	// The row is read as the administrator because owner_id is NULL too: migration
	// 00014 left every pre-V5 row owned by nobody, and the scope the repository
	// builds matches that column as `owner_id = ? OR owner_id IS NULL` for the
	// administrator and `owner_id = ?` for everyone else. A path from before this
	// version is therefore the administrator's to read, which is the state this case
	// needs to exercise the version guard's own NULL handling.
	if err := h.resource.GORM().Exec("INSERT INTO mastery_paths "+
		"(mastery_path_id, owner_session_id, state_json, version, creat_time) "+
		"VALUES ('mp_old', 's_1', NULL, NULL, ?)", atNine).Error; err != nil {
		t.Fatalf("write a path nobody has changed: %v", err)
	}

	path, err := h.repository.Path(context.Background(), "mp_old", h.admin)
	if err != nil {
		t.Fatalf("read it: %v", err)
	}
	if path.Version != 0 {
		t.Errorf("a row with no version reads as %d, want 0", path.Version)
	}
	if len(path.Tree.Modules) != 0 {
		t.Errorf("a row with no state reads as the tree %+v, want none", path.Tree)
	}
	// And the metadata is missing rather than empty, which is a different answer
	// and one the caller has to be able to tell apart (Load reports the tree).
	if _, err := h.repository.Meta(context.Background(), "mp_old", h.admin); !errors.Is(err, ErrNotFound) {
		t.Errorf("reading the metadata of a path that has none: %v, want %v", err, ErrNotFound)
	}

	version, err := h.repository.SaveTree(context.Background(), "mp_old", 0, aTree(), atTen, h.admin)
	if err != nil {
		t.Fatalf("write the first tree: %v", err)
	}
	if version != 1 {
		t.Errorf("the first write reached version %d, want 1", version)
	}
	if stored := h.storedPath(t, "mp_old"); stored.Version == nil || *stored.Version != 1 {
		t.Errorf("the row is at version %v, want 1", stored.Version)
	}
}

func TestDBAMetadataRowIsStartedOnceAndThenRewrittenInPlace(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	h.create(t, "mp_1", "学会闭包", actor)
	ctx := context.Background()

	// Turning a path into something else is a rewrite of one row, not a version and
	// not a history: mastery_events is the tree's history, and a reworded goal is
	// not a fact about the tree.
	goal := "学会闭包和逃逸"
	meta, err := h.service.UpdateMeta(ctx, MetaRequest{PathID: "mp_1", Goal: &goal}, h.caller)
	if err != nil {
		t.Fatalf("rename the path: %v", err)
	}
	if meta.Goal != goal {
		t.Errorf("the path is now called %q, want %q", meta.Goal, goal)
	}
	if got := h.countRows(t, "mastery_topic_meta"); got != 1 {
		t.Errorf("%d metadata rows are stored, want the rewrite to have been in place", got)
	}
	stored := h.storedPath(t, "mp_1")
	if stored.Version == nil || *stored.Version != 1 {
		t.Errorf("renaming took the version to %v, want it left at 1", stored.Version)
	}
	if got := h.countRows(t, "mastery_events"); got != 1 {
		t.Errorf("%d events are stored, want only the creation: a new goal is not a tree change", got)
	}

	// And a path from before this version has a tree and no metadata row; the first
	// rewrite is how it gets one rather than a failure. It is written as the
	// administrator for the same reason as the case above: the row has no owner, so
	// the account that can reach it is the one whose scope admits NULL.
	if err := h.resource.GORM().Exec("INSERT INTO mastery_paths "+
		"(mastery_path_id, owner_session_id, state_json, version, creat_time) "+
		"VALUES ('mp_old', 's_1', '{}', 1, ?)", atNine).Error; err != nil {
		t.Fatalf("write an old path: %v", err)
	}
	description := "从零开始"
	if _, err := h.service.UpdateMeta(ctx, MetaRequest{PathID: "mp_old", Description: &description}, h.admin); err != nil {
		t.Fatalf("write metadata for a path that has none: %v", err)
	}
	read, err := h.repository.Meta(ctx, "mp_old", h.admin)
	if err != nil {
		t.Fatalf("read it back: %v", err)
	}
	if read.Description != description || read.PathID != "mp_old" {
		t.Errorf("the metadata of the old path is %+v", read)
	}
	// A status this version does not write is refused rather than stored: nothing
	// in MP-01–05 reads the column, so accepting "archived" would be inventing a
	// vocabulary and then not honouring it.
	archived := "archived"
	if _, err := h.service.UpdateMeta(ctx, MetaRequest{PathID: "mp_1", Status: &archived}, h.caller); err == nil {
		t.Error("a status this version does not write was accepted")
	}
}

func TestDBAPathCannotBeWrittenHalfWay(t *testing.T) {
	h := newHarness(t)
	h.conversation(t, "s_1", "t_1")
	// The schema states what the domain states, and it states it for every writer:
	// these are the rows the CHECK constraints and the foreign key in migration
	// 00004 exist to refuse, and a test that only went through the adapters would
	// never see them fire.
	for _, scenario := range []struct{ name, query string }{
		{"a path with no id",
			"INSERT INTO mastery_paths (mastery_path_id, owner_session_id) VALUES ('', 's_1')"},
		{"a path owned by nobody",
			"INSERT INTO mastery_paths (mastery_path_id, owner_session_id) VALUES ('mp_x', '')"},
		{"a path at version zero",
			"INSERT INTO mastery_paths (mastery_path_id, owner_session_id, version) VALUES ('mp_x', 's_1', 0)"},
		{"a path over a conversation that is not there",
			"INSERT INTO mastery_paths (mastery_path_id, owner_session_id) VALUES ('mp_x', 's_missing')"},
		{"a link to a path that is not there",
			"INSERT INTO mastery_path_sessions (path_id, session_id, last_seen_time) VALUES " +
				"('mp_missing', 's_1', NOW())"},
		{"a metadata row for a path that is not there",
			"INSERT INTO mastery_topic_meta (path_id, goal) VALUES ('mp_missing', 'x')"},
	} {
		// The insert is attempted and expected to fail. testdb.Try is the way to
		// attempt one without failing the case on the error it wants.
		if err := h.try(scenario.query); err == nil {
			t.Errorf("%s was accepted, want the schema to refuse it", scenario.name)
		}
	}
	if got := h.countRows(t, "mastery_paths"); got != 0 {
		t.Errorf("%d paths are stored, want none", got)
	}

	// Deleting the conversation takes its paths with them: the owner is a foreign
	// key with ON DELETE CASCADE, and a path whose conversation is gone is a tree
	// nobody can reach.
	h.create(t, "mp_1", "学会闭包", Actor{SessionID: "s_1", TurnID: "t_1"})
	if err := h.resource.GORM().Exec("DELETE FROM sessions WHERE session_id = 's_1'").Error; err != nil {
		t.Fatalf("delete the conversation: %v", err)
	}
	for _, table := range []string{
		"mastery_paths", "mastery_events", "mastery_topic_meta", "mastery_path_sessions",
	} {
		if got := h.countRows(t, table); got != 0 {
			t.Errorf("%d rows are left in %s after the conversation went", got, table)
		}
	}
}
