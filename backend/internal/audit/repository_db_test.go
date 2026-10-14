package audit

// The storage half of the audit, checked against a real seekdb.
//
// What is pinned here is what a double cannot be wrong about: that a row
// round-trips every column, that a missing account is NULL rather than zero,
// that a moment is compared inclusively at one end and exclusively at the
// other, and that the two CHECK constraints refuse what nobody would notice was
// missing. The double in service_test.go can restate any of this and agree with
// itself; only the database can refuse.
//
// One database per package, so nothing here calls t.Parallel.

import (
	"context"
	"testing"
	"time"

	"backend/internal/data"
	"backend/internal/data/models"
	"backend/internal/data/testdb"
)

// atSecond is a whole second on a fixed day. creat_time is a DATETIME, which
// keeps whole seconds, so a test that let the clock pick one would be comparing
// against a rounded value.
func atSecond(second int) time.Time {
	return time.Date(2026, 10, 1, 12, 0, second, 0, time.UTC)
}

func newDBStore(t *testing.T) (*GORMStore, *data.Resource) {
	t.Helper()
	resource := testdb.Open(t, "audit")
	// Nothing points at audit_events and it points at nothing: an entry has to
	// outlive the account it names, which is why 00016 gives actor_id no
	// foreign key. So this table is both the parent and the child.
	testdb.Reset(t, resource, "audit_events")
	return NewGORMStore(resource.GORM()), resource
}

// entry is an event with the moment already chosen, so that a fixture is never
// racing the clock.
func entry(action Action, at time.Time) Event {
	return Event{
		Actor:     Actor{ID: 7, Name: "wei", Role: "user"},
		Action:    action,
		Target:    Ref{Kind: TargetSession, ID: "ses_abc"},
		Result:    ResultOK,
		CreatedAt: at,
	}
}

func appendOne(t *testing.T, store *GORMStore, event Event) int64 {
	t.Helper()
	id, err := store.Append(context.Background(), event)
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	return id
}

func page(t *testing.T, store *GORMStore, filter Filter) []Event {
	t.Helper()
	events, err := store.Page(context.Background(), filter)
	if err != nil {
		t.Fatalf("Page() error = %v", err)
	}
	return events
}

func TestDBAppendRoundTripsEveryColumn(t *testing.T) {
	store, _ := newDBStore(t)
	event := entry(ActionReportsViewed, atSecond(0))
	event.SubjectID = 42
	event.RequestID = "req_0123456789"
	event.Detail = Detail{
		Reason: "permission_held",
		Layer:  "guardian",
		Counts: map[string]int{"read": 2},
		Items:  []Ref{{Kind: TargetAccount, ID: "42"}},
	}

	id := appendOne(t, store, event)
	if id == 0 {
		t.Fatal("Append() returned no id")
	}
	events := page(t, store, Filter{})
	if len(events) != 1 {
		t.Fatalf("Page() returned %d entries, want 1", len(events))
	}
	got := events[0]
	if got.ID != id {
		t.Errorf("ID = %d, want %d", got.ID, id)
	}
	if got.Actor.ID != 7 || got.Actor.Name != "wei" || got.Actor.Role != "user" {
		t.Errorf("Actor = %+v, want the account that was named", got.Actor)
	}
	if got.SubjectID != 42 {
		t.Errorf("SubjectID = %d, want 42", got.SubjectID)
	}
	if got.Action != ActionReportsViewed {
		t.Errorf("Action = %q, want %q", got.Action, ActionReportsViewed)
	}
	if got.Target.Kind != TargetSession || got.Target.ID != "ses_abc" {
		t.Errorf("Target = %+v, want the pointer that was written", got.Target)
	}
	if got.Result != ResultOK {
		t.Errorf("Result = %q, want %q", got.Result, ResultOK)
	}
	if got.RequestID != "req_0123456789" {
		t.Errorf("RequestID = %q, want the one that was written", got.RequestID)
	}
	if !got.CreatedAt.Equal(atSecond(0)) {
		t.Errorf("CreatedAt = %s, want %s", got.CreatedAt, atSecond(0))
	}
	if got.Detail.Reason != "permission_held" || got.Detail.Layer != "guardian" {
		t.Errorf("Detail = %+v, want the reason and layer back", got.Detail)
	}
	if got.Detail.Counts["read"] != 2 {
		t.Errorf("Detail.Counts = %v, want read=2", got.Detail.Counts)
	}
	if len(got.Detail.Items) != 1 || got.Detail.Items[0].ID != "42" {
		t.Errorf("Detail.Items = %+v, want the account it was about", got.Detail.Items)
	}
}

