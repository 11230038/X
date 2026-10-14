package memory

// What a cleanup does in two runs, and what it refuses to do.
//
// The repository is the fake, so what these cases read is the service's decisions:
// which facts the query names, that a preview writes nothing at all, that the
// commit is handed exactly the batch the preview named, that a cleanup which finds
// nothing leaves no line, and that a cutoff is required rather than defaulted.
// Whether the rows such a purge removes are the rows the engine then drops — the
// evidence under them included — is repository_lifecycle_db_test.go's question.

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

var (
	august = time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	// midSeptember is the cutoff the cases below clean up to: it falls between the
	// facts of August and September and the ones of October, which is the ordinary
	// shape of "forget everything from before then".
	midSeptember = time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
)

// aCleanupUpTo is the query the cases spell out once.
func aCleanupUpTo(before time.Time) CleanupQuery {
	return CleanupQuery{OwnerID: 7, Before: before, Actor: ActorUser}
}

// memoryOfThreeSurfaces is one account with facts admitted in three months, one of
// them another account's, one of them already taken back.
func memoryOfThreeSurfaces(t *testing.T) (*Service, *fakeRepository) {
	t.Helper()
	service, repository := newFakeService(t, gateOptions())
	repository.
		withFact(aFactOn("mf_august", 7, SurfaceMastery, StatusActive, august)).
		withFact(aFactOn("mf_september", 7, SurfaceReading, StatusActive, time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC))).
		withFact(aFactOn("mf_october", 7, SurfaceMastery, StatusActive, time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))).
		withFact(aFactOn("mf_theirs", 8, SurfaceMastery, StatusActive, august)).
		withTrace(Trace{ID: "mt_august", OwnerID: 7, Surface: SurfaceMastery, Kind: "goal",
			Statement: statement, Fingerprint: Fingerprint(statement), CreatedAt: august}).
		withTrace(Trace{ID: "mt_october", OwnerID: 7, Surface: SurfaceMastery, Kind: "goal",
			Statement: statement, Fingerprint: Fingerprint(statement), CreatedAt: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}).
		withSource("mf_august", "mt_august").
		withSource("mf_october", "mt_october")
	return service, repository
}

func aFactOn(id string, owner int64, surface Surface, status Status, admitted time.Time) Fact {
	return Fact{
		ID: id, OwnerID: owner, Surface: surface, Statement: statement,
		Fingerprint: Fingerprint(statement), Confidence: 80, Status: status,
		ConfirmedBy: ConfirmerGate, Version: 1, CreatedAt: admitted,
	}
}

func TestACleanupPreviewNamesTheBatchAndRemovesNothing(t *testing.T) {
	service, repository := memoryOfThreeSurfaces(t)

	preview, err := service.Cleanup(context.Background(), CleanupQuery{
		OwnerID: 7, Before: midSeptember, Actor: ActorUser, DryRun: true,
	})
	if err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if !preview.DryRun || preview.Purged != 0 {
		t.Errorf("a preview reported %+v, want nothing purged", preview)
	}
	// Most recently changed first, which is the listing's order and therefore the
	// order the caller reads the batch in.
	if got := factIDs(preview.Facts); !slices.Equal(got, []string{"mf_september", "mf_august"}) {
		t.Errorf("the preview named %v, want the two facts admitted before the cutoff", got)
	}
	if len(repository.facts) != 4 {
		t.Errorf("a preview left %d facts, want all four", len(repository.facts))
	}
	if len(repository.audits) != 0 {
		t.Errorf("a preview wrote %+v", repository.audits)
	}
	if len(repository.purged) != 0 {
		t.Errorf("a preview purged %v", repository.purged)
	}
}

func TestACleanupNarrowsBySurfaceAndByStatus(t *testing.T) {
	service, repository := memoryOfThreeSurfaces(t)
	// One of the two facts in the window is out of force, which is what makes the
	// status filter a filter rather than a synonym for "everything old".
	for index := range repository.facts {
		if repository.facts[index].ID == "mf_september" {
			repository.facts[index].Status = StatusRetracted
		}
	}

	oneSurface, err := service.Cleanup(context.Background(), CleanupQuery{
		OwnerID: 7, Before: midSeptember, Surface: SurfaceMastery, Actor: ActorUser, DryRun: true,
	})
	if err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if got := factIDs(oneSurface.Facts); !slices.Equal(got, []string{"mf_august"}) {
		t.Errorf("the preview of one surface named %v, want the one mastery fact", got)
	}

	inForce, err := service.Cleanup(context.Background(), CleanupQuery{
		OwnerID: 7, Before: midSeptember, Statuses: []Status{StatusActive}, Actor: ActorUser, DryRun: true,
	})
	if err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if got := factIDs(inForce.Facts); !slices.Equal(got, []string{"mf_august"}) {
		t.Errorf("the preview of the facts in force named %v, want the one that still is", got)
	}
}

