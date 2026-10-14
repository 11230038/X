package config

import "fmt"

// The rate limit section (§2.10).
//
// Four numbers rather than a table of key/value pairs: what may be limited is
// three named classes of work — a call to the model provider, a call to the
// embedding provider, and a file written to this instance's disk — and a
// deployment chooses numbers for those three rather than inventing classes.
// The set is closed here and in the limiter, and both refuse a fourth by
// refusing to start rather than by ignoring it.
//
// A rate of zero is a deployment saying "do not bound this", which is the
// design's own default position while §23.5 第 25 条's threshold is unset: being
// refused while nobody has decided how fast is worse than being fast (§2.10).
// It is spelled out in the file rather than left out of it, because every other
// section of this configuration works that way — a key that is absent is a
// startup failure, never a number this program made up on a deployment's
// behalf.

// LimitConfig holds how fast one account may do each class of expensive work.
type LimitConfig struct {
	// ModelPerMinute bounds calls to the shared chat provider. It is the one
	// that costs money per request, and the one §17.3's key pool is rotating
	// for.
	ModelPerMinute int
	// RetrievalPerMinute bounds questions put to the embedding provider. It is
	// a separate bucket from the model's because the two are different
	// upstreams with different prices, and one being busy says nothing about
	// the other.
	RetrievalPerMinute int
	// UploadPerMinute bounds files written to disk. It costs no upstream call;
	// what it bounds is the one resource an account can exhaust for everybody
	// on the machine.
	UploadPerMinute int
	// Burst is how many of any one of the three may be done back to back
	// before the rate applies. It is shared because it answers the same
	// question for all three — how fast a person can click — while each rate
	// answers how much of that thing the instance will carry.
	Burst int
}

// Validate checks the four numbers. A rate may be zero, which is "not limited";
// the burst may not, because a burst of zero is a bucket that never holds a
// token and would refuse every request of all three classes — a deployment
// that meant to turn the limiter off says so with a zero *rate*, and a zero
// here is a typo that must not read as an outage.
func (c LimitConfig) Validate() error {
	for _, field := range []struct {
		key   string
		value int
	}{
		{"RATE_LIMIT_MODEL_PER_MINUTE", c.ModelPerMinute},
		{"RATE_LIMIT_RETRIEVAL_PER_MINUTE", c.RetrievalPerMinute},
		{"RATE_LIMIT_UPLOAD_PER_MINUTE", c.UploadPerMinute},
	} {
		if field.value < 0 {
			return fmt.Errorf("%s must not be negative", field.key)
		}
	}
	if c.Burst < 1 {
		return fmt.Errorf("RATE_LIMIT_BURST must be a positive integer")
	}
	return nil
}

func loadLimitConfig(values map[string]string) (LimitConfig, error) {
	model, err := parseNonNegativeInt(values, "RATE_LIMIT_MODEL_PER_MINUTE")
	if err != nil {
		return LimitConfig{}, err
	}
	retrieval, err := parseNonNegativeInt(values, "RATE_LIMIT_RETRIEVAL_PER_MINUTE")
	if err != nil {
		return LimitConfig{}, err
	}
	upload, err := parseNonNegativeInt(values, "RATE_LIMIT_UPLOAD_PER_MINUTE")
	if err != nil {
		return LimitConfig{}, err
	}
	burst, err := parsePositiveInt(values, "RATE_LIMIT_BURST")
	if err != nil {
		return LimitConfig{}, err
	}
	return LimitConfig{
		ModelPerMinute:     model,
		RetrievalPerMinute: retrieval,
		UploadPerMinute:    upload,
		Burst:              burst,
	}, nil
}
