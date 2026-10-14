package memory

// What MEM-02's three verbs do, and what they leave behind.
//
// The repository is the fake, and what is being read here is the service's
// decisions: which write an edit performs, which state a retraction reaches,
// which of the two actions is refused, and what each one puts in the audit trail.
// Whether the rows those calls asked for are the rows the engine then keeps is the
// other file's question.

import (
	"context"
	"errors"
	"testing"
	"time"
)

// heldFact is one fact of account 7 already in force, which is where every case
// about a lifecycle action starts.
func heldFact(t *testing.T) (*Service, *fakeRepository, Fact) {
	t.Helper()
	service, repository := newFakeService(t, gateOptions())
	fact := Fact{
		ID: "mf_1", OwnerID: 7, Surface: SurfaceMastery, Scope: "learning",
		Statement: statement, Fingerprint: Fingerprint(statement), Confidence: 80,
		Status: StatusActive, ConfirmedBy: ConfirmerGate, Version: 1,
		CreatedAt: time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC),
	}
	repository.withFact(fact)
	return service, repository, fact
}

// asked is the fact request every case here spells the same way.
func asked(factID string) FactRequest {
	return FactRequest{OwnerID: 7, FactID: factID, Actor: ActorUser}
}

func TestAnEditRewritesTheStatementAndTheFingerprintWithIt(t *testing.T) {
	service, repository, _ := heldFact(t)

	edited, err := service.Edit(context.Background(), EditRequest{
		OwnerID: 7, FactID: "mf_1", Statement: ptr("对方正在学 Rust"), Actor: ActorUser,
	})
	if err != nil {
		t.Fatalf("Edit() error = %v", err)
	}
	if edited.Fact.Statement != "对方正在学 Rust" {
		t.Errorf("Edit() left the statement %q", edited.Fact.Statement)
	}
	// The fingerprint is what the gate and the dedupe read by, so an edit that left
	// the old one would be a fact whose statement and address disagree — findable by
	// nobody and duplicate forever.
	if edited.Fact.Fingerprint != Fingerprint("对方正在学 Rust") {
		t.Errorf("Edit() left the fingerprint %q, want the new statement's", edited.Fact.Fingerprint)
	}
	if edited.Fact.Version != 2 {
		t.Errorf("Edit() reached version %d, want 2", edited.Fact.Version)
	}
	if edited.Fact.UpdatedAt.IsZero() {
		t.Error("Edit() left the fact with no update time")
	}
	if len(repository.facts) != 1 || repository.facts[0].Statement != "对方正在学 Rust" {
		t.Errorf("Edit() left storage holding %+v", repository.facts)
	}

	// One action, one line: what it said before, what it says now.
	if len(repository.audits) != 1 {
		t.Fatalf("Edit() wrote %d audit lines, want one", len(repository.audits))
	}
	line := repository.audits[0]
	if line.Action != ActionFactEdited || line.TargetKind != TargetFact || line.TargetID != "mf_1" {
		t.Errorf("Edit() recorded %+v, want an edit of the fact", line)
	}
	if !sameJSON(t, line.Detail, `{"before":{"statement":"`+statement+`","scope":"learning","fingerprint":"`+
		Fingerprint(statement)+`","version":1},"after":{"statement":"对方正在学 Rust","scope":"learning","fingerprint":"`+
		Fingerprint("对方正在学 Rust")+`","version":2}}`) {
		t.Errorf("Edit() recorded the detail %s", line.Detail)
	}
}

func TestAnEditChangesOnlyWhatItWasGiven(t *testing.T) {
	service, repository, _ := heldFact(t)

	scoped, err := service.Edit(context.Background(), EditRequest{
		OwnerID: 7, FactID: "mf_1", Scope: ptr("health"), Actor: ActorUser,
	})
	if err != nil {
		t.Fatalf("Edit() error = %v", err)
	}
	if scoped.Fact.Scope != "health" || scoped.Fact.Statement != statement {
		t.Errorf("Edit() of the scope alone = %+v, want the statement left as it was", scoped.Fact)
	}
	// A scope is not part of the address, so it does not move the fingerprint.
	if scoped.Fact.Fingerprint != Fingerprint(statement) {
		t.Errorf("Edit() moved the fingerprint to %q for a scope change", scoped.Fact.Fingerprint)
	}

	// And an empty scope is how a scope is taken away: "" is a value the column can
	// hold, and there is no second way to say it.
	cleared, err := service.Edit(context.Background(), EditRequest{
		OwnerID: 7, FactID: "mf_1", Scope: ptr(""), Actor: ActorUser,
	})
	if err != nil {
		t.Fatalf("Edit() error = %v", err)
	}
	if cleared.Fact.Scope != "" {
		t.Errorf("Edit() left the scope %q, want it cleared", cleared.Fact.Scope)
	}
	if len(repository.audits) != 2 {
		t.Errorf("two edits wrote %d audit lines, want two", len(repository.audits))
	}
}

