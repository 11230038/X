package audit

// The rules about what one entry may say (§2.6).
//
// These are the tests that make the audit's columns a shape rather than a
// convention: what is refused here is refused before a row exists, and the
// refusals are the assertion — an entry that tried to carry a prompt has to be
// *rejected*, because an entry that quietly dropped the field would let the
// writer believe the audit kept what it was handed.

import (
	"errors"
	"testing"
)

// anEvent is an entry that passes every check, so that each test below can
// spoil exactly one thing about it.
func anEvent() Event {
	return Event{
		Actor:  Actor{ID: 7, Name: "wei", Role: "user"},
		Action: ActionSessionClaimed,
		Target: Ref{Kind: TargetSession, ID: "ses_abc"},
		Result: ResultOK,
	}
}

func TestEventValidateAcceptsAnOrdinaryEntry(t *testing.T) {
	if err := anEvent().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestEventValidateAcceptsAnEntryThatSaysNothing(t *testing.T) {
	// An entry with no detail and no subject is the ordinary case, not a
	// degraded one: most actions have nothing to add beyond their columns.
	event := anEvent()
	event.Detail = Detail{}
	event.SubjectID = 0

	if err := event.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestEventValidateRefusesWhatThisBuildDoesNotRecord(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Event)
		want   error
	}{
		{name: "an action nobody wrote", mutate: func(e *Event) { e.Action = "authorization.denied" },
			want: ErrUnknownAction},
		{name: "an action spelled differently", mutate: func(e *Event) { e.Action = "Session.Claimed" },
			want: ErrUnknownAction},
		{name: "a result outside the check", mutate: func(e *Event) { e.Result = "maybe" },
			want: ErrUnknownResult},
		{name: "a target kind nobody wrote", mutate: func(e *Event) { e.Target.Kind = "prompt" },
			want: ErrUnknownTargetKind},
		{name: "a target id that is a sentence",
			mutate: func(e *Event) { e.Target.ID = "the user asked about chapter 4" },
			want:   ErrNotACode},
		{name: "a reason that is a sentence",
			mutate: func(e *Event) { e.Detail = Detail{Reason: "because the learner preset is closed"} },
			want:   ErrNotACode},
		{name: "a reason in a language without spaces",
			mutate: func(e *Event) { e.Detail = Detail{Reason: "因为学习者没有权限"} },
			want:   ErrNotACode},
		{name: "a layer that is not one of the four",
			mutate: func(e *Event) { e.Detail = Detail{Layer: "policy engine"} },
			want:   ErrNotACode},
		{name: "a count named by a sentence",
			mutate: func(e *Event) { e.Detail = Detail{Counts: map[string]int{"how many rows were written": 3}} },
			want:   ErrNotACode},
		{name: "a negative count",
			mutate: func(e *Event) { e.Detail = Detail{Counts: map[string]int{"written": -1}} },
			want:   ErrNotACode},
		{name: "an item of a kind nobody wrote",
			mutate: func(e *Event) { e.Detail = Detail{Items: []Ref{{Kind: "paragraph", ID: "p1"}}} },
			want:   ErrUnknownTargetKind},
		{name: "an item id that is a sentence",
			mutate: func(e *Event) { e.Detail = Detail{Items: []Ref{{Kind: TargetSession, ID: "the one about chapter 4"}}} },
			want:   ErrNotACode},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			event := anEvent()
			tt.mutate(&event)

			err := event.Validate()
			if err == nil {
				t.Fatal("Validate() error = nil, want an error")
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("Validate() error = %v, want %v", err, tt.want)
			}
		})
	}
}

// Half the item kinds of a grant are spelled with an empty id, meaning "every
// one of them" (§2.2). A check that refused an empty identifier would refuse
// the entries about the widest decisions, which are the ones most worth
// recording.
func TestEventValidateAcceptsAnEmptyTargetID(t *testing.T) {
	event := anEvent()
	event.Action = ActionGrantWritten
	event.Target = Ref{Kind: TargetGrant, ID: ""}

	if err := event.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want an empty target id accepted", err)
	}
}

