package mastery

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Moving to another path.
//
// A turn is on one path at a time, and the lease is what says so: switching is
// therefore a lease handover, and it is why this is not just "read the other
// path". The tree that was left keeps everything that was written to it — the
// design is explicit that switching is not a loss of work — and the one arrived
// at is claimed before anything may be written to it (§2.3, §2.5).

// SwitchRequest is a turn leaving one path for another.
type SwitchRequest struct {
	Actor Actor
	// From is the path the turn is on. A turn switching away from a path it
	// never held is not refused — the release reports whether it removed
	// anything and the answer is used for the history, not as a gate — because
	// the lease may legitimately have been taken over while this turn was
	// running, and refusing then would trap the turn on a path it has lost.
	From string
	// To is the path being moved to. It has to exist, and it has to be free or
	// expired: a target another turn is writing gives back HeldError, naming the
	// holder.
	To string
}

// Switch moves the turn's claim from one path to another and reports the path it
// arrived at.
//
// The order is release first, then acquire, and it is not interchangeable:
// acquiring before releasing would leave a turn holding two leases at once, and
// a turn that stopped in between would keep the path it left locked until the
// lease expired — which is the failure the lease exists to prevent, made by the
// code that was supposed to hand it over.
//
// The history line is written last, in its own transaction, and it is written
// about the path arrived at. That is the honest order: the event says where the
// turn ended up, and a move that could not take the lease did not happen, so
// there is nothing to record. What it costs is a window — a lease taken and the
// process gone before the event — and the cost is bounded by the lease's own
// expiry, which is the same bound every other lease operation has.
func (s *Service) Switch(ctx context.Context, request SwitchRequest) (View, error) {
	if err := request.Actor.Validate(); err != nil {
		return View{}, err
	}
	if strings.TrimSpace(request.To) == "" {
		return View{}, fmt.Errorf("%w: no path was named to switch to", ErrInvalidRequest)
	}
	if request.To == request.From {
		// Switching to where one already is is a read. It is accepted rather than
		// refused because the model has no way to know it is a no-op — the state
		// it read is the state it would get back — and a refusal would cost a
		// round trip to say nothing.
		return s.Load(ctx, request.To)
	}

	released, err := s.Release(ctx, request.From, request.Actor)
	if err != nil {
		return View{}, err
	}
	lease, err := s.Acquire(ctx, request.To, request.Actor)
	if err != nil {
		return View{}, err
	}

	view, err := s.Load(ctx, request.To)
	if err != nil {
		return View{}, err
	}
	now := s.clock()
	err = s.repository.Within(ctx, func(repository Repository) error {
		payload, err := json.Marshal(map[string]any{
			"from":     request.From,
			"to":       request.To,
			"released": released,
			"holder":   lease.TurnID,
		})
		if err != nil {
			return fmt.Errorf("encode the move to %s: %w", request.To, err)
		}
		eventID, err := newID(eventIDPrefix)
		if err != nil {
			return err
		}
		event := Event{
			ID:        eventID,
			PathID:    request.To,
			Type:      EventPathSwitched,
			Version:   view.Path.Version,
			Payload:   payload,
			Actor:     request.Actor,
			CreatedAt: now,
		}
		if err := repository.AppendEvent(ctx, event); err != nil {
			return err
		}
		return repository.TouchSession(ctx, request.To, request.Actor, now)
	})
	if err != nil {
		return View{}, err
	}
	return view, nil
}
