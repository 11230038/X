package workspace

// The join between a selection and the domains that hold it.
//
// There are two things to get right here and neither is about a domain. One is
// that the objects that come back are exactly the objects that were asked for —
// not fewer, because a package that is missing something lies about itself, and
// not more, because an object nobody asked for is an object an import would
// write after showing it to nobody. The other is that a kind with nowhere to
// read from fails while the process is being assembled rather than in front of a
// user who has been promised a package.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"backend/internal/authz"
)

// kindSource answers the refs it is told to answer, and can be made to misbehave
// in the two ways the collector is supposed to catch.
type kindSource struct {
	kind    Kind
	extra   []Object
	skipIDs map[string]struct{}
	err     error
	asked   [][]Ref
	callers []authz.Caller
}

func (s *kindSource) Objects(_ context.Context, caller authz.Caller, refs []Ref) ([]Object, error) {
	s.asked = append(s.asked, append([]Ref(nil), refs...))
	s.callers = append(s.callers, caller)
	if s.err != nil {
		return nil, s.err
	}
	objects := make([]Object, 0, len(refs)+len(s.extra))
	for _, ref := range refs {
		if _, skipped := s.skipIDs[ref.ID]; skipped {
			continue
		}
		objects = append(objects, Object{Ref: ref, Data: []byte(`{"kind":"` + string(ref.Kind) + `"}`)})
	}
	return append(objects, s.extra...), nil
}

func newKindSource(kind Kind) *kindSource {
	return &kindSource{kind: kind, skipIDs: map[string]struct{}{}}
}

func allKinds(t *testing.T) map[Kind]Source {
	t.Helper()
	return map[Kind]Source{
		KindSession:       newKindSource(KindSession),
		KindMaterial:      newKindSource(KindMaterial),
		KindQuestion:      newKindSource(KindQuestion),
		KindKnowledgeBase: newKindSource(KindKnowledgeBase),
	}
}

func TestTheSourcesAnswerEveryRefInTheOrderAsked(t *testing.T) {
	sources, err := NewSources(allKinds(t))
	if err != nil {
		t.Fatalf("NewSources() error = %v", err)
	}
	refs := []Ref{
		{Kind: KindSession, ID: "ses_1"},
		{Kind: KindKnowledgeBase, ID: "kb_1"},
		{Kind: KindSession, ID: "ses_2"},
		{Kind: KindMaterial, ID: "mat_1"},
	}
	caller := authz.Caller{UserID: 7}
	objects, err := sources.Collect(context.Background(), caller, refs)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(objects) != len(refs) {
		t.Fatalf("collected %d objects, want %d", len(objects), len(refs))
	}
	for index, ref := range refs {
		if objects[index].Ref != ref {
			t.Errorf("object %d is %s, want %s — the caller's order is what makes a failure reproducible", index, objects[index].Ref, ref)
		}
	}
}

// TestEachKindIsAskedOnce: the reads are grouped and each group is one call.
// Four refs of one kind are one question to that domain, not four — and the
// order the kinds are asked in is the closed set's, so two runs read the domains
// in the same sequence.
func TestEachKindIsAskedOnce(t *testing.T) {
	held := allKinds(t)
	sources, err := NewSources(held)
	if err != nil {
		t.Fatalf("NewSources() error = %v", err)
	}
	refs := []Ref{
		{Kind: KindSession, ID: "ses_1"},
		{Kind: KindSession, ID: "ses_2"},
		{Kind: KindQuestion, ID: "q_1"},
		{Kind: KindMaterial, ID: "mat_1"},
	}
	if _, err := sources.Collect(context.Background(), authz.Caller{UserID: 7}, refs); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	sessions := held[KindSession].(*kindSource)
	if len(sessions.asked) != 1 {
		t.Fatalf("the session source was asked %d times, want once", len(sessions.asked))
	}
	if len(sessions.asked[0]) != 2 {
		t.Errorf("the session source was asked for %v, want both sessions", sessions.asked[0])
	}
	if sessions.callers[0].UserID != 7 {
		t.Errorf("the session source was called as %+v, want the account", sessions.callers[0])
	}
	// A kind nobody selected is not asked at all. An empty call would be a read
	// of the whole table in the domains that treat "no ids" as "all of them".
	if len(held[KindKnowledgeBase].(*kindSource).asked) != 0 {
		t.Errorf("a kind nobody selected was still asked")
	}
}

