package memory

// What the service decides: that an observation is always written down, that
// only some of them are believed, that believing something twice is evidence
// rather than a second fact, and that a merge moves the evidence instead of
// copying or dropping it.
//
// The repository here is the fake, which is a record of what the service asked
// for. Whether the rows it asked for are the rows the engine then keeps is the
// other file's question.

import (
	"context"
	"errors"
	"testing"
	"time"
)

// steppedClock returns times a second apart, so that two rows written in one
// test have an order rather than a tie: creat_time is a DATETIME of whole
// seconds and a test that ran fast would produce two rows in the same one.
func steppedClock() func() time.Time {
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	return func() time.Time {
		at = at.Add(time.Second)
		return at
	}
}

func newFakeService(t *testing.T, options Options) (*Service, *fakeRepository) {
	t.Helper()
	repository := newFakeRepository()
	service, err := NewService(repository, options)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	service.clock = steppedClock()
	return service, repository
}

func TestANewServiceNeedsWhatItCannotDoWithout(t *testing.T) {
	options := gateOptions()
	for _, test := range []struct {
		name       string
		repository Repository
		options    Options
	}{
		{name: "no repository", options: options},
		{name: "no repository and no options"},
		{name: "a confidence threshold above a hundred",
			repository: newFakeRepository(), options: withThreshold(options, 101)},
		{name: "a negative confidence threshold",
			repository: newFakeRepository(), options: withThreshold(options, -1)},
		{name: "no corroboration required", repository: newFakeRepository(),
			options: withCorroboration(options, 0)},
		{name: "no surface required", repository: newFakeRepository(),
			options: withSurfaces(options, 0)},
		{name: "no synthesis window", repository: newFakeRepository(),
			options: withWindow(options, 0)},
		{name: "no per-surface bound", repository: newFakeRepository(),
			options: withFactsPerSurface(options, 0)},
		{name: "an empty kind in the sensitive list", repository: newFakeRepository(),
			options: withSensitiveKinds(options, "preference", "")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewService(test.repository, test.options); err == nil {
				t.Error("NewService() accepted a service it cannot run")
			}
		})
	}
}

func TestAnObservationThatClearsTheGateBecomesAFact(t *testing.T) {
	service, repository := newFakeService(t, gateOptions())
	statement := "对方正在学 Go"
	// What the reading surface already reported, in a turn of its own.
	repository.withTrace(Trace{
		ID: "mt_reading", OwnerID: 7, Surface: SurfaceReading, Kind: "goal",
		TurnID: "turn_reading", Statement: statement, Fingerprint: Fingerprint(statement),
		CreatedAt: time.Date(2026, 10, 1, 11, 0, 0, 0, time.UTC),
	})

	observed, err := service.Observe(context.Background(), Observation{
		OwnerID: 7, Surface: SurfaceMastery, Kind: "goal", TurnID: "turn_mastery",
		Statement: statement, Confidence: 80, Scope: "learning",
	})
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if !observed.Admitted || observed.Proposed {
		t.Errorf("Observe() = %+v, want an admitted observation", observed)
	}

	if len(repository.facts) != 1 {
		t.Fatalf("Observe() left %d facts, want 1", len(repository.facts))
	}
	fact := repository.facts[0]
	if fact.ID != observed.FactID {
		t.Errorf("Observe() reported fact %s and wrote %s", observed.FactID, fact.ID)
	}
	if fact.Status != StatusActive || fact.ConfirmedBy != ConfirmerGate || fact.Version != 1 {
		t.Errorf("Observe() wrote %+v, want an active fact at version 1 confirmed by the gate", fact)
	}
	if fact.Confidence != 80 {
		t.Errorf("Observe() wrote confidence %d, want the admitting observation's 80", fact.Confidence)
	}
	if fact.Scope != "learning" {
		t.Errorf("Observe() wrote scope %q, want the one the observation named", fact.Scope)
	}
	// MEM-01: the fact rests on *every* observation of the statement, the one
	// that admitted it included. A chain with only the last voice in it would
	// answer "why does the system think this" with one side of the story.
	if evidence := repository.evidenceOf(fact.ID); len(evidence) != 2 {
		t.Errorf("Observe() supported the fact with %v, want both observations", evidence)
	}
}

