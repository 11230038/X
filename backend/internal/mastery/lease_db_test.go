package mastery

// The lease and the fence, against a real seekdb.
//
// §7.2 calls this the version's one behaviour that reasoning cannot settle, and
// the reason is that every claim here is about two writers reaching one row: that
// the claim is a primary key and not a read, that the takeover is a
// compare-and-swap and not an update, that the loser of either is told which
// holder is there rather than that something went wrong. A double can restate
// all three and agree with itself; only the engine can say whether the second
// insert collides and whether the second takeover matches no row.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDBTheFirstTurnToAskForAPathGetsIt(t *testing.T) {
	h := newHarness(t)
	first := h.conversation(t, "s_1", "t_1")
	h.create(t, "mp_1", "学会闭包", first)
	ctx := context.Background()

	lease, err := h.service.Acquire(ctx, "mp_1", first)
	if err != nil {
		t.Fatalf("take the lease: %v", err)
	}
	if lease.TurnID != "t_1" || lease.SessionID != "s_1" || !lease.AcquiredAt.Equal(atNine) {
		t.Errorf("the lease is %+v", lease)
	}
	// The fence goes up with the claim, and it starts at one: it is a chain along a
	// path, and a turn that displaced nobody has nothing to be above.
	token, err := h.repository.FencingToken(ctx, "t_1")
	if err != nil {
		t.Fatalf("read the fence: %v", err)
	}
	if token != 1 {
		t.Errorf("the first holder's fence is %d, want 1", token)
	}
	// A turn that has never held anything has a NULL fence, and NULL is the same
	// answer as zero rather than an error: the caller that wants a starting point
	// should not have to tell the two apart.
	h.turn(t, "t_idle", "s_1")
	if token, err := h.repository.FencingToken(ctx, "t_idle"); err != nil || token != 0 {
		t.Errorf("an idle turn's fence is %d (%v), want 0", token, err)
	}

	// Asking twice is a turn calling its own setup twice, and it is not a
	// refusal: the answer to "is this mine" is the same either way.
	again, err := h.service.Acquire(ctx, "mp_1", first)
	if err != nil {
		t.Fatalf("take the lease again: %v", err)
	}
	if again != lease {
		t.Errorf("the second call reports %+v, want the first call's %+v", again, lease)
	}
	if got := h.countRows(t, "mastery_path_leases"); got != 1 {
		t.Errorf("%d leases are stored, want one", got)
	}
	if token, err := h.repository.FencingToken(ctx, "t_1"); err != nil || token != 1 {
		t.Errorf("re-taking the lease moved the fence to %d, want it left at 1", token)
	}
}

func TestDBATurnIsToldWhoIsWritingAndNotJustThatSomebodyIs(t *testing.T) {
	h := newHarness(t)
	first := h.conversation(t, "s_1", "t_1")
	h.create(t, "mp_1", "学会闭包", first)
	second := h.turn(t, "t_2", "s_1")
	ctx := context.Background()

	if _, err := h.service.Acquire(ctx, "mp_1", first); err != nil {
		t.Fatalf("take the lease: %v", err)
	}
	_, err := h.service.Acquire(ctx, "mp_1", second)
	if !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("a second turn asking for a path in use: %v, want %v", err, ErrLeaseHeld)
	}
	// The refusal is structured, because what a turn does next depends on who is
	// writing: it can offer to wait or work elsewhere, while a turn told only
	// "conflict" can give up.
	var held *HeldError
	if !errors.As(err, &held) {
		t.Fatalf("the refusal is %T, want the holder carried with it", err)
	}
	if held.Lease.TurnID != "t_1" || held.Lease.SessionID != "s_1" {
		t.Errorf("the refusal names the holder as %+v, want t_1 in s_1", held.Lease)
	}
	// The message names the two ids and nothing else: those are already in the
	// caller's world, while the path's contents are not this message's to expose.
	if !strings.Contains(err.Error(), "t_1") || strings.Contains(err.Error(), "kp_capture") {
		t.Errorf("the refusal reads %q", err)
	}
	// Nothing was written on the loser's behalf.
	if got := h.countRows(t, "mastery_path_leases"); got != 1 {
		t.Errorf("%d leases are stored, want the holder's one", got)
	}
}

