package memory

// The memory tables in memory, for the tests that are about rules rather than
// about storage.
//
// It answers the contract and not the database: an ErrDuplicate here stands in
// for the primary key, an ErrStaleState for the version guard, and the orders it
// returns are the ones the adapter promises. What it cannot attest to is
// anything the engine decides — which key collides, what a foreign key cascades,
// whether a transaction rolls back — and those are the tests in
// repository_db_test.go, which run against a real seekdb. This file exists so
// that the service's decisions can be read one at a time without a database in
// the loop, and so that a failure can be injected where one cannot be provoked.

import (
	"cmp"
	"context"
	"slices"
	"time"
)

type fakeRepository struct {
	traces  []Trace
	facts   []Fact
	sources []FactSource
	audits  []Audit

	// Errors a test asks for, so that the failure paths a service has are
	// reachable without a database that is unwell.
	appendTraceErr     error
	tracesErr          error
	tracesOfErr        error
	activeFactErr      error
	factErr            error
	factsErr           error
	duplicateGroupsErr error
	createFactErr      error
	saveFactErr        error
	attachSourceErr    error
	purgeFactsErr      error
	moveSourcesErr     error
	appendAuditErr     error
	auditsErr          error

	// attached and moved record what reached storage, which is how "the fact was
	// given its evidence" is told from "the fact was given one row".
	attached []FactSource
	moved    []FactSource
	// purged records the ids a purge removed, in the order they went, which is
	// what a cleanup's two runs are compared on.
	purged []string
	// expires records positions a listing was asked for.
	traceReads []TraceQuery
}

var _ Repository = (*fakeRepository)(nil)

func newFakeRepository() *fakeRepository { return &fakeRepository{} }

// Within runs the work against the same store. There is no rollback here, and a
// test that needs one is a test about the engine: an L1 row that survives a
// failed fact write is a fact the transaction proved, not this.
func (f *fakeRepository) Within(_ context.Context, work func(Repository) error) error {
	return work(f)
}

func (f *fakeRepository) AppendTrace(_ context.Context, trace Trace) error {
	if f.appendTraceErr != nil {
		return f.appendTraceErr
	}
	if slices.ContainsFunc(f.traces, func(held Trace) bool { return held.ID == trace.ID }) {
		return ErrDuplicate
	}
	f.traces = append(f.traces, trace)
	return nil
}

func (f *fakeRepository) Traces(_ context.Context, query TraceQuery) ([]Trace, error) {
	f.traceReads = append(f.traceReads, query)
	if f.tracesErr != nil {
		return nil, f.tracesErr
	}
	var found []Trace
	for _, trace := range f.traces {
		if trace.OwnerID != query.OwnerID {
			continue
		}
		if query.Fingerprint != "" && trace.Fingerprint != query.Fingerprint {
			continue
		}
		if query.Surface != "" && trace.Surface != query.Surface {
			continue
		}
		if query.Kind != "" && trace.Kind != query.Kind {
			continue
		}
		if !query.Since.IsZero() && trace.CreatedAt.Before(query.Since) {
			continue
		}
		if !query.Until.IsZero() && trace.CreatedAt.After(query.Until) {
			continue
		}
		found = append(found, trace)
	}
	slices.SortFunc(found, func(left, right Trace) int {
		return cmp.Or(right.CreatedAt.Compare(left.CreatedAt), cmp.Compare(right.ID, left.ID))
	})
	if query.Limit > 0 && len(found) > query.Limit {
		found = found[:query.Limit]
	}
	return found, nil
}

func (f *fakeRepository) TracesOf(_ context.Context, factID string) ([]Trace, error) {
	if f.tracesOfErr != nil {
		return nil, f.tracesOfErr
	}
	var found []Trace
	for _, source := range f.sources {
		if source.FactID != factID {
			continue
		}
		for _, trace := range f.traces {
			if trace.ID == source.TraceID {
				found = append(found, trace)
			}
		}
	}
	slices.SortFunc(found, func(left, right Trace) int {
		return cmp.Or(left.CreatedAt.Compare(right.CreatedAt), cmp.Compare(left.ID, right.ID))
	})
	return found, nil
}