// An entry with no account is an entry about an account that has since been
// deleted, and it keeps the name it had: the id is NULL and the snapshot is
// not. This is the whole reason 00016 leaves actor_id nullable and gives it no
// foreign key, and it is a claim only the database can settle.
func TestDBAppendKeepsTheNameWhenThereIsNoAccount(t *testing.T) {
	store, resource := newDBStore(t)
	event := entry(ActionAccountCreated, atSecond(0))
	event.Actor = Actor{ID: 0, Name: "wei", Role: "user"}
	event.SubjectID = 0

	appendOne(t, store, event)

	var row models.AuditEvent
	if err := resource.GORM().First(&row).Error; err != nil {
		t.Fatalf("read the row back: %v", err)
	}
	if row.ActorID != nil {
		t.Errorf("actor_id = %d, want NULL for an account that is not there", *row.ActorID)
	}
	if row.SubjectID != nil {
		t.Errorf("subject_id = %d, want NULL for an entry with no subject", *row.SubjectID)
	}
	if row.ActorName != "wei" {
		t.Errorf("actor_name = %q, want the snapshot kept", row.ActorName)
	}
	events := page(t, store, Filter{})
	if events[0].Actor.ID != 0 || events[0].Actor.Name != "wei" {
		t.Errorf("Actor = %+v, want no id and the name", events[0].Actor)
	}
}

// The column is NULL when there is nothing to say, and not `{}`. The two read
// the same and count differently, and "how many entries carry a reason" has to
// be a question about the audit rather than about the JSON type.
func TestDBAbsentDetailIsNullAndAPresentOneIsJSON(t *testing.T) {
	store, resource := newDBStore(t)
	appendOne(t, store, entry(ActionSessionClaimed, atSecond(0)))

	detailed := entry(ActionPresetChanged, atSecond(1))
	detailed.Detail = Detail{Reason: "administrator", Layer: "role"}
	appendOne(t, store, detailed)

	var rows []models.AuditEvent
	if err := resource.GORM().Order("audit_id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("read the rows back: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("read %d rows, want 2", len(rows))
	}
	if len(rows[0].Detail) != 0 {
		t.Errorf("detail_json = %s, want NULL for an entry with nothing to say", rows[0].Detail)
	}
	if len(rows[1].Detail) == 0 {
		t.Fatal("detail_json = NULL for an entry that says why")
	}
	events := page(t, store, Filter{})
	for _, got := range events {
		if got.Action == ActionSessionClaimed && !got.Detail.Empty() {
			t.Errorf("Detail = %+v, want nothing read back for the entry that had nothing", got.Detail)
		}
		if got.Action == ActionPresetChanged && got.Detail.Reason != "administrator" {
			t.Errorf("Detail = %+v, want the reason read back", got.Detail)
		}
	}
}

// Two entries written in the same second come back in the order they were
// written, and two reads agree about it. creat_time alone would leave that to
// the storage engine, which is a page that can arrive in a different order
// each time it is asked for.
func TestDBPageIsNewestFirstAndStableWithinASecond(t *testing.T) {
	store, _ := newDBStore(t)
	first := appendOne(t, store, entry(ActionSessionClaimed, atSecond(0)))
	second := appendOne(t, store, entry(ActionSessionDeleted, atSecond(0)))
	third := appendOne(t, store, entry(ActionFileDeleted, atSecond(0)))

	want := []int64{third, second, first}
	for attempt := 0; attempt < 2; attempt++ {
		events := page(t, store, Filter{})
		if len(events) != len(want) {
			t.Fatalf("Page() returned %d entries, want %d", len(events), len(want))
		}
		for index, id := range want {
			if events[index].ID != id {
				t.Fatalf("Page() attempt %d = %v, want newest first by id %v",
					attempt, ids(events), want)
			}
		}
	}
}

// A minute later sorts above everything in the second before, which is the
// other half of the ordering: the tie-break must not outrank the clock.
func TestDBPagePutsTheLaterSecondFirst(t *testing.T) {
	store, _ := newDBStore(t)
	older := appendOne(t, store, entry(ActionSessionClaimed, atSecond(0)))
	newer := appendOne(t, store, entry(ActionSessionDeleted, atSecond(59)))

	events := page(t, store, Filter{})
	if len(events) != 2 || events[0].ID != newer || events[1].ID != older {
		t.Fatalf("Page() = %v, want [%d %d]", ids(events), newer, older)
	}
}

