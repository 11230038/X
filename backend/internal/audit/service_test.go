package audit

// The rules the service holds over a store, checked against a double.
//
// The double is the right instrument here and the database is not: what these
// tests are about is what the service *refuses* — a page above the cap, an
// action this build does not record, a window computed from a clock — and half
// of them are about a store that fails, which is the one thing a real seekdb
// will not do on request. What the database is for is the shape of a row, and
// that is in repository_db_test.go.

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeStore records what it was asked and answers with what it was told to.
type fakeStore struct {
	appended []Event
	nextID   int64
	filter   Filter
	pruned   time.Time
	prunedN  int64
	events   []Event
	err      error
}

func (f *fakeStore) Append(_ context.Context, event Event) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.nextID++
	f.appended = append(f.appended, event)
	return f.nextID, nil
}

func (f *fakeStore) Page(_ context.Context, filter Filter) ([]Event, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.filter = filter
	return f.events, nil
}

func (f *fakeStore) Prune(_ context.Context, before time.Time) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.pruned = before
	return f.prunedN, nil
}

var _ Store = (*fakeStore)(nil)

// aClock is a fixed moment, so that a stamped entry has a time this file chose
// rather than one the machine happened to be at.
func aClock() time.Time { return time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC) }

func newTestService(t *testing.T, store Store, options Options) *Service {
	t.Helper()
	if options.MaxPageSize == 0 {
		options.MaxPageSize = 100
	}
	if options.RetentionDays == 0 {
		options.RetentionDays = 180
	}
	if options.Now == nil {
		options.Now = aClock
	}
	service, err := NewService(store, options)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

func TestNewServiceRefusesWhatItCannotWorkWith(t *testing.T) {
	cases := []struct {
		name    string
		store   Store
		options Options
	}{
		{name: "no store", store: nil, options: Options{MaxPageSize: 10, RetentionDays: 10}},
		{name: "no page cap", store: &fakeStore{}, options: Options{RetentionDays: 10}},
		{name: "a negative page cap", store: &fakeStore{}, options: Options{MaxPageSize: -1, RetentionDays: 10}},
		{name: "no window", store: &fakeStore{}, options: Options{MaxPageSize: 10}},
		{name: "a negative window", store: &fakeStore{}, options: Options{MaxPageSize: 10, RetentionDays: -1}},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewService(tt.store, tt.options); err == nil {
				t.Fatal("NewService() error = nil, want an error")
			}
		})
	}
}

func TestRecordStampsTheMomentItWasCalled(t *testing.T) {
	store := &fakeStore{}
	service := newTestService(t, store, Options{})

	if err := service.Record(context.Background(), anEvent()); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if len(store.appended) != 1 {
		t.Fatalf("Append() called %d times, want once", len(store.appended))
	}
	if got := store.appended[0].CreatedAt; !got.Equal(aClock()) {
		t.Errorf("CreatedAt = %s, want %s", got, aClock())
	}
}

// The moment is stamped once for the whole action and not per entry: an
// old-value / new-value pair stamped separately can straddle a second boundary
// and read as two unrelated events.
func TestRecordKeepsAMomentItWasGiven(t *testing.T) {
	store := &fakeStore{}
	service := newTestService(t, store, Options{})
	written := time.Date(2020, 3, 4, 5, 6, 7, 0, time.UTC)

	event := anEvent()
	event.CreatedAt = written
	if err := service.Record(context.Background(), event); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if got := store.appended[0].CreatedAt; !got.Equal(written) {
		t.Errorf("CreatedAt = %s, want the moment it was given: %s", got, written)
	}
}

// The service refuses before the store sees anything. A store that had to
// refuse would be a store that had already been handed an entry.
func TestRecordRefusesAnEntryItMayNotWrite(t *testing.T) {
	store := &fakeStore{}
	service := newTestService(t, store, Options{})

	event := anEvent()
	event.Action = "something.else"
	err := service.Record(context.Background(), event)

	if !errors.Is(err, ErrUnknownAction) {
		t.Fatalf("Record() error = %v, want %v", err, ErrUnknownAction)
	}
	if len(store.appended) != 0 {
		t.Fatal("the store was written to with an entry the service had refused")
	}
}

func TestRecordReportsAStoreThatFailed(t *testing.T) {
	store := &fakeStore{err: errors.New("the disk is on fire")}
	service := newTestService(t, store, Options{})

	err := service.Record(context.Background(), anEvent())
	if err == nil || !errors.Is(err, store.err) {
		t.Fatalf("Record() error = %v, want the store's own error", err)
	}
}

