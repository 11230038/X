package usage

// What the context carries.
//
// Small enough to look obvious, and worth having apart from the recorder: this
// identity is the only thing standing between a model call and a row that says
// nothing about who made it, and the two ways of putting it there are used from
// different packages — the work declares the account and the conversation, and a
// component inside it says what its own call is.

import (
	"context"
	"testing"
)

func TestWhatWithCallPutInIsWhatCallFromFinds(t *testing.T) {
	want := Call{OwnerID: 7, SessionID: "ses_1", TurnID: "turn_1", Source: "chat"}
	ctx := WithCall(context.Background(), want)

	got, ok := CallFrom(ctx)

	if !ok {
		t.Fatal("CallFrom() found no call in a context one was put into")
	}
	if got != want {
		t.Errorf("CallFrom() = %+v, want %+v", got, want)
	}
}

// A short call is a call with no conversation, and the context says that by
// saying nothing: there is no placeholder to forget to strip at the other end.
func TestACallOutsideAConversationCarriesNoConversation(t *testing.T) {
	ctx := WithCall(context.Background(), Call{OwnerID: 7, Source: "practice.judge"})

	got, ok := CallFrom(ctx)

	if !ok {
		t.Fatal("CallFrom() found no call")
	}
	if got.SessionID != "" || got.TurnID != "" {
		t.Errorf("Call = %+v, want no conversation", got)
	}
	if got.OwnerID != 7 || got.Source != "practice.judge" {
		t.Errorf("Call = %+v, want the account and the source it was given", got)
	}
}

// Saying what a call is keeps whose work it is in: naming a conversation is the
// turn's business, and the turn is the thing that knows whose conversation it
// is. A source that reset the rest would charge the naming call to nobody.
func TestSayingWhatACallIsKeepsTheAccountAndTheConversation(t *testing.T) {
	ctx := WithCall(context.Background(), Call{
		OwnerID: 7, SessionID: "ses_1", TurnID: "turn_1", Source: "chat",
	})

	got, _ := CallFrom(WithSource(ctx, SourceTurnTitle))

	want := Call{OwnerID: 7, SessionID: "ses_1", TurnID: "turn_1", Source: SourceTurnTitle}
	if got != want {
		t.Errorf("CallFrom() = %+v, want %+v", got, want)
	}
}

// A source with no work under it is a source and nothing else — no account is
// invented for it, and the recorder refuses it. That is the direction that keeps
// a component from naming a call that belongs to nobody.
func TestASourceOnNothingInventsNoAccount(t *testing.T) {
	got, ok := CallFrom(WithSource(context.Background(), SourcePracticeJudge))

	if !ok {
		t.Fatal("CallFrom() found no call after WithSource")
	}
	if got.OwnerID != 0 {
		t.Errorf("OwnerID = %d, want none: a source says what a call is, not whose", got.OwnerID)
	}
	if got.Source != SourcePracticeJudge {
		t.Errorf("Source = %q, want %q", got.Source, SourcePracticeJudge)
	}
}

func TestAnEmptyContextCarriesNoCall(t *testing.T) {
	if call, ok := CallFrom(context.Background()); ok {
		t.Errorf("CallFrom() = %+v, true; want no call in a bare context", call)
	}
}

// The sources are what §17.1 groups by, so two call sites sharing one word would
// be two questions answered as one, and an empty word would be a group nobody
// can name.
func TestTheSourcesAreDistinctAndNamed(t *testing.T) {
	sources := []string{
		SourceTurnTitle,
		SourcePracticeJudge,
		SourceMasteryJudge,
		SourceReadingExtension,
		SourceBook,
	}
	seen := map[string]bool{}
	for _, source := range sources {
		if source == "" {
			t.Error("a source is empty: a group nobody can name")
		}
		if seen[source] {
			t.Errorf("the source %q is used for two things", source)
		}
		seen[source] = true
	}
}

// The outcome is the column's vocabulary (migration 00015), and the empty string
// belongs to the column's other meaning — a call that is still open. A result
// that happened to be empty would be a finished call the table reads as an
// unfinished one.
func TestTheOutcomesAreTheThreeTheColumnAllows(t *testing.T) {
	for _, outcome := range []Outcome{OutcomeOK, OutcomeError, OutcomeCancelled} {
		if outcome == "" {
			t.Error("an outcome is empty, which is what the column means by still open")
		}
	}
	if OutcomeOK == OutcomeError || OutcomeError == OutcomeCancelled || OutcomeOK == OutcomeCancelled {
		t.Error("two outcomes are the same word")
	}
}
