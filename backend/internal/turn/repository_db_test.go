package turn

// The turn half of the storage contract, checked against a real seekdb: the
// slot a session gives to one turn at a time, the version a state change is
// guarded by, the sequence number an event claims, and the columns a failure
// keeps. Every one of them is a claim about what the database does — a lock, a
// unique key, a foreign key, a row count — and none is visible to a double.
//
// The runtime's own tests drive a whole turn through the memory repository in
// memory_test.go. This file is the other half: the same adapter a deployment
// uses, against the same engine.
//
// One database per package means nothing here calls t.Parallel, and every test
// starts on empty tables. turn_event_types is never one of them: the migration
// seeds it and the events reference it.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"backend/internal/data"
	"backend/internal/data/testdb"
	"backend/internal/event"
)

// dbTables are the tables these tests write, children first. messages is here
// because a session's delete reaches it before sessions can go.
var dbTables = []string{"turn_events", "turns", "messages", "sessions"}

func newDBRepository(t *testing.T) (*GORMRepository, *data.Resource) {
	t.Helper()
	resource := testdb.Open(t, "turn")
	testdb.Reset(t, resource, dbTables...)
	return NewGORMRepository(resource.GORM()), resource
}

// atSecond is a whole second on a fixed day. Timestamps are spelled out because
// DATETIME keeps whole seconds: two rows written in the same second tie, and an
// ordering asserted over tied rows asserts whatever the plan did that time.
func atSecond(second int) time.Time {
	return time.Date(2026, 10, 1, 12, 0, second, 0, time.UTC)
}

// storeSession writes the session a turn belongs to. It is raw rather than an
// adapter call because turns are what this package stores, and a turn's foreign
// key only needs the row to be there.
func storeSession(t *testing.T, resource *data.Resource, id string, at time.Time) {
	t.Helper()
	testdb.Exec(t, resource, "INSERT INTO sessions "+
		"(session_id, title, creat_time, update_time, is_deleted) VALUES (?, ?, ?, ?, FALSE)",
		id, id, at, at)
}

// queued is a turn as the service hands it to the adapter: a status, a first
// version, and the capability it was asked for.
func queued(id, sessionID string, at time.Time) Turn {
	return Turn{
		ID: id, SessionID: sessionID, Status: StatusQueued, Capability: "deep_solve",
		StateVersion: InitialStateVersion, CreatedAt: at, UpdatedAt: at,
	}
}

// start stores one turn, asserting it was accepted.
func start(t *testing.T, repo *GORMRepository, record Turn) {
	t.Helper()
	if err := repo.Start(context.Background(), record); err != nil {
		t.Fatalf("start turn %s: %v", record.ID, err)
	}
}

// move applies one status change to the stored turn and returns it as it stands
// after it, which is what the next change is prepared from.
func move(t *testing.T, repo *GORMRepository, record Turn, to Status, at time.Time) Turn {
	t.Helper()
	moved, err := record.Transition(to, at)
	if err != nil {
		t.Fatalf("prepare the move to %s: %v", to, err)
	}
	if err := repo.Save(context.Background(), moved, record.Status); err != nil {
		t.Fatalf("save the move to %s: %v", to, err)
	}
	return moved
}

// appendEvent writes one event of one turn, asserting it was accepted.
func appendEvent(t *testing.T, repo *GORMRepository, turnID string, seq int64, at time.Time) {
	t.Helper()
	err := repo.AppendEvent(context.Background(), event.Event{
		TurnID: turnID, Seq: seq, Type: event.TypeContent, Timestamp: at,
		Content: fmt.Sprintf("第 %d 段", seq),
	})
	if err != nil {
		t.Fatalf("append event %d of %s: %v", seq, turnID, err)
	}
}

func countRows(t *testing.T, resource *data.Resource, table string) int64 {
	t.Helper()
	var count int64
	err := resource.GORM().Raw("SELECT COUNT(*) FROM `" + table + "`").Scan(&count).Error
	if err != nil {
		t.Fatalf("count the rows of %s: %v", table, err)
	}
	return count
}

