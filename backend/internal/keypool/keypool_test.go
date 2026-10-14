package keypool

// What §17.3 asks of the pool, one case each: the keys take turns, a key that
// is refused repeatedly sits out, a key that is refused once does not, the rest
// ends on its own, a success clears the count, and callers arriving together do
// not get the same key.
//
// Two of these are about what a failed pool must *not* do. No case may put a key
// in an error message — the same rule the database fixture follows, and the
// reason every message here names a position — so the refusals are asserted by
// searching the message for the key's own text. And nothing may let a call go
// out without a key, which is why the pool always answers.

import (
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// theKeys are what most cases rotate. They are shaped like credentials only so
// that a message that leaked one would be recognisable.
var theKeys = []string{"sk-live-aaa", "sk-live-bbb", "sk-live-ccc"}

func newPool(t *testing.T, keys []string, threshold int, cooldown time.Duration) *Pool {
	t.Helper()
	pool, err := New(Options{Keys: keys, FailureThreshold: threshold, Cooldown: cooldown})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return pool
}

// frozen is a clock a case can move, so that a cooldown is tested by comparing
// two moments rather than by sleeping through one.
type frozen struct {
	mu  sync.Mutex
	now time.Time
}

func newFrozen() *frozen { return &frozen{now: time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)} }

func (f *frozen) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *frozen) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

// take asks the pool for n keys in a row.
func take(pool *Pool, n int) []string {
	keys := make([]string, 0, n)
	for range n {
		keys = append(keys, pool.Next())
	}
	return keys
}

func equalKeys(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// A pool that cannot rotate is a pool that would hand out the same credential
// for every call, so it is refused at construction rather than at the first
// call. Every message names a position: the keys are secrets, and this message
// is the one somebody copies into a ticket.
func TestAPoolRefusesWhatItCouldNotRotate(t *testing.T) {
	for _, tt := range []struct {
		name string
		opts Options
	}{
		{name: "no keys at all", opts: Options{FailureThreshold: 3, Cooldown: time.Minute}},
		{name: "a blank key", opts: Options{Keys: []string{"sk-live-aaa", "  "}, FailureThreshold: 3, Cooldown: time.Minute}},
		{name: "the same key twice", opts: Options{Keys: []string{"sk-live-aaa", "sk-live-bbb", "sk-live-aaa"}, FailureThreshold: 3, Cooldown: time.Minute}},
		{name: "a threshold of zero", opts: Options{Keys: theKeys, FailureThreshold: 0, Cooldown: time.Minute}},
		{name: "a negative threshold", opts: Options{Keys: theKeys, FailureThreshold: -1, Cooldown: time.Minute}},
		{name: "no cooldown", opts: Options{Keys: theKeys, FailureThreshold: 3, Cooldown: 0}},
		{name: "a negative cooldown", opts: Options{Keys: theKeys, FailureThreshold: 3, Cooldown: -time.Minute}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// The options are never printed: printing the mistake would print the
			// keys, which is the one thing a failure message must not carry.
			pool, err := New(tt.opts)
			if err == nil {
				t.Fatalf("New(%s) was accepted", tt.name)
			}
			if pool != nil {
				t.Error("a pool was returned alongside the failure")
			}
			if strings.Contains(err.Error(), "sk-live") {
				t.Errorf("the message carries a key: %s", err)
			}
		})
	}
}

// The repeat case names the two positions, because the fix is a line edit and
// the positions are what says which line.
func TestTheRepeatIsRefusedByPosition(t *testing.T) {
	_, err := New(Options{
		Keys:             []string{"sk-live-aaa", "sk-live-bbb", "sk-live-aaa"},
		FailureThreshold: 3,
		Cooldown:         time.Minute,
	})
	if err == nil {
		t.Fatal("New() was accepted")
	}
	for _, want := range []string{"position 3", "position 1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to carry %q", err, want)
		}
	}
}

func TestTheKeysTakeTurns(t *testing.T) {
	pool := newPool(t, theKeys, 3, time.Minute)
	got := take(pool, 7)
	want := []string{theKeys[0], theKeys[1], theKeys[2], theKeys[0], theKeys[1], theKeys[2], theKeys[0]}
	if !equalKeys(got, want) {
		t.Errorf("keys = %v, want %v", got, want)
	}
}

// Before the threshold there is nothing to rest: a credential that was refused
// once is a credential in a minute that was busy.
func TestOneRefusalIsNotEnough(t *testing.T) {
	pool := newPool(t, theKeys, 3, time.Minute)
	pool.RateLimited(theKeys[0])
	pool.RateLimited(theKeys[0])

	if got := pool.Next(); got != theKeys[0] {
		t.Errorf("first key = %q, want the twice-refused one back in rotation", got)
	}
}

func TestARefusedKeyRestsAtTheThreshold(t *testing.T) {
	pool := newPool(t, theKeys, 3, time.Minute)
	for range 3 {
		pool.RateLimited(theKeys[0])
	}

	got := take(pool, 4)
	want := []string{theKeys[1], theKeys[2], theKeys[1], theKeys[2]}
	if !equalKeys(got, want) {
		t.Errorf("keys = %v, want %v: the refused key is out", got, want)
	}
}

// A threshold of one is a legitimate way to say "rest it the first time" — and
// the shortest path through the counting, so it is worth its own case.
func TestAThresholdOfOneRestsImmediately(t *testing.T) {
	pool := newPool(t, theKeys, 1, time.Minute)
	pool.RateLimited(theKeys[0])

	if got := pool.Next(); got == theKeys[0] {
		t.Error("the refused key came back with a threshold of one")
	}
}

