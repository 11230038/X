package audit

// The service: the rules about writing and reading an entry, over a store.
//
// It holds two numbers, and both come from configuration (§5): how far back
// entries are kept, and how many one read may return. Neither is a rule about
// the audit's shape — that is in types.go and cannot be configured — and both
// are deployment decisions about a deployment's own volume.
//
// The service is also the only place `Record` is called from, and that is worth
// knowing when reading a caller: every domain writes its entry through here, so
// "what does an audit entry look like" has one answer rather than one per
// package.

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Errors the service refuses with.
var (
	// ErrNotConfigured is what a service built without a store answers with.
	// It is a different sentence from a storage failure: one is a wiring
	// mistake that no retry fixes, and the other is a database that may come
	// back.
	ErrNotConfigured = errors.New("audit is not configured")
)

// Options are the two configured numbers, plus the clock.
type Options struct {
	// MaxPageSize is how many entries one read may return (AUDIT_MAX_PAGE_SIZE).
	MaxPageSize int
	// RetentionDays is how far back entries are kept (AUDIT_RETENTION_DAYS).
	RetentionDays int
	// Now is the clock, for tests.
	Now func() time.Time
}

// Service records and reads the audit.
type Service struct {
	store         Store
	maxPageSize   int
	retentionDays int
	now           func() time.Time
}

// NewService builds the service over a store.
func NewService(store Store, options Options) (*Service, error) {
	if store == nil {
		return nil, ErrNotConfigured
	}
	if options.MaxPageSize <= 0 {
		return nil, fmt.Errorf("invalid audit configuration: the page cap must be positive")
	}
	if options.RetentionDays <= 0 {
		return nil, fmt.Errorf("invalid audit configuration: the retention window must be positive")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		store:         store,
		maxPageSize:   options.MaxPageSize,
		retentionDays: options.RetentionDays,
		now:           now,
	}, nil
}

// MaxPageSize is the cap one read is served under.
func (s *Service) MaxPageSize() int { return s.maxPageSize }

// Record writes one entry.
//
// It stamps the time here rather than in the store, so that two entries written
// in the same action share a moment: an "old value / new value" pair stamped
// separately can straddle a second boundary and read as two unrelated events.
func (s *Service) Record(ctx context.Context, event Event) error {
	if s == nil || s.store == nil {
		return ErrNotConfigured
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = s.now().UTC()
	}
	if err := event.Validate(); err != nil {
		return err
	}
	_, err := s.store.Append(ctx, event)
	return err
}

// List reads entries, newest first.
//
// A limit above the cap is refused rather than clamped, and refusal is the
// design's choice rather than this function's: a page silently shortened to the
// cap reads as "this is everything", which on an audit is the one wrong answer
// it must never give. Nothing here inspects the caller's authority — what makes
// this administrative is the route in front of it (§2.16).
func (s *Service) List(ctx context.Context, filter Filter) ([]Event, error) {
	if s == nil || s.store == nil {
		return nil, ErrNotConfigured
	}
	if filter.Action != "" && !KnownAction(filter.Action) {
		return nil, fmt.Errorf("%w: %q", ErrUnknownAction, filter.Action)
	}
	if filter.TargetKind != "" && !KnownTargetKind(filter.TargetKind) {
		return nil, fmt.Errorf("%w: %q", ErrUnknownTargetKind, filter.TargetKind)
	}
	if filter.Limit > s.maxPageSize {
		return nil, fmt.Errorf("%w: the cap is %d", ErrPageTooLarge, s.maxPageSize)
	}
	if filter.Limit <= 0 {
		filter.Limit = s.maxPageSize
	}
	return s.store.Page(ctx, filter)
}

// Prune removes entries older than the configured window and reports how many
// went.
//
// It answers with the count rather than nothing because the caller is the one
// who decides whether to have run it, and a cleanup that reports "0" is
// information: it is the difference between a window that is working and a
// window nobody ever ran.
func (s *Service) Prune(ctx context.Context) (int64, error) {
	if s == nil || s.store == nil {
		return 0, ErrNotConfigured
	}
	before := s.now().UTC().AddDate(0, 0, -s.retentionDays)
	return s.store.Prune(ctx, before)
}

// RetentionDays is the configured window, for a caller that reports it.
func (s *Service) RetentionDays() int { return s.retentionDays }