// countRowsWhere counts the rows of one table that match one column, which is
// how many turns a session has.
func countRowsWhere(t *testing.T, resource *data.Resource, table, column, value string) int64 {
	t.Helper()
	var count int64
	err := resource.GORM().Raw("SELECT COUNT(*) FROM `"+table+"` WHERE `"+column+"` = ?", value).
		Scan(&count).Error
	if err != nil {
		t.Fatalf("count the rows of %s where %s = %q: %v", table, column, value, err)
	}
	return count
}

// turnRow is the stored turn, read whole: the columns a state change writes are
// what the guard's outcome is visible in, and a few of them are absent when they
// do not apply.
type turnRow struct {
	Status       string     `gorm:"column:status"`
	StateVersion int64      `gorm:"column:state_version"`
	Error        *string    `gorm:"column:error"`
	FailureCode  *string    `gorm:"column:failure_code"`
	Retryable    *int16     `gorm:"column:retryable"`
	FinishedAt   *time.Time `gorm:"column:finish_time"`
}

func readTurn(t *testing.T, resource *data.Resource, turnID string) turnRow {
	t.Helper()
	var row turnRow
	err := resource.GORM().Raw("SELECT status, state_version, error, failure_code, retryable, "+
		"finish_time FROM turns WHERE turn_id = ?", turnID).Scan(&row).Error
	if err != nil {
		t.Fatalf("read turn %s: %v", turnID, err)
	}
	return row
}

func TestDBASessionRunsOneTurnAtATime(t *testing.T) {
	repo, resource := newDBRepository(t)
	storeSession(t, resource, "s_1", atSecond(1))
	first := queued("turn_1", "s_1", atSecond(2))
	start(t, repo, first)

	// A second turn while the first has not stopped is refused. The check and
	// the insert are one transaction because they have to be: a client that
	// asked twice, or asked again before the first answer arrived, would
	// otherwise get two turns writing into one conversation.
	err := repo.Start(context.Background(), queued("turn_2", "s_1", atSecond(3)))
	if !errors.Is(err, ErrActiveTurnExists) {
		t.Errorf("a second turn in one session: %v, want %v", err, ErrActiveTurnExists)
	}
	if got := countRows(t, resource, "turns"); got != 1 {
		t.Errorf("%d turns are stored, want the one that was accepted", got)
	}

	// The session's active turn is the stored one, and a session that has none
	// is not an error the caller has to interpret.
	active, err := repo.ActiveTurn(context.Background(), "s_1")
	if err != nil {
		t.Fatalf("read the active turn: %v", err)
	}
	if active.ID != first.ID {
		t.Errorf("the active turn is %s, want %s", active.ID, first.ID)
	}
	storeSession(t, resource, "s_2", atSecond(1))
	if _, err := repo.ActiveTurn(context.Background(), "s_2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a session with no turn: %v, want %v", err, ErrNotFound)
	}

	// A turn that stopped frees the slot, and only then: the next one is
	// accepted once the first is completed.
	running := move(t, repo, first, StatusRunning, atSecond(4))
	move(t, repo, running, StatusCompleted, atSecond(5))
	start(t, repo, queued("turn_3", "s_1", atSecond(6)))
	active, err = repo.ActiveTurn(context.Background(), "s_1")
	if err != nil {
		t.Fatalf("read the active turn after the slot was freed: %v", err)
	}
	if active.ID != "turn_3" {
		t.Errorf("the active turn is %s, want the one that took the freed slot", active.ID)
	}
	if got := countRows(t, resource, "turns"); got != 2 {
		t.Errorf("%d turns are stored, want both of them", got)
	}
}