func TestAnObservationThatDoesNotClearTheGateStaysInL1(t *testing.T) {
	service, repository := newFakeService(t, gateOptions())

	observed, err := service.Observe(context.Background(), Observation{
		OwnerID: 7, Surface: SurfaceMastery, Kind: "goal", TurnID: "turn_1",
		Statement: "对方正在学 Go", Confidence: 90,
	})
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if observed.Admitted || !observed.Proposed {
		t.Errorf("Observe() = %+v, want a proposed observation", observed)
	}
	if len(repository.facts) != 0 {
		t.Errorf("Observe() wrote %d facts for one observation", len(repository.facts))
	}
	// Not believed is not unheard: the row is what MEM-03's second state is made
	// of, and the interface can only show it if it exists.
	if len(repository.traces) != 1 || repository.traces[0].ID != observed.TraceID {
		t.Errorf("Observe() left the traces %+v", repository.traces)
	}
	if repository.traces[0].Fingerprint != Fingerprint("对方正在学 Go") {
		t.Errorf("Observe() stored the fingerprint %q", repository.traces[0].Fingerprint)
	}
}

func TestAnObservationOfSomethingAlreadyBelievedJoinsItsEvidence(t *testing.T) {
	service, repository := newFakeService(t, gateOptions())
	statement := "对方正在学 Go"
	held := Fact{
		ID: "mf_held", OwnerID: 7, Surface: SurfaceMastery, Statement: statement,
		Fingerprint: Fingerprint(statement), Confidence: 80, Status: StatusActive,
		ConfirmedBy: ConfirmerGate, Version: 1, CreatedAt: time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC),
	}
	repository.withFact(held)

	observed, err := service.Observe(context.Background(), Observation{
		OwnerID: 7, Surface: SurfaceReading, Kind: "goal", TurnID: "turn_2",
		Statement: statement, Confidence: 30,
	})
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if !observed.Admitted || observed.FactID != held.ID {
		t.Errorf("Observe() = %+v, want it joined %s", observed, held.ID)
	}
	if len(repository.facts) != 1 {
		t.Errorf("Observe() left %d facts, want the one that was already there", len(repository.facts))
	}
	if evidence := repository.evidenceOf(held.ID); len(evidence) != 1 || evidence[0] != observed.TraceID {
		t.Errorf("Observe() gave the fact the evidence %v, want the new observation", evidence)
	}
	// A confidence of 30 would not have admitted anything on its own. It does not
	// need to: the claim is settled, and this observation is one more thing that
	// was said about it.
	if observed.TraceID == "" {
		t.Error("Observe() reported no trace id")
	}
}

func TestObserveWillNotReachIntoAnotherAccountsMemory(t *testing.T) {
	service, repository := newFakeService(t, gateOptions())
	statement := "对方正在学 Go"
	repository.withFact(Fact{
		ID: "mf_theirs", OwnerID: 8, Surface: SurfaceMastery, Statement: statement,
		Fingerprint: Fingerprint(statement), Confidence: 80, Status: StatusActive,
		ConfirmedBy: ConfirmerGate, Version: 1,
	})

	_, err := service.Observe(context.Background(), Observation{
		OwnerID: 7, Surface: SurfaceMastery, Kind: "goal", TurnID: "turn_1",
		Statement: statement, Confidence: 90,
	})
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	// Account 8's fact is not account 7's evidence, and account 7's observation
	// does not corroborate it either.
	if evidence := repository.evidenceOf("mf_theirs"); len(evidence) != 0 {
		t.Errorf("Observe() attached account 7's observation to account 8's fact: %v", evidence)
	}
	if len(repository.facts) != 1 {
		t.Errorf("Observe() left %d facts, want only account 8's", len(repository.facts))
	}
}

func TestTheUserWritingSomethingDownSkipsTheGate(t *testing.T) {
	service, repository := newFakeService(t, gateOptions())

	observed, err := service.Observe(context.Background(), Observation{
		OwnerID: 7, Surface: SurfaceUser, Kind: KindUserStated,
		Statement: "我住在杭州", Confidence: 100,
	})
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if !observed.Admitted {
		t.Fatalf("Observe() = %+v, want the user's own word admitted", observed)
	}
	fact := repository.facts[0]
	if fact.ConfirmedBy != ConfirmerUser || fact.Status != StatusActive {
		t.Errorf("Observe() wrote %+v, want an active fact confirmed by the user", fact)
	}
	if fact.Surface != SurfaceUser {
		t.Errorf("Observe() recorded the surface %q, want the user's", fact.Surface)
	}
}

