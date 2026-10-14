package memory

// What Synthesize is as a function of its arguments.
//
// These are the cases that read the function rather than a service: the same facts
// handed over in a different order come back the same synthesis, a fact out of
// force cannot be shown however it arrived, and each knob §5 ships does what it
// says — including on the edges, which is where a window and a bound are actually
// defined.
//
// They are apart from synthesis_test.go because they ask a different question. That
// file asks what the service hands its reader, with a fake repository standing in
// for storage; here the input is a slice of facts the case wrote, and two of the
// cases never build a service at all. The fixtures both files read — aSaidFact,
// frozenAt and the two admission dates — still live in synthesis_test.go, which is
// where the service-shaped cases that need them most are.

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"
)

func TestASynthesisIsAFunctionOfItsInputsAlone(t *testing.T) {
	facts := []Fact{
		aSaidFact("mf_a", SurfaceMastery, "learning", ConfirmerGate, admittedJuly),
		aSaidFact("mf_b", SurfaceReading, "", ConfirmerGate, admittedSeptember),
		aSaidFact("mf_c", SurfaceMastery, "learning", ConfirmerUser, atNoon),
		aSaidFact("mf_d", SurfacePractice, "learning", ConfirmerGate, atNoon),
	}
	options := gateOptions()

	// The same facts, handed over in three orders, including the one an engine is
	// most likely to return — nothing in particular.
	reversed := slices.Clone(facts)
	slices.Reverse(reversed)
	shuffled := []Fact{facts[3], facts[1], facts[0], facts[2]}

	want := Synthesize(7, facts, atNoon, options)
	for _, test := range []struct {
		name  string
		input []Fact
	}{
		{"in the order they were written", facts},
		{"backwards", reversed},
		{"in no order at all", shuffled},
	} {
		if got := Synthesize(7, test.input, atNoon, options); !reflect.DeepEqual(got, want) {
			t.Errorf("Synthesize() given the facts %s = %+v, want the same synthesis", test.name, got)
		}
	}
	// The order inside a section is the facts' own, not the input's.
	if got := factIDs(want.Recent); !slices.Equal(got, []string{"mf_d", "mf_c", "mf_b"}) {
		t.Errorf("Recent = %v, want newest first with the tie broken by id", got)
	}

	// And the same answer comes out of a different service holding the same facts,
	// which is the other half of "the facts are the only input": there is no state
	// on a service for two of them to disagree about.
	first, firstRepository := frozenAt(t, atNoon, options)
	second, secondRepository := frozenAt(t, atNoon, options)
	for _, fact := range facts {
		firstRepository.withFact(fact)
	}
	for _, fact := range reversed {
		secondRepository.withFact(fact)
	}
	fromFirst, err := first.Synthesis(context.Background(), 7)
	if err != nil {
		t.Fatalf("Synthesis() error = %v", err)
	}
	fromSecond, err := second.Synthesis(context.Background(), 7)
	if err != nil {
		t.Fatalf("Synthesis() error = %v", err)
	}
	if !reflect.DeepEqual(fromFirst, fromSecond) {
		t.Errorf("two services over the same facts disagree:\n%+v\n%+v", fromFirst, fromSecond)
	}
}

