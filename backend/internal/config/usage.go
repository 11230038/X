package config

import "fmt"

// The usage section (§5).
//
// One key here and two more in stage 13, and the split is the same one the
// design makes: this file is about how long a call is remembered, and the quota
// keys are about how much an account may spend. The second is a product
// decision that §23.5 第 25 条 has not made yet, which is why the quota keys
// default to "no limit" rather than to a number somebody invented.
//
// Neither is a rule about what a call records. What an entry may contain is the
// shape of its columns (§2.8) and no deployment can widen or narrow it: an
// accounting record whose fields are configurable is one whose numbers nobody
// can compare across deployments.

// UsageConfig holds the retention window for the usage table.
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
}

// Validate checks the window is usable. It says nothing about which value
// failed, for the same reason every other section does not: the message reaches
// a log, and a retention window is a deployment's own business.
func (c UsageConfig) Validate() error {
	if c.RetentionDays <= 0 {
		return fmt.Errorf("USAGE_RETENTION_DAYS must be a positive number of days")
	}
	return nil
}

func loadUsageConfig(values map[string]string) (UsageConfig, error) {
	retention, err := parsePositiveInt(values, "USAGE_RETENTION_DAYS")
	if err != nil {
		return UsageConfig{}, err
	}
	return UsageConfig{RetentionDays: retention}, nil
}