// A service built as a zero value is what a handler holds when a deployment
// wired nothing. It answers with a wiring mistake rather than a nil panic, and
// the two are different sentences because only one of them a retry could fix.
func TestAnUnbuiltServiceAnswersThatItIsNotConfigured(t *testing.T) {
	var service *Service
	ctx := context.Background()

	if err := service.Record(ctx, anEvent()); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("Record() error = %v, want %v", err, ErrNotConfigured)
	}
	if _, err := service.List(ctx, Filter{}); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("List() error = %v, want %v", err, ErrNotConfigured)
	}
	if _, err := service.Prune(ctx); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("Prune() error = %v, want %v", err, ErrNotConfigured)
	}
}

func TestListRefusesAPageLargerThanTheCap(t *testing.T) {
	store := &fakeStore{}
	service := newTestService(t, store, Options{MaxPageSize: 100})

	_, err := service.List(context.Background(), Filter{Limit: 101})
	if !errors.Is(err, ErrPageTooLarge) {
		t.Fatalf("List() error = %v, want %v", err, ErrPageTooLarge)
	}
	if store.filter != (Filter{}) {
		t.Error("the store was asked for a page the service had refused")
	}
}

// A read with no limit asked for everything the deployment serves, which is the
// cap. It is a cap and not a default: the number is the deployment's, and there
// is no second number the code invented.
func TestListTreatsNoLimitAsTheCap(t *testing.T) {
	for _, limit := range []int{0, -1} {
		store := &fakeStore{}
		service := newTestService(t, store, Options{MaxPageSize: 100})

		if _, err := service.List(context.Background(), Filter{Limit: limit}); err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if store.filter.Limit != 100 {
			t.Errorf("the store was asked for %d entries, want the cap of 100", store.filter.Limit)
		}
	}
}

func TestListPassesTheCapThrough(t *testing.T) {
	store := &fakeStore{}
	service := newTestService(t, store, Options{MaxPageSize: 100})

	if _, err := service.List(context.Background(), Filter{Limit: 10}); err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if store.filter.Limit != 10 {
		t.Errorf("the store was asked for %d entries, want 10", store.filter.Limit)
	}
}

func TestListRefusesANarrowingThisBuildCannotAnswer(t *testing.T) {
	cases := []struct {
		name   string
		filter Filter
		want   error
	}{
		{name: "an action nobody wrote", filter: Filter{Action: "authorization.denied"}, want: ErrUnknownAction},
		{name: "a kind nobody wrote", filter: Filter{TargetKind: "prompt"}, want: ErrUnknownTargetKind},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{}
			service := newTestService(t, store, Options{})

			_, err := service.List(context.Background(), tt.filter)
			if !errors.Is(err, tt.want) {
				t.Fatalf("List() error = %v, want %v", err, tt.want)
			}
			if store.filter != (Filter{}) {
				t.Error("the store was asked a question the service had refused")
			}
		})
	}
}

// An empty narrowing is not an unknown one: "every action" is what a filter
// with no action means, and refusing it would make the whole stream unreadable.
func TestListAcceptsAnEmptyNarrowing(t *testing.T) {
	store := &fakeStore{}
	service := newTestService(t, store, Options{})

	if _, err := service.List(context.Background(), Filter{}); err != nil {
		t.Fatalf("List() error = %v", err)
	}
}

func TestListReturnsWhatTheStoreAnswered(t *testing.T) {
	store := &fakeStore{events: []Event{anEvent(), anEvent()}}
	service := newTestService(t, store, Options{})

	events, err := service.List(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("List() returned %d entries, want 2", len(events))
	}
}

func TestPruneRemovesEverythingOlderThanTheWindow(t *testing.T) {
	store := &fakeStore{prunedN: 3}
	service := newTestService(t, store, Options{RetentionDays: 30})

	removed, err := service.Prune(context.Background())
	if err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	if removed != 3 {
		t.Errorf("Prune() = %d, want 3", removed)
	}
	want := aClock().AddDate(0, 0, -30)
	if !store.pruned.Equal(want) {
		t.Errorf("Prune() cut at %s, want %s", store.pruned, want)
	}
}

// A window that removed nothing is not an error. A quiet instance is what this
// is supposed to look like, and making silence a failure is how an operator
// learns to ignore it.
func TestPruneSucceedsWhenThereIsNothingToRemove(t *testing.T) {
	store := &fakeStore{}
	service := newTestService(t, store, Options{})

	removed, err := service.Prune(context.Background())
	if err != nil {
		t.Fatalf("Prune() error = %v, want nil", err)
	}
	if removed != 0 {
		t.Errorf("Prune() = %d, want 0", removed)
	}
}

func TestMaxPageSizeIsTheConfiguredCap(t *testing.T) {
	service := newTestService(t, &fakeStore{}, Options{MaxPageSize: 42})

	if got := service.MaxPageSize(); got != 42 {
		t.Errorf("MaxPageSize() = %d, want 42", got)
	}
}