// TestASynthesisWillNotShowAFactOutOfForce holds the function itself to the rule
// the service enforces in its read.
//
// Synthesize is reachable with any list of facts, including all of L2 — which is
// what the export has, and what a caller that read a table directly would have. A
// fact the account has taken back cannot be shown because its reader forgot to
// filter; the filter is here because this is where "what memory looks like now"
// is decided.
func TestASynthesisWillNotShowAFactOutOfForce(t *testing.T) {
	facts := []Fact{
		aSaidFact("mf_in_force", SurfaceMastery, "learning", ConfirmerGate, atNine),
		aFact("mf_taken_back", 7, StatusRetracted, atTen),
		aFact("mf_merged_away", 7, StatusSuperseded, atEleven),
	}
	// A fact the user stated themselves and later took back is the sharpest case:
	// it belongs in Preferences on one column and in nothing on the other.
	takenBack := aSaidFact("mf_mine", SurfaceUser, "", ConfirmerUser, atNoon)
	takenBack.Status = StatusRetracted
	facts = append(facts, takenBack)

	synthesis := Synthesize(7, facts, atNoon, gateOptions())

	if got := factIDs(synthesis.Recent); !slices.Equal(got, []string{"mf_in_force"}) {
		t.Errorf("Recent = %v, want only the fact in force", got)
	}
	if got := factIDs(synthesis.Preferences); len(got) != 0 {
		t.Errorf("Preferences = %v, want nothing: the user taking it back outranks the user saying it", got)
	}
	if got := scopeNames(synthesis); !slices.Equal(got, []string{"learning"}) {
		t.Errorf("Scopes = %v, want only the scope of the fact in force", got)
	}
	if got := profileFacts(synthesis); got != 0 {
		t.Errorf("Profile shows %d facts, want none: the fact in force is inside the window", got)
	}
}

func TestASynthesisWindowIsWhatMakesAFactRecent(t *testing.T) {
	// Noon on the first of October less thirty days is noon on the first of
	// September, and the three facts below sit a second before it, on it, and an
	// hour after it.
	opens := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	facts := []Fact{
		aSaidFact("mf_before", SurfaceMastery, "learning", ConfirmerGate, opens.Add(-time.Second)),
		aSaidFact("mf_on_the_edge", SurfaceMastery, "learning", ConfirmerGate, opens),
		aSaidFact("mf_after", SurfaceMastery, "learning", ConfirmerGate, opens.Add(time.Hour)),
	}

	for _, test := range []struct {
		name    string
		window  int
		recent  []string
		settled []string
	}{
		// The window is "within the last N days", so the edge is inside it: a fact
		// admitted exactly N days ago is recent, which is the reading that cannot
		// make a fact of the account's disappear by a second.
		{"the shipped thirty days", 30,
			[]string{"mf_after", "mf_on_the_edge"}, []string{"mf_before"}},
		// A shorter window moves a fact out of Recent and into Profile without any
		// fact changing, because the window is an input and not a property of a fact.
		{"a week", 7, []string{}, []string{"mf_after", "mf_on_the_edge", "mf_before"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			synthesis := Synthesize(7, facts, atNoon, withWindow(gateOptions(), test.window))

			if synthesis.WindowDays != test.window {
				t.Errorf("the synthesis reports a window of %d days", synthesis.WindowDays)
			}
			if got := factIDs(synthesis.Recent); !slices.Equal(got, test.recent) {
				t.Errorf("Recent = %v, want %v", got, test.recent)
			}
			if got := profileFactIDs(synthesis); !slices.Equal(got, test.settled) {
				t.Errorf("Profile = %v, want %v", got, test.settled)
			}
			// Whatever the window, the two sections are the whole of what is shown —
			// which is what makes them a partition rather than two filters.
			if shown := len(synthesis.Recent) + profileFacts(synthesis); shown != len(facts) {
				t.Errorf("the two sections show %d facts, want all %d", shown, len(facts))
			}
		})
	}
}

