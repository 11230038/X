package mastery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"backend/internal/authz"
)

// The lease, and the fence that goes with it.
//
// MP-04 asks for "lease/乐观锁" against two turns overwriting each other's view
// of a path, and the two words name two mechanisms that stop two different
// things (§2.3):
//
//   - the lease — one row per path in mastery_path_leases, so at most one turn
//     is in the middle of "I am going to change this tree";
//   - the version guard — mastery_paths.version, so that a turn which read the
//     tree and then thought about it for a while cannot write over a change
//     that landed in between (repository.go, SaveTree).
//
// The lease alone leaks the read-modify-write window; the guard alone lets a
// second turn do a whole turn of work before finding out it lost. Together, the
// turn holding the lease is not competing for the guard at all, and the guard
// is still what decides the one boundary the lease cannot: a lease that expired
// and was taken over while its holder was still running.
//
// The fence is how the late writer finds out. It is `turns.fencing_token` — a
// column migration 00002 put there and nothing has written until now.

// ErrLeaseHeld is a path another turn is writing. It is not a failure of the
// request: the answer the caller needs is *which* turn holds it, which is why
// the error carries one (HeldError) and why a tool can tell the model to carry
// on with what is already there instead of starting over.
var ErrLeaseHeld = errors.New("mastery: another turn holds this path")

// HeldError is the lease of somebody else, reported with the holder.
type HeldError struct {
	Lease Lease
}

// Error names the turn and the session, and nothing else: those two ids are
// already in the caller's world, while the path's contents are not this
// message's to expose.
func (e *HeldError) Error() string {
	return fmt.Sprintf("mastery: path %s is held by turn %s in session %s",
		e.Lease.PathID, e.Lease.TurnID, e.Lease.SessionID)
}

// Is makes errors.Is(err, ErrLeaseHeld) true for every HeldError, so a caller
// that only wants to know whether it was refused does not have to type-assert,
// and a caller that wants the holder does.
func (e *HeldError) Is(target error) bool {
	return target == ErrLeaseHeld
}

// Lease is one turn's claim on one path.
//
// There is no expiry column, and that is the schema's shape rather than an
// omission to be worked around: the table has creat_time and nothing else, so
// "expired" is a comparison against the clock, made by whoever asks (Expired).
// V4 does not add a column and does not add a background process to reap stale
// leases — the same judgement as V3's "the queue is a query, not a job", and it
// has the same consequence: a lease that nobody takes over costs nothing, and a
// turn that finds an expired one takes it as part of the request that found it.
type Lease struct {
	PathID     string
	SessionID  string
	TurnID     string
	AcquiredAt time.Time
}

// HeldBy reports whether this lease belongs to the actor.
func (l Lease) HeldBy(actor Actor) bool {
	return l.TurnID != "" && l.TurnID == actor.TurnID
}

// Expired reports whether the lease may be taken over at now.
//
// The comparison is open at the end — a lease exactly ttl old is not expired
// yet — which matters only at the boundary and is worth choosing deliberately
// rather than by whichever comparison operator got typed.
func (l Lease) Expired(now time.Time, ttl time.Duration) bool {
	if l.TurnID == "" {
		return true
	}
	return now.Sub(l.AcquiredAt) > ttl
}

// Holder reads who is writing a path. It reports the lease whatever state it is
// in — expired ones included — because "nobody is writing this" and "somebody
// was writing this two hours ago" are different answers, and only the caller
// knows whether the second one matters.
func (s *Service) Holder(ctx context.Context, pathID string, caller authz.Caller) (Lease, error) {
	return s.repository.Lease(ctx, pathID, caller)
}

