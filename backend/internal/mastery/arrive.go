package mastery

import (
	"context"
	"errors"
	"time"

	"backend/internal/authz"
)

// Arriving on a path: which one this turn is on, and whether it may write it.
//
// Open answers the first half and Acquire the second, and this is the two of
// them in the order a turn meets them. It exists as one call because the two
// answers are announced together — a turn says in its first event which path it
// is on and who is writing it (§4) — and two calls that a caller has to remember
// to make in the right order is how a turn ends up holding a path it never told
// anybody about.

// ArriveRequest is a turn starting up with a conversation and, usually, a
// question.
type ArriveRequest struct {
	Actor Actor
	// Goal is what this turn is about. It is only used when the conversation has
	// no path yet, where it becomes the new path's goal (Open).
	Goal string
}

// Arrival is where a turn ended up, and what it may do there.
type Arrival struct {
	// Path is the path the conversation is on, created if it had none.
	Path Path
	// Lease is who is writing the path now, which is not always this turn.
	Lease Lease
	// Ours reports whether the lease is this turn's. A turn whose path is held
	// by somebody else still runs — reading a path is not writing it, and a
	// learner should not be locked out of their own material because a second
	// tab is mid-sentence — but every write it attempts is refused and says who
	// holds the path (§2.3).
	Ours bool
	// TookOver reports that this arrival took an expired lease from another
	// turn. It is what tells "I am first here" apart from "I am here because the
	// turn before me stopped", and only the second one is worth saying out loud.
	TookOver bool
	// ExpiresAt is when the lease — whoever holds it — stops being a claim that
	// other turns must respect. It is computed rather than stored: the table has
	// no expiry column, and "expired" is a comparison against the clock made by
	// whoever asks (§2.3).
	ExpiresAt time.Time
}

// Arrive settles which path a turn is on and takes the lease on it.
//
// A path another turn is writing is not an error here, and that is the one
// judgement in this file worth stating: Arrive is called before a turn says
// anything, so failing it would turn "somebody else is writing this path" into a
// turn that never starts. What the turn gets instead is an arrival that says who
// the holder is, and tools that refuse writes with that name in the refusal.
func (s *Service) Arrive(ctx context.Context, request ArriveRequest, caller authz.Caller) (Arrival, error) {
	path, err := s.Open(ctx, OpenRequest{Actor: request.Actor, Goal: request.Goal}, caller)
	if err != nil {
		return Arrival{}, err
	}

	// Read before acquiring, because "did this turn take the path over" is a
	// question only the state *before* the acquisition can answer, and Acquire
	// reports the lease it holds rather than the one it replaced.
	now := s.clock()
	before, err := s.Holder(ctx, path.ID, caller)
	switch {
	case errors.Is(err, ErrNotFound):
		before = Lease{}
	case err != nil:
		return Arrival{}, err
	}
	tookOver := before.TurnID != "" && before.TurnID != request.Actor.TurnID && before.Expired(now, s.leaseTTL)

	lease, err := s.Acquire(ctx, path.ID, request.Actor, caller)
	if err != nil {
		var held *HeldError
		if errors.As(err, &held) {
			return Arrival{
				Path:      path,
				Lease:     held.Lease,
				ExpiresAt: held.Lease.AcquiredAt.Add(s.leaseTTL),
			}, nil
		}
		return Arrival{}, err
	}
	return Arrival{
		Path:  path,
		Lease: lease,
		Ours:  true,
		// A takeover that lost the race is not a takeover: the lease that came
		// back belongs to whoever won, and this line would then be claiming
		// credit for somebody else's write.
		TookOver:  tookOver && lease.HeldBy(request.Actor),
		ExpiresAt: lease.AcquiredAt.Add(s.leaseTTL),
	}, nil
}

// Metadata is the arrival as a turn announces it: the payload of
// `metadata.lease` on the opening session event (§4).
//
// It is built here rather than by the caller because the four fields are one
// reading of one thing — who holds this path, until when, and whether this turn
// is the one who took it — and a caller assembling them from the fields above
// would be a second place where "holder" could mean the holder before the
// acquisition rather than after it.
func (a Arrival) Metadata() map[string]any {
	return map[string]any{
		"path_id":    a.Path.ID,
		"holder":     a.Lease.TurnID,
		"expires_at": a.ExpiresAt,
		"took_over":  a.TookOver,
	}
}
