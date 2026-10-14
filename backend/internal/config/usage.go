package config

import "fmt"

// The usage section (§5).
//
// Two things live here and they are different kinds of number: how long a call
// is remembered, and how much an account may spend. The second is a product
// decision that §23.5 第 25 条 has not made yet, which is why both quota keys
// are allowed to be zero — and zero is not "a limit of zero", it is no limit.
// The design's stated defaults are `0` and they are what .env.example carries;
// as everywhere else in this file, a key that is absent is a deployment that
// cannot start rather than one that quietly gets the default.
//
// Neither is a rule about what a call records. What an entry may contain is the
// shape of its columns (§2.8) and no deployment can widen or narrow it: an
// accounting record whose fields are configurable is one whose numbers nobody
// can compare across deployments.

// UsageConfig holds the retention window for the usage table and the two limits
// an account is measured against.
type UsageConfig struct {
	// RetentionDays is how far back a call is kept. It is read by one statement
	// that deletes by window, and never by a background job — §0.2's tenth item,
	// the same shape as the audit's window and as a lease expiring being a
	// comparison rather than a scheduler.
	//
	// It is deliberately far longer than the audit's by default, and the reason
	// is §2.9: the quota is computed from this table, so a window shorter than
	// the period a quota is measured over would leave the quota reading a table
	// that had been emptied under it.
	RetentionDays int
	// DailyCallsPerAccount and MonthlyTokensPerAccount are §2.9's two periods,
	// and zero means that period is not bounded.
	//
	// They are two numbers rather than one because they bound two different
	// ways of being expensive: a day of many short calls is not a month of long
	// ones, and a deployment that could set only one of them would have to
	// choose which of the two it is willing to be surprised by.
	DailyCallsPerAccount    int
	MonthlyTokensPerAccount int
}

// Validate checks the window is usable and the two limits are numbers of things
// rather than of something else. It says nothing about which value failed, for
// the same reason every other section does not: the message reaches a log, and
// a retention window is a deployment's own business.
func (c UsageConfig) Validate() error {
	if c.RetentionDays <= 0 {
		return fmt.Errorf("USAGE_RETENTION_DAYS must be a positive number of days")
	}
	if c.DailyCallsPerAccount < 0 {
		return fmt.Errorf("USAGE_DAILY_CALLS_PER_ACCOUNT must not be negative")
	}
	if c.MonthlyTokensPerAccount < 0 {
		return fmt.Errorf("USAGE_MONTHLY_TOKENS_PER_ACCOUNT must not be negative")
	}
	return nil
}

func loadUsageConfig(values map[string]string) (UsageConfig, error) {
	retention, err := parsePositiveInt(values, "USAGE_RETENTION_DAYS")
	if err != nil {
		return UsageConfig{}, err
	}
	// Non-negative and not positive, and the two keys are the only place that
	// distinction is drawn: zero is a deployment saying it does not bound this
	// period, which is the state §23.5 第 25 条 has left every deployment in.
	dailyCalls, err := parseNonNegativeInt(values, "USAGE_DAILY_CALLS_PER_ACCOUNT")
	if err != nil {
		return UsageConfig{}, err
	}
	monthlyTokens, err := parseNonNegativeInt(values, "USAGE_MONTHLY_TOKENS_PER_ACCOUNT")
	if err != nil {
		return UsageConfig{}, err
	}
	return UsageConfig{
		RetentionDays:           retention,
		DailyCallsPerAccount:    dailyCalls,
		MonthlyTokensPerAccount: monthlyTokens,
	}, nil
}