func TestDBTwoTurnsStartedAtOnceLeaveOne(t *testing.T) {
	repo, resource := newDBRepository(t)

	// Several sessions, each asked for four turns at the same instant: sixteen
	// asks in flight, released together. Both the read that looks for an active
	// turn and the insert are what has to hold here — the read alone finds
	// nothing in an empty range and lets every one of them through, and this is
	// the one thing a double cannot have an opinion about.
	const sessions, askers = 4, 4
	for i := range sessions {
		storeSession(t, resource, fmt.Sprintf("s_%d", i+1), atSecond(1))
	}

	type ask struct {
		session string
		record  Turn
	}
	asks := make([]ask, 0, sessions*askers)
	for i := range sessions {
		session := fmt.Sprintf("s_%d", i+1)
		for j := range askers {
			asks = append(asks, ask{
				session: session,
				record:  queued(fmt.Sprintf("turn_%d_%d", i+1, j+1), session, atSecond(j+2)),
			})
		}
	}

	start := make(chan struct{})
	errs := make([]error, len(asks))
	var wg sync.WaitGroup
	for index, ask := range asks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[index] = repo.Start(context.Background(), ask.record)
		}()
	}
	close(start)
	wg.Wait()

	accepted := map[string]int{}
	for index, err := range errs {
		switch {
		case err == nil:
			accepted[asks[index].session]++
		case errors.Is(err, ErrActiveTurnExists):
		default:
			t.Errorf("ask %d failed for a reason that is not the race: %v", index, err)
		}
	}
	for session := 1; session <= sessions; session++ {
		id := fmt.Sprintf("s_%d", session)
		if accepted[id] != 1 {
			t.Errorf("%d of the asks for %s were accepted, want one", accepted[id], id)
		}
		// Read from the table as well: one accepted is what the adapter said,
		// and one stored turn is what the session actually has.
		if got := countRowsWhere(t, resource, "turns", "session_id", id); got != 1 {
			t.Errorf("%s holds %d turns, want one", id, got)
		}
	}
}

func TestDBAnEventSequenceIsTakenOnce(t *testing.T) {
	repo, resource := newDBRepository(t)
	storeSession(t, resource, "s_1", atSecond(1))
	start(t, repo, queued("turn_1", "s_1", atSecond(2)))
	ctx := context.Background()

	appendEvent(t, repo, "turn_1", 1, atSecond(3))
	appendEvent(t, repo, "turn_1", 2, atSecond(3))

	// Writing the same sequence number twice is refused by the unique key on
	// (turn_id, seq), and it is reported as another writer holding the turn
	// rather than as a storage failure — the difference between a bug and a
	// second process is what the caller does about it.
	err := repo.AppendEvent(ctx, event.Event{
		TurnID: "turn_1", Seq: 2, Type: event.TypeDone, Timestamp: atSecond(4),
	})
	if !errors.Is(err, ErrSeqTaken) {
		t.Errorf("writing a taken sequence number: %v, want %v", err, ErrSeqTaken)
	}
	if got := countRows(t, resource, "turn_events"); got != 2 {
		t.Errorf("%d events are stored, want the two that were accepted", got)
	}

	// The stream reads oldest first, from wherever the reader got to, and the
	// last one is what a subscriber that is up to date asks for.
	events, err := repo.Events(ctx, "turn_1", 0)
	if err != nil {
		t.Fatalf("read the events: %v", err)
	}
	if len(events) != 2 || events[0].Seq != 1 || events[1].Seq != 2 {
		t.Fatalf("the events are %+v, want sequences 1 and 2 in that order", events)
	}
	if events[0].Content != "第 1 段" || events[0].Type != event.TypeContent {
		t.Errorf("the first event reads %+v, want the content that was appended", events[0])
	}
	if after, err := repo.Events(ctx, "turn_1", 1); err != nil || len(after) != 1 || after[0].Seq != 2 {
		t.Errorf("the events after 1 are %+v (err %v), want the second one only", after, err)
	}
	if seq, err := repo.LastSeq(ctx, "turn_1"); err != nil || seq != 2 {
		t.Errorf("the last sequence is %d (err %v), want 2", seq, err)
	}
	last, err := repo.LastEvent(ctx, "turn_1")
	if err != nil {
		t.Fatalf("read the last event: %v", err)
	}
	if last.Seq != 2 || last.Content != "第 2 段" {
		t.Errorf("the last event is %+v, want the second one", last)
	}

	// A turn that has produced nothing has no last event, and a sequence of
	// zero rather than a missing answer: the next event of every turn is 1.
	storeSession(t, resource, "s_2", atSecond(1))
	start(t, repo, queued("turn_silent", "s_2", atSecond(2)))
	if seq, err := repo.LastSeq(ctx, "turn_silent"); err != nil || seq != 0 {
		t.Errorf("the last sequence of a turn with no events is %d (err %v), want 0", seq, err)
	}
	if _, err := repo.LastEvent(ctx, "turn_silent"); !errors.Is(err, ErrNotFound) {
		t.Errorf("the last event of a turn with no events: %v, want %v", err, ErrNotFound)
	}

	// The type column is a foreign key into the table the migration seeded, so
	// an event whose type is not one of the protocol's is refused where it is
	// stored rather than read back as something else later.
	if err := testdb.Try(resource, "INSERT INTO turn_events "+
		"(turn_events_id, turn_id, seq, timestamp, type) VALUES ('evt_9', 'turn_1', 9, NOW(), 99)"); err == nil {
		t.Error("an event with an unknown type id was accepted, want the foreign key to refuse it")
	}
}

