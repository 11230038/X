package mastery

// Arriving, changing the tree, and listing — against a real seekdb.
//
// The reads and writes here are the service's, not the adapter's: whether
// arriving creates a path or finds one, whether a change honours the version the
// caller read, and whether a listing reflects the cache the writes maintain. All
// three are decided by the SQL underneath, which is why they are here and not
// over a double.

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDBArrivingWithNothingCreatesThePathTheConversationNames(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	ctx := context.Background()

	arrival, err := h.service.Arrive(ctx, ArriveRequest{Actor: actor, Goal: "  学会闭包  "}, h.caller)
	if err != nil {
		t.Fatalf("arrive: %v", err)
	}
	// The goal is stored trimmed and the tree starts empty: a path is a goal
	// before it is anything else, and the turn that follows draws the tree one
	// call at a time.
	if len(arrival.Path.Tree.Modules) != 0 {
		t.Errorf("a path created from a goal arrived with %d modules", len(arrival.Path.Tree.Modules))
	}
	// This arrival is the one that opened the path, which is what lets the turn
	// replace the goal with a summary once it has an answer. Only the opening
	// turn gets to do that; a later arrival must not.
	if !arrival.Created {
		t.Error("the first arrival did not report opening the path")
	}
	view, err := h.service.Load(ctx, arrival.Path.ID, h.caller)
	if err != nil {
		t.Fatalf("read the path back: %v", err)
	}
	if view.Meta.Goal != "学会闭包" {
		t.Errorf("the goal is stored as %q, want it trimmed", view.Meta.Goal)
	}
	if view.Meta.Status != StatusActive || view.Meta.Emoji != defaultEmoji {
		t.Errorf("the metadata is %+v", view.Meta)
	}
	// The version is one and not zero: a path that has been created has been
	// written once, and zero is what a row from an older deployment looks like.
	if view.Path.Version != 1 {
		t.Errorf("a new path is at version %d, want 1", view.Path.Version)
	}
	// The first turn to arrive holds the path, and it did not take it from
	// anybody — which is the difference the arrival reports.
	if !arrival.Ours || arrival.TookOver {
		t.Errorf("the arrival is ours=%v tookOver=%v", arrival.Ours, arrival.TookOver)
	}
	if arrival.Lease.TurnID != "t_1" || !arrival.ExpiresAt.After(arrival.Lease.AcquiredAt) {
		t.Errorf("the lease is %+v expiring %s", arrival.Lease, arrival.ExpiresAt)
	}
	// One transaction wrote the path, its metadata, its history and its link to
	// the conversation, and the link is the only one of the four the arrival
	// itself cannot be read off.
	pathID, err := h.repository.PathOfSession(ctx, "s_1", h.caller)
	if err != nil {
		t.Fatalf("read the path of the conversation: %v", err)
	}
	if pathID != arrival.Path.ID {
		t.Errorf("the conversation is on path %s, want %s", pathID, arrival.Path.ID)
	}
	if got := h.countRows(t, "mastery_topic_meta"); got != 1 {
		t.Errorf("%d metadata rows are stored, want one", got)
	}
}

