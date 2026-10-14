package mastery

// Observations, what they do to the state, and what a reader is told about them.
//
// The rule in this file that no double could decide is the one §2.3 states and
// §7.2 asks for by name: a turn whose lease expired while it was still working
// loses the version guard, and losing it costs it the *state* write and nothing
// else — the observation it made is still true, so the evidence and its event
// land anyway. Both halves have to be asserted together ("the state was not
// overwritten" and "the evidence is there"), and both are claims about rows.
//
// The guard is made to lose by a repository that refuses one write, which is the
// one thing a double is for. Everything else in the case is the real adapter: the
// evidence row, the event and the link are written by the same SQL they always
// are, because the question is what survives, and only the database can say.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"backend/internal/authz"
)

// refusingSave is the real repository with one failure injected: SaveTree refuses
// as it would if another turn had moved the path between this observation's read
// and its write.
//
// It has to wrap Within as well, and that is the whole trick: the service takes
// its transaction by calling Within and then works on whatever repository the
// transaction hands it, so a decorator that only replaced the outer object would
// be decorating a repository the write never goes through.
type refusingSave struct {
	Repository
	refuse bool
}

func (r *refusingSave) SaveTree(
	ctx context.Context, pathID string, expected int64, tree Tree, now time.Time, caller authz.Caller,
) (int64, error) {
	if r.refuse {
		return 0, fmt.Errorf("%w: path %s was written by another turn", ErrStaleState, pathID)
	}
	return r.Repository.SaveTree(ctx, pathID, expected, tree, now, caller)
}

func (r *refusingSave) Within(ctx context.Context, work func(Repository) error) error {
	return r.Repository.Within(ctx, func(inner Repository) error {
		return work(&refusingSave{Repository: inner, refuse: r.refuse})
	})
}

// serviceOver builds a second service over the same harness: the same
// thresholds, the same clock, a different repository.
func (h *harness) serviceOver(t *testing.T, repository Repository) *Service {
	t.Helper()
	service, err := NewService(repository, Options{
		LeaseTTL: time.Hour,
		Gate:     GateOptions{Consecutive: 3, MinEvidence: 2},
	})
	if err != nil {
		t.Fatalf("build a service over the refusing repository: %v", err)
	}
	service.clock = h.clock.read
	return service
}