func TestDBAStateChangeIsGuardedByWhatItWasPreparedFrom(t *testing.T) {
	repo, resource := newDBRepository(t)
	storeSession(t, resource, "s_1", atSecond(1))
	stored := queued("turn_1", "s_1", atSecond(2))
	start(t, repo, stored)
	ctx := context.Background()

	// The change goes through, and it is the version in the guard that says the
	// stored turn was still the one the caller read.
	running, err := stored.Transition(StatusRunning, atSecond(3))
	if err != nil {
		t.Fatalf("prepare the move to running: %v", err)
	}
	if err := repo.Save(ctx, running, stored.Status); err != nil {
		t.Fatalf("save the move to running: %v", err)
	}
	row := readTurn(t, resource, "turn_1")
	if row.Status != string(StatusRunning) || row.StateVersion != 2 {
		t.Fatalf("the stored turn is %s at version %d, want running at 2", row.Status, row.StateVersion)
	}

	// A caller that read version 1 prepares the same move after somebody else's
	// has already landed. Both are guardable against the same column, and only
	// one of them may win: the second is refused and writes nothing.
	stale, err := stored.Transition(StatusRunning, atSecond(4))
	if err != nil {
		t.Fatalf("prepare the stale move: %v", err)
	}
	if err := repo.Save(ctx, stale, stored.Status); !errors.Is(err, ErrStateConflict) {
		t.Errorf("a change prepared from a state that has moved: %v, want %v", err, ErrStateConflict)
	}
	if row := readTurn(t, resource, "turn_1"); row.Status != string(StatusRunning) || row.StateVersion != 2 {
		t.Errorf("the refused change left %s at version %d, want the stored turn untouched", row.Status, row.StateVersion)
	}

	// A turn that is not there at all is a different answer from a turn that
	// moved, and telling them apart is what the second read is for: a caller
	// that lost a race retries, and one looking at a turn that does not exist
	// has nothing to retry.
	missing := queued("turn_missing", "s_1", atSecond(5))
	if err := repo.Save(ctx, missing, StatusQueued); !errors.Is(err, ErrNotFound) {
		t.Errorf("saving a turn that is not there: %v, want %v", err, ErrNotFound)
	}
}

