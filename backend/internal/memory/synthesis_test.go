package memory

// What L3 is: what memory looks like now, computed rather than stored.
//
// The claims here are the ones §2.6 makes about the layer seen through a service:
// that it has no second source of truth — an edit and a retraction each change the
// very next read, because there is nowhere for an old answer to live — that its
// sections are regroupings of one set of facts and so cannot disagree with each
// other, that what the user wrote themselves is marked as such, and that a
// synthesis belongs to exactly one account.
//
// The cases about Synthesize as a function of its arguments — purity, the in-force
// filter, the window and the per-surface bound — are in synthesis_pure_test.go,
// because they ask what the function does rather than what the service hands back.
// The fixtures both files read live here.
//
// The repository is the fake, which is the right one here: what is being read is
// the service's decisions about a set of facts, and which facts a real seekdb
// hands back is the repository's own cases' question.

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

var (
	// admittedSeptember is inside the thirty-day window that reaches back from noon
	// on the first of October; admittedJuly is well outside it. Both are far enough
	// from the boundary that a case using them is about the section it names rather
	// than about an edge — the edge has a case of its own.
	admittedSeptember = time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	admittedJuly      = time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
)

// aSaidFact is one fact as a surface left it in L2: which surface said it, the
// scope the admitting observation named, who put it in force, and when.
func aSaidFact(id string, surface Surface, scope string, by Confirmer, admitted time.Time) Fact {
	return Fact{
		ID: id, OwnerID: 7, Surface: surface, Scope: scope, Statement: statement,
		Fingerprint: Fingerprint(statement), Confidence: 80, Status: StatusActive,
		ConfirmedBy: by, Version: 1, CreatedAt: admitted,
	}
}

// frozenAt is a service whose clock does not move, which is what a case about a
// window needs: "thirty days back from when" has to be a fact of the case rather
// than of how many reads happened before it.
func frozenAt(t *testing.T, at time.Time, options Options) (*Service, *fakeRepository) {
	t.Helper()
	service, repository := newFakeService(t, options)
	service.clock = func() time.Time { return at }
	return service, repository
}

func TestASynthesisIsWhatMemoryLooksLikeNow(t *testing.T) {
	service, repository := frozenAt(t, atNoon, gateOptions())
	repository.
		withFact(aSaidFact("mf_new_reading", SurfaceReading, "learning", ConfirmerGate, atTen)).
		withFact(aSaidFact("mf_new_mastery", SurfaceMastery, "learning", ConfirmerGate, atNine)).
		withFact(aSaidFact("mf_mine", SurfaceUser, "", ConfirmerUser, atEleven)).
		withFact(aSaidFact("mf_old_mastery", SurfaceMastery, "career", ConfirmerGate, admittedJuly)).
		// Two facts that are in storage and must not be in the picture: one this
		// account has taken back, and one that is another account's.
		withFact(aFact("mf_taken_back", 7, StatusRetracted, atNoon)).
		withFact(aFactOn("mf_theirs", 8, SurfaceReading, StatusActive, atNoon))

	synthesis, err := service.Synthesis(context.Background(), 7)
	if err != nil {
		t.Fatalf("Synthesis() error = %v", err)
	}
	if synthesis.OwnerID != 7 || !synthesis.ComputedAt.Equal(atNoon) || synthesis.WindowDays != 30 {
		t.Errorf("Synthesis() = %+v, want it to say whose memory, when and over what window", synthesis)
	}

	// Recent is the window's side of the partition, newest first.
	if got := factIDs(synthesis.Recent); !slices.Equal(got, []string{"mf_mine", "mf_new_reading", "mf_new_mastery"}) {
		t.Errorf("Recent = %v, want the three admitted inside the window", got)
	}
	// Profile is the other side, one row per surface — and only the surfaces that
	// have something to say, which is why reading and user are not rows.
	if got := profileSurfaces(synthesis); !slices.Equal(got, []Surface{SurfaceMastery}) {
		t.Errorf("Profile covers the surfaces %v, want only the one that has settled", got)
	}
	if got := factIDs(synthesis.Profile[0].Facts); !slices.Equal(got, []string{"mf_old_mastery"}) {
		t.Errorf("the mastery profile holds %v, want the fact admitted before the window", got)
	}
	if synthesis.Profile[0].Truncated != 0 {
		t.Errorf("the mastery profile reports %d left out, want none", synthesis.Profile[0].Truncated)
	}
	// And together the two are a partition: every fact shown is in exactly one.
	if shown := len(synthesis.Recent) + profileFacts(synthesis); shown != 4 {
		t.Errorf("Recent and Profile show %d facts between them, want the four in force", shown)
	}

	// Scopes are the same facts cut a second way, and the empty scope is a group
	// rather than an absence.
	if got := scopeNames(synthesis); !slices.Equal(got, []string{"", "career", "learning"}) {
		t.Errorf("Scopes = %v, want the scopes named, the blank one included", got)
	}
	if got := factIDs(synthesis.Scopes[2].Facts); !slices.Equal(got, []string{"mf_new_reading", "mf_new_mastery"}) {
		t.Errorf("the learning group holds %v, want both facts admitted under that scope", got)
	}

	// Preferences are the facts the user wrote down, which in this memory is one.
	if got := factIDs(synthesis.Preferences); !slices.Equal(got, []string{"mf_mine"}) {
		t.Errorf("Preferences = %v, want the fact the user stated themselves", got)
	}
}

