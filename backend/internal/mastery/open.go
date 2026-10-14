package mastery

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"backend/internal/authz"
)

// Arriving on a path.
//
// A mastery turn starts with a conversation and no path, and the first thing
// that has to be settled is which tree it is about. This is that decision, and
// it is here rather than in the runtime because it is a rule about paths: a
// conversation continues the path it was last on, and one that has never been on
// a path starts the one this turn is about — as long as it is not already in the
// middle of as many as a person may work on at once.

// OpenRequest is a turn arriving with a conversation and, usually, a goal.
type OpenRequest struct {
	Actor Actor
	// Goal is what this turn is about, and it is what names a path that has to be
	// created. It is not validated and not trimmed into a sentence: it is the
	// user's own words on the way to being a path's goal, and a turn that arrives
	// with nothing sayable still has to land somewhere.
	Goal string
}

// Open returns the path this conversation should be working on, creating one if
// it has none.
//
// The three outcomes are the whole of it:
//
//   - the conversation has been here before: the path it was last seen on, which
//     is the only reading of "where was I" the link table supports;
//   - it has never been anywhere: a new path, named after what this turn is
//     about. The id is made here rather than by the caller because the caller is
//     a turn that has to announce it in its first event, and an id invented twice
//     would be two paths (NewPathID);
//   - it has never been anywhere and is already in the middle of
//     MaxActivePaths paths: ErrTooManyPaths. This build cannot reach that case;
//     see below.
//
// The limit counts *active* paths, and "active" is the topic metadata's status —
// which is why the count is a read of the link table joined to the metadata
// rather than a count of rows. It counts one conversation's paths and not one
// person's, because a path's owner here is a conversation.
//
// What it does today is refuse nothing. The count is read only after
// PathOfSession has said this conversation is on no path, and both reads go to
// the same links: a conversation that has an active path returns above and never
// arrives at the count, and one with no links has nothing to count. The bound is
// configuration because how many paths a person can hold in mind is a product
// decision rather than this package's (MASTERY_MAX_ACTIVE_PATHS), and a decision
// that makes it fire has to say what it counts and what it refuses. What the code
// does in the meantime is pinned by
// TestDBArrivingIsNeverRefusedForHavingTooManyPaths.
func (s *Service) Open(ctx context.Context, request OpenRequest, caller authz.Caller) (Path, error) {
	if err := request.Actor.Validate(); err != nil {
		return Path{}, err
	}
	// Both reads are scoped to the caller, which is what keeps a conversation from
	// arriving on a path that is not this account's: the link table is searched
	// only over owned paths, so a link to somebody else's path is not found and
	// this turn starts — or continues — one of its own instead.
	pathID, err := s.repository.PathOfSession(ctx, request.Actor.SessionID, caller)
	switch {
	case err == nil:
		return s.repository.Path(ctx, pathID, caller)
	case !errors.Is(err, ErrNotFound):
		return Path{}, err
	}

	active, err := s.repository.ActivePathsOfSession(ctx, request.Actor.SessionID, caller)
	if err != nil {
		return Path{}, err
	}
	if len(active) >= s.maxActivePaths {
		return Path{}, fmt.Errorf(
			"%w: session %s is already working on %d paths and the limit is %d",
			ErrTooManyPaths, request.Actor.SessionID, len(active), s.maxActivePaths)
	}

	id, err := NewPathID()
	if err != nil {
		return Path{}, err
	}
	return s.Create(ctx, Create{
		PathID: id,
		Goal:   strings.TrimSpace(request.Goal),
		Actor:  request.Actor,
	}, caller)
}