func TestAnEditThatChangesNothingIsRefused(t *testing.T) {
	service, repository, held := heldFact(t)

	_, err := service.Edit(context.Background(), EditRequest{OwnerID: 7, FactID: "mf_1", Actor: ActorUser})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Edit() with nothing to change = %v, want a refusal", err)
	}
	for _, test := range []struct {
		name    string
		request EditRequest
	}{
		{"a statement that is blank",
			EditRequest{OwnerID: 7, FactID: "mf_1", Statement: ptr("   "), Actor: ActorUser}},
		{"a fact that is not named",
			EditRequest{OwnerID: 7, Statement: ptr("别的"), Actor: ActorUser}},
		{"no account",
			EditRequest{FactID: "mf_1", Statement: ptr("别的"), Actor: ActorUser}},
		{"nobody asking",
			EditRequest{OwnerID: 7, FactID: "mf_1", Statement: ptr("别的")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.Edit(context.Background(), test.request); !errors.Is(err, ErrInvalidRequest) {
				t.Errorf("Edit() = %v, want a refusal", err)
			}
		})
	}

	// A refused edit is not an action, so it is not a line either — and the fact it
	// was aimed at is exactly as it was.
	if len(repository.audits) != 0 {
		t.Errorf("refused edits wrote %+v", repository.audits)
	}
	if repository.facts[0] != held {
		t.Errorf("a refused edit changed the fact: %+v", repository.facts[0])
	}
}

func TestAnEditOverAFactThatMovedOnIsRefused(t *testing.T) {
	service, repository, _ := heldFact(t)
	repository.saveFactErr = ErrStaleState

	_, err := service.Edit(context.Background(), EditRequest{
		OwnerID: 7, FactID: "mf_1", Statement: ptr("对方正在学 Rust"), Actor: ActorUser,
	})
	if !errors.Is(err, ErrStaleState) {
		t.Fatalf("Edit() error = %v, want a stale state", err)
	}
	// The losing write records nothing: the edit did not happen, and a trail line
	// saying it did would be the one thing an audit trail must never hold.
	if len(repository.audits) != 0 {
		t.Errorf("an edit that lost its race wrote %+v", repository.audits)
	}
}

func TestRetractingTakesAFactOutOfForceButNotOutOfTheRecord(t *testing.T) {
	service, repository, _ := heldFact(t)
	// The observations the fact was admitted on, which a retraction must not touch.
	repository.withTrace(Trace{ID: "mt_1", OwnerID: 7, Surface: SurfaceMastery, Kind: "goal",
		Statement: statement, Fingerprint: Fingerprint(statement),
		CreatedAt: time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)}).
		withSource("mf_1", "mt_1")

	retracted, err := service.Retract(context.Background(), asked("mf_1"))
	if err != nil {
		t.Fatalf("Retract() error = %v", err)
	}
	if retracted.Status != StatusRetracted || retracted.RetractTime.IsZero() {
		t.Errorf("Retract() left %+v, want a retracted fact with the time it was taken back", retracted)
	}
	if retracted.Version != 2 || retracted.UpdatedAt.IsZero() {
		t.Errorf("Retract() left version %d and update time %s", retracted.Version, retracted.UpdatedAt)
	}

	// Out of the views: the in-force listing no longer shows it.
	inForce, err := service.Facts(context.Background(), FactQuery{OwnerID: 7, Statuses: []Status{StatusActive}})
	if err != nil {
		t.Fatalf("Facts() error = %v", err)
	}
	if len(inForce) != 0 {
		t.Errorf("a retracted fact is still listed in force: %+v", inForce)
	}
	// And still readable by id, with its chain, which is the whole of what makes a
	// retraction different from a deletion: the interface has to be able to show the
	// row in order to offer to bring it back.
	read, err := service.Fact(context.Background(), 7, "mf_1")
	if err != nil {
		t.Fatalf("Fact() error = %v", err)
	}
	if read.Fact.Status != StatusRetracted || len(read.Traces) != 1 || read.Traces[0].ID != "mt_1" {
		t.Errorf("Fact() after a retraction = %+v, want the row and the chain it was admitted on", read)
	}
	// The observation is in L1 what it always was (§7.1's case for this).
	observations, err := service.Traces(context.Background(), TraceQuery{OwnerID: 7})
	if err != nil {
		t.Fatalf("Traces() error = %v", err)
	}
	if len(observations) != 1 {
		t.Errorf("a retraction left %d observations, want the one it was admitted on", len(observations))
	}

	if len(repository.audits) != 1 || repository.audits[0].Action != ActionFactRetracted ||
		repository.audits[0].TargetID != "mf_1" {
		t.Errorf("Retract() recorded %+v", repository.audits)
	}
}

