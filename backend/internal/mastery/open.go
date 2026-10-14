package mastery

import (
	"context"
	"errors"
	"strings"

	"backend/internal/authz"
)

// Arriving on a path.
//
// A mastery turn starts with a conversation and no path, and the first thing
// that has to be settled is which tree it is about. This is that decision, and
// it is here rather than in the runtime because it is a rule about paths: a
// conversation continues the path it was last on, and one that has never been on
// a path starts the one this turn is about.

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
// The two outcomes are the whole of it:
//
//   - the conversation has been here before: the path it was last seen on, which
//     is the only reading of "where was I" the link table supports;
//   - it has never been anywhere: a new path, named after what this turn is
//     about. The id is made here rather than by the caller because the caller is
//     a turn that has to announce it in its first event, and an id invented twice
//     would be two paths (NewPathID).
//
// There was a third — a conversation already in the middle of as many paths as
// it may hold is refused — and it is gone. The count it compared against was
// read only after PathOfSession had said this conversation was on no path at
// all, and both reads go to the same links, so the comparison could never fire:
// a conversation with an active path returned above, and one with no links had
// nothing to count. A limit that fires has to say what it counts and what it
// refuses, and nothing here has said so (V4设计方案 §0.3 第 10 条).
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