func TestACleanupRemovesExactlyWhatItPreviewed(t *testing.T) {
	service, repository := memoryOfThreeSurfaces(t)
	query := aCleanupUpTo(midSeptember)

	preview, err := service.Cleanup(context.Background(), withDryRun(query))
	if err != nil {
		t.Fatalf("Cleanup() preview error = %v", err)
	}
	committed, err := service.Cleanup(context.Background(), query)
	if err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}

	// The second run named the same batch as the first, and removed it. This is the
	// whole of what "the second run only touches what the report said it would"
	// means: the two runs read one query, and the purge is handed that read's ids.
	if got, want := factIDs(committed.Facts), factIDs(preview.Facts); !slices.Equal(got, want) {
		t.Errorf("the commit named %v and the preview named %v", got, want)
	}
	if committed.DryRun || committed.Purged != 2 {
		t.Errorf("the commit reported %+v, want two rows purged", committed)
	}
	if got := slices.Clone(repository.purged); len(got) != 2 {
		t.Errorf("the purge was handed %v, want the two", got)
	}
	if got := factIDs(repository.facts); !slices.Equal(got, []string{"mf_october", "mf_theirs"}) {
		t.Errorf("storage holds %v, want the October fact and the other account's", got)
	}
	// Another account's fact is inside the window and stays: the owner is part of
	// the purge's WHERE clause, not a check afterwards.
	if !slices.Contains(factIDs(repository.facts), "mf_theirs") {
		t.Error("a cleanup removed another account's fact")
	}

	// The evidence of a purged fact goes with it; the evidence of a fact that
	// stayed does not. Anything else would leave a chain pointing at a row that is
	// not there, or take away the support of a fact that is.
	if evidence := repository.evidenceOf("mf_august"); len(evidence) != 0 {
		t.Errorf("the purged fact still owns the evidence %v", evidence)
	}
	if evidence := repository.evidenceOf("mf_october"); !slices.Equal(evidence, []string{"mt_october"}) {
		t.Errorf("the surviving fact owns %v, want the observation it rests on", evidence)
	}
	// And the observation itself is still in L1: a purge removes what the system
	// believed, not what a surface said (PRD §11.3 makes L1 append-only).
	if len(repository.traces) != 2 {
		t.Errorf("a purge left %d observations, want both", len(repository.traces))
	}

	if len(repository.audits) != 1 {
		t.Fatalf("the commit wrote %d audit lines, want one for the batch", len(repository.audits))
	}
	line := repository.audits[0]
	if line.Action != ActionFactPurged || line.TargetKind != TargetMemory || line.TargetID != "" {
		t.Errorf("the commit recorded %+v, want one line about the batch", line)
	}
	// The ids are in the row because the rows are not: after a purge, the trail is
	// the only place they survive.
	want := `{"before":"2026-09-15T00:00:00Z","surface":"","statuses":[],"facts":["mf_september","mf_august"],"purged":2}`
	if !sameJSON(t, line.Detail, want) {
		t.Errorf("the commit recorded the detail %s", line.Detail)
	}
}

func TestACleanupThatFindsNothingWritesNoLine(t *testing.T) {
	service, repository := memoryOfThreeSurfaces(t)

	report, err := service.Cleanup(context.Background(), aCleanupUpTo(august))
	if err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	// Nothing was admitted before the cutoff, so there was nothing to clean — and a
	// line saying a cleanup ran would be a line about an action that did not happen.
	if len(report.Facts) != 0 || report.Purged != 0 {
		t.Errorf("a cleanup with nothing to do reported %+v", report)
	}
	if len(repository.audits) != 0 {
		t.Errorf("a cleanup with nothing to do wrote %+v", repository.audits)
	}
	if len(repository.facts) != 4 {
		t.Errorf("a cleanup with nothing to do removed %d facts", 4-len(repository.facts))
	}
}

func TestACleanupNeedsACutoffAndSomebodyToAsk(t *testing.T) {
	service, repository := memoryOfThreeSurfaces(t)

	for _, test := range []struct {
		name  string
		query CleanupQuery
	}{
		{"no cutoff", CleanupQuery{OwnerID: 7, Actor: ActorUser}},
		// A cutoff past the present bounds nothing, so it is not a bound: the caller
		// who wrote it meant something else, and a cleanup is the wrong place to
		// guess.
		{"a cutoff in the future",
			CleanupQuery{OwnerID: 7, Before: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), Actor: ActorUser}},
		{"nobody asking", CleanupQuery{OwnerID: 7, Before: midSeptember}},
		{"no account", CleanupQuery{Before: midSeptember, Actor: ActorUser}},
		{"a surface that is not one",
			CleanupQuery{OwnerID: 7, Before: midSeptember, Surface: "telepathy", Actor: ActorUser}},
		{"a status that is not one",
			CleanupQuery{OwnerID: 7, Before: midSeptember, Statuses: []Status{"deleted"}, Actor: ActorUser}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.Cleanup(context.Background(), test.query); !errors.Is(err, ErrInvalidRequest) {
				t.Errorf("Cleanup() = %v, want a refusal", err)
			}
		})
	}
	if len(repository.facts) != 4 || len(repository.audits) != 0 || len(repository.purged) != 0 {
		t.Errorf("a refused cleanup reached storage: %+v", repository)
	}
}

func TestACleanupReportsStorageThatFailed(t *testing.T) {
	service, repository := memoryOfThreeSurfaces(t)
	repository.purgeFactsErr = errors.New("the disk is on fire")

	if _, err := service.Cleanup(context.Background(), aCleanupUpTo(midSeptember)); err == nil {
		t.Fatal("Cleanup() reported success over a purge that failed")
	}
	// The facts are still there, and no line claims they are not.
	if len(repository.facts) != 4 {
		t.Errorf("a failed purge left %d facts, want all four", len(repository.facts))
	}
	if len(repository.audits) != 0 {
		t.Errorf("a failed purge wrote %+v", repository.audits)
	}
}

func withDryRun(query CleanupQuery) CleanupQuery {
	query.DryRun = true
	return query
}