func TestDBArrivingAgainComesBackToTheSamePath(t *testing.T) {
	h := newHarness(t)
	first := h.conversation(t, "s_1", "t_1")
	ctx := context.Background()

	opened, err := h.service.Arrive(ctx, ArriveRequest{Actor: first, Goal: "学会闭包"}, h.caller)
	if err != nil {
		t.Fatalf("arrive: %v", err)
	}
	// Two hours, so that the first turn's lease has run out rather than being
	// exactly at its limit — the comparison is open at the end, and a lease
	// exactly an hour old is still live.
	h.clock.advance(2 * time.Hour)

	// A second turn in the same conversation is the ordinary case — a person
	// sending a second message — and it lands on the path the conversation is
	// already on. A goal of its own does not start a second one: the goal names a
	// path only when there is no path to name.
	second := h.turn(t, "t_2", "s_1")
	again, err := h.service.Arrive(ctx, ArriveRequest{Actor: second, Goal: "学点别的"}, h.caller)
	if err != nil {
		t.Fatalf("arrive again: %v", err)
	}
	if again.Path.ID != opened.Path.ID {
		t.Errorf("the second turn arrived on path %s, want %s", again.Path.ID, opened.Path.ID)
	}
	// The path was opened by the first turn, not this one. The second turn must
	// not rename what the first one named or the learner renamed by hand.
	if !opened.Created || again.Created {
		t.Errorf("created: first=%v second=%v, want true then false", opened.Created, again.Created)
	}
	if got := h.countRows(t, "mastery_paths"); got != 1 {
		t.Errorf("%d paths are stored, want the one the conversation already had", got)
	}
	// The second turn is the one holding it now, and it took it over because the
	// first turn's lease ran out while it was thinking — one hour, not two, so
	// the boundary is not being leant on here.
	if !again.Ours || !again.TookOver {
		t.Errorf("the second arrival is ours=%v tookOver=%v", again.Ours, again.TookOver)
	}
	if again.Lease.TurnID != "t_2" {
		t.Errorf("the path is held by %s, want the second turn", again.Lease.TurnID)
	}
	seen, err := h.repository.SeenAt(ctx, opened.Path.ID, "s_1", h.caller)
	if err != nil {
		t.Fatalf("read when the conversation last saw the path: %v", err)
	}
	// The link still says the hour the path was created, because arriving on a path
	// a conversation already has writes nothing but the lease: "where was I" moves
	// when the conversation writes, not when it looks.
	if !seen.Equal(atNine) {
		t.Errorf("the link was last seen at %s, want the creation's %s", seen, atNine)
	}
}

func TestDBWhereIWasIsOneAnswerWhenTheLinksTie(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	ctx := context.Background()

	// Three paths on one conversation, made by Create and not by arriving: an
	// arrival can only ever start a conversation's first one, so a conversation
	// in this state is one that has been put on paths by other means.
	for _, id := range []string{"mp_a", "mp_b", "mp_c"} {
		h.create(t, id, "目标 "+id, actor)
	}
	// The fixture's clock only moves when it is told to, so all three links carry
	// the same last_seen_time and the read has to break the tie itself.
	arrival, err := h.service.Arrive(ctx, ArriveRequest{Actor: actor, Goal: "再来一次"}, h.caller)
	if err != nil {
		t.Fatalf("arrive on a conversation that has three paths: %v", err)
	}
	// The tie breaker is the one the read names, the path id ascending, and that
	// is what makes "where was I" a question with one answer rather than whichever
	// row the engine reached first.
	if arrival.Path.ID != "mp_a" {
		t.Errorf("the arrival is on path %s, want mp_a — the tie on last_seen is broken by id",
			arrival.Path.ID)
	}
	if got := h.countRows(t, "mastery_paths"); got != 3 {
		t.Errorf("%d paths are stored, want the three that were created", got)
	}
}