// Acquire takes the lease on a path for one turn, and reports the lease it ends
// up holding.
//
// The three outcomes are the whole of MP-04's first half:
//
//   - nobody holds it: this turn does, and its fence is raised;
//   - this turn already holds it: nothing happens, because a turn that asks
//     twice is a turn that called its own setup twice;
//   - somebody else holds it and the lease has not expired: HeldError, naming
//     them. This is a *structured* refusal rather than a failure, because a turn
//     that is told who is writing can offer to wait or to work elsewhere, while
//     a turn that is told "conflict" can only give up.
//
// The fourth case — somebody else holds it and the lease *has* expired — is the
// takeover, and it is a compare-and-swap rather than a read: the update names
// the holder that was read, so of two turns that both decided the lease was
// expired, exactly one wins. The loser does not fail; it re-reads and reports
// the winner as the holder, which is the truth about the path now.
func (s *Service) Acquire(ctx context.Context, pathID string, actor Actor, caller authz.Caller) (Lease, error) {
	if err := actor.Validate(); err != nil {
		return Lease{}, err
	}
	// Scoped before anything is written: a lease taken on another account's path
	// is the same failure as reading one, and refusing it here is what stops a
	// turn from claiming the right to rewrite somebody else's tree.
	if err := s.requirePath(ctx, pathID, caller); err != nil {
		return Lease{}, err
	}
	now := s.clock()

	var held Lease
	err := s.repository.Within(ctx, func(repository Repository) error {
		current, err := repository.Lease(ctx, pathID, caller)
		switch {
		case errors.Is(err, ErrNotFound):
			lease := Lease{PathID: pathID, SessionID: actor.SessionID, TurnID: actor.TurnID, AcquiredAt: now}
			if err := repository.ClaimLease(ctx, lease, caller); err != nil {
				return err
			}
			if err := s.fence(ctx, repository, "", actor.TurnID); err != nil {
				return err
			}
			held = lease
			return nil
		case err != nil:
			return err
		}

		if current.HeldBy(actor) {
			held = current
			return nil
		}
		if !current.Expired(now, s.leaseTTL) {
			return &HeldError{Lease: current}
		}

		taken := Lease{PathID: pathID, SessionID: actor.SessionID, TurnID: actor.TurnID, AcquiredAt: now}
		moved, err := repository.TakeoverLease(ctx, pathID, current.TurnID, taken, caller)
		if err != nil {
			return err
		}
		if !moved {
			// Somebody else read the same expired lease and got there first. Re-reading
			// rather than reporting the lease that was read is what makes the refusal
			// name the holder that is actually there.
			winner, err := repository.Lease(ctx, pathID, caller)
			if errors.Is(err, ErrNotFound) {
				// The winner released it again between the two reads, which leaves the
				// path free. Reporting a holder that is gone would be wrong; the next
				// call takes it.
				return &HeldError{Lease: current}
			}
			if err != nil {
				return err
			}
			return &HeldError{Lease: winner}
		}
		if err := s.fence(ctx, repository, current.TurnID, actor.TurnID); err != nil {
			return err
		}
		held = taken
		return nil
	})
	if err != nil {
		return Lease{}, err
	}
	return held, nil
}

// Release gives up the lease, and reports whether this call is the one that
// removed it.
//
// Only the holder may release: the delete names the turn, so a turn whose lease
// was taken over while it was still running cannot release the new holder's. It
// does not fail when that happens — it reports false, because "you no longer
// hold this" is the answer, not an error.
func (s *Service) Release(ctx context.Context, pathID string, actor Actor, caller authz.Caller) (bool, error) {
	if err := actor.Validate(); err != nil {
		return false, err
	}
	var released bool
	err := s.repository.Within(ctx, func(repository Repository) error {
		moved, err := repository.ReleaseLease(ctx, pathID, actor.TurnID, caller)
		if err != nil {
			return err
		}
		released = moved
		return nil
	})
	if err != nil {
		return false, err
	}
	return released, nil
}

// fence raises the new holder's token above the outgoing holder's.
//
// The token is a chain along a path: each takeover sets the new holder to one
// more than the holder it displaced, starting from 1 when there was none. A
// token therefore only compares meaningfully against the tokens of turns that
// held the same path, which is exactly what it is for — a late writer's token is
// never greater than the current holder's, so a write under an old token can be
// recognised as old.
//
// The chain is the schema's limit as much as this function's: there is nowhere
// to keep a per-path counter except in the holder's own turn row, and the
// alternative — a counter column on mastery_path_leases — would be a migration
// V4 does not need to justify.
func (s *Service) fence(ctx context.Context, repository Repository, fromTurnID, toTurnID string) error {
	base := int64(0)
	if fromTurnID != "" {
		previous, err := repository.FencingToken(ctx, fromTurnID)
		if err != nil {
			return err
		}
		base = previous
	}
	return repository.SetFencingToken(ctx, toTurnID, base+1)
}

// requirePath refuses a lease on a path that is not there.
//
// Without it, a lease would be the one way to create a row in a table whose
// foreign key says the path exists — and a lease nobody can ever take over,
// because the path it names cannot be read.
//
// "Not there" includes "not the caller's": the read is scoped, so a path another
// account owns is ErrNotFound here exactly as a path that was never created is,
// and the lease that follows never has another account's path to name.
func (s *Service) requirePath(ctx context.Context, pathID string, caller authz.Caller) error {
	if _, err := s.repository.Path(ctx, pathID, caller); err != nil {
		return err
	}
	return nil
}