func TestDBAFailureKeepsItsReasonAndOnlyAFailureHasOne(t *testing.T) {
	repo, resource := newDBRepository(t)
	storeSession(t, resource, "s_1", atSecond(1))
	start(t, repo, queued("turn_failed", "s_1", atSecond(2)))
	running := move(t, repo, queued("turn_failed", "s_1", atSecond(2)), StatusRunning, atSecond(3))

	// A failure writes the reason and whether it is worth repeating, which is
	// what a client acts on. None of these columns is set on the way in.
	failed, err := running.Fail(Failure{
		Error: "the provider refused", Code: CodeTimeout, Retryable: true,
	}, atSecond(4))
	if err != nil {
		t.Fatalf("prepare the failure: %v", err)
	}
	if err := repo.Save(context.Background(), failed, StatusRunning); err != nil {
		t.Fatalf("save the failure: %v", err)
	}
	row := readTurn(t, resource, "turn_failed")
	if row.Status != string(StatusFailed) {
		t.Errorf("the turn is %s, want %s", row.Status, StatusFailed)
	}
	if row.Error == nil || *row.Error != "the provider refused" {
		t.Errorf("the stored reason is %v, want it kept for whoever looks next", row.Error)
	}
	if row.FailureCode == nil || *row.FailureCode != CodeTimeout {
		t.Errorf("the stored code is %v, want %s", row.FailureCode, CodeTimeout)
	}
	if row.Retryable == nil || *row.Retryable != 1 {
		t.Errorf("the stored retryable flag is %v, want it set", row.Retryable)
	}
	if row.FinishedAt == nil {
		t.Error("the failed turn has no finish time, want the moment it stopped")
	}

	// A turn that completed carries none of them: a failure code on a finished
	// turn would be a column claiming something the turn never said.
	start(t, repo, queued("turn_done", "s_1", atSecond(5)))
	done := move(t, repo, queued("turn_done", "s_1", atSecond(5)), StatusRunning, atSecond(6))
	move(t, repo, done, StatusCompleted, atSecond(7))
	row = readTurn(t, resource, "turn_done")
	if row.Error != nil || row.FailureCode != nil || row.Retryable != nil {
		t.Errorf("the completed turn carries error %v, code %v, retryable %v, want none",
			row.Error, row.FailureCode, row.Retryable)
	}
	if row.FinishedAt == nil {
		t.Error("the completed turn has no finish time")
	}

	// Nor is a finish time written for a terminal turn that has not been given
	// one: the column is about when the turn stopped, and there are turns that
	// ended without the runtime saying when.
	storeSession(t, resource, "s_2", atSecond(1))
	start(t, repo, queued("turn_stopped", "s_2", atSecond(5)))
	stopped := move(t, repo, queued("turn_stopped", "s_2", atSecond(5)), StatusRunning, atSecond(6))
	record := Turn{
		ID: "turn_stopped", SessionID: "s_2", Status: StatusCancelled,
		StateVersion: stopped.StateVersion + 1, UpdatedAt: atSecond(8),
	}
	if err := repo.Save(context.Background(), record, StatusRunning); err != nil {
		t.Fatalf("save a cancellation with no finish time: %v", err)
	}
	if row := readTurn(t, resource, "turn_stopped"); row.FinishedAt != nil {
		t.Errorf("the cancellation carries a finish time of %v, want none written", row.FinishedAt)
	}
}

func TestDBDeletingATurnTakesItsEvents(t *testing.T) {
	repo, resource := newDBRepository(t)
	storeSession(t, resource, "s_1", atSecond(1))
	storeSession(t, resource, "s_2", atSecond(1))
	start(t, repo, queued("turn_1", "s_1", atSecond(2)))
	start(t, repo, queued("turn_2", "s_2", atSecond(2)))
	appendEvent(t, repo, "turn_1", 1, atSecond(3))
	appendEvent(t, repo, "turn_1", 2, atSecond(3))
	appendEvent(t, repo, "turn_2", 1, atSecond(3))

	// An event describes something that happened inside a turn, so it does not
	// outlive it: the cascade is what keeps a deleted turn's stream from being
	// readable as somebody's history.
	testdb.Exec(t, resource, "DELETE FROM turns WHERE turn_id = ?", "turn_1")
	if got := countRows(t, resource, "turn_events"); got != 1 {
		t.Errorf("%d events are left, want the other turn's one", got)
	}

	// And deleting the session takes the turn and its events with it, which is
	// the same cascade one level up.
	testdb.Exec(t, resource, "DELETE FROM sessions WHERE session_id = ?", "s_2")
	if got := countRows(t, resource, "turns"); got != 0 {
		t.Errorf("%d turns are left, want none", got)
	}
	if got := countRows(t, resource, "turn_events"); got != 0 {
		t.Errorf("%d events are left, want none", got)
	}
	// The table the migration seeded is not touched by any of it: the events
	// point at it, they are not stored in it.
	if got := countRows(t, resource, "turn_event_types"); got != int64(len(eventTypeNames())) {
		t.Errorf("the event type table holds %d rows, want the %d the migration seeded",
			got, len(eventTypeNames()))
	}
}

// eventTypeNames is every type the protocol defines, which is what the seeded
// table has to hold one row for.
func eventTypeNames() []event.Type {
	return []event.Type{
		event.TypeStageStart, event.TypeStageEnd, event.TypeThinking, event.TypeObservation,
		event.TypeContent, event.TypeToolCall, event.TypeToolResult, event.TypeProgress,
		event.TypeSources, event.TypeResult, event.TypeError, event.TypeSession,
		event.TypeSessionMeta, event.TypeDone, event.TypeWaitForInput,
	}
}
