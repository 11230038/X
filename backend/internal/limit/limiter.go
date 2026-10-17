package limit

import (
	"fmt"
	"sync"
	"time"
)

// A Limiter is one process's buckets, and it is one process's on purpose.
//
// Two instances behind a load balancer each count what they answered, so the
// limit a deployment actually gets is N times what it wrote down. That is not a
// defect and not something this package works around: §2.10 records it as the
// consequence of V1 §6 决策 3 — no Redis until there are genuinely two
// instances — and this build is a single instance.
//
// Being per process is also why nothing here is persisted: a restart empties
// the buckets, and for a limit measured in seconds that is the right answer
// rather than a gap in the accounting.
type Limiter struct {
	// The mutex is held across a decision and nothing else — no I/O, no call
	// out, no allocation beyond a map write. One lock rather than a sharded or
	// lock-free one: the contention here is one comparison per expensive
	// request, and a lock-free map would be a harder thing to be sure about in
	// exchange for nothing measurable.
	mu      sync.Mutex
	rules   map[Action]rule
	buckets map[key]bucket
	// clock is time.Now. It is a field so that a test can decide what "an hour
	// later" means instead of sleeping through it.
	clock func() time.Time
}

// rule is what one action is allowed, resolved into the units the bucket works
// in.
type rule struct {
	// perSecond is the rate the bucket refills at. It is zero for an action the
	// deployment does not limit.
	perSecond float64
	capacity  float64
}

// key is one bucket's identity: an account and the class of work it is doing.
// The two are separate buckets rather than one shared allowance, so that an
// account which has been uploading files all afternoon can still ask a question
// (§2.10: a limiter that made one kind of work crowd out another would be felt
// as the product being broken).
type key struct {
	account int64
	action  Action
}

// New builds a limiter from a deployment's numbers.
//
// Every action is present in the result even when its rate is zero, which is
// how an action that is not limited is told apart from one this limiter has
// never heard of: the first is allowed unconditionally, the second is an error.
func New(opts Options) (*Limiter, error) {
	if opts.Burst < 1 {
		// The key is named for the same reason the four rates below name theirs:
		// the deployment reading this has five numbers in front of it, and a
		// sentence about "the burst" alone leaves it to guess which line of the
		// file the sentence is about.
		return nil, fmt.Errorf("limit: RATE_LIMIT_BURST must be a positive number of actions")
	}
	// A slice in a fixed order rather than a map, so that a deployment with two
	// bad numbers is told about the first one every time instead of either one
	// at random.
	rates := []struct {
		action     Action
		perMinute  int
		configures string
	}{
		{Model, opts.ModelPerMinute, "RATE_LIMIT_MODEL_PER_MINUTE"},
		{Retrieval, opts.RetrievalPerMinute, "RATE_LIMIT_RETRIEVAL_PER_MINUTE"},
		{Upload, opts.UploadPerMinute, "RATE_LIMIT_UPLOAD_PER_MINUTE"},
		{Search, opts.SearchPerMinute, "RATE_LIMIT_SEARCH_PER_MINUTE"},
	}
	l := &Limiter{
		rules:   make(map[Action]rule, len(rates)),
		buckets: make(map[key]bucket),
		clock:   time.Now,
	}
	for _, entry := range rates {
		if entry.perMinute < 0 {
			return nil, fmt.Errorf("limit: %s must not be negative", entry.configures)
		}
		// The capacity is the burst even where the burst is larger than a
		// minute's worth of the rate. A bucket may then hold more than a
		// minute's allowance, which is what "ten at once" means when the rate
		// is one a minute: the burst is the size of the bucket and the rate is
		// how fast it refills, and reading the burst as a share of the rate
		// would let a small rate silently cancel a configured burst.
		l.rules[entry.action] = rule{
			perSecond: float64(entry.perMinute) / 60,
			capacity:  float64(opts.Burst),
		}
	}
	return l, nil
}

// Allow takes one token for this account doing this action, and reports whether
// it got one.
//
// An action the deployment did not limit is allowed without a bucket ever being
// made for it, which is also what keeps an unlimited deployment from growing a
// map entry per account per action for nothing.
//
// Nothing prunes the map. An entry is one account, one action and about forty
// bytes, and what a stale one costs is the memory of a struct — the state an
// account that has gone away leaves behind is bounded by the number of accounts
// and not by anything it did, which is the price of the comparison being the
// whole mechanism (§0.2).
func (l *Limiter) Allow(account int64, action Action) (Verdict, error) {
	r, known := l.rules[action]
	if !known {
		return Verdict{}, errUnknownAction(action)
	}
	if r.perSecond <= 0 {
		return Verdict{Allowed: true}, nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock()
	k := key{account: account, action: action}
	b, exists := l.buckets[k]
	if !exists {
		b = newBucket(now, r.capacity)
	}
	allowed, retryAfter := b.take(now, r.perSecond, r.capacity)
	// The bucket is written back whether or not a token was taken, so that the
	// map holds the bucket as it stands rather than as it stood at the last
	// request that got one. The refill is lossless, so this changes no number
	// either way; it is here so that the state in the map is the state.
	l.buckets[k] = b
	return Verdict{Allowed: allowed, RetryAfter: retryAfter}, nil
}
