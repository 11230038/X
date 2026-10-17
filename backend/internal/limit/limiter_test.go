package limit

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// A clock the cases move by hand. A limit is measured in seconds and minutes
// and a test that slept through them would be a test that takes a minute to
// say one thing (§2.11's key cooldown was tested the same way).
type clock struct {
	now time.Time
}

func newClock() *clock {
	return &clock{now: time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)}
}

func (c *clock) Now() time.Time { return c.now }

func (c *clock) advance(d time.Duration) { c.now = c.now.Add(d) }

func newLimiter(t *testing.T, opts Options) (*Limiter, *clock) {
	t.Helper()
	limiter, err := New(opts)
	if err != nil {
		t.Fatalf("New(%+v) error = %v", opts, err)
	}
	frozen := newClock()
	limiter.clock = frozen.Now
	return limiter, frozen
}

// take is one request, and the answer is what the bucket said.
func take(t *testing.T, limiter *Limiter, account int64, action Action) Verdict {
	t.Helper()
	verdict, err := limiter.Allow(account, action)
	if err != nil {
		t.Fatalf("Allow(%d, %s) error = %v", account, action, err)
	}
	return verdict
}

func takeAll(t *testing.T, limiter *Limiter, account int64, action Action) {
	t.Helper()
	if verdict := take(t, limiter, account, action); !verdict.Allowed {
		t.Fatalf("Allow(%d, %s) was refused, want allowed", account, action)
	}
}

func takeRefused(t *testing.T, limiter *Limiter, account int64, action Action) time.Duration {
	t.Helper()
	verdict := take(t, limiter, account, action)
	if verdict.Allowed {
		t.Fatalf("Allow(%d, %s) was allowed, want refused", account, action)
	}
	return verdict.RetryAfter
}

func TestTheLimiterRefusesNumbersItCouldNotBound(t *testing.T) {
	// Every message names the key it is about, so that a deployment with five
	// numbers in front of it is told which line of the file to look at rather
	// than left to work out which number "the burst" was.
	tests := []struct {
		name  string
		opts  Options
		names string
	}{
		{name: "a negative model rate", opts: Options{ModelPerMinute: -1, Burst: 1},
			names: "RATE_LIMIT_MODEL_PER_MINUTE"},
		{name: "a negative retrieval rate", opts: Options{RetrievalPerMinute: -1, Burst: 1},
			names: "RATE_LIMIT_RETRIEVAL_PER_MINUTE"},
		{name: "a negative upload rate", opts: Options{UploadPerMinute: -1, Burst: 1},
			names: "RATE_LIMIT_UPLOAD_PER_MINUTE"},
		{name: "a negative search rate", opts: Options{SearchPerMinute: -1, Burst: 1},
			names: "RATE_LIMIT_SEARCH_PER_MINUTE"},
		{name: "no burst at all", opts: Options{ModelPerMinute: 60}, names: "RATE_LIMIT_BURST"},
		{name: "a negative burst", opts: Options{Burst: -3}, names: "RATE_LIMIT_BURST"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limiter, err := New(tt.opts)
			if err == nil {
				t.Fatalf("New(%+v) = %v, want an error", tt.opts, limiter)
			}
			if !strings.Contains(err.Error(), tt.names) {
				t.Errorf("error = %q, want it to name %s", err, tt.names)
			}
		})
	}
}

// TestARateOfZeroIsADeploymentThatDoesNotLimitThis is the one configuration
// that is accepted while bounding nothing, and it is accepted deliberately: the
// design's §23.5 第 25 条 threshold is unset, and a build that refused requests
// while nobody had decided how fast would be worse than a fast one.
func TestARateOfZeroIsADeploymentThatDoesNotLimitThis(t *testing.T) {
	limiter, _ := newLimiter(t, Options{Burst: 1})
	for i := 0; i < 100; i++ {
		takeAll(t, limiter, 7, Model)
	}
	// Nothing was remembered, because nothing was measured: an unlimited
	// deployment must not pay for a bucket per account per action.
	if len(limiter.buckets) != 0 {
		t.Fatalf("buckets = %d, want 0 for an unlimited action", len(limiter.buckets))
	}
}

// TestABucketStartsFull pins the one thing that decides whether a limit is felt
// as the product being broken: the first requests of a fresh bucket are not
// refused. No time passes in this case at all.
func TestABucketStartsFull(t *testing.T) {
	limiter, _ := newLimiter(t, Options{ModelPerMinute: 1, Burst: 3})
	for i := 0; i < 3; i++ {
		takeAll(t, limiter, 1, Model)
	}
	if retryAfter := takeRefused(t, limiter, 1, Model); retryAfter <= 0 {
		t.Fatalf("retry after = %v, want a positive wait", retryAfter)
	}
}

// TestTheBurstIsTheBucketEvenWhenTheRateIsFarSlower pins the reading of the two
// numbers: the burst is the size of the bucket and the rate is how fast it
// refills. A deployment that writes a burst larger than a minute's worth of its
// rate means "ten at once, then slowly", and the other reading — the burst as a
// share of the rate — would silently cancel it.
func TestTheBurstIsTheBucketEvenWhenTheRateIsFarSlower(t *testing.T) {
	limiter, _ := newLimiter(t, Options{ModelPerMinute: 1, Burst: 10})
	for i := 0; i < 10; i++ {
		takeAll(t, limiter, 1, Model)
	}
	takeRefused(t, limiter, 1, Model)
}