// TestAnEditAndARetractionShowUpInTheNextSynthesis is the case behind "no second
// source of truth".
//
// If L3 were a table, the second read here would return the first answer until
// something refreshed it, and "a fact the user took back is still in what the
// system believes about them" is exactly the failure MEM-02 names. There is no
// refresh step in this case because there is nothing to refresh.
func TestAnEditAndARetractionShowUpInTheNextSynthesis(t *testing.T) {
	service, repository := frozenAt(t, atNoon, gateOptions())
	repository.withFact(aSaidFact("mf_1", SurfaceMastery, "learning", ConfirmerGate, admittedSeptember))

	before, err := service.Synthesis(context.Background(), 7)
	if err != nil {
		t.Fatalf("Synthesis() error = %v", err)
	}
	if got := scopeNames(before); !slices.Equal(got, []string{"learning"}) {
		t.Fatalf("the synthesis began with the scopes %v, want the one the fact named", got)
	}

	// The only door an edit has, and the answer moves in the same breath.
	if _, err := service.Edit(context.Background(), EditRequest{
		OwnerID: 7, FactID: "mf_1", Scope: ptr("career"), Actor: ActorUser,
	}); err != nil {
		t.Fatalf("Edit() error = %v", err)
	}
	after, err := service.Synthesis(context.Background(), 7)
	if err != nil {
		t.Fatalf("Synthesis() error = %v", err)
	}
	if got := scopeNames(after); !slices.Equal(got, []string{"career"}) {
		t.Errorf("the synthesis after the edit has the scopes %v, want the fact moved", got)
	}
	// Moved rather than copied or lost: the same one fact, in its new group.
	if got := factIDs(after.Scopes[0].Facts); !slices.Equal(got, []string{"mf_1"}) {
		t.Errorf("the group holds %v, want the one fact", got)
	}

	// And a retraction is the same statement about the next read: a fact that is
	// not in force is not read, so it cannot be shown.
	if _, err := service.Retract(context.Background(), asked("mf_1")); err != nil {
		t.Fatalf("Retract() error = %v", err)
	}
	gone, err := service.Synthesis(context.Background(), 7)
	if err != nil {
		t.Fatalf("Synthesis() error = %v", err)
	}
	if !showsNothing(gone) {
		t.Errorf("the synthesis after the retraction is %+v, want all four sections empty", gone)
	}
	// Empty because it is out of force and not because it was deleted: the fact is
	// still there to be restored, and everything stays in L1.
	held, err := repository.Fact(context.Background(), 7, "mf_1")
	if err != nil {
		t.Fatalf("Fact() error = %v", err)
	}
	if held.Status != StatusRetracted {
		t.Errorf("the fact is %q in storage, want it taken back rather than gone", held.Status)
	}
}