func TestDBPageNarrowsByWhoDidItAndWhoItWasAbout(t *testing.T) {
	store, _ := newDBStore(t)
	bySeven := entry(ActionReportsViewed, atSecond(0))
	bySeven.SubjectID = 42
	appendOne(t, store, bySeven)

	byNine := entry(ActionReportsViewed, atSecond(1))
	byNine.Actor = Actor{ID: 9, Name: "li", Role: "user"}
	byNine.SubjectID = 43
	appendOne(t, store, byNine)

	if got := page(t, store, Filter{ActorID: 9}); len(got) != 1 || got[0].Actor.Name != "li" {
		t.Errorf("Page(actor_id=9) = %v, want the one account's entry", ids(got))
	}
	if got := page(t, store, Filter{SubjectID: 42}); len(got) != 1 || got[0].Actor.ID != 7 {
		t.Errorf("Page(subject_id=42) = %v, want the entry about that account", ids(got))
	}
	// An actor and a subject are two different questions: asking for both is
	// asking for the entries where that account did it to that account.
	if got := page(t, store, Filter{ActorID: 9, SubjectID: 42}); len(got) != 0 {
		t.Errorf("Page(actor_id=9, subject_id=42) = %v, want nothing", ids(got))
	}
	if got := page(t, store, Filter{SubjectID: 43}); len(got) != 1 || got[0].Actor.ID != 9 {
		t.Errorf("Page(subject_id=43) = %v, want the entry by nine", ids(got))
	}
}

func TestDBPageNarrowsByAction(t *testing.T) {
	store, _ := newDBStore(t)
	appendOne(t, store, entry(ActionSessionClaimed, atSecond(0)))
	appendOne(t, store, entry(ActionSessionDeleted, atSecond(1)))

	got := page(t, store, Filter{Action: ActionSessionDeleted})
	if len(got) != 1 || got[0].Action != ActionSessionDeleted {
		t.Fatalf("Page(action=%s) = %v, want one entry", ActionSessionDeleted, ids(got))
	}
}

func TestDBPageNarrowsByWhatItPointedAt(t *testing.T) {
	store, _ := newDBStore(t)
	one := entry(ActionSessionClaimed, atSecond(0))
	one.Target = Ref{Kind: TargetSession, ID: "ses_abc"}
	appendOne(t, store, one)

	other := entry(ActionFileDeleted, atSecond(1))
	other.Target = Ref{Kind: TargetLibraryFile, ID: "lib_abc"}
	appendOne(t, store, other)

	if got := page(t, store, Filter{TargetKind: TargetLibraryFile}); len(got) != 1 {
		t.Errorf("Page(target_kind=library_file) = %v, want the one file", ids(got))
	}
	if got := page(t, store, Filter{TargetID: "ses_abc"}); len(got) != 1 {
		t.Errorf("Page(target_id=ses_abc) = %v, want the one session", ids(got))
	}
	// A kind and an id are asked together as often as apart, and asking for a
	// pair that never met has to answer nothing rather than one of the two.
	if got := page(t, store, Filter{TargetKind: TargetSession, TargetID: "lib_abc"}); len(got) != 0 {
		t.Errorf("Page(a kind and another kind's id) = %v, want nothing", ids(got))
	}
}

// A window is inclusive at the start and exclusive at the end, and the
// difference is not cosmetic: two windows that both included their end would
// show the same entry twice to an operator paging through a day at a time.
func TestDBPageSinceIsInclusiveAndUntilIsExclusive(t *testing.T) {
	store, _ := newDBStore(t)
	boundary := appendOne(t, store, entry(ActionSessionClaimed, atSecond(0)))
	inside := appendOne(t, store, entry(ActionSessionDeleted, atSecond(10)))
	after := appendOne(t, store, entry(ActionFileDeleted, atSecond(20)))

	got := page(t, store, Filter{Since: atSecond(0), Until: atSecond(20)})
	if len(got) != 2 || got[0].ID != inside || got[1].ID != boundary {
		t.Fatalf("Page([0,20)) = %v, want [%d %d]", ids(got), inside, boundary)
	}
	if contains(ids(got), after) {
		t.Error("the entry outside the window was in it")
	}
	if got := page(t, store, Filter{Since: atSecond(20)}); len(got) != 1 || got[0].ID != after {
		t.Errorf("Page(since=20) = %v, want the last entry", ids(got))
	}
	if got := page(t, store, Filter{Until: atSecond(0)}); len(got) != 0 {
		t.Errorf("Page(until=0) = %v, want nothing: the moment is excluded", ids(got))
	}
}

