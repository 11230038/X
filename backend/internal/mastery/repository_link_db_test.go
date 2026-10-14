package mastery

// The link between a path and the conversations that have touched it, and the
// listing that is read off it.
//
// The link is the one table in this package whose question is an ordering rather
// than a value: "where was I" is the newest row and "which paths am I working on"
// is those rows filtered by a status that lives in another table, and both are
// answered by the engine reading a DATETIME column and a tie-breaking id. None of
// that is knowable from a double, which is why it is here.

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDBALinkIsWrittenOnceAndThenMoved(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	h.create(t, "mp_1", "学会闭包", actor)
	ctx := context.Background()
	h.clock.advance(time.Hour)

	// The upsert on the composite key: "which conversations have touched this path"
	// and "when was the last time" are one row's two questions, and the first one
	// never stops being true.
	if err := h.service.Touch(ctx, "mp_1", actor, h.caller); err != nil {
		t.Fatalf("touch the path: %v", err)
	}
	if got := h.countRows(t, "mastery_path_sessions"); got != 1 {
		t.Fatalf("%d links are stored, want the second touch to have moved the first", got)
	}
	seen, err := h.repository.SeenAt(ctx, "mp_1", "s_1", h.caller)
	if err != nil {
		t.Fatalf("read when the conversation last saw the path: %v", err)
	}
	if !seen.Equal(atTen) {
		t.Errorf("the link says %s, want the second touch's %s", seen, atTen)
	}

	// A second conversation on the same path is a second row, and the two are read
	// most recently seen first.
	h.session(t, "s_2")
	second := h.turn(t, "t_2", "s_2")
	h.clock.advance(time.Hour)
	if err := h.service.Touch(ctx, "mp_1", second, h.caller); err != nil {
		t.Fatalf("touch the path from another conversation: %v", err)
	}
	sessions, err := h.repository.SessionsOn(ctx, "mp_1", h.caller)
	if err != nil {
		t.Fatalf("read the conversations on the path: %v", err)
	}
	if len(sessions) != 2 || sessions[0] != "s_2" {
		t.Errorf("the conversations on the path are %v, want the newest first", sessions)
	}
	// "Where was I" is the newest link, which is a question with one answer.
	newest, err := h.repository.PathOfSession(ctx, "s_2", h.caller)
	if err != nil {
		t.Fatalf("read the path of the second conversation: %v", err)
	}
	if newest != "mp_1" {
		t.Errorf("the second conversation is on %q, want mp_1", newest)
	}
	if _, err := h.repository.PathOfSession(ctx, "s_nobody", h.caller); !errors.Is(err, ErrNotFound) {
		t.Errorf("a conversation that is on no path: %v, want %v", err, ErrNotFound)
	}
}