func TestDBAnExpiredLeaseIsTakenOverAndOneTurnWinsIt(t *testing.T) {
	h := newHarness(t)
	first := h.conversation(t, "s_1", "t_1")
	h.create(t, "mp_1", "学会闭包", first)
	second := h.turn(t, "t_2", "s_1")
	third := h.turn(t, "t_3", "s_1")
	ctx := context.Background()

	if _, err := h.service.Acquire(ctx, "mp_1", first); err != nil {
		t.Fatalf("take the lease: %v", err)
	}
	// Expiry is a comparison against a clock and not a background process: there is
	// no reaper, and this is the moment an expired lease is noticed — by the turn
	// that wanted the path. Two hours and not one, because the comparison is open at
	// the end and a lease exactly its ttl old is not expired yet (the boundary has
	// a case of its own below).
	h.clock.advance(2 * time.Hour)
	if _, err := h.service.Acquire(ctx, "mp_1", second); err != nil {
		t.Fatalf("take over an expired lease: %v", err)
	}

	held, err := h.repository.Lease(ctx, "mp_1")
	if err != nil {
		t.Fatalf("read the lease: %v", err)
	}
	if held.TurnID != "t_2" {
		t.Fatalf("the path is held by %s, want the takeover", held.TurnID)
	}
	if !held.AcquiredAt.Equal(atEleven) {
		t.Errorf("the takeover was acquired at %s, want %s", held.AcquiredAt, atEleven)
	}
	// The fence is what the late writer is recognised by, so the takeover has to
	// raise it above the holder it displaced.
	if token, err := h.repository.FencingToken(ctx, "t_2"); err != nil || token != 2 {
		t.Errorf("the new holder's fence is %d (%v), want 2 above the displaced 1", token, err)
	}
	if token, err := h.repository.FencingToken(ctx, "t_1"); err != nil || token != 1 {
		t.Errorf("the displaced turn's fence is %d, want its own 1 left alone", token)
	}

	// And the displaced turn cannot take it back while the new one is live: a lease
	// that could be re-taken at will would be no lease at all.
	if _, err := h.service.Acquire(ctx, "mp_1", first); !errors.Is(err, ErrLeaseHeld) {
		t.Errorf("the displaced turn asking again: %v, want %v", err, ErrLeaseHeld)
	}
	// A third turn arriving on a live lease is refused as well, and it is refused
	// naming the holder that is there now rather than the one it might have read.
	_, err = h.service.Acquire(ctx, "mp_1", third)
	var heldErr *HeldError
	if !errors.As(err, &heldErr) || heldErr.Lease.TurnID != "t_2" {
		t.Errorf("a third turn was refused with %v, want the current holder named", err)
	}

	// The compare-and-swap itself, one level down, where the case can say what the
	// condition is: the update names the holder that was read, so a second call on
	// the same displaced holder matches no row and reports that rather than
	// overwriting the holder that is actually there.
	moved, err := h.repository.TakeoverLease(ctx, "mp_1", "t_1",
		Lease{PathID: "mp_1", SessionID: "s_3", TurnID: "t_3", AcquiredAt: atEleven})
	if err != nil {
		t.Fatalf("take over from a holder that is gone: %v", err)
	}
	if moved {
		t.Error("a takeover from a holder that was already displaced reported success")
	}
	still, err := h.repository.Lease(ctx, "mp_1")
	if err != nil {
		t.Fatalf("read the lease again: %v", err)
	}
	if still.TurnID != "t_2" {
		t.Errorf("the path is now held by %s, want the loser to have changed nothing", still.TurnID)
	}
}