func TestDBPageHonoursTheLimit(t *testing.T) {
	store, _ := newDBStore(t)
	for second := 0; second < 5; second++ {
		appendOne(t, store, entry(ActionSessionClaimed, atSecond(second)))
	}

	got := page(t, store, Filter{Limit: 2})
	if len(got) != 2 {
		t.Fatalf("Page(limit=2) returned %d entries, want 2", len(got))
	}
	if got[0].CreatedAt.Before(got[1].CreatedAt) {
		t.Error("the limited page is not newest first")
	}
}

// Prune is by age and by nothing else, and the boundary is strict: the moment
// it is given is the first moment that is kept.
func TestDBPruneRemovesOnlyWhatIsOlderThanTheMoment(t *testing.T) {
	store, _ := newDBStore(t)
	old := appendOne(t, store, entry(ActionSessionClaimed, atSecond(0)))
	kept := appendOne(t, store, entry(ActionSessionDeleted, atSecond(10)))
	recent := appendOne(t, store, entry(ActionFileDeleted, atSecond(20)))

	removed, err := store.Prune(context.Background(), atSecond(10))
	if err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	if removed != 1 {
		t.Errorf("Prune() removed %d entries, want 1", removed)
	}
	remaining := ids(page(t, store, Filter{}))
	if len(remaining) != 2 || remaining[0] != recent || remaining[1] != kept {
		t.Fatalf("after Prune() the table holds %v, want [%d %d]", remaining, recent, kept)
	}
	if contains(remaining, old) {
		t.Errorf("the entry older than the moment is still there: %v", remaining)
	}
}

func TestDBPruneOnAQuietInstanceIsNotAFailure(t *testing.T) {
	store, _ := newDBStore(t)

	removed, err := store.Prune(context.Background(), atSecond(0))
	if err != nil {
		t.Fatalf("Prune() error = %v, want nil", err)
	}
	if removed != 0 {
		t.Errorf("Prune() removed %d entries from an empty table", removed)
	}
}

// A window is a window and not an editing tool: there is no way to name the
// rows it removes, and asking for a moment before everything keeps everything.
func TestDBPruneBeforeEverythingRemovesNothing(t *testing.T) {
	store, _ := newDBStore(t)
	appendOne(t, store, entry(ActionSessionClaimed, atSecond(0)))

	removed, err := store.Prune(context.Background(), atSecond(0).Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	if removed != 0 || len(page(t, store, Filter{})) != 1 {
		t.Errorf("Prune() removed %d entries, want none", removed)
	}
}

// The two CHECK constraints, reached the only way they can be: past the store,
// with a row written by hand. They are the last line rather than the first —
// validate() refuses both of these — and the point of proving them is that a
// writer who forgot to validate would be told so by the database instead of
// putting an unreadable row in the record.
func TestDBCheckRefusesWhatValidateWouldHave(t *testing.T) {
	_, resource := newDBStore(t)

	badResult := models.AuditEvent{
		Action: string(ActionSessionClaimed), TargetKind: string(TargetSession),
		Result: "maybe", CreatedAt: atSecond(0),
	}
	if err := resource.GORM().Create(&badResult).Error; err == nil {
		t.Error("the table accepted a result outside ok/denied/failed")
	}

	blankAction := models.AuditEvent{
		Action: "   ", TargetKind: string(TargetSession),
		Result: string(ResultOK), CreatedAt: atSecond(0),
	}
	if err := resource.GORM().Create(&blankAction).Error; err == nil {
		t.Error("the table accepted a blank action")
	}
}

// Nothing references audit_events and it references nothing, so an entry may
// name an account the users table has never held. That is what lets a record
// outlive the account it is about, and it is the property an added foreign key
// would take away without anything in Go changing.
func TestDBAnEntryMayNameAnAccountThatIsNotThere(t *testing.T) {
	store, _ := newDBStore(t)
	event := entry(ActionSessionClaimed, atSecond(0))
	event.Actor = Actor{ID: 9999, Name: "gone", Role: "user"}

	appendOne(t, store, event)

	got := page(t, store, Filter{ActorID: 9999})
	if len(got) != 1 || got[0].Actor.Name != "gone" {
		t.Fatalf("Page(actor_id=9999) = %v, want the entry naming an account nobody holds", ids(got))
	}
}

// ids is the entry ids in the order a page returned them, which is what an
// ordering assertion is actually about.
func ids(events []Event) []int64 {
	listed := make([]int64, 0, len(events))
	for _, event := range events {
		listed = append(listed, event.ID)
	}
	return listed
}
