package memory

// What an export hands over, and what it leaves behind in doing so.
//
// The requirement being checked is PRD §11.3's: the L2 → L1 link has to survive the
// export, which is to say a fact must not arrive without the observations it was
// admitted on. The rest of the cases are about the other two parts — the
// observations that support nothing, and the trail — and about the row the export
// itself leaves.

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestAnExportHandsOverTheGraphWithItsEvidence(t *testing.T) {
	service, repository, held := heldFact(t)
	// A second fact nobody corroborated, so that the export has an observation that
	// supports a fact and an observation that does not.
	loose := "用户想先学并发"
	repository.
		withTrace(Trace{ID: "mt_held", OwnerID: 7, Surface: SurfaceMastery, Kind: "goal",
			Statement: statement, Fingerprint: Fingerprint(statement), CreatedAt: august}).
		withTrace(Trace{ID: "mt_loose", OwnerID: 7, Surface: SurfaceReading, Kind: "goal",
			Statement: loose, Fingerprint: Fingerprint(loose), CreatedAt: august}).
		withSource(held.ID, "mt_held")

	exported, err := service.Export(context.Background(), ExportRequest{OwnerID: 7, Actor: ActorUser})
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if exported.OwnerID != 7 || exported.ExportedAt.IsZero() {
		t.Errorf("Export() = %+v, want it to say whose memory and when", exported)
	}
	if len(exported.Facts) != 1 {
		t.Fatalf("Export() handed over %d facts, want the one", len(exported.Facts))
	}
	// MEM-01's link, which is the whole of why a fact and its evidence are one
	// field: the fact travels with the observations it was admitted on.
	if evidence := exported.Facts[0].Traces; len(evidence) != 1 || evidence[0].ID != "mt_held" {
		t.Errorf("the exported fact arrived with %+v, want the observation it rests on", evidence)
	}
	// And L1 in full, the observation that supports nothing included: "the system
	// heard this and does not believe it" is a state the user has to be able to see,
	// and an export is where they see all of it.
	if ids := traceIDs(exported.Observations); !slices.Equal(ids, []string{"mt_loose", "mt_held"}) {
		t.Errorf("the export carried the observations %v, want both", ids)
	}

	if len(repository.audits) != 1 {
		t.Fatalf("Export() wrote %d audit lines, want one", len(repository.audits))
	}
	line := repository.audits[0]
	if line.Action != ActionMemoryExported || line.TargetKind != TargetMemory || line.TargetID != "" {
		t.Errorf("Export() recorded %+v, want one line about the whole memory", line)
	}
	if !sameJSON(t, line.Detail, `{"facts":1,"observations":2,"actions":0}`) {
		t.Errorf("Export() recorded the detail %s", line.Detail)
	}
}

func TestAnExportCarriesTheFactsOutOfForceAndTheTrailThatSaysSo(t *testing.T) {
	service, _, _ := heldFact(t)
	if _, err := service.Retract(context.Background(), asked("mf_1")); err != nil {
		t.Fatalf("Retract() error = %v", err)
	}

	exported, err := service.Export(context.Background(), ExportRequest{OwnerID: 7, Actor: ActorUser})
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	// A retracted fact is a thing the system has about the account, and the export is
	// "what do you have about me" rather than "what do you believe now" — the views
	// answer the second question, and the export is not one of them.
	if len(exported.Facts) != 1 || exported.Facts[0].Fact.Status != StatusRetracted {
		t.Errorf("Export() carried %+v, want the fact that was taken back", exported.Facts)
	}
	// The trail is what cannot be re-derived: the fact above looks like any other
	// retracted fact, and only the trail says who took it back and when.
	if len(exported.Actions) != 1 || exported.Actions[0].Action != ActionFactRetracted {
		t.Errorf("Export() carried the trail %+v, want the retraction", exported.Actions)
	}
}

func TestAnExportDoesNotContainItsOwnLine(t *testing.T) {
	service, repository, _ := heldFact(t)

	first, err := service.Export(context.Background(), ExportRequest{OwnerID: 7, Actor: ActorUser})
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	second, err := service.Export(context.Background(), ExportRequest{OwnerID: 7, Actor: ActorUser})
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	// The first export describes the memory before anything had asked for it, and
	// the second describes it after the first asked. So the second carries one line
	// the first did not — its own exclusion is not that the trail is left out, it is
	// that the row is written after the trail was read.
	if len(first.Actions) != 0 {
		t.Errorf("the first export carried %+v, want no actions before it", first.Actions)
	}
	if len(second.Actions) != 1 || second.Actions[0].Action != ActionMemoryExported {
		t.Errorf("the second export carried %+v, want the first export's line", second.Actions)
	}
	if len(repository.audits) != 2 {
		t.Errorf("two exports wrote %d audit lines, want two", len(repository.audits))
	}
}

func TestAnExportSaysWhoAsked(t *testing.T) {
	service, repository, _ := heldFact(t)

	for _, test := range []struct {
		name    string
		request ExportRequest
	}{
		{"no account", ExportRequest{Actor: ActorUser}},
		{"nobody asking", ExportRequest{OwnerID: 7}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.Export(context.Background(), test.request); !errors.Is(err, ErrInvalidRequest) {
				t.Errorf("Export() = %v, want a refusal", err)
			}
		})
	}
	if len(repository.audits) != 0 {
		t.Errorf("a refused export wrote %+v", repository.audits)
	}
}

// traceIDs reads a list of observations as their ids, in the order they came.
func traceIDs(traces []Trace) []string {
	ids := make([]string, 0, len(traces))
	for _, trace := range traces {
		ids = append(ids, trace.ID)
	}
	return ids
}
