package practice

import (
	"context"
	"strings"
	"time"
)

// DueNext is the question bank's view of the schedule (§1.2, notebook.DueReader).
//
// The bank declares what it needs and this is the half that answers it, joined
// in the assembly rather than imported: practice already imports notebook, so
// the bank importing practice back would be a cycle.
//
// It reads the stored due time and computes nothing. The curve runs when a
// review is recorded — that is where the "how long until next time" arithmetic
// lives, once — and a read that recomputed it would be a second implementation
// of the same rule, free to disagree with the first.
func (s *Service) DueNext(ctx context.Context, entryIDs []string) (map[string]time.Time, error) {
	due := make(map[string]time.Time)
	wanted := make([]string, 0, len(entryIDs))
	for _, id := range entryIDs {
		if cleaned := strings.TrimSpace(id); cleaned != "" {
			wanted = append(wanted, cleaned)
		}
	}
	if len(wanted) == 0 {
		// Nothing was asked about, so there is nothing to read. An empty map
		// rather than a nil one: a caller ranging over the answer is the same
		// either way, and a nil map from a read that succeeded looks like a
		// failure.
		return due, nil
	}
	return s.repository.DueTimes(ctx, wanted)
}
