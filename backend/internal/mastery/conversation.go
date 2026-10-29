package mastery

import (
	"context"
	"strings"
	"time"

	"backend/internal/authz"
)

// The conversations that own paths, as one history page reads them.
//
// A path is created by the conversation that first worked on it (Open), and
// that conversation is the one the path's history belongs to. Other
// conversations may have touched the path — mastery_switch links them — but
// they own paths of their own, and listing them here would put one path under
// several conversations. The link table is still the place a turn asks "where
// was I"; this read is the other question, "which conversation made this path",
// and the answer is mastery_paths.owner_session_id.
//
// The listing therefore has one row per path, never more, and a path whose
// owner conversation is gone from the live list is not here either. That is the
// same rule the rest of this package follows: a path is readable only through
// the account that owns it.

// Conversation is one row of the path-chat history: a mastery conversation,
// the path it created, and when either was last active.
type Conversation struct {
	SessionID  string
	Title      string
	PathID     string
	Goal       string
	Emoji      string
	LastSeenAt time.Time
	UpdatedAt  time.Time
}

// Conversations lists the mastery conversations that own a path, most recently
// active first.
//
// It takes no session filter: this is the first-level page, and the second
// level already has the session id it opened. A caller narrowing to one session
// has PathOfSession for that.
func (s *Service) Conversations(ctx context.Context, caller authz.Caller) ([]Conversation, error) {
	listed, err := s.repository.Conversations(ctx, caller)
	if err != nil {
		return nil, err
	}
	for index := range listed {
		// The same substitution List makes: an emoji nobody chose is the default
		// one, not a blank in the row.
		if strings.TrimSpace(listed[index].Emoji) == "" {
			listed[index].Emoji = defaultEmoji
		}
	}
	return listed, nil
}