func TestRestoringPutsTheFactBackInForce(t *testing.T) {
	service, repository, _ := heldFact(t)

	if _, err := service.Retract(context.Background(), asked("mf_1")); err != nil {
		t.Fatalf("Retract() error = %v", err)
	}
	restored, err := service.Restore(context.Background(), asked("mf_1"))
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if restored.Status != StatusActive {
		t.Errorf("Restore() left the fact %s", restored.Status)
	}
	// The column goes back to NULL rather than keeping the time it was once taken
	// back at: "retracted" is a state, not a mark, and the history of the state is
	// the audit trail's job.
	if !restored.RetractTime.IsZero() {
		t.Errorf("Restore() left the retraction time %s on the fact", restored.RetractTime)
	}
	if restored.Version != 3 {
		t.Errorf("Restore() reached version %d, want 3", restored.Version)
	}

	inForce, err := service.Facts(context.Background(), FactQuery{OwnerID: 7, Statuses: []Status{StatusActive}})
	if err != nil {
		t.Fatalf("Facts() error = %v", err)
	}
	if len(inForce) != 1 {
		t.Errorf("a restored fact is not listed in force: %+v", inForce)
	}
	want := []Action{ActionFactRetracted, ActionFactRestored}
	if len(repository.audits) != 2 || repository.audits[0].Action != want[0] || repository.audits[1].Action != want[1] {
		t.Errorf("the two actions recorded %+v, want %v", repository.audits, want)
	}
}

func TestTakingBackSomethingThatIsNotInForceIsRefused(t *testing.T) {
	service, repository, _ := heldFact(t)
	if _, err := service.Retract(context.Background(), asked("mf_1")); err != nil {
		t.Fatalf("Retract() error = %v", err)
	}
	// A fact that is already back is not one to take back a second time. The refusal
	// is the point: a no-op that wrote a line would make the trail say the user took
	// something back twice, and only one of those was a decision.
	if _, err := service.Retract(context.Background(), asked("mf_1")); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("retracting a retracted fact = %v, want a refusal", err)
	}
	if _, err := service.Restore(context.Background(), asked("mf_1")); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if _, err := service.Restore(context.Background(), asked("mf_1")); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("restoring a fact in force = %v, want a refusal", err)
	}

	// A merged fact is neither: its claim stands, in the fact it was folded into,
	// and undoing a merge is not a state change but a second merge in reverse.
	repository.withFact(Fact{ID: "mf_merged", OwnerID: 7, Surface: SurfaceMastery,
		Statement: statement, Fingerprint: Fingerprint(statement), Confidence: 80,
		Status: StatusSuperseded, ConfirmedBy: ConfirmerGate, Version: 3})
	if _, err := service.Retract(context.Background(), asked("mf_merged")); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("retracting a merged fact = %v, want a refusal", err)
	}
	if _, err := service.Restore(context.Background(), asked("mf_merged")); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("restoring a merged fact = %v, want a refusal", err)
	}

	// Another account's fact is not this account's to take back, and the read that
	// would have found it says not found.
	if _, err := service.Fact(context.Background(), 8, "mf_1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("another account's read = %v, want not found", err)
	}
	// Six calls above, two actions: the trail holds what happened, not what was
	// asked for.
	if len(repository.audits) != 2 {
		t.Errorf("the trail holds %d lines, want the two actions that happened", len(repository.audits))
	}
}

