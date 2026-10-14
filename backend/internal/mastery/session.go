package mastery

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Which path a conversation is working on.
//
// It is a question about the link table rather than a field anywhere, because a
// path does not belong to a conversation — one conversation may work on several
// paths over its life, and one path is worked on by many conversations (§2.1).
// What the link says is "this conversation has been here", and the newest
// last_seen_time is the only reading of "where it is now" that the schema
// supports.
//
// This is the seam a turn is opened through. A turn arrives with a conversation
// and no path, and the first thing that has to be settled is which tree it is
// about: the answer is here, and when there is no answer, the turn is what makes
// one — which is the runtime's decision and not this package's.

// SessionPath reports the path a conversation was last working on.
//
// ErrNotFound when the conversation has never touched one, which is not a
// failure: it is a conversation that has not started a path yet, and the caller
// decides whether that means creating one or doing nothing.
func (s *Service) SessionPath(ctx context.Context, sessionID string) (string, error) {
	if strings.TrimSpace(sessionID) == "" {
		return "", fmt.Errorf("%w: the session is blank", ErrInvalidRequest)
	}
	return s.repository.PathOfSession(ctx, sessionID)
}

// SessionsOn reports the conversations that have touched a path, most recently
// seen first.
//
// It is the other direction of the same table. A turn never asks it — a turn
// knows its own conversation — and the endpoints that show a path's history do,
// which is why it returns ids rather than a count: the caller has to look each
// one up to say anything about it.
func (s *Service) SessionsOn(ctx context.Context, pathID string) ([]string, error) {
	if strings.TrimSpace(pathID) == "" {
		return nil, fmt.Errorf("%w: the path is blank", ErrInvalidRequest)
	}
	return s.repository.SessionsOn(ctx, pathID)
}

// SeenAt reports when a conversation last touched a path, and ErrNotFound when
// the two were never linked.
//
// It is a read of its own rather than something SessionPath hands back, because
// it is asked about a pair of ids rather than about a conversation: a caller
// showing a path's history asks it about every conversation on the path, and a
// caller deciding whether a conversation is still working on a path asks it
// about the newest one. The second is the only caller that could have got the
// answer for free, and it does not need a time.
func (s *Service) SeenAt(ctx context.Context, pathID, sessionID string) (time.Time, error) {
	return s.repository.SeenAt(ctx, pathID, sessionID)
}
