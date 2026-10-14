package usage

import (
	"strings"
	"testing"
)

// The ids are the primary key's whole story: distinct, so that §17.3's
// deduplication has nothing to do, and marked, so that a row can be recognised.
func TestTheCallIDsAreDistinctAndMarked(t *testing.T) {
	seen := make(map[string]bool, 200)
	for range 200 {
		id, err := newCallID()
		if err != nil {
			t.Fatalf("newCallID() error = %v", err)
		}
		if !strings.HasPrefix(id, callIDPrefix) || len(id) == len(callIDPrefix) {
			t.Fatalf("id = %q, want the prefix and something after it", id)
		}
		if seen[id] {
			t.Fatalf("the id %q was made twice", id)
		}
		seen[id] = true
	}
}
