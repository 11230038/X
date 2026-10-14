package config

import "fmt"

// The audit section (§5).
//
// Two keys, and neither is a rule about what gets recorded. Which actions exist
// is a closed set in internal/audit/actions.go, and what an entry may contain is
// the shape of its columns (§2.6) — a deployment cannot widen either, which is
// the point: an audit whose fields are configurable is an audit whose fields
// nobody can rely on.

// AuditConfig holds the retention window and the page cap.
type AuditConfig struct {
	// RetentionDays is how far back an entry is kept. It is read by one
	// statement that deletes by window, and never by a background job: §0.2's
	// tenth item makes cleanup a query a caller runs, in the same way a lease
	// expiring is a comparison and not a scheduler.
	RetentionDays int
	// MaxPageSize is how many entries one read may return. It is a cap and not
	// a default: a caller asking for more is refused rather than served a
	// smaller page, because a silently shortened answer to "what happened" is
	// an answer that reads as "nothing else happened".
	MaxPageSize int
}

// Validate checks both keys are usable. The two are positive integers and it
// says so without naming the value that failed, for the same reason every other
// section does: the message reaches a log, and a retention window is a
// deployment's own business.
func (c AuditConfig) Validate() error {
	if c.RetentionDays <= 0 {
		return fmt.Errorf("AUDIT_RETENTION_DAYS must be a positive number of days")
	}
	if c.MaxPageSize <= 0 {
		return fmt.Errorf("AUDIT_MAX_PAGE_SIZE must be a positive number of entries")
	}
	return nil
}

func loadAuditConfig(values map[string]string) (AuditConfig, error) {
	retention, err := parsePositiveInt(values, "AUDIT_RETENTION_DAYS")
	if err != nil {
		return AuditConfig{}, err
	}
	pageSize, err := parsePositiveInt(values, "AUDIT_MAX_PAGE_SIZE")
	if err != nil {
		return AuditConfig{}, err
	}
	return AuditConfig{RetentionDays: retention, MaxPageSize: pageSize}, nil
}