func TestDBALeaseIsReleasedByItsHolderAndNotByAnyoneElse(t *testing.T) {
	h := newHarness(t)
	first := h.conversation(t, "s_1", "t_1")
	h.create(t, "mp_1", "学会闭包", first)
	second := h.turn(t, "t_2", "s_1")
	ctx := context.Background()

	if _, err := h.service.Acquire(ctx, "mp_1", first); err != nil {
		t.Fatalf("take the lease: %v", err)
	}
	// A turn that does not hold the path releases nothing, and it is not an error:
	// "you no longer hold this" is an answer, and reporting it as a failure would
	// make every turn that lost a takeover log one.
	released, err := h.service.Release(ctx, "mp_1", second)
	if err != nil {
		t.Fatalf("release somebody else's lease: %v", err)
	}
	if released {
		t.Error("a turn that holds nothing reported releasing it")
	}
	if got := h.countRows(t, "mastery_path_leases"); got != 1 {
		t.Errorf("%d leases are stored, want the holder's one left alone", got)
	}

	released, err = h.service.Release(ctx, "mp_1", first)
	if err != nil {
		t.Fatalf("release the lease: %v", err)
	}
	if !released {
		t.Error("the holder's own release reported removing nothing")
	}
	// Released and not rewritten: nothing holds the path, which is a state the
	// table says with no row rather than with a row naming nobody.
	if got := h.countRows(t, "mastery_path_leases"); got != 0 {
		t.Errorf("%d leases are stored, want none", got)
	}
	if _, err := h.repository.Lease(ctx, "mp_1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("reading the lease of a free path: %v, want %v", err, ErrNotFound)
	}

	// And having released it, the turn can take it again.
	if _, err := h.service.Acquire(ctx, "mp_1", first); err != nil {
		t.Fatalf("take the lease again after releasing: %v", err)
	}
}

func TestDBALeaseOnAPathThatIsNotThereIsRefused(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	ctx := context.Background()

	// Without this the lease would be the one way to write a row into a table whose
	// foreign key says the path exists — and a lease nobody can ever take over,
	// because the path it names cannot be read.
	if _, err := h.service.Acquire(ctx, "mp_missing", actor); !errors.Is(err, ErrNotFound) {
		t.Errorf("taking the lease on a path that is not there: %v, want %v", err, ErrNotFound)
	}
	if got := h.countRows(t, "mastery_path_leases"); got != 0 {
		t.Errorf("%d leases are stored, want none", got)
	}

	// A lease is carried by a turn, and the schema is what says so: the claim names
	// the turn as a foreign key, so a turn with no row is refused by the engine
	// before any of this package's own checks run.
	//
	// That refusal is not one of the package's sentinels, and it is recorded here as
	// what it is rather than as what would read better. The two are different
	// things: a path that is not there is a question a caller can legitimately ask
	// (requirePath answers it with ErrNotFound), while a turn that is not there
	// means the runtime and this domain disagree about which turn is running, which
	// is a programming error with nothing useful to say back.
	h.create(t, "mp_1", "学会闭包", actor)
	stranger := Actor{SessionID: "s_1", TurnID: "t_stranger"}
	if _, err := h.service.Acquire(ctx, "mp_1", stranger); err == nil {
		t.Error("a turn with no row took a lease")
	}
	// And the claim went with it: the transaction that would have raised the fence
	// rolled the claim back, so the path is free for a turn that exists.
	if _, err := h.repository.Lease(ctx, "mp_1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("the path is held by somebody, want it free after the failed claim: %v", err)
	}
	if _, err := h.service.Acquire(ctx, "mp_1", actor); err != nil {
		t.Errorf("the turn that exists could not take the path: %v", err)
	}
}

