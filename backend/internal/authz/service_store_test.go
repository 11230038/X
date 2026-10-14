package authz

// The store the service cases run against: a map that remembers, and the three
// accounts a relationship is built out of.
//
// It is a double and not evidence. The cases that are about storage — the unique
// key, the CHECKs, the cascade — are in repository_db_test.go, because only the
// real database can answer them.

import (
	"context"
	"slices"
	"testing"
	"time"
)

// fakeStore is a store that remembers. It is deliberately literal: it applies
// the same two facts the real one does (a pair is unique, a link's permissions
// go with it) and nothing else, so that a case which passes here is passing for
// the rule's sake rather than the double's cleverness.
type fakeStore struct {
	presets map[int64]string
	names   map[int64]string
	links   map[string]Link
	grants  map[int64][]Assignment
	nextID  int
	fail    error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		presets: map[int64]string{},
		names:   map[int64]string{},
		links:   map[string]Link{},
		grants:  map[int64][]Assignment{},
	}
}

func (f *fakeStore) failWith(err error) { f.fail = err }

func (f *fakeStore) Preset(_ context.Context, userID int64) (string, error) {
	if f.fail != nil {
		return "", f.fail
	}
	preset, ok := f.presets[userID]
	if !ok {
		return "", ErrUnknownUser
	}
	return preset, nil
}

func (f *fakeStore) SavePreset(_ context.Context, userID int64, preset string) error {
	if f.fail != nil {
		return f.fail
	}
	if _, ok := f.presets[userID]; !ok {
		return ErrUnknownUser
	}
	f.presets[userID] = preset
	return nil
}

func (f *fakeStore) Accounts(_ context.Context) ([]Account, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	ids := make([]int64, 0, len(f.presets))
	for id := range f.presets {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	accounts := make([]Account, 0, len(ids))
	for _, id := range ids {
		accounts = append(accounts, Account{
			UserID:   id,
			Username: f.names[id],
			Role:     RoleUser,
			Preset:   f.presets[id],
		})
	}
	return accounts, nil
}

func (f *fakeStore) Link(_ context.Context, guardianID, learnerID int64) (Link, error) {
	if f.fail != nil {
		return Link{}, f.fail
	}
	for _, link := range f.links {
		if link.GuardianID == guardianID && link.LearnerID == learnerID {
			return link, nil
		}
	}
	return Link{}, ErrLinkNotFound
}

func (f *fakeStore) LinkByID(_ context.Context, linkID string) (Link, error) {
	if f.fail != nil {
		return Link{}, f.fail
	}
	if link, ok := f.links[linkID]; ok {
		return link, nil
	}
	return Link{}, ErrLinkNotFound
}

func (f *fakeStore) Links(_ context.Context, guardianID int64) ([]Link, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	var links []Link
	for _, link := range f.links {
		if link.GuardianID == guardianID {
			links = append(links, link)
		}
	}
	return links, nil
}

func (f *fakeStore) AllLinks(_ context.Context) ([]Link, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	links := make([]Link, 0, len(f.links))
	for _, link := range f.links {
		links = append(links, link)
	}
	slices.SortFunc(links, func(a, b Link) int {
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})
	return links, nil
}

func (f *fakeStore) SaveLink(_ context.Context, link Link) error {
	if f.fail != nil {
		return f.fail
	}
	for _, existing := range f.links {
		if existing.GuardianID == link.GuardianID && existing.LearnerID == link.LearnerID {
			return ErrLinkExists
		}
	}
	f.links[link.ID] = link
	return nil
}

func (f *fakeStore) DeleteLink(_ context.Context, linkID string) error {
	if f.fail != nil {
		return f.fail
	}
	delete(f.links, linkID)
	return nil
}

func (f *fakeStore) SavePermission(_ context.Context, linkID string, permission Permission, grantedBy int64) error {
	if f.fail != nil {
		return f.fail
	}
	link, ok := f.links[linkID]
	if !ok {
		return ErrLinkNotFound
	}
	if link.Holds(permission) {
		return nil
	}
	link.Permissions = append(link.Permissions, permission)
	f.links[linkID] = link
	return nil
}

func (f *fakeStore) DeletePermission(_ context.Context, linkID string, permission Permission) error {
	if f.fail != nil {
		return f.fail
	}
	link, ok := f.links[linkID]
	if !ok {
		return ErrLinkNotFound
	}
	kept := make([]Permission, 0, len(link.Permissions))
	for _, held := range link.Permissions {
		if held != permission {
			kept = append(kept, held)
		}
	}
	link.Permissions = kept
	f.links[linkID] = link
	return nil
}

func (f *fakeStore) Assignments(_ context.Context, userID int64) ([]Assignment, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	return append([]Assignment(nil), f.grants[userID]...), nil
}

func (f *fakeStore) SaveAssignment(_ context.Context, assignment Assignment) error {
	if f.fail != nil {
		return f.fail
	}
	f.grants[assignment.UserID] = replaceAssignment(f.grants[assignment.UserID], assignment)
	return nil
}

func (f *fakeStore) DeleteAssignment(_ context.Context, userID int64, item Item) error {
	if f.fail != nil {
		return f.fail
	}
	kept := make([]Assignment, 0, len(f.grants[userID]))
	for _, assignment := range f.grants[userID] {
		if assignment.Item != item {
			kept = append(kept, assignment)
		}
	}
	f.grants[userID] = kept
	return nil
}

const (
	adminID    int64 = 1
	parentID   int64 = 2
	childID    int64 = 3
	otherID    int64 = 4
	strangerID int64 = 5
)

func newService(t *testing.T, store *fakeStore) *Service {
	t.Helper()
	store.presets[adminID] = PresetStandard
	store.presets[parentID] = PresetStandard
	store.presets[childID] = PresetLearner
	store.presets[otherID] = PresetStandard
	store.presets[strangerID] = PresetLearner
	service, err := NewService(store, New(NewBaseline(DefaultFloor(), nil, nil)))
	if err != nil {
		t.Fatalf("build the service: %v", err)
	}
	service.random = func(buffer []byte) (int, error) {
		store.nextID++
		for index := range buffer {
			buffer[index] = byte(store.nextID + index)
		}
		return len(buffer), nil
	}
	service.now = func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }
	return service
}

func admin() Caller { return Caller{UserID: adminID, Role: RoleAdmin} }