// The rest ends by itself. Nothing is woken up, no goroutine runs: the moment
// passes and the next call finds it gone — which is the whole design (§2.11).
func TestTheRestEndsByItself(t *testing.T) {
	pool := newPool(t, theKeys, 1, time.Minute)
	clock := newFrozen()
	pool.clock = clock.Now

	pool.RateLimited(theKeys[0])
	if got := pool.Next(); got == theKeys[0] {
		t.Fatal("the refused key came back immediately")
	}

	clock.advance(time.Minute - time.Second)
	if got := pool.Next(); got == theKeys[0] {
		t.Error("the rest ended a second early")
	}

	clock.advance(time.Second)
	// The cursor is where the two calls above left it, so a whole round is taken
	// to see the rested key rather than reading the first answer as its return.
	back := false
	for range len(theKeys) {
		if pool.Next() == theKeys[0] {
			back = true
		}
	}
	if !back {
		t.Error("the key did not come back once its rest ended")
	}
}

// "Consecutive" is the count since the key last worked, and a success is what
// says it worked.
func TestASuccessClearsTheCount(t *testing.T) {
	pool := newPool(t, theKeys, 3, time.Minute)
	pool.RateLimited(theKeys[0])
	pool.RateLimited(theKeys[0])
	pool.Succeeded(theKeys[0])
	pool.RateLimited(theKeys[0])
	pool.RateLimited(theKeys[0])

	if got := pool.Next(); got != theKeys[0] {
		t.Errorf("first key = %q, want it still in rotation: the count restarted", got)
	}
}

// The count belongs to a key rather than to the pool: two keys refused once each
// is not one key refused twice.
func TestTheCountIsPerKey(t *testing.T) {
	pool := newPool(t, theKeys, 2, time.Minute)
	pool.RateLimited(theKeys[0])
	pool.RateLimited(theKeys[1])

	got := take(pool, 3)
	want := []string{theKeys[0], theKeys[1], theKeys[2]}
	if !equalKeys(got, want) {
		t.Errorf("keys = %v, want %v: neither key was rested by a count that is not its own", got, want)
	}
}

// When everything is resting the pool still answers, with the key that comes
// back first: refusing to call would turn the provider's back-pressure into an
// outage of ours.
func TestTheKeyThatComesBackFirstIsTheOneUsed(t *testing.T) {
	pool := newPool(t, theKeys, 1, time.Minute)
	clock := newFrozen()
	pool.clock = clock.Now

	pool.RateLimited(theKeys[0])
	clock.advance(10 * time.Second)
	pool.RateLimited(theKeys[1])
	clock.advance(10 * time.Second)
	pool.RateLimited(theKeys[2])

	// Every key is resting. The one refused first is the one that has rested
	// longest, so it is the one most likely to be accepted now.
	if got := pool.Next(); got != theKeys[0] {
		t.Errorf("key = %q, want %q: the one that has rested longest", got, theKeys[0])
	}
}

// Callers arriving together take different keys. This is what the cursor buys,
// and it is the reason a rotation is worth having at all: four requests that
// arrive at once should not all spend the same credential.
func TestCallersArrivingTogetherTakeDifferentKeys(t *testing.T) {
	keys := []string{"sk-live-aaa", "sk-live-bbb", "sk-live-ccc", "sk-live-ddd"}
	pool := newPool(t, keys, 3, time.Minute)

	var (
		start  sync.WaitGroup
		done   sync.WaitGroup
		mu     sync.Mutex
		handed []string
	)
	start.Add(1)
	for range len(keys) {
		done.Add(1)
		go func() {
			defer done.Done()
			// The barrier is what makes these simultaneous: without it the first
			// goroutine could finish a whole round before the second asks.
			start.Wait()
			key := pool.Next()
			mu.Lock()
			defer mu.Unlock()
			handed = append(handed, key)
		}()
	}
	start.Done()
	done.Wait()

	sort.Strings(handed)
	if !equalKeys(handed, keys) {
		t.Errorf("handed = %v, want each key once: %v", handed, keys)
	}
}

// The pool is shared by every call in the process, so this is the case that
// keeps the lock honest. The race detector is the assertion; the check that no
// call comes back empty is what catches the other way a shared pool breaks.
func TestThePoolIsSafeUnderConcurrentUse(t *testing.T) {
	pool := newPool(t, theKeys, 2, time.Millisecond)
	clock := newFrozen()
	pool.clock = clock.Now

	var (
		wg          sync.WaitGroup
		mu          sync.Mutex
		emptyHanded int
		moved       int
	)
	for worker := range 8 {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for range 50 {
				key := pool.Next()
				if key == "" {
					mu.Lock()
					emptyHanded++
					mu.Unlock()
					continue
				}
				switch worker % 3 {
				case 0:
					pool.RateLimited(key)
					mu.Lock()
					moved++
					clock.advance(time.Millisecond)
					mu.Unlock()
				case 1:
					pool.Succeeded(key)
				default:
					// The third caller says nothing at all, which is what a call that
					// never reached the provider reports.
				}
			}
		}(worker)
	}
	wg.Wait()

	if emptyHanded > 0 {
		t.Errorf("Next() answered no key %d times", emptyHanded)
	}
	if moved == 0 {
		t.Error("no refusal was ever recorded: the case did not exercise anything")
	}
}

// Reporting about a key the pool never handed out is a caller's mistake, and it
// must not become an entry this pool remembers — nor a panic.
func TestReportingAnUnknownKeyIsIgnored(t *testing.T) {
	pool := newPool(t, theKeys, 1, time.Minute)
	pool.RateLimited("sk-live-not-mine")
	pool.Succeeded("sk-live-not-mine")

	got := take(pool, 3)
	if !equalKeys(got, theKeys) {
		t.Errorf("keys = %v, want %v: an unknown key changed nothing", got, theKeys)
	}
}
