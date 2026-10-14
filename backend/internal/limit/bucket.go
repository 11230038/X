package limit

import (
	"math"
	"time"
)

// A bucket is one account's allowance for one action.
//
// It is the textbook shape and nothing more: how many tokens are in it, when it
// was last filled, a rate to fill it at, and a capacity it may not exceed. The
// capacity is the burst, and a bucket starts *full* — a deployment's limit is
// not a reason for the first request after a restart, or the first request of a
// new account, to be refused.
//
// The rate is per second rather than per minute because that is the unit the
// refill is computed in; the minute is the unit the configuration is written
// in and nothing else.
type bucket struct {
	tokens float64
	// last is when the tokens were last brought up to date. Tokens are added
	// on the way past rather than by a ticker: nothing here runs when the
	// account is idle, and an account that has been away for an hour has a full
	// bucket without anything having had to notice that it left (§0.2's "a
	// lease expires by comparison").
	last time.Time
}

// newBucket starts one full.
func newBucket(now time.Time, capacity float64) bucket {
	return bucket{tokens: capacity, last: now}
}

// take removes one token if there is one, and reports how long it would take
// for the next to arrive when there is not.
//
// The wait is not an approximation of the rate: it is the missing fraction of a
// token divided by how fast tokens come back, rounded *up*, because a client
// that waits exactly the time it was told and is refused again would be reading
// a number that was wrong by a rounding.
func (b *bucket) take(now time.Time, perSecond, capacity float64) (bool, time.Duration) {
	// A clock that went backwards is left alone: the time is still b.last, and
	// the alternative is a refill that never happened refunding tokens.
	if now.After(b.last) {
		b.tokens += now.Sub(b.last).Seconds() * perSecond
		if b.tokens > capacity {
			b.tokens = capacity
		}
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	missing := 1 - b.tokens
	return false, time.Duration(math.Ceil(missing / perSecond * float64(time.Second)))
}