// TestTheWaitItReportsIsWhenTheTokenArrives is the case that makes retry_after
// worth sending: an account that waits exactly what it was told must not be
// refused again. It is checked for several rates because the number is
// arithmetic on a fraction of a second, and a rounding in the wrong direction
// would make the answer wrong by a whole second.
func TestTheWaitItReportsIsWhenTheTokenArrives(t *testing.T) {
	tests := []struct {
		perMinute int
		want      time.Duration
	}{
		{perMinute: 1, want: time.Minute},
		{perMinute: 2, want: 30 * time.Second},
		{perMinute: 3, want: 20 * time.Second},
		{perMinute: 100, want: 600 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.want.String(), func(t *testing.T) {
			limiter, frozen := newLimiter(t, Options{ModelPerMinute: tt.perMinute, Burst: 1})
			takeAll(t, limiter, 1, Model)
			if got := takeRefused(t, limiter, 1, Model); got != tt.want {
				t.Fatalf("retry after = %v, want %v", got, tt.want)
			}
			frozen.advance(tt.want)
			takeAll(t, limiter, 1, Model)
		})
	}
}

// TestTheBucketRefillsOnlyToTheBurst: an account that has been away for an hour
// gets its burst back and not an hour's worth of the rate. This is the half of
// a token bucket that the design chose it for — a patient account must not
// accumulate a minute's allowance it can spend at once.
func TestTheBucketRefillsOnlyToTheBurst(t *testing.T) {
	limiter, frozen := newLimiter(t, Options{ModelPerMinute: 60, Burst: 3})
	for i := 0; i < 3; i++ {
		takeAll(t, limiter, 1, Model)
	}
	takeRefused(t, limiter, 1, Model)
	frozen.advance(time.Hour)
	for i := 0; i < 3; i++ {
		takeAll(t, limiter, 1, Model)
	}
	takeRefused(t, limiter, 1, Model)
}

// TestOneAccountIsNotAnother is §7.2's ninth acceptance item said the shortest
// way: a limit is about the account, so the account beside it is untouched.
func TestOneAccountIsNotAnother(t *testing.T) {
	limiter, _ := newLimiter(t, Options{ModelPerMinute: 1, Burst: 1})
	takeAll(t, limiter, 1, Model)
	takeRefused(t, limiter, 1, Model)
	takeAll(t, limiter, 2, Model)
}

// TestOneActionIsNotAnother is why the buckets are per action: an account that
// has been uploading files all afternoon can still ask a question.
func TestOneActionIsNotAnother(t *testing.T) {
	limiter, _ := newLimiter(t, Options{
		ModelPerMinute: 1, RetrievalPerMinute: 1, UploadPerMinute: 1,
		SearchPerMinute: 1, Burst: 1,
	})
	takeAll(t, limiter, 1, Model)
	takeRefused(t, limiter, 1, Model)
	takeAll(t, limiter, 1, Retrieval)
	takeAll(t, limiter, 1, Upload)
	// Search spends somebody else's quota and has a bucket of its own: an
	// account that has exhausted its model calls can still look a video up.
	takeAll(t, limiter, 1, Search)
}

// TestAnActionThisLimiterDoesNotKnowIsAnError is the loud failure that replaces
// a silent one. A route mounted with an action nobody configured would, without
// this, be a route that is not limited and says nothing about it.
func TestAnActionThisLimiterDoesNotKnowIsAnError(t *testing.T) {
	limiter, _ := newLimiter(t, Options{ModelPerMinute: 1, Burst: 1})
	verdict, err := limiter.Allow(1, Action("chat"))
	if !errors.Is(err, ErrUnknownAction) {
		t.Fatalf("Allow() error = %v, want ErrUnknownAction", err)
	}
	if !strings.Contains(err.Error(), "chat") {
		t.Errorf("error = %q, want it to name the action", err)
	}
	if verdict.Allowed {
		t.Error("an action the limiter does not know was allowed")
	}
	if len(limiter.buckets) != 0 {
		t.Errorf("buckets = %d, want 0 after an unknown action", len(limiter.buckets))
	}
}

// TestTheCountIsExactUnderConcurrentUse runs the one lock this package has. The
// answer has to be exactly the burst: one account racing itself is still one
// account, and a bucket that let a few extra through because two requests read
// the same token would be a limit that is never quite the one that was
// configured. It is also the case `-race` is run for.
func TestTheCountIsExactUnderConcurrentUse(t *testing.T) {
	const (
		workers = 20
		burst   = 5
	)
	limiter, _ := newLimiter(t, Options{ModelPerMinute: 60, Burst: burst})

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		allowed int
	)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			verdict, err := limiter.Allow(1, Model)
			if err != nil {
				return
			}
			if verdict.Allowed {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != burst {
		t.Fatalf("allowed = %d, want exactly %d", allowed, burst)
	}
}

// TestAClockThatWentBackwardsDoesNotRefundTokens: the refill is a subtraction
// between two moments, and a moment from the future — an NTP step, a machine
// resumed from sleep with a clock that was set back — would otherwise be a
// refill that never happened handing out tokens.
func TestAClockThatWentBackwardsDoesNotRefundTokens(t *testing.T) {
	limiter, frozen := newLimiter(t, Options{ModelPerMinute: 60, Burst: 1})
	takeAll(t, limiter, 1, Model)
	frozen.now = frozen.now.Add(-time.Hour)
	takeRefused(t, limiter, 1, Model)
}