// TestARefNothingAnswersIsRefused is the invariant the manifest depends on. A
// package that quietly held three of the four things somebody selected would
// verify perfectly and be wrong.
func TestARefNothingAnswersIsRefused(t *testing.T) {
	held := allKinds(t)
	held[KindMaterial].(*kindSource).skipIDs["mat_1"] = struct{}{}
	sources, err := NewSources(held)
	if err != nil {
		t.Fatalf("NewSources() error = %v", err)
	}
	refs := []Ref{
		{Kind: KindSession, ID: "ses_1"},
		{Kind: KindMaterial, ID: "mat_1"},
	}
	_, err = sources.Collect(context.Background(), authz.Caller{UserID: 7}, refs)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Collect() error = %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "material/mat_1") {
		t.Errorf("error = %v, want it to name the ref that was not answered", err)
	}
}

// TestASourceAnsweringAnotherKindIsRefused: a source that answered with
// something other than what it was asked for would put an object in a package
// under a kind nobody requested, and the manifest would then be right about a
// package that is wrong.
func TestASourceAnsweringAnotherKindIsRefused(t *testing.T) {
	held := allKinds(t)
	held[KindSession].(*kindSource).extra = []Object{
		{Ref: Ref{Kind: KindQuestion, ID: "q_1"}, Data: []byte("{}")},
	}
	sources, err := NewSources(held)
	if err != nil {
		t.Fatalf("NewSources() error = %v", err)
	}
	_, err = sources.Collect(context.Background(), authz.Caller{UserID: 7}, []Ref{{Kind: KindSession, ID: "ses_1"}})
	if err == nil {
		t.Fatalf("Collect() accepted an object nobody asked for")
	}
	if !strings.Contains(err.Error(), "answered with a question") {
		t.Errorf("error = %v, want it to say what came back instead", err)
	}
}

func TestASourceAnsweringTwiceIsRefused(t *testing.T) {
	held := allKinds(t)
	held[KindSession].(*kindSource).extra = []Object{
		{Ref: Ref{Kind: KindSession, ID: "ses_1"}, Data: []byte(`{"second":true}`)},
	}
	sources, err := NewSources(held)
	if err != nil {
		t.Fatalf("NewSources() error = %v", err)
	}
	if _, err := sources.Collect(context.Background(), authz.Caller{UserID: 7}, []Ref{{Kind: KindSession, ID: "ses_1"}}); err == nil {
		t.Fatalf("Collect() accepted one object twice")
	}
}

func TestASourceThatFailsStopsTheCollection(t *testing.T) {
	held := allKinds(t)
	held[KindMaterial].(*kindSource).err = errors.New("the reading layer does not answer")
	sources, err := NewSources(held)
	if err != nil {
		t.Fatalf("NewSources() error = %v", err)
	}
	_, err = sources.Collect(context.Background(), authz.Caller{UserID: 7}, []Ref{{Kind: KindMaterial, ID: "mat_1"}})
	if err == nil {
		t.Fatalf("Collect() error = nil, want the source's failure")
	}
	// The error names the kind, because "the reading layer does not answer" on
	// its own does not say which of four reads was the one that failed.
	if !strings.Contains(err.Error(), "read material") {
		t.Errorf("error = %v, want it to name the kind", err)
	}
}

func TestTheCollectorRefusesARefItCannotAddress(t *testing.T) {
	sources, err := NewSources(allKinds(t))
	if err != nil {
		t.Fatalf("NewSources() error = %v", err)
	}
	for _, ref := range []Ref{{Kind: "memory", ID: "mem_1"}, {Kind: KindSession, ID: "../x"}} {
		if _, err := sources.Collect(context.Background(), authz.Caller{UserID: 7}, []Ref{ref}); err == nil {
			t.Errorf("Collect() accepted %+v", ref)
		}
	}
}

// TestAKindWithNowhereToReadFromIsRefusedAtConstruction is what makes the closed
// set in types.go worth having: a kind added there becomes a kind every
// deployment can write, and without this check the first anybody would hear
// about a missing reader is an export failing halfway through.
func TestAKindWithNowhereToReadFromIsRefusedAtConstruction(t *testing.T) {
	for _, kind := range Kinds {
		t.Run(string(kind), func(t *testing.T) {
			held := allKinds(t)
			delete(held, kind)
			_, err := NewSources(held)
			if err == nil {
				t.Fatalf("NewSources() accepted a collector with no %s source", kind)
			}
			if !strings.Contains(err.Error(), string(kind)) {
				t.Errorf("error = %v, want it to name %s", err, kind)
			}
		})
	}
}

func TestAnUnbuiltCollectorRefusesEverything(t *testing.T) {
	var sources *Sources
	if _, err := sources.Collect(context.Background(), authz.Caller{UserID: 7}, []Ref{{Kind: KindSession, ID: "ses_1"}}); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("Collect() error = %v, want ErrNotConfigured", err)
	}
}