// The detail is not optional in one direction: an entry that says why is an
// entry a reader can act on, and the two codes are the whole of the vocabulary
// a refusal needs.
func TestDetailEmptyReportsWhetherThereIsAnythingToStore(t *testing.T) {
	for _, tt := range []struct {
		name   string
		detail Detail
		want   bool
	}{
		{name: "nothing", detail: Detail{}, want: true},
		{name: "a reason", detail: Detail{Reason: "baseline_closed"}},
		{name: "a layer", detail: Detail{Layer: "guardian"}},
		{name: "a count", detail: Detail{Counts: map[string]int{"written": 0}}},
		{name: "an item", detail: Detail{Items: []Ref{{Kind: TargetSession, ID: "ses_abc"}}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.detail.Empty(); got != tt.want {
				t.Errorf("Empty() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Zero counts are not nothing: "an import that wrote none" is a statement, and
// an Empty that answered true for it would drop the one detail that says the
// run happened and changed nothing.
func TestDetailWithAZeroCountIsNotEmpty(t *testing.T) {
	if (Detail{Counts: map[string]int{"written": 0}}).Empty() {
		t.Error("Empty() = true for a detail carrying a zero count")
	}
}

func TestKnownActionAndKindAndResultAgreeWithTheirLists(t *testing.T) {
	for _, action := range Actions {
		if !KnownAction(action) {
			t.Errorf("KnownAction(%q) = false for an action in the list", action)
		}
	}
	for _, kind := range TargetKinds {
		if !KnownTargetKind(kind) {
			t.Errorf("KnownTargetKind(%q) = false for a kind in the list", kind)
		}
	}
	for _, result := range Results {
		if !KnownResult(result) {
			t.Errorf("KnownResult(%q) = false for a result in the list", result)
		}
	}
	if KnownAction("") || KnownTargetKind("") || KnownResult("") {
		t.Error("an empty name was reported as known")
	}
}

// The closed set has no duplicates, because a list with the same name twice is
// a list where the second entry is a typo nobody would notice.
func TestTheClosedSetsHaveNoDuplicates(t *testing.T) {
	seen := map[Action]bool{}
	for _, action := range Actions {
		if seen[action] {
			t.Errorf("the action %q is listed twice", action)
		}
		seen[action] = true
	}
	kinds := map[TargetKind]bool{}
	for _, kind := range TargetKinds {
		if kinds[kind] {
			t.Errorf("the target kind %q is listed twice", kind)
		}
		kinds[kind] = true
	}
}

func TestIsCodeAcceptsTheCodesThisBuildWrites(t *testing.T) {
	for _, code := range []string{
		"administrator", "baseline_closed", "session.claimed", "guardian.surface",
		"grant:written", "v2", "a-b",
	} {
		if !isCode(code) {
			t.Errorf("isCode(%q) = false, want true", code)
		}
	}
}

func TestIsCodeRefusesWhatIsNotOne(t *testing.T) {
	for _, code := range []string{
		"", "Two Words", "trailing space ", " prompt", "Naïve", "因为", "café",
		"a\nb", "a\tb", string(make([]byte, 65)),
	} {
		if isCode(code) {
			t.Errorf("isCode(%q) = true, want false", code)
		}
	}
}

// Long codes are refused rather than truncated: a code longer than the column
// is a code that was never one, and a write that trimmed it would file the
// entry under a name nobody chose.
func TestIsCodeRefusesACodeLongerThanTheColumn(t *testing.T) {
	long := ""
	for len(long) < codeMax {
		long += "a"
	}
	if !isCode(long) {
		t.Fatalf("isCode(%d characters) = false, want true at the limit", len(long))
	}
	if isCode(long + "a") {
		t.Fatalf("isCode(%d characters) = true, want false one over the limit", len(long)+1)
	}
}

func TestIsIdentifierIsWiderThanACode(t *testing.T) {
	for _, id := range []string{"", "lnk_1_42", "ses_AbC123", "md/lib_ab12.md", "user@example.com", "a:b-c.d/e_f"} {
		if !isIdentifier(id, idMax) {
			t.Errorf("isIdentifier(%q) = false, want true", id)
		}
	}
	for _, id := range []string{"a b", "a+b", "因为", "a\nb"} {
		if isIdentifier(id, idMax) {
			t.Errorf("isIdentifier(%q) = true, want false", id)
		}
	}
}