func TestTheUserWritingSomethingDownBecomesAFactAtOnce(t *testing.T) {
	service, repository := newFakeService(t, gateOptions())

	written, err := service.State(context.Background(), StateRequest{
		OwnerID: 7, Statement: "我住在杭州", Scope: "profile", Actor: ActorUser,
	})
	if err != nil {
		t.Fatalf("State() error = %v", err)
	}
	if !written.Fact.InForce() {
		t.Errorf("State() left the fact %s", written.Fact.Status)
	}
	if written.Fact.ConfirmedBy != ConfirmerUser {
		t.Errorf("State() wrote confirmed_by %q, want the user", written.Fact.ConfirmedBy)
	}
	if written.Fact.Surface != SurfaceUser {
		t.Errorf("State() recorded the surface %q, want the user's", written.Fact.Surface)
	}
	if written.Fact.Confidence != userConfidence || written.Fact.Scope != "profile" {
		t.Errorf("State() wrote %+v, want the scope it was given and a fact as sure as this scale goes", written.Fact)
	}
	// The statement is an observation like the surfaces' are: L1 holds it, with the
	// two constants that make it the fourth path.
	if len(repository.traces) != 1 {
		t.Fatalf("State() left %d observations, want the one the user made", len(repository.traces))
	}
	observation := repository.traces[0]
	if observation.Surface != SurfaceUser || observation.Kind != KindUserStated {
		t.Errorf("State() recorded %+v, want an observation of the user's own word", observation)
	}
	if written.Traces[0].ID != observation.ID {
		t.Errorf("State() handed back the chain %+v, want the observation it made", written.Traces)
	}

	if len(repository.audits) != 1 {
		t.Fatalf("State() wrote %d audit lines, want one", len(repository.audits))
	}
	line := repository.audits[0]
	if line.Action != ActionFactStated || line.TargetID != written.Fact.ID {
		t.Errorf("State() recorded %+v, want a statement about the fact it wrote", line)
	}
	if !sameJSON(t, line.Detail, `{"trace":"`+observation.ID+`"}`) {
		t.Errorf("State() recorded the detail %s", line.Detail)
	}
}

func TestTheUsersOwnWordIsNotMeasuredByTheGatesKnobs(t *testing.T) {
	// Thresholds nothing a model could report would clear, on purpose: the fourth
	// path of §2.7 skips the three conditions, and a service that quietly applied
	// them anyway would be a service where the user's own statement about
	// themselves stayed a proposal.
	service, repository := newFakeService(t, withCorroboration(withThreshold(gateOptions(), 100), 5))

	written, err := service.State(context.Background(), StateRequest{
		OwnerID: 7, Statement: "我住在杭州", Actor: ActorUser,
	})
	if err != nil {
		t.Fatalf("State() error = %v", err)
	}
	if !written.Fact.InForce() || len(repository.facts) != 1 {
		t.Errorf("State() under a closed gate = %+v, want the fact the user wrote", written.Fact)
	}
}

func TestWritingDownSomethingAlreadyBelievedJoinsItAndSaysSo(t *testing.T) {
	service, repository, held := heldFact(t)

	written, err := service.State(context.Background(), StateRequest{
		OwnerID: 7, Statement: statement, Actor: ActorUser,
	})
	if err != nil {
		t.Fatalf("State() error = %v", err)
	}
	if written.Fact.ID != held.ID {
		t.Errorf("State() wrote fact %s, want the one already in force %s", written.Fact.ID, held.ID)
	}
	if len(repository.facts) != 1 {
		t.Errorf("State() left %d facts, want the one that was there", len(repository.facts))
	}
	// Two voices, one fact: the user's statement is evidence for what the system
	// already believed, which is MEM-01's chain growing rather than a second row.
	if evidence := repository.evidenceOf(held.ID); len(evidence) != 1 {
		t.Errorf("State() gave the fact the evidence %v, want the user's observation", evidence)
	}
	// And the line names the fact rather than the observation that admitted it: the
	// detail's trace is what says which observation this statement made.
	if len(repository.audits) != 1 || repository.audits[0].TargetID != held.ID {
		t.Errorf("State() recorded %+v, want a statement about the fact in force", repository.audits)
	}
}

func TestWritingDownNothingIsRefused(t *testing.T) {
	service, repository := newFakeService(t, gateOptions())

	for _, test := range []struct {
		name    string
		request StateRequest
	}{
		{"a statement that is blank", StateRequest{OwnerID: 7, Statement: "  ", Actor: ActorUser}},
		{"no account", StateRequest{Statement: "我住在杭州", Actor: ActorUser}},
		{"nobody asking", StateRequest{OwnerID: 7, Statement: "我住在杭州"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.State(context.Background(), test.request); !errors.Is(err, ErrInvalidRequest) {
				t.Errorf("State() = %v, want a refusal", err)
			}
		})
	}
	if len(repository.traces) != 0 || len(repository.facts) != 0 || len(repository.audits) != 0 {
		t.Errorf("a refused statement reached storage: %+v", repository)
	}
}

// ptr is how a case says "change this field" to an edit, where a nil means "leave
// it alone".
func ptr(value string) *string { return &value }