func TestDBAChangeHonoursTheVersionTheCallerRead(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	h.create(t, "mp_1", "学会闭包", actor)
	ctx := context.Background()

	// The change a caller makes with the version it has just read goes through, and
	// the event it leaves names the version the state reached rather than the one it
	// started from: the two are read together, and an event naming the older of
	// them would put its own change outside its own explanation.
	//
	// The clock moves first, because the history is ordered by time and broken apart
	// by id — the same tie-break the link table and the evidence reads use, and one
	// this package pins on purpose. Two events in the same second are two events in
	// the same second: reading them in the order they were written is not a promise
	// the column can keep, so a case that needs the order has to give them two
	// seconds.
	h.clock.advance(time.Hour)
	one := int64(1)
	updated, err := h.service.AddKnowledgePoint(ctx, AddKnowledgePointRequest{
		Change:   Change{PathID: "mp_1", Actor: actor, Expected: &one},
		ModuleID: "m_basics", ID: "kp_new", Name: "新的知识点",
	}, h.caller)
	if err != nil {
		t.Fatalf("add a knowledge point: %v", err)
	}
	if updated.Version != 2 {
		t.Errorf("the change landed at version %d, want 2", updated.Version)
	}
	if _, _, ok := updated.Tree.KnowledgePoint("kp_new"); !ok {
		t.Error("the knowledge point the caller added is not in the tree that came back")
	}
	events, err := h.repository.Events(ctx, "mp_1", h.caller)
	if err != nil {
		t.Fatalf("read the history: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("%d events are stored, want the creation and the addition", len(events))
	}
	if events[1].Version != 2 || events[1].Type != EventPathMapped {
		t.Errorf("the addition's event is %+v, want a mapped change at version 2", events[1])
	}

	// Now the same caller again, still holding version 1 — because it read the path
	// before the change above and thought about it for a while. Its edit is refused
	// rather than applied on top of a tree it never saw, and the refusal is a
	// sentinel it can act on: reread and try again is a thing to do, while "something
	// went wrong" is not.
	edit := Edit{Name: ptr("换个说法")}
	if _, err := h.service.EditKnowledgePoint(ctx, EditKnowledgePointRequest{
		Change: Change{PathID: "mp_1", Actor: actor, Expected: &one},
		ID:     "kp_new", Edit: edit,
	}, h.caller); !errors.Is(err, ErrStaleState) {
		t.Fatalf("a change against a version that has moved: %v, want %v", err, ErrStaleState)
	}
	if stored := h.storedPath(t, "mp_1"); stored.Version == nil || *stored.Version != 2 {
		t.Errorf("the path is at version %v, want the 2 the loser did not move", stored.Version)
	}
	if got := h.countRows(t, "mastery_events"); got != 2 {
		t.Errorf("%d events are stored, want the refused change to have written none", got)
	}
	// A caller that read nothing and says so is not making a claim about a version,
	// and the guard falls back to the one on the row — which is the ordinary case,
	// a tool applying an edit to whatever the path is now.
	next, err := h.service.EditKnowledgePoint(ctx, EditKnowledgePointRequest{
		Change: Change{PathID: "mp_1", Actor: actor},
		ID:     "kp_new", Edit: edit,
	}, h.caller)
	if err != nil {
		t.Fatalf("edit without naming a version: %v", err)
	}
	if next.Version != 3 {
		t.Errorf("the unguarded edit landed at version %d, want 3", next.Version)
	}
	point, _, ok := next.Tree.KnowledgePoint("kp_new")
	if !ok || point.Name != "换个说法" {
		t.Errorf("the knowledge point reads back as %+v", point)
	}

	// And a change that names nothing which exists is refused before anything is
	// written, with the tree and its version left where they were.
	if _, err := h.service.EditKnowledgePoint(ctx, EditKnowledgePointRequest{
		Change: Change{PathID: "mp_1", Actor: actor},
		ID:     "kp_nobody",
		Edit:   Edit{Name: ptr("换个说法")},
	}, h.caller); !errors.Is(err, ErrNotFound) {
		t.Errorf("editing a knowledge point that is not in the tree: %v, want %v", err, ErrNotFound)
	}
	if stored := h.storedPath(t, "mp_1"); stored.Version == nil || *stored.Version != 3 {
		t.Errorf("the path is at version %v, want the 3 it was at", stored.Version)
	}
	if got := h.countRows(t, "mastery_events"); got != 3 {
		t.Errorf("%d events are stored, want the refused edit to have written none", got)
	}
}

func TestDBNamingThePathsWritesNoHistoryAndTakesNoVersion(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	h.create(t, "mp_1", "学会闭包", actor)
	ctx := context.Background()

	renamed, err := h.service.UpdateMeta(ctx, MetaRequest{
		PathID: "mp_1", Goal: ptr("把闭包学明白"), Emoji: ptr("📚"),
	}, h.caller)
	if err != nil {
		t.Fatalf("rename the path: %v", err)
	}
	if renamed.Goal != "把闭包学明白" || renamed.Emoji != "📚" || renamed.Status != StatusActive {
		t.Errorf("the metadata is %+v", renamed)
	}
	// The version is the tree's, and rewording what a path is for is not a change
	// to the tree: a version bump here would make every turn holding a read state
	// look stale for a reason that has nothing to do with the tree, and the
	// history would gain a line about something that is not in it.
	if stored := h.storedPath(t, "mp_1"); stored.Version == nil || *stored.Version != 1 {
		t.Errorf("the path is at version %v, want the unchanged 1", stored.Version)
	}
	if got := h.countRows(t, "mastery_events"); got != 1 {
		t.Errorf("%d events are stored, want the creation's one", got)
	}

	// A status this version does not write is refused rather than stored: nothing
	// reads the column, and accepting "archived" would be this package inventing
	// a vocabulary and then not honouring it.
	if _, err := h.service.UpdateMeta(ctx, MetaRequest{PathID: "mp_1", Status: ptr("archived")}, h.caller); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("setting a status this version does not write: %v, want %v", err, ErrInvalidRequest)
	}
	if _, err := h.service.UpdateMeta(ctx, MetaRequest{PathID: "mp_1"}, h.caller); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("a request that names nothing to change: %v, want %v", err, ErrInvalidRequest)
	}
	if _, err := h.service.UpdateMeta(ctx, MetaRequest{
		PathID: "mp_missing", Goal: ptr("无处安放"),
	}, h.caller); !errors.Is(err, ErrNotFound) {
		t.Errorf("renaming a path that is not there: %v, want %v", err, ErrNotFound)
	}
	// And the refusals wrote nothing: the metadata is what the successful rename
	// left, not a half-applied later one.
	view, err := h.service.Load(ctx, "mp_1", h.caller)
	if err != nil {
		t.Fatalf("read the path back: %v", err)
	}
	if view.Meta.Goal != "把闭包学明白" || view.Meta.Status != StatusActive {
		t.Errorf("the metadata is %+v, want the rename and no later change", view.Meta)
	}
}