func TestObserveWritesNothingItCannotStore(t *testing.T) {
	service, repository := newFakeService(t, gateOptions())

	_, err := service.Observe(context.Background(), Observation{
		OwnerID: 0, Surface: SurfaceMastery, Kind: "goal", Statement: "对方正在学 Go",
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Observe() error = %v, want a refusal", err)
	}
	if len(repository.traces) != 0 || len(repository.facts) != 0 {
		t.Errorf("a refused observation reached storage: %d traces, %d facts",
			len(repository.traces), len(repository.facts))
	}
}

func TestObserveReportsStorageThatFailed(t *testing.T) {
	service, repository := newFakeService(t, gateOptions())
	repository.appendTraceErr = errors.New("the disk is on fire")

	_, err := service.Observe(context.Background(), Observation{
		OwnerID: 7, Surface: SurfaceMastery, Kind: "goal", TurnID: "turn_1",
		Statement: "对方正在学 Go", Confidence: 90,
	})
	if err == nil {
		t.Fatal("Observe() reported success over a storage failure")
	}
	if len(repository.facts) != 0 {
		t.Errorf("Observe() wrote a fact after the trace failed: %+v", repository.facts)
	}
}

func TestAFactReadsWithItsEvidenceAndOnlyItsOwners(t *testing.T) {
	service, repository := newFakeService(t, gateOptions())
	statement := "对方正在学 Go"
	repository.
		withFact(Fact{ID: "mf_1", OwnerID: 7, Statement: statement,
			Fingerprint: Fingerprint(statement), Status: StatusActive, Version: 1}).
		withTrace(Trace{ID: "mt_1", OwnerID: 7, Statement: statement, Fingerprint: Fingerprint(statement),
			CreatedAt: time.Date(2026, 10, 1, 11, 0, 0, 0, time.UTC)}).
		withTrace(Trace{ID: "mt_2", OwnerID: 7, Statement: statement, Fingerprint: Fingerprint(statement),
			CreatedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}).
		withSource("mf_1", "mt_1").
		withSource("mf_1", "mt_2")

	read, err := service.Fact(context.Background(), 7, "mf_1")
	if err != nil {
		t.Fatalf("Fact() error = %v", err)
	}
	if len(read.Traces) != 2 {
		t.Fatalf("Fact() came back with %d observations, want the whole chain", len(read.Traces))
	}
	// Oldest first: a chain read newest first reads as a pile, and what the chain
	// is for is the order the system came to believe something.
	if read.Traces[0].ID != "mt_1" {
		t.Errorf("Fact() listed the evidence %v, want the oldest first", read.Traces)
	}

	if _, err := service.Fact(context.Background(), 8, "mf_1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Fact() of another account's fact = %v, want not found", err)
	}
	if _, err := service.Fact(context.Background(), 7, "  "); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("Fact() of a blank id = %v, want a refusal", err)
	}
}

func TestListingsRefuseAQuestionAboutNobody(t *testing.T) {
	service, _ := newFakeService(t, gateOptions())
	if _, err := service.Traces(context.Background(), TraceQuery{}); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("Traces() with no owner = %v, want a refusal", err)
	}
	if _, err := service.Facts(context.Background(), FactQuery{OwnerID: 7, Statuses: []Status{"deleted"}}); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("Facts() filtered by a status that is not one = %v, want a refusal", err)
	}
	if _, err := service.Traces(context.Background(), TraceQuery{OwnerID: 7, Surface: "telepathy"}); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("Traces() filtered by a surface that is not one = %v, want a refusal", err)
	}
}

// The option helpers, so that a case about one knob changes one knob.
func withThreshold(options Options, value int) Options {
	options.ConfidenceThreshold = value
	return options
}

func withCorroboration(options Options, value int) Options {
	options.MinCorroboration = value
	return options
}

func withSurfaces(options Options, value int) Options {
	options.MinSurfaces = value
	return options
}

func withWindow(options Options, value int) Options {
	options.SynthesisWindowDays = value
	return options
}

func withFactsPerSurface(options Options, value int) Options {
	options.MaxFactsPerSurface = value
	return options
}

func withSensitiveKinds(options Options, kinds ...string) Options {
	options.SensitiveKinds = kinds
	return options
}