func TestDBAFenceThatDoesNotHaveToMoveIsNotAMissingTurn(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	ctx := context.Background()
	first := h.create(t, "mp_1", "学会闭包", actor)

	// The fence is stored on the turn's row, which is one row carrying whatever
	// path that turn last claimed — so a turn's second path starts its chain at one
	// again, over a row that already says one. The engine counts changed rows, so
	// this write changes nothing, and the difference between "nothing changed" and
	// "there is no such turn" is the whole of what this case is about: a claim
	// refused as a missing turn would be a lease that could be taken only once per
	// turn, for a reason no caller could see.
	second := h.create(t, "mp_2", "再学一次", actor)
	if _, err := h.service.Acquire(ctx, first.ID, actor); err != nil {
		t.Fatalf("take the first path: %v", err)
	}
	if _, err := h.service.Acquire(ctx, second.ID, actor); err != nil {
		t.Fatalf("a turn taking a second path: %v", err)
	}
	if got := h.countRows(t, "mastery_path_leases"); got != 2 {
		t.Errorf("%d leases are stored, want one per path", got)
	}

	// The same no-op reached the other way: release and take the same path again.
	if released, err := h.service.Release(ctx, first.ID, actor); err != nil || !released {
		t.Fatalf("release the first path: %v (%v)", err, released)
	}
	if _, err := h.service.Acquire(ctx, first.ID, actor); err != nil {
		t.Fatalf("take the same path again after releasing it: %v", err)
	}
	// The fence is still the turn's own, and the write that set it to the value it
	// already had left it there rather than raising it.
	if token, err := h.repository.FencingToken(ctx, "t_1"); err != nil || token != 1 {
		t.Errorf("the fence is %d (%v), want the 1 the chain started at", token, err)
	}
	// A turn that is genuinely absent is still refused, which is the other half of
	// the branch and the reason it exists at all.
	if err := h.repository.SetFencingToken(ctx, "t_nobody", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("raising the fence of a turn that is not there: %v, want %v", err, ErrNotFound)
	}
}

func TestDBALeaseOutlivesNobodyAndDiesWithItsPath(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	h.create(t, "mp_1", "学会闭包", actor)
	ctx := context.Background()

	if _, err := h.service.Acquire(ctx, "mp_1", actor); err != nil {
		t.Fatalf("take the lease: %v", err)
	}
	// The lease names the path as a foreign key with ON DELETE CASCADE: a path that
	// is gone is a path nobody can write, so the row saying who was writing it has
	// nothing left to say.
	if err := h.resource.GORM().Exec("DELETE FROM mastery_paths WHERE mastery_path_id = 'mp_1'").Error; err != nil {
		t.Fatalf("delete the path: %v", err)
	}
	if got := h.countRows(t, "mastery_path_leases"); got != 0 {
		t.Errorf("%d leases are left after the path went", got)
	}
	// Deleting the conversation takes the path and the lease with it, which is the
	// same cascade one step out.
	path := h.create(t, "mp_2", "再学一次", actor)
	if _, err := h.service.Acquire(ctx, path.ID, actor); err != nil {
		t.Fatalf("take the lease on the second path: %v", err)
	}
	if err := h.resource.GORM().Exec("DELETE FROM sessions WHERE session_id = 's_1'").Error; err != nil {
		t.Fatalf("delete the conversation: %v", err)
	}
	if got := h.countRows(t, "mastery_path_leases"); got != 0 {
		t.Errorf("%d leases are left after the conversation went", got)
	}
}

func TestDBALeaseExactlyAtItsLimitIsNotExpiredYet(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	// The comparison is open at the end, and the boundary is worth choosing rather
	// than leaving to whichever operator got typed: a lease that expired the
	// instant it was taken would be a lease that does nothing.
	lease := Lease{PathID: "mp_1", SessionID: "s_1", TurnID: "t_1", AcquiredAt: atNine}
	if lease.Expired(atTen, time.Hour) {
		t.Error("a lease exactly an hour old reads as expired")
	}
	if !lease.Expired(atTen.Add(time.Second), time.Hour) {
		t.Error("a lease an hour and a second old reads as live")
	}
	// A lease naming no turn is a path nobody holds, and it is expired in the only
	// sense that matters here: it may be taken.
	if !(Lease{PathID: "mp_1"}).Expired(atNine, time.Hour) {
		t.Error("a lease naming no turn reads as held")
	}
	if (Lease{TurnID: "t_1"}).HeldBy(actor) != true || (Lease{}).HeldBy(actor) {
		t.Error("HeldBy counts a lease with no turn as somebody's")
	}
}
