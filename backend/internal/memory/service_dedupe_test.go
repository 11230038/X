package memory

// The merge, which is the one thing Observe cannot prevent and the reason a
// preview exists (§2.14).
//
// Two observations of one statement arriving at once each find no fact and each
// admit one, because "one fact in force per fingerprint" is not expressible as a
// constraint here. Dedupe is the repair, and what these cases pin down is that it
// repairs rather than papers over: the evidence moves with the claim, a fact that
// moved on while the merge was running is left alone, and every run leaves a line
// in the audit trail.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// twoFactsOfOneStatement is the state a merge exists for: an account holding two
// facts that say the same thing, each with evidence of its own.
func twoFactsOfOneStatement() (*fakeRepository, string) {
	statement := "对方正在学 Go"
	fingerprint := Fingerprint(statement)
	repository := newFakeRepository().
		withFact(Fact{
			ID: "mf_old", OwnerID: 7, Surface: SurfaceMastery, Statement: statement,
			Fingerprint: fingerprint, Status: StatusActive, ConfirmedBy: ConfirmerGate,
			Version: 1, CreatedAt: time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC),
		}).
		withFact(Fact{
			ID: "mf_new", OwnerID: 7, Surface: SurfaceReading, Statement: statement,
			Fingerprint: fingerprint, Status: StatusActive, ConfirmedBy: ConfirmerGate,
			Version: 1, CreatedAt: time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC),
		}).
		withSource("mf_old", "mt_mastery").
		withSource("mf_new", "mt_reading")
	return repository, fingerprint
}

func TestAMergePreviewReportsWhatItWouldDoAndWritesNothing(t *testing.T) {
	repository, _ := twoFactsOfOneStatement()
	service, err := NewService(repository, gateOptions())
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	service.clock = steppedClock()

	report, err := service.Dedupe(context.Background(), DedupeRequest{
		OwnerID: 7, DryRun: true, Actor: ActorUser,
	})
	if err != nil {
		t.Fatalf("Dedupe() error = %v", err)
	}
	if len(report.Groups) != 1 {
		t.Fatalf("Dedupe() found %d groups, want the one pair", len(report.Groups))
	}
	group := report.Groups[0]
	if group.Survivor != "mf_old" {
		t.Errorf("Dedupe() would keep %s, want the older fact", group.Survivor)
	}
	if len(group.Superseded) != 1 || group.Superseded[0] != "mf_new" {
		t.Errorf("Dedupe() would supersede %v, want the newer fact", group.Superseded)
	}
	if report.Superseded != 0 {
		t.Errorf("a preview reported %d superseded facts, want 0", report.Superseded)
	}
	// The preview is what a caller looks at before deciding, so "nothing was
	// written" has to include the things a merge does besides the rows it shows:
	// the evidence it moves and the audit it leaves behind.
	if len(repository.moved) != 0 || len(repository.audits) != 0 {
		t.Errorf("a preview wrote: %v moved, %v audited", repository.moved, repository.audits)
	}
	for _, fact := range repository.facts {
		if fact.Status != StatusActive {
			t.Errorf("a preview left %s %s", fact.ID, fact.Status)
		}
	}
}