func TestASynthesisKeepsTheNewestFewPerSurface(t *testing.T) {
	options := withFactsPerSurface(gateOptions(), 2)
	mastery := func(day int) time.Time {
		return time.Date(2026, 8, day, 9, 0, 0, 0, time.UTC)
	}

	service, repository := frozenAt(t, atNoon, options)
	repository.
		withFact(aSaidFact("mf_1", SurfaceMastery, "learning", ConfirmerGate, mastery(1))).
		withFact(aSaidFact("mf_2", SurfaceMastery, "learning", ConfirmerGate, mastery(2))).
		withFact(aSaidFact("mf_3", SurfaceMastery, "learning", ConfirmerGate, mastery(3))).
		withFact(aSaidFact("mf_4", SurfaceMastery, "learning", ConfirmerGate, mastery(4))).
		// A second surface with a fact of its own, so that the bound is read as a
		// bound per surface rather than a limit on the synthesis.
		withFact(aSaidFact("mf_reading", SurfaceReading, "learning", ConfirmerGate, mastery(1)))

	synthesis, err := service.Synthesis(context.Background(), 7)
	if err != nil {
		t.Fatalf("Synthesis() error = %v", err)
	}

	if got := profileSurfaces(synthesis); !slices.Equal(got, []Surface{SurfaceMastery, SurfaceReading}) {
		t.Fatalf("Profile covers %v, want both surfaces that contributed", got)
	}
	// Mastery said four things and is shown two: the newest two, with the two it
	// said longest ago gone — and the count of them in the row, because a bound
	// nobody can see would make a bounded synthesis read as the whole picture.
	masteryRow := synthesis.Profile[0]
	if got := factIDs(masteryRow.Facts); !slices.Equal(got, []string{"mf_4", "mf_3"}) {
		t.Errorf("the mastery profile holds %v, want its two newest facts", got)
	}
	if masteryRow.Truncated != 2 {
		t.Errorf("the mastery profile reports %d left out, want the two oldest", masteryRow.Truncated)
	}
	// Reading said one thing and keeps it: the bound is not a budget shared between
	// surfaces.
	readingRow := synthesis.Profile[1]
	if got := factIDs(readingRow.Facts); !slices.Equal(got, []string{"mf_reading"}) {
		t.Errorf("the reading profile holds %v, want the one fact it has", got)
	}
	if readingRow.Truncated != 0 {
		t.Errorf("the reading profile reports %d left out, want none", readingRow.Truncated)
	}
	// The dropped facts are dropped everywhere, not just from the section that
	// reports them: the sections are regroupings of one set.
	if got := scopeNames(synthesis); !slices.Equal(got, []string{"learning"}) {
		t.Fatalf("Scopes = %v, want the one scope", got)
	}
	if got := factIDs(synthesis.Scopes[0].Facts); !slices.Equal(got, []string{"mf_4", "mf_3", "mf_reading"}) {
		t.Errorf("the scope group holds %v, want the facts the bound kept", got)
	}
}

// TestASynthesisBoundIsReportedByTheSurfaceItCut pins the corner the per-surface
// section has to carry: a surface whose facts are all inside the window still
// reports what the bound took, because the row is that surface's line in the
// synthesis rather than a summary of what has settled.
func TestASynthesisBoundIsReportedByTheSurfaceItCut(t *testing.T) {
	service, repository := frozenAt(t, atNoon, withFactsPerSurface(gateOptions(), 2))
	repository.
		withFact(aSaidFact("mf_1", SurfaceMastery, "learning", ConfirmerGate, atNine)).
		withFact(aSaidFact("mf_2", SurfaceMastery, "learning", ConfirmerGate, atTen)).
		withFact(aSaidFact("mf_3", SurfaceMastery, "learning", ConfirmerGate, atEleven))

	synthesis, err := service.Synthesis(context.Background(), 7)
	if err != nil {
		t.Fatalf("Synthesis() error = %v", err)
	}
	if got := factIDs(synthesis.Recent); !slices.Equal(got, []string{"mf_3", "mf_2"}) {
		t.Errorf("Recent = %v, want the two the bound kept", got)
	}
	if len(synthesis.Profile) != 1 || synthesis.Profile[0].Surface != SurfaceMastery {
		t.Fatalf("Profile = %+v, want a row for the surface the bound cut", synthesis.Profile)
	}
	if facts, dropped := len(synthesis.Profile[0].Facts), synthesis.Profile[0].Truncated; facts != 0 || dropped != 1 {
		t.Errorf("the row reports %d facts and %d left out, want the count and nothing to show", facts, dropped)
	}
}
