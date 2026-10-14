// Package keypool rotates several provider credentials and rests the ones the
// provider is refusing.
//
// §17.3: when one provider is configured with more than one key, calls take
// turns between them, and a key that trips the provider's rate limit repeatedly
// rests for a while instead of slowing every request down.
//
// The whole of it is a slice and one mutex, and two of its decisions are the
// reason it is a small package rather than a small feature:
//
//   - a rest is a moment per key, and whether a key is resting is one comparison
//     against the clock. Nothing runs in the background to let a key back in: the
//     moment passes and the next call finds it gone. This is the fourth time this
//     shape appears — a queue is a query (V3), a lease expires by comparison
//     (V4) — and for the same reason: a periodic unlock brings a whole failure
//     mode with it (a goroutine that did not start, a restart that lost it, a
//     timezone) to buy something a comparison already gives.
//   - "consecutive" is a count per key rather than a rate over a window. A rate
//     would leave a key that is throttled occasionally permanently at the edge of
//     its budget, and it answers a question nobody asked: what matters is how
//     long since this credential last worked.
//
// There is no lock-free path. Rotation happens once per model call, so atomics
// would buy a performance nothing is waiting on.
//
// This package imports nothing but the standard library, and it never touches
// the network: it is state, and a caller that is about to make a call.
package keypool

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Options is one deployment's key configuration, verbatim from the file.
type Options struct {
	// Keys are the credentials, in the order they were configured.
	Keys []string
	// FailureThreshold is how many consecutive rate limits rest a key. It is at
	// least one: a threshold of zero would mean no key is ever rested, which is
	// the same as not having a pool.
	FailureThreshold int
	// Cooldown is how long a rested key stays out of rotation.
	Cooldown time.Duration
}

// Pool hands out one key per call and rests the ones being throttled.
//
// The zero value is not usable; build one with New.
type Pool struct {
	mu        sync.Mutex
	keys      []entry
	next      int
	threshold int
	cooldown  time.Duration
	// clock is time.Now. It is a field rather than a call so that a case about a
	// rest can move time instead of sleeping through it.
	clock func() time.Time
}

// entry is one credential and what this pool remembers about it.
type entry struct {
	value string
	// streak counts the rate limits this key has taken since it last worked.
	streak int
	// resting is the moment this key may be used again. The zero moment is the
	// beginning of time, so a key that has never been rested is never resting.
	resting time.Time
}

// New builds a pool.
//
// Every failure here is about the list rather than about a key's value. That is
// the point: the list is what a deployment can fix, and a key must not travel —
// not into a log, and not into a message somebody pastes into a ticket. So an
// error names a position ("the key at position 2") and never a value.
func New(opts Options) (*Pool, error) {
	if len(opts.Keys) == 0 {
		return nil, errors.New("keypool: at least one key is required")
	}
	if opts.FailureThreshold < 1 {
		return nil, errors.New("keypool: the failure threshold must be at least one")
	}
	if opts.Cooldown <= 0 {
		return nil, errors.New("keypool: the cooldown must be a positive duration")
	}
	entries := make([]entry, 0, len(opts.Keys))
	seen := make(map[string]int, len(opts.Keys))
	for i, key := range opts.Keys {
		if strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("keypool: the key at position %d is empty", i+1)
		}
		// One credential listed twice cannot be rested. A rate limit is the
		// provider refusing the key, not the entry that asked with it, so the copy
		// would keep being handed out while the original rested — which is the
		// exact failure this pool exists to prevent. A list that means two keys
		// always means two different ones, so a repeat is a mistake rather than a
		// configuration, and it is refused instead of half honoured.
		if first, ok := seen[key]; ok {
			return nil, fmt.Errorf("keypool: the key at position %d repeats the one at position %d", i+1, first)
		}
		seen[key] = i + 1
		entries = append(entries, entry{value: key})
	}
	return &Pool{
		keys:      entries,
		threshold: opts.FailureThreshold,
		cooldown:  opts.Cooldown,
		clock:     time.Now,
	}, nil
}

// Next returns the key for one call.
//
// It takes the next key in turn, skipping the ones that are resting, and when
// every key is resting it takes the one that comes back first. That last choice
// is why this never answers "no key": the pool exists so that one throttled
// credential does not slow the service down, and refusing to call at all because
// every key is briefly out would turn the provider's own back-pressure into an
// outage of ours. The key that has rested longest is the one most likely to be
// accepted, and if it is not, the provider says so again — which is the signal
// this pool already knows how to read.
func (p *Pool) Next() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.clock()
	// The cursor is what makes this a rotation rather than a preference: callers
	// arriving together inside one cooldown take different keys, and the key
	// chosen last is not the first one chosen again.
	for i := 0; i < len(p.keys); i++ {
		at := (p.next + i) % len(p.keys)
		if !p.keys[at].resting.After(now) {
			p.next = (at + 1) % len(p.keys)
			return p.keys[at].value
		}
	}
	soonest := 0
	for i := 1; i < len(p.keys); i++ {
		if p.keys[i].resting.Before(p.keys[soonest].resting) {
			soonest = i
		}
	}
	p.next = (soonest + 1) % len(p.keys)
	return p.keys[soonest].value
}

// Succeeded records that the call made with key came back.
func (p *Pool) Succeeded(key string) { p.report(key, false) }

// RateLimited records that the provider refused key for being used too fast.
func (p *Pool) RateLimited(key string) { p.report(key, true) }

// report is where every remembered fact is changed, under the one lock.
//
// A key this pool never handed out is ignored rather than blamed: there is
// nothing to remember about it, and inventing an entry would be a way for a
// caller's mistake to become a credential this pool does not have.
func (p *Pool) report(key string, limited bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	at := p.indexOf(key)
	if at < 0 {
		return
	}
	if !limited {
		p.keys[at].streak = 0
		return
	}
	p.keys[at].streak++
	if p.keys[at].streak >= p.threshold {
		// Re-armed rather than set once: a key that is refused again the moment it
		// comes back is not ready, and each refusal is a fresh statement about how
		// long it needs.
		p.keys[at].resting = p.clock().Add(p.cooldown)
	}
}

func (p *Pool) indexOf(key string) int {
	for i := range p.keys {
		if p.keys[i].value == key {
			return i
		}
	}
	return -1
}