func TestDBAnObservationSurvivesTheVersionGuardLosing(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	h.create(t, "mp_1", "学会闭包", actor)
	ctx := context.Background()

	// The turn read the path at version 1 and then took longer than its lease, so
	// by the time it writes, another turn owns the tree. Its write of the *state*
	// has to lose — the other turn's tree is the tree — and its observation has to
	// land anyway: the learner did answer, whatever happened to the tree
	// afterwards, and an observation thrown away because a lease expired would be
	// the learner's work lost to a race they never saw.
	before := h.storedPath(t, "mp_1")
	// The clock moves before the write, because the event's time comes from the
	// service's clock and not from the observation's own. Two events in the same
	// second would be ordered by their ids, which are random, and a case that read
	// them in the other order would be reading the creation's payload.
	h.clock.advance(time.Hour)
	late := h.serviceOver(t, &refusingSave{Repository: h.repository, refuse: true})
	recorded, err := late.RecordEvidence(ctx, Evidence{
		ID: "mle_late", PathID: "mp_1", KnowledgePoint: "kp_capture", Result: ResultCorrect,
		Source: SourceAssessment, Actor: actor, CreatedAt: atTen,
	}, h.caller)
	if err != nil {
		t.Fatalf("record an observation whose state write loses: %v", err)
	}
	if recorded.ID != "mle_late" {
		t.Errorf("the observation came back as %+v", recorded)
	}

	// Half one: the evidence is there.
	if got := h.countRows(t, "mastery_learning_evidences"); got != 1 {
		t.Errorf("%d observations are stored, want the one that survived the lost guard", got)
	}
	// Half two: the state was not overwritten. The bytes are compared rather than
	// the version alone, because "the version did not move" and "the row was not
	// written" are the same claim only if nothing else on the row changed — and the
	// state column is the one a losing writer would have replaced.
	stored := h.storedPath(t, "mp_1")
	if stored.Version == nil || *stored.Version != 1 {
		t.Errorf("the path is at version %v, want the 1 the loser did not move", stored.Version)
	}
	if stored.State == nil || before.State == nil || *stored.State != *before.State {
		t.Errorf("the state was rewritten by the writer that lost the guard")
	}
	// And the event says which of the two versions it is about, because the version
	// it names is the only thing that tells a reader whether this observation
	// reached the state or only the history.
	events, err := h.repository.Events(ctx, "mp_1", h.caller)
	if err != nil {
		t.Fatalf("read the history: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("%d events are stored, want the creation and the observation", len(events))
	}
	var payload map[string]any
	if err := json.Unmarshal(events[1].Payload, &payload); err != nil {
		t.Fatalf("the observation's payload is not JSON: %v", err)
	}
	if payload["state_updated"] != false || events[1].Version != 1 {
		t.Errorf("the event says %v at version %d, want the state unmoved at version 1",
			payload["state_updated"], events[1].Version)
	}

	// The next writer recomputes the levels over *all* the evidence, which is what
	// makes waiting for it safe: the observation the loser could not write is not
	// lost from the levels either, it is just written by somebody else.
	h.clock.advance(time.Hour)
	next := h.serviceOver(t, &refusingSave{Repository: h.repository})
	if _, err := next.RecordEvidence(ctx, Evidence{
		ID: "mle_next", PathID: "mp_1", KnowledgePoint: "kp_capture", Result: ResultCorrect,
		Source: SourceAssessment, Actor: actor, CreatedAt: atEleven,
	}, h.caller); err != nil {
		t.Fatalf("record the next observation: %v", err)
	}
	if stored := h.storedPath(t, "mp_1"); stored.Version == nil || *stored.Version != 2 {
		t.Errorf("the path is at version %v, want the recovered writer's 2", stored.Version)
	}
	path, err := h.repository.Path(ctx, "mp_1", h.caller)
	if err != nil {
		t.Fatalf("read the path: %v", err)
	}
	point, _, ok := path.Tree.KnowledgePoint("kp_capture")
	if !ok {
		t.Fatal("the knowledge point left the tree")
	}
	if point.Mastery != LevelLearning {
		t.Errorf("the cached level is %q, want %q from both observations", point.Mastery, LevelLearning)
	}
}

func TestDBAnObservationAboutACoordinateThatIsNotInTheTreeIsRefused(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	h.create(t, "mp_1", "学会闭包", actor)
	ctx := context.Background()

	// A coordinate is not a row anywhere, so this is the only check there can be
	// (§2.2): an observation about a knowledge point the path does not have is a
	// caller holding the wrong path or the wrong id, and either way it is not
	// something to store.
	if _, err := h.service.RecordEvidence(ctx, Evidence{
		PathID: "mp_1", KnowledgePoint: "kp_nobody", Result: ResultCorrect,
		Source: SourceAssessment, Actor: actor,
	}, h.caller); !errors.Is(err, ErrNotFound) {
		t.Errorf("an observation about a knowledge point that is not in the tree: %v, want %v",
			err, ErrNotFound)
	}
	if got := h.countRows(t, "mastery_learning_evidences"); got != 0 {
		t.Errorf("%d observations are stored, want the refusal to have written none", got)
	}
	if got := h.countRows(t, "mastery_events"); got != 1 {
		t.Errorf("%d events are stored, want only the creation's", got)
	}
	// And the evidence a caller numbers itself keeps that id, which is what makes
	// a replay or an import possible at all.
	stored, err := h.service.RecordEvidence(ctx, Evidence{
		ID: "mle_mine", PathID: "mp_1", KnowledgePoint: "kp_loop", Result: ResultCorrect,
		Source: SourceAssessment, Actor: actor,
	}, h.caller)
	if err != nil {
		t.Fatalf("record an observation with an id of the caller's own: %v", err)
	}
	if stored.ID != "mle_mine" {
		t.Errorf("the observation came back numbered %s, want the caller's own", stored.ID)
	}
}

func TestDBTheInsightTellsUnknownApartFromWeak(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	h.create(t, "mp_1", "学会闭包", actor)
	ctx := context.Background()

	// Two observations, the second one wrong: enough evidence for the gate to have
	// an opinion, and the trailing run says the knowledge point is not held. The
	// other two points have never been observed, and that is a different answer —
	// a knowledge point nobody has met is not a weakness (MP-05).
	h.storeEvidence(t, "mle_a", "mp_1", "kp_capture", ResultCorrect, actor, atNine)
	h.storeEvidence(t, "mle_b", "mp_1", "kp_capture", ResultIncorrect, actor, atTen)

	insight, err := h.service.Insight(ctx, "mp_1", h.caller)
	if err != nil {
		t.Fatalf("read the insight: %v", err)
	}
	// Every knowledge point of the tree is reported, in tree order, because a
	// reader that drew only what it was told about could not tell an absent point
	// from an unknown one.
	want := []string{"kp_capture", "kp_loop", "kp_escape"}
	if len(insight.Points) != len(want) {
		t.Fatalf("%d points are reported, want the tree's %d", len(insight.Points), len(want))
	}
	for index, id := range want {
		if insight.Points[index].ID != id {
			t.Errorf("point %d is %s, want %s in tree order", index, insight.Points[index].ID, id)
		}
	}
	capture := insight.Points[0]
	if capture.Level != LevelWeak {
		t.Errorf("the knowledge point with a wrong answer last reads as %q, want %q",
			capture.Level, LevelWeak)
	}
	if capture.Total != 2 || capture.Correct != 1 || capture.Incorrect != 1 || capture.Partial != 0 {
		t.Errorf("the counts are %+v, want two observations split one and one", capture)
	}
	if !capture.LastObservedAt.Equal(atTen) {
		t.Errorf("the last observation is reported at %s, want %s", capture.LastObservedAt, atTen)
	}
	// A wrong answer comes back tomorrow, so the due time is the observation plus
	// the first interval rather than nothing at all.
	if !capture.DueAt.Equal(atTen.Add(24 * time.Hour)) {
		t.Errorf("the knowledge point is due at %s, want %s", capture.DueAt, atTen.Add(24*time.Hour))
	}
	for _, point := range insight.Points[1:] {
		if point.Level != LevelUnknown {
			t.Errorf("%s has never been observed and reads as %q", point.ID, point.Level)
		}
		// An unstarted point is not overdue: zero, not "now".
		if !point.DueAt.IsZero() {
			t.Errorf("%s has never been observed and is due at %s", point.ID, point.DueAt)
		}
	}
	if len(insight.Weak) != 1 || insight.Weak[0] != "kp_capture" {
		t.Errorf("the weak list is %v, want just the knowledge point that was failed", insight.Weak)
	}
	if len(insight.Evidence) != 2 {
		t.Errorf("%d observations are reported, want both", len(insight.Evidence))
	}
	// The next move is the untested knowledge point and not the weak one: nothing
	// is due yet, and a coordinate nobody has met comes before one that has been
	// scheduled for tomorrow.
	if insight.Next.Kind != StepLearn || insight.Next.KnowledgePointID != "kp_loop" {
		t.Errorf("the next step is %+v, want the first untested knowledge point", insight.Next)
	}
}

func TestDBTheInsightDropsAnObservationWhoseCoordinateLeftTheTree(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	h.create(t, "mp_1", "学会闭包", actor)
	ctx := context.Background()

	h.storeEvidence(t, "mle_a", "mp_1", "kp_capture", ResultCorrect, actor, atNine)
	h.storeEvidence(t, "mle_b", "mp_1", "kp_capture", ResultIncorrect, actor, atTen)
	if _, err := h.service.RemoveKnowledgePoint(ctx, RemoveKnowledgePointRequest{
		Change: Change{PathID: "mp_1", Actor: actor}, ID: "kp_capture",
	}, h.caller); err != nil {
		t.Fatalf("remove the knowledge point: %v", err)
	}

	insight, err := h.service.Insight(ctx, "mp_1", h.caller)
	if err != nil {
		t.Fatalf("read the insight: %v", err)
	}
	// The observations are not deleted — a question is an asset of the bank and so
	// is what somebody answered — but nothing aggregates them any more: a
	// coordinate that does not resolve is not a finding (§2.2, §8 第 12 条), and
	// reporting it as a weak point would be reporting a knowledge point the path
	// no longer has.
	if len(insight.Evidence) != 2 {
		t.Errorf("%d observations are reported, want both kept", len(insight.Evidence))
	}
	for _, point := range insight.Points {
		if point.ID == "kp_capture" {
			t.Error("the removed knowledge point is still reported as a point")
		}
	}
	if len(insight.Weak) != 0 {
		t.Errorf("the weak list is %v, want nothing: the coordinate it was about is gone", insight.Weak)
	}
	if insight.Next.KnowledgePointID == "kp_capture" {
		t.Errorf("the next step names the knowledge point that was removed: %+v", insight.Next)
	}
	// And the removal did not delete anything out of the evidence table.
	if got := h.countRows(t, "mastery_learning_evidences"); got != 2 {
		t.Errorf("%d observations are stored, want the two the removal kept", got)
	}
}

func TestDBTheNextStepMovesFromLearningToReviewingOnItsOwn(t *testing.T) {
	h := newHarness(t)
	actor := h.conversation(t, "s_1", "t_1")
	h.create(t, "mp_1", "学会闭包", actor)
	ctx := context.Background()

	// Three right answers on one knowledge point, all in the same second: the curve
	// schedules it seven days out, so on the day they were made there is nothing due
	// and the move is the knowledge point nobody has met.
	for _, id := range []string{"mle_a", "mle_b", "mle_c"} {
		h.storeEvidence(t, id, "mp_1", "kp_capture", ResultCorrect, actor, atNine)
	}
	insight, err := h.service.Insight(ctx, "mp_1", h.caller)
	if err != nil {
		t.Fatalf("read the insight: %v", err)
	}
	if insight.Next.Kind != StepLearn {
		t.Errorf("the next step is %q, want a lesson while nothing is due", insight.Next.Kind)
	}

	// A week and a day later the same path answers differently with no write in
	// between: the step is computed, not stored, and it moves because the clock did.
	h.clock.advance(8 * 24 * time.Hour)
	insight, err = h.service.Insight(ctx, "mp_1", h.caller)
	if err != nil {
		t.Fatalf("read the insight again: %v", err)
	}
	if insight.Next.Kind != StepReview || insight.Next.KnowledgePointID != "kp_capture" {
		t.Fatalf("the next step is %+v, want a review of the knowledge point that came due", insight.Next)
	}
	// A review names what it stands on, which is COURSE-04's "推荐应显示依据" and the
	// reason a step carries ids rather than a sentence.
	if len(insight.Next.EvidenceIDs) != 3 {
		t.Errorf("the review cites %v, want the three observations behind it", insight.Next.EvidenceIDs)
	}
	if insight.Next.Reason == "" {
		t.Error("the review says nothing about why")
	}
	// The level is the one the gate decided from the same evidence, and it is
	// mastered: three right answers in a row is what the deployment asked for.
	if insight.Points[0].Level != LevelMastered {
		t.Errorf("the knowledge point reads as %q, want %q", insight.Points[0].Level, LevelMastered)
	}
}

func TestDBReleasingAPathIsTheEndpointsActAndSaysWhoHeldIt(t *testing.T) {
	h := newHarness(t)
	first := h.conversation(t, "s_1", "t_1")
	h.create(t, "mp_1", "学会闭包", first)
	second := h.turn(t, "t_2", "s_1")
	ctx := context.Background()

	if _, err := h.service.Acquire(ctx, "mp_1", first, h.caller); err != nil {
		t.Fatalf("take the lease: %v", err)
	}
	// The endpoint's act: a person saying "nobody is writing this any more" about a
	// claim a crashed turn left behind, which is why it names no holder and reports
	// the one it dropped.
	held, released, err := h.service.ReleaseAny(ctx, "mp_1", h.caller)
	if err != nil {
		t.Fatalf("release whatever holds the path: %v", err)
	}
	if !released || held.TurnID != "t_1" {
		t.Errorf("the release reports %v having dropped %+v", released, held)
	}
	if got := h.countRows(t, "mastery_path_leases"); got != 0 {
		t.Errorf("%d leases are stored, want none", got)
	}
	// Asking again is not a failure: the state it asked for is the state it is in,
	// and a second release — or one after the lease expired — is an ordinary thing
	// for a surface to send.
	if _, released, err := h.service.ReleaseAny(ctx, "mp_1", h.caller); err != nil || released {
		t.Errorf("releasing a path nobody holds: released=%v err=%v", released, err)
	}
	// It names no holder, so it is not a way for a turn to drop somebody else's
	// claim without saying so — but it is also the only lease operation a turn
	// cannot reach by guessing, because it is reached by an endpoint and not a tool.
	if _, err := h.service.Acquire(ctx, "mp_1", second, h.caller); err != nil {
		t.Errorf("the path was not free after the release: %v", err)
	}
	if _, _, err := h.service.ReleaseAny(ctx, "  ", h.caller); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("releasing a blank path: %v, want %v", err, ErrInvalidRequest)
	}
}