func TestDBTheListingIsMostRecentlySeenFirstAndAnUntouchedPathComesLast(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	before := h.clock.read()
	// Written in the order the ids sort in, so that a listing coming back in id
	// order would be indistinguishable from one that sorted by anything else.
	h.create(t, "mp_a", "甲", actor)
	h.clock.advance(time.Hour)
	h.create(t, "mp_b", "乙", actor)
	h.clock.advance(time.Hour)
	h.create(t, "mp_c", "丙", actor)

	// Move the middle one's link to the front, so the order is neither the id order
	// nor the order they were made in.
	if err := h.resource.GORM().Exec("UPDATE mastery_path_sessions SET last_seen_time = ? "+
		"WHERE path_id = 'mp_b' AND session_id = 's_1'", atNoon).Error; err != nil {
		t.Fatalf("move a link: %v", err)
	}
	// And take one out of the conversation entirely: a path nobody has touched is a
	// path that exists and has not been worked on, and it belongs after the ones
	// that have.
	if err := h.resource.GORM().Exec("DELETE FROM mastery_path_sessions WHERE path_id = 'mp_a'").Error; err != nil {
		t.Fatalf("unlink a path: %v", err)
	}

	listed, err := h.service.List(context.Background(), ListRequest{Caller: h.caller})
	if err != nil {
		t.Fatalf("list the paths: %v", err)
	}
	got := ids(listed)
	if want := []string{"mp_b", "mp_c", "mp_a"}; !equal(got, want) {
		t.Errorf("the listing is %v, want %v", got, want)
	}
	// A path no conversation has touched is still a path, and one with the whole of
	// its tree counted: the counts are read off the state rather than recomputed
	// from the evidence, which is what the level cache is for.
	if listed[2].Modules != 2 || listed[2].KnowledgePoints != 3 {
		t.Errorf("the untouched path lists as %+v", listed[2])
	}
	// The zero the repository reports is not a time, and it must not be mistaken for
	// one: a row that read as 1970 would sort before everything.
	if !listed[2].LastSeenAt.IsZero() {
		t.Errorf("the untouched path was last seen at %s, want nothing", listed[2].LastSeenAt)
	}
	// And a path that has only ever been created has no update time either, because
	// update_time is written by a change and not by the creation (§2.1: a new tree
	// is a change, a new path is not). The distinction is worth pinning: it is the
	// column the dashboard falls back to for a path nobody has opened.
	if !listed[2].UpdatedAt.IsZero() {
		t.Errorf("a path nobody has changed was written at %s, want nothing", listed[2].UpdatedAt)
	}
	if before != atNine {
		t.Fatalf("the fixture's clock started at %s, want %s", before, atNine)
	}

	// Narrowed to a conversation, the same listing loses the path it never touched.
	narrowed, err := h.service.List(context.Background(), ListRequest{Caller: h.caller, SessionID: "s_1"})
	if err != nil {
		t.Fatalf("list one conversation's paths: %v", err)
	}
	if want := []string{"mp_b", "mp_c"}; !equal(ids(narrowed), want) {
		t.Errorf("the narrowed listing is %v, want %v", ids(narrowed), want)
	}
}

func TestDBOnlyTheLinksOnActivePathsCount(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	// The id that sorts first is the one worked on first, so that "most recently
	// seen first" and "by path id" are different answers and the case can tell
	// which one it got.
	h.create(t, "mp_a", "甲", actor)
	h.clock.advance(time.Hour)
	h.create(t, "mp_z", "乙", actor)
	ctx := context.Background()

	paths, err := h.repository.ActivePathsOfSession(ctx, "s_1", h.caller)
	if err != nil {
		t.Fatalf("read the active paths: %v", err)
	}
	if !equal(paths, []string{"mp_z", "mp_a"}) {
		t.Errorf("the conversation is working on %v, want both, most recently seen first", paths)
	}

	// A path whose status is not "active" is one the person has put down. The
	// column is written here rather than through the domain because this version
	// writes only one status (UpdateMeta), and the read is what is being checked.
	if err := h.resource.GORM().Exec("UPDATE mastery_topic_meta SET status = 'archived' " +
		"WHERE path_id = 'mp_a'").Error; err != nil {
		t.Fatalf("put a path down: %v", err)
	}
	paths, err = h.repository.ActivePathsOfSession(ctx, "s_1", h.caller)
	if err != nil {
		t.Fatalf("read the active paths again: %v", err)
	}
	if !equal(paths, []string{"mp_z"}) {
		t.Errorf("the conversation is working on %v, want only the one still active", paths)
	}
	// A path with no metadata row at all counts as active: the LEFT JOIN's COALESCE
	// is there because a path this version has not renamed is reported everywhere
	// else rather than refused.
	if err := h.resource.GORM().Exec("DELETE FROM mastery_topic_meta WHERE path_id = 'mp_a'").Error; err != nil {
		t.Fatalf("drop a metadata row: %v", err)
	}
	paths, err = h.repository.ActivePathsOfSession(ctx, "s_1", h.caller)
	if err != nil {
		t.Fatalf("read the active paths once more: %v", err)
	}
	if !equal(paths, []string{"mp_z", "mp_a"}) {
		t.Errorf("a path with no metadata reads as %v, want it counted as active", paths)
	}
}

// ids is a listing by path id, which is what its order is about.
func ids(listed []Summary) []string {
	out := make([]string, 0, len(listed))
	for _, summary := range listed {
		out = append(out, summary.PathID)
	}
	return out
}

func equal(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