func (f *fakeRepository) ActiveFact(_ context.Context, ownerID int64, fingerprint string) (Fact, error) {
	if f.activeFactErr != nil {
		return Fact{}, f.activeFactErr
	}
	for _, fact := range f.facts {
		if fact.OwnerID == ownerID && fact.Fingerprint == fingerprint && fact.Status == StatusActive {
			return fact, nil
		}
	}
	return Fact{}, ErrNotFound
}

func (f *fakeRepository) Fact(_ context.Context, ownerID int64, factID string) (Fact, error) {
	if f.factErr != nil {
		return Fact{}, f.factErr
	}
	for _, fact := range f.facts {
		if fact.OwnerID == ownerID && fact.ID == factID {
			return fact, nil
		}
	}
	return Fact{}, ErrNotFound
}

func (f *fakeRepository) Facts(_ context.Context, query FactQuery) ([]Fact, error) {
	if f.factsErr != nil {
		return nil, f.factsErr
	}
	var found []Fact
	for _, fact := range f.facts {
		if fact.OwnerID != query.OwnerID {
			continue
		}
		if len(query.Statuses) > 0 && !slices.Contains(query.Statuses, fact.Status) {
			continue
		}
		if query.Surface != "" && fact.Surface != query.Surface {
			continue
		}
		if !query.Before.IsZero() && !fact.CreatedAt.Before(query.Before) {
			continue
		}
		found = append(found, fact)
	}
	slices.SortFunc(found, func(left, right Fact) int {
		changed := func(fact Fact) time.Time {
			if fact.UpdatedAt.IsZero() {
				return fact.CreatedAt
			}
			return fact.UpdatedAt
		}
		return cmp.Or(changed(right).Compare(changed(left)), cmp.Compare(right.ID, left.ID))
	})
	if query.Limit > 0 && len(found) > query.Limit {
		found = found[:query.Limit]
	}
	return found, nil
}

func (f *fakeRepository) DuplicateGroups(_ context.Context, ownerID int64) ([]FactGroup, error) {
	if f.duplicateGroupsErr != nil {
		return nil, f.duplicateGroupsErr
	}
	byFingerprint := map[string][]Fact{}
	var order []string
	for _, fact := range f.facts {
		if fact.OwnerID != ownerID || fact.Status != StatusActive {
			continue
		}
		if _, seen := byFingerprint[fact.Fingerprint]; !seen {
			order = append(order, fact.Fingerprint)
		}
		byFingerprint[fact.Fingerprint] = append(byFingerprint[fact.Fingerprint], fact)
	}
	slices.Sort(order)
	var groups []FactGroup
	for _, fingerprint := range order {
		facts := byFingerprint[fingerprint]
		if len(facts) < 2 {
			continue
		}
		slices.SortFunc(facts, func(left, right Fact) int {
			return cmp.Or(left.CreatedAt.Compare(right.CreatedAt), cmp.Compare(left.ID, right.ID))
		})
		groups = append(groups, FactGroup{Fingerprint: fingerprint, Facts: facts})
	}
	return groups, nil
}

func (f *fakeRepository) CreateFact(_ context.Context, fact Fact) error {
	if f.createFactErr != nil {
		return f.createFactErr
	}
	if slices.ContainsFunc(f.facts, func(held Fact) bool { return held.ID == fact.ID }) {
		return ErrDuplicate
	}
	f.facts = append(f.facts, fact)
	return nil
}

func (f *fakeRepository) SaveFact(_ context.Context, fact Fact, expected int32, now time.Time) (int32, error) {
	if f.saveFactErr != nil {
		return 0, f.saveFactErr
	}
	for index, held := range f.facts {
		if held.ID != fact.ID {
			continue
		}
		if held.Version != expected {
			return 0, ErrStaleState
		}
		fact.Version = expected + 1
		fact.UpdatedAt = now
		f.facts[index] = fact
		return fact.Version, nil
	}
	return 0, ErrNotFound
}

