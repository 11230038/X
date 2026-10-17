// Package limit is the token bucket the expensive kinds of route are answered
// through (§2.10).
//
// Rate limiting and the quota answer two different questions, and §2.10 keeps
// them apart because each is the wrong answer to the other: "how fast" is about
// now — seconds, minutes, process memory, and it is *correct* for it to start
// over when the process does — while "how much" is about a period that a
// restart must not shorten, which is why the quota is a query over the calls
// already written down and this is not.
//
// It holds no storage, imports nothing outside the standard library, and
// touches no network. What it holds is a rate per action, one bucket per
// account, and one mutex: a request that is at the limit is refused by an
// arithmetic comparison, the same shape as a lease expiring (§0.2).
package limit

import (
	"errors"
	"fmt"
	"time"
)

// An Action is one class of work that costs something outside this process, and
// is therefore worth bounding per account.
//
// The four are closed rather than open: what may be limited is a decision about
// the product (a route that reaches an upstream provider, or fills the disk),
// and a deployment chooses numbers for these four rather than inventing
// classes. A limiter built without one of them is refused.
type Action string

const (
	// Model is a call to the shared chat provider: the request that costs
	// money, and the one §17.3's key pool exists to keep flowing.
	Model Action = "model"
	// Retrieval is a question put to the embedding provider and answered from
	// the vector index — one upstream call that is not a conversation.
	Retrieval Action = "retrieval"
	// Upload is bytes written to this instance's disk. It costs no upstream
	// call, and what it bounds is the one resource an account can exhaust for
	// everybody on the machine.
	Upload Action = "upload"
	// Search is a keyword sent to a public video site. It spends no key of ours,
	// but it does spend somebody else's quota — which is the resource that, once
	// exhausted, takes the feature away from every account at once.
	Search Action = "search"
)

// Options is what one deployment allows one account to do, per minute.
//
// A rate of zero means that action is not limited at all, which is the
// deployment's own choice and not this package's: the design's §23.5 第 25 条
// threshold has not been set, and being refused while nobody has decided how
// fast is worse than being fast.
type Options struct {
	ModelPerMinute     int
	RetrievalPerMinute int
	UploadPerMinute    int
	SearchPerMinute    int
	// Burst is the shared size of every bucket: how many actions one account
	// may do back to back before the rate applies. It is shared because it
	// answers the same question for all four — "how fast can a person click" —
	// while the rate answers how much of each the instance will carry.
	Burst int
}

// A Verdict is what the limiter decided about one attempt.
type Verdict struct {
	Allowed bool
	// RetryAfter is how long until the account may do this again: zero when
	// Allowed, and how long the missing token takes to come back when not. It
	// is carried out of here rather than computed again by the caller, because
	// only the bucket knows when it was last filled.
	RetryAfter time.Duration
}

// ErrUnknownAction is what Allow returns for an action this limiter was not
// built with.
//
// It exists to make one mistake loud. A route mounted with a misspelled action
// would otherwise be a route nothing limits — the failure §2.10's "only three
// route classes" is meant to prevent, arriving silently. Here it reaches the
// middleware as an error, which answers 500 and logs it: a wiring mistake, said
// to be one, rather than a limit that quietly stopped applying.
var ErrUnknownAction = errors.New("limit: unknown action")

// errUnknownAction names the action, which is a class of work and never
// anything an account sent.
func errUnknownAction(action Action) error {
	return fmt.Errorf("%w: %q", ErrUnknownAction, action)
}