func TestDBAListingReadsTheTreeAndTheCachedLevels(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	h.create(t, "mp_1", "学会闭包", actor)
	ctx := context.Background()

	// Three right answers on one knowledge point promote it, and the promotion
	// reaches the listing through the cached level the write recomputed — which is
	// the point of the cache: a listing that ran the gate over every path's every
	// observation would get slower the longer somebody studied.
	for index := 0; index < 3; index++ {
		h.storeEvidence(t, "mle_"+string(rune('a'+index)), "mp_1", "kp_capture",
			ResultCorrect, actor, atNine)
	}
	// Writing the evidence through the service is what recomputes the tree's
	// levels; the fixture's own writer appends the row and stops there.
	if _, err := h.service.AddKnowledgePoint(ctx, AddKnowledgePointRequest{
		Change:   Change{PathID: "mp_1", Actor: actor},
		ModuleID: "m_advanced", ID: "kp_second", Name: "第二个",
	}, h.caller); err != nil {
		t.Fatalf("force the tree to be rewritten: %v", err)
	}

	list, err := h.service.List(ctx, ListRequest{Caller: h.caller})
	if err != nil {
		t.Fatalf("list the paths: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("%d paths are listed, want one", len(list))
	}
	row := list[0]
	if row.PathID != "mp_1" || row.Goal != "学会闭包" || row.Emoji != defaultEmoji {
		t.Errorf("the row is %+v", row)
	}
	if row.Modules != 2 || row.KnowledgePoints != 4 {
		t.Errorf("the row counts %d modules and %d knowledge points, want 2 and 4",
			row.Modules, row.KnowledgePoints)
	}
	if row.Mastered != 1 {
		t.Errorf("the row counts %d mastered, want the one with three right answers", row.Mastered)
	}
	// The version is the tree's and the update time is the tree's, while the last
	// seen time is the link's — three columns from three places, which is what the
	// listing is for.
	if row.Version != 2 || !row.UpdatedAt.Equal(atNine) {
		t.Errorf("the row is at version %d updated %s", row.Version, row.UpdatedAt)
	}
	if !row.LastSeenAt.Equal(atNine) {
		t.Errorf("the row was last seen at %s, want %s", row.LastSeenAt, atNine)
	}

	// Narrowing by conversation is the other half of the request, and a
	// conversation that has nothing lists nothing rather than listing everything.
	mine, err := h.service.List(ctx, ListRequest{Caller: h.caller, SessionID: "s_1"})
	if err != nil {
		t.Fatalf("list this conversation's paths: %v", err)
	}
	if len(mine) != 1 || mine[0].PathID != "mp_1" {
		t.Errorf("the narrowed listing is %+v", mine)
	}
	theirs, err := h.service.List(ctx, ListRequest{Caller: h.caller, SessionID: "s_elsewhere"})
	if err != nil {
		t.Fatalf("list another conversation's paths: %v", err)
	}
	if len(theirs) != 0 {
		t.Errorf("a conversation that has been on nothing lists %+v", theirs)
	}
}

func TestDBAPathsOwnerIsWhatDecidesWhoCanReadIt(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	ctx := context.Background()

	// One path, written for h.caller, which is the account every read here has to
	// be scoped to. §7.2 asks for this case by name and on every listing rather
	// than on one of them, because a read that forgets the scope does not fail: it
	// returns another account's rows, and a list with too much in it looks exactly
	// like a list that works.
	h.create(t, "mp_mine", "学会闭包", actor)

	// The owner's listing holds the path, and the count is of the caller's rows
	// rather than of the table's — which is the whole of the difference the scope
	// makes.
	mine, err := h.service.List(ctx, ListRequest{Caller: h.caller})
	if err != nil {
		t.Fatalf("list as the owner: %v", err)
	}
	if len(mine) != 1 || mine[0].PathID != "mp_mine" {
		t.Errorf("the owner's listing is %+v, want the one path it made", mine)
	}
	// Another account's listing is empty, and not because it was filtered after the
	// fact: the scope is in the SQL, so the row is never a candidate. An empty list
	// is also the honest answer rather than a refusal — a person who has started
	// nothing has no paths, and that is true whether the rows are theirs or
	// somebody else's.
	theirs, err := h.service.List(ctx, ListRequest{Caller: h.other})
	if err != nil {
		t.Fatalf("list as another account: %v", err)
	}
	if len(theirs) != 0 {
		t.Errorf("another account's listing is %+v, want nothing of the owner's", theirs)
	}

	// Reading the path by id is the same rule at one row: not a 403 and not a
	// refusal of any kind, but the absence sentinel the handler already maps to a
	// 404 (§2.5). So another account cannot even learn that the path exists, which
	// is why the answer is not "you may not have this" — that would say it is
	// there.
	if _, err := h.service.Load(ctx, "mp_mine", h.other); !errors.Is(err, ErrNotFound) {
		t.Errorf("another account reading the owner's path: %v, want %v", err, ErrNotFound)
	}
	// The reads that hang off the same path answer the same way, because they are
	// scoped the same way: the insight, the lease and the write that renames all
	// go through the path's owner before they do anything else.
	if _, err := h.service.Insight(ctx, "mp_mine", h.other); !errors.Is(err, ErrNotFound) {
		t.Errorf("another account reading the insight: %v, want %v", err, ErrNotFound)
	}
	if _, err := h.service.Holder(ctx, "mp_mine", h.other); !errors.Is(err, ErrNotFound) {
		t.Errorf("another account reading the lease: %v, want %v", err, ErrNotFound)
	}
	goal := "偷看别人的目标"
	if _, err := h.service.UpdateMeta(ctx, MetaRequest{PathID: "mp_mine", Goal: &goal}, h.other); !errors.Is(err, ErrNotFound) {
		t.Errorf("another account renaming the path: %v, want %v", err, ErrNotFound)
	}
	// The rename did not land, which is the half a status code cannot show.
	if view, err := h.service.Load(ctx, "mp_mine", h.caller); err != nil {
		t.Fatalf("the owner reading its own path: %v", err)
	} else if view.Meta.Goal == goal {
		t.Error("another account's rename reached the owner's path")
	}

	// The administrator is not a way past the scope, and that is the rule rather
	// than an oversight: admin admits the rows that belong to nobody (the pre-V5
	// history), not the rows that belong to somebody. An owned path is scoped away
	// from the administrator exactly as it is from any other account, so the only
	// paths the administrator reads are NULL-owner ones and its own.
	if _, err := h.service.Load(ctx, "mp_mine", h.admin); !errors.Is(err, ErrNotFound) {
		t.Errorf("the administrator reading an owned path: %v, want %v", err, ErrNotFound)
	}
}

// ptr is the address of a string literal, which is how a request says "change
// this field" as opposed to "leave it alone".
func ptr[T any](value T) *T { return &value }