func TestASynthesisShowsTheFactsTheUserWrote(t *testing.T) {
	service, repository := frozenAt(t, atNoon, gateOptions())
	repository.
		withFact(aSaidFact("mf_mine_now", SurfaceUser, "", ConfirmerUser, atEleven)).
		// What the user said themselves is not subject to the window: it is the one
		// thing in memory whose authority is the user rather than the gate, and it
		// leaves the synthesis when they take it back rather than when it gets old.
		withFact(aSaidFact("mf_mine_long_ago", SurfaceUser, "", ConfirmerUser, admittedJuly)).
		withFact(aSaidFact("mf_believed", SurfaceMastery, "learning", ConfirmerGate, atTen))

	synthesis, err := service.Synthesis(context.Background(), 7)
	if err != nil {
		t.Fatalf("Synthesis() error = %v", err)
	}

	if got := factIDs(synthesis.Preferences); !slices.Equal(got, []string{"mf_mine_now", "mf_mine_long_ago"}) {
		t.Errorf("Preferences = %v, want both facts the user stated, newest first", got)
	}
	// The fact the gate admitted is in the synthesis but not in this section: the
	// section is "what you told us", not "what we concluded".
	if slices.Contains(factIDs(synthesis.Preferences), "mf_believed") {
		t.Error("Preferences holds a fact the gate admitted")
	}
	if got := len(synthesis.Recent) + profileFacts(synthesis); got != 3 {
		t.Errorf("the synthesis shows %d facts, want all three in force", got)
	}
}

func TestASynthesisCarriesNoOtherAccountsFacts(t *testing.T) {
	service, repository := frozenAt(t, atNoon, gateOptions())
	repository.
		withFact(aSaidFact("mf_mine", SurfaceMastery, "learning", ConfirmerGate, admittedSeptember)).
		withFact(aFactOn("mf_theirs", 8, SurfaceMastery, StatusActive, admittedSeptember))

	theirs, err := service.Synthesis(context.Background(), 8)
	if err != nil {
		t.Fatalf("Synthesis() error = %v", err)
	}
	// Both accounts have a fact in this memory and each synthesis is one of them,
	// because the owner is in the read rather than checked afterwards.
	if got := factIDs(theirs.Recent); !slices.Equal(got, []string{"mf_theirs"}) {
		t.Errorf("the other account's synthesis shows %v, want its own fact", got)
	}
	mine, err := service.Synthesis(context.Background(), 7)
	if err != nil {
		t.Fatalf("Synthesis() error = %v", err)
	}
	if got := factIDs(mine.Recent); !slices.Equal(got, []string{"mf_mine"}) {
		t.Errorf("Synthesis() shows %v, want this account's fact and no one else's", got)
	}
}

func TestASynthesisNeedsAnAccountAndReportsStorageThatFailed(t *testing.T) {
	service, repository := frozenAt(t, atNoon, gateOptions())
	for _, ownerID := range []int64{0, -1} {
		if _, err := service.Synthesis(context.Background(), ownerID); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("Synthesis(%d) = %v, want a refusal", ownerID, err)
		}
	}

	repository.withFact(aSaidFact("mf_1", SurfaceMastery, "learning", ConfirmerGate, atNine))
	repository.factsErr = errors.New("the disk is on fire")

	// A synthesis of facts that could not be read is not an empty memory: it is an
	// error, and an empty answer here would be a picture of an account with nothing
	// in it.
	synthesis, err := service.Synthesis(context.Background(), 7)
	if err == nil {
		t.Fatal("Synthesis() reported success over a read that failed")
	}
	if !showsNothing(synthesis) {
		t.Errorf("a failed read produced %+v, want nothing at all", synthesis)
	}
}

// showsNothing reports whether all four sections came back empty, which is what a
// synthesis of a memory with nothing in force has to be.
func showsNothing(synthesis Synthesis) bool {
	return len(synthesis.Recent) == 0 && len(synthesis.Profile) == 0 &&
		len(synthesis.Scopes) == 0 && len(synthesis.Preferences) == 0
}

// profileSurfaces names the surfaces Profile has a row for, in the order they came.
func profileSurfaces(synthesis Synthesis) []Surface {
	surfaces := make([]Surface, 0, len(synthesis.Profile))
	for _, profile := range synthesis.Profile {
		surfaces = append(surfaces, profile.Surface)
	}
	return surfaces
}

// profileFacts counts the facts Profile is showing, which is what the partition
// against Recent is checked on.
func profileFacts(synthesis Synthesis) int {
	count := 0
	for _, profile := range synthesis.Profile {
		count += len(profile.Facts)
	}
	return count
}

// profileFactIDs reads every fact Profile is showing, section by section.
func profileFactIDs(synthesis Synthesis) []string {
	ids := make([]string, 0, profileFacts(synthesis))
	for _, profile := range synthesis.Profile {
		ids = append(ids, factIDs(profile.Facts)...)
	}
	return ids
}

// scopeNames names the scopes the synthesis grouped by, in the order they came.
func scopeNames(synthesis Synthesis) []string {
	names := make([]string, 0, len(synthesis.Scopes))
	for _, group := range synthesis.Scopes {
		names = append(names, group.Scope)
	}
	return names
}