func (f *fakeRepository) AttachSource(_ context.Context, factID, traceID string, now time.Time) error {
	if f.attachSourceErr != nil {
		return f.attachSourceErr
	}
	source := FactSource{FactID: factID, TraceID: traceID, CreatedAt: now}
	if slices.Contains(f.sources, source) {
		return nil
	}
	for _, held := range f.sources {
		if held.FactID == factID && held.TraceID == traceID {
			return nil
		}
	}
	f.sources = append(f.sources, source)
	f.attached = append(f.attached, source)
	return nil
}

func (f *fakeRepository) PurgeFacts(_ context.Context, ownerID int64, factIDs []string) (int, error) {
	if f.purgeFactsErr != nil {
		return 0, f.purgeFactsErr
	}
	var kept []Fact
	removed := 0
	for _, fact := range f.facts {
		if fact.OwnerID == ownerID && slices.Contains(factIDs, fact.ID) {
			f.purged = append(f.purged, fact.ID)
			removed++
			continue
		}
		kept = append(kept, fact)
	}
	f.facts = kept
	// The evidence goes with the fact, the way the foreign key does it: a purge
	// here that left the rows behind would make the fake kinder than the engine.
	var surviving []FactSource
	for _, source := range f.sources {
		if slices.ContainsFunc(kept, func(fact Fact) bool { return fact.ID == source.FactID }) {
			surviving = append(surviving, source)
		}
	}
	f.sources = surviving
	return removed, nil
}

func (f *fakeRepository) MoveSources(_ context.Context, fromFactID, toFactID string) error {
	if f.moveSourcesErr != nil {
		return f.moveSourcesErr
	}
	var kept []FactSource
	for _, source := range f.sources {
		if source.FactID != fromFactID {
			kept = append(kept, source)
			continue
		}
		moved := FactSource{FactID: toFactID, TraceID: source.TraceID, CreatedAt: source.CreatedAt}
		f.moved = append(f.moved, moved)
		if !slices.Contains(kept, moved) {
			kept = append(kept, moved)
		}
	}
	f.sources = kept
	return nil
}

func (f *fakeRepository) AppendAudit(_ context.Context, audit Audit) error {
	if f.appendAuditErr != nil {
		return f.appendAuditErr
	}
	if slices.ContainsFunc(f.audits, func(held Audit) bool { return held.ID == audit.ID }) {
		return ErrDuplicate
	}
	f.audits = append(f.audits, audit)
	return nil
}

func (f *fakeRepository) Audits(_ context.Context, query AuditQuery) ([]Audit, error) {
	if f.auditsErr != nil {
		return nil, f.auditsErr
	}
	var found []Audit
	for _, audit := range f.audits {
		if audit.OwnerID != query.OwnerID {
			continue
		}
		if query.Action != "" && audit.Action != query.Action {
			continue
		}
		found = append(found, audit)
	}
	slices.SortFunc(found, func(left, right Audit) int {
		return cmp.Or(right.CreatedAt.Compare(left.CreatedAt), cmp.Compare(right.ID, left.ID))
	})
	if query.Limit > 0 && len(found) > query.Limit {
		found = found[:query.Limit]
	}
	return found, nil
}

// withTrace puts one observation in L1.
func (f *fakeRepository) withTrace(trace Trace) *fakeRepository {
	f.traces = append(f.traces, trace)
	return f
}

// withFact puts one fact in L2.
func (f *fakeRepository) withFact(fact Fact) *fakeRepository {
	f.facts = append(f.facts, fact)
	return f
}

// withSource puts one piece of evidence behind one fact.
func (f *fakeRepository) withSource(factID, traceID string) *fakeRepository {
	f.sources = append(f.sources, FactSource{FactID: factID, TraceID: traceID})
	return f
}

// evidenceOf reads the trace ids attached to one fact, which is what a merge is
// checked on.
func (f *fakeRepository) evidenceOf(factID string) []string {
	var ids []string
	for _, source := range f.sources {
		if source.FactID == factID {
			ids = append(ids, source.TraceID)
		}
	}
	slices.Sort(ids)
	return ids
}