func TestAMergeFoldsTheEvidenceIntoTheOldestFact(t *testing.T) {
	repository, _ := twoFactsOfOneStatement()
	service, err := NewService(repository, gateOptions())
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	service.clock = steppedClock()

	report, err := service.Dedupe(context.Background(), DedupeRequest{OwnerID: 7, Actor: ActorUser})
	if err != nil {
		t.Fatalf("Dedupe() error = %v", err)
	}
	if len(report.Groups) != 1 || report.Groups[0].Survivor != "mf_old" {
		t.Fatalf("Dedupe() reported %+v, want mf_old keeping the claim", report)
	}
	if report.Superseded != 1 || report.Result != ResultDone {
		t.Errorf("Dedupe() = %+v, want one fact superseded and a done result", report)
	}
	if report.Groups[0].Superseded[0] != "mf_new" {
		t.Errorf("Dedupe() superseded %v, want the newer fact", report.Groups[0].Superseded)
	}
	// The evidence is what a merge is careful with: the observations are the same
	// observations afterwards, so the survivor has to rest on both voices and the
	// loser has to be left owning nothing.
	if evidence := repository.evidenceOf("mf_old"); len(evidence) != 2 {
		t.Errorf("the survivor rests on %v, want both facts' evidence", evidence)
	}
	if evidence := repository.evidenceOf("mf_new"); len(evidence) != 0 {
		t.Errorf("the superseded fact still owns %v", evidence)
	}
	for _, fact := range repository.facts {
		if fact.ID != "mf_new" {
			continue
		}
		if fact.Status != StatusSuperseded || fact.Version != 2 {
			t.Errorf("the superseded fact is %+v, want superseded at version 2", fact)
		}
	}
	if len(repository.audits) != 1 {
		t.Fatalf("a merge wrote %d audit rows, want one", len(repository.audits))
	}
	audit := repository.audits[0]
	if audit.Action != ActionFactMerged || audit.Actor != ActorUser || audit.TargetID != "mf_old" {
		t.Errorf("the audit row is %+v, want a merge by the user of mf_old", audit)
	}
	if !strings.Contains(string(audit.Detail), "mf_new") {
		t.Errorf("the audit detail %q does not say what was absorbed", audit.Detail)
	}
}

func TestAMergeLeavesAFactThatMovedOnAlone(t *testing.T) {
	repository, _ := twoFactsOfOneStatement()
	// Somebody edited a fact while the merge was running: the version it was read
	// at is not the version on the row any more.
	repository.saveFactErr = ErrStaleState
	service, err := NewService(repository, gateOptions())
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	service.clock = steppedClock()

	report, err := service.Dedupe(context.Background(), DedupeRequest{OwnerID: 7, Actor: ActorUser})
	if err != nil {
		t.Fatalf("Dedupe() lost a race and reported it as a failure: %v", err)
	}
	if report.Result != ResultPartial {
		t.Errorf("Dedupe() reported %q, want a partial result", report.Result)
	}
	if report.Superseded != 0 {
		t.Errorf("Dedupe() reported %d superseded facts, want 0", report.Superseded)
	}
	// "Left as it is" means the evidence too. A merge that moved the evidence and
	// then gave up would leave the fact it refused to touch standing with nothing
	// behind it.
	if len(repository.moved) != 0 {
		t.Errorf("Dedupe() moved %v while leaving the fact alone", repository.moved)
	}
	if evidence := repository.evidenceOf("mf_new"); len(evidence) != 1 {
		t.Errorf("the untouched fact owns %v, want its own evidence", evidence)
	}
	// The action still happened and is still recorded — a partial result is a
	// result, and the next run is expected to find the group again.
	if len(repository.audits) != 1 || repository.audits[0].Result != ResultPartial {
		t.Errorf("the audit rows are %+v, want one recording a partial merge", repository.audits)
	}
}

func TestAMergeIsToldWhoAsked(t *testing.T) {
	service, repository := newFakeService(t, gateOptions())

	if _, err := service.Dedupe(context.Background(), DedupeRequest{OwnerID: 7, Actor: "  "}); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("Dedupe() with no actor = %v, want a refusal", err)
	}
	_, err := service.Dedupe(context.Background(), DedupeRequest{OwnerID: 7, Actor: ActorUser})
	if err != nil {
		t.Fatalf("Dedupe() error = %v", err)
	}
	if len(repository.audits) != 0 {
		t.Errorf("a refusal wrote an audit row: %+v", repository.audits)
	}
	// The owner is the other half of the same question, and it is asked first:
	// an audit row that cannot say whose action it was is not worth writing.
	if _, err := service.Dedupe(context.Background(), DedupeRequest{Actor: ActorUser}); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("Dedupe() with no owner = %v, want a refusal", err)
	}
}

func TestAMergeThatCannotReadItsGroupsFails(t *testing.T) {
	service, repository := newFakeService(t, gateOptions())
	repository.duplicateGroupsErr = errors.New("the disk is on fire")

	if _, err := service.Dedupe(context.Background(), DedupeRequest{OwnerID: 7, Actor: ActorUser}); err == nil {
		t.Error("Dedupe() reported a merge it could not read")
	}
}
