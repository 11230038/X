package usage

// The service: the rules about one report, over a ledger.
//
// It holds one number and it comes from configuration (§5): how far back a call
// is kept. That number is not a rule about the report's shape — what a call
// records is the shape of its columns (§2.8) and no deployment can widen it —
// and it is here rather than in the ledger because the ledger knows how to
// delete by a moment and only the deployment knows which moment.
//
// The service is also where a query is checked before it reaches SQL, which is
// the same division the audit makes: the store is one direction over one table,
// and what a caller is allowed to ask is decided above it.

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Errors a report refuses with.
var (
	// ErrUnknownGroupBy is a breakdown this build does not serve.
	ErrUnknownGroupBy = errors.New("usage: not a breakdown this build serves")
	// ErrEmptyWindow is a window that ends before it starts. It is refused
	// rather than served as a report of zero, and the difference matters: a
	// report of nothing reads as "you spent nothing", which is an answer to a
	// question nobody asked.
	ErrEmptyWindow = errors.New("usage: a window has to end after it starts")
	// ErrNoAccount is a report about nobody's calls. §2.9 sums usage per
	// account, so a report with no account behind it is not a narrower report —
	// it is a different table.
	ErrNoAccount = errors.New("usage: a report is about one account's calls")
)

// Service answers reports over a ledger.
type Service struct {
	ledger        Ledger
	retentionDays int
}

// NewService builds the service over a ledger and the deployment's retention
// window (USAGE_RETENTION_DAYS).
//
// The window is an argument rather than an Options struct because there is
// exactly one of it, and because the other half of this package already has an
// Options: a second one under the same name would be two shapes a reader has to
// tell apart at every call site.
func NewService(ledger Ledger, retentionDays int) (*Service, error) {
	if ledger == nil {
		return nil, ErrNotConfigured
	}
	if retentionDays <= 0 {
		return nil, fmt.Errorf("invalid usage configuration: the retention window must be positive")
	}
	return &Service{ledger: ledger, retentionDays: retentionDays}, nil
}

// Read answers one report.
//
// The window and the breakdown are checked here and the rest is the ledger's:
// which rows are whose, and how they add up, is a question about the table.
// Nothing here inspects the caller's authority — what makes a report this
// account's is the query the route above built (§2.16).
func (s *Service) Read(ctx context.Context, query Query) (Report, error) {
	if s == nil || s.ledger == nil {
		return Report{}, ErrNotConfigured
	}
	if query.OwnerID <= 0 {
		return Report{}, fmt.Errorf("%w: this report named no account", ErrNoAccount)
	}
	if query.GroupBy != GroupNone && !KnownGroupBy(query.GroupBy) {
		return Report{}, fmt.Errorf("%w: %q", ErrUnknownGroupBy, query.GroupBy)
	}
	if !query.Since.IsZero() && !query.Until.IsZero() && !query.Until.After(query.Since) {
		return Report{}, fmt.Errorf("%w: since %s, until %s", ErrEmptyWindow,
			query.Since.UTC().Format(time.RFC3339), query.Until.UTC().Format(time.RFC3339))
	}
	report, err := s.ledger.Read(ctx, query)
	if err != nil {
		return Report{}, err
	}
	// The window and the breakdown travel with the numbers rather than being
	// assumed by whoever reads them: a total without the window it is over is a
	// total nobody can check.
	report.Since = moment(query.Since)
	report.Until = moment(query.Until)
	report.GroupBy = query.GroupBy
	if report.Groups == nil && query.GroupBy != GroupNone {
		report.Groups = []Group{}
	}
	return report, nil
}

// Prune lets go of the calls older than the configured window and reports how
// many went.
//
// It answers with the count rather than nothing because whoever calls it is the
// one who decided to: a cleanup that reports zero is information — it is the
// difference between a window that is working and a window nobody ever ran.
func (s *Service) Prune(ctx context.Context) (int64, error) {
	if s == nil || s.ledger == nil {
		return 0, ErrNotConfigured
	}
	before := time.Now().UTC().AddDate(0, 0, -s.retentionDays)
	return s.ledger.Prune(ctx, before)
}

// Unfinished counts the calls that have not come back, over the whole table.
//
// It is the one read in this package that is not a report: no account, no
// window, no breakdown, and one number back. The status route asks it, and what
// it is asking is whether the two-part write (§2.7) is losing rows — which is a
// property of the process rather than of anybody's spending.
func (s *Service) Unfinished(ctx context.Context) (int64, error) {
	if s == nil || s.ledger == nil {
		return 0, ErrNotConfigured
	}
	return s.ledger.Unfinished(ctx)
}

// RetentionDays is the configured window, for a caller that reports it.
func (s *Service) RetentionDays() int { return s.retentionDays }

// moment turns a query's bound into the report's, keeping "no bound" as the
// absence of a value rather than as a moment in the year one.
func moment(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}
	utc := at.UTC()
	return &utc
}
