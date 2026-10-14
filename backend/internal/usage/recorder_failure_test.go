package usage

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// A call that came back with an error is recorded as one. The row is not left
// open: "did not come back" and "came back badly" are different things, and a
// table that ran them together could not answer either.
func TestACallThatFailedIsRecordedAsAnError(t *testing.T) {
	store := &scriptStore{}
	provider := &scriptProvider{err: errors.New("the upstream hung up")}
	recorder := newRecorder(t, provider, store, Options{})

	ctx := WithCall(context.Background(), aCall())
	if _, err := recorder.Stream(ctx, aRequest(), collect(new([]string))); err == nil {
		t.Fatal("Stream() returned no error for a provider that failed")
	}

	if got := store.completed[0].completion.Outcome; got != OutcomeError {
		t.Errorf("Outcome = %q, want %q", got, OutcomeError)
	}
}

// A call that ended because the caller's context did is the caller's own
// cancellation coming back, whichever error the provider wrapped it in — and it
// is told apart from a provider failure, because only one of the two is about
// the model.
func TestACallTheCallerCancelledIsRecordedAsCancelled(t *testing.T) {
	store := &scriptStore{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The provider fails while the caller's context is being cancelled, which is
	// what a call the user stopped looks like from here: the error is the
	// provider's, and the reason is the context's.
	provider := &scriptProvider{
		err:    errors.New("call provider: context canceled"),
		during: func(context.Context) { cancel() },
	}
	recorder := newRecorder(t, provider, store, Options{})

	if _, err := recorder.Stream(WithCall(ctx, aCall()), aRequest(), collect(new([]string))); err == nil {
		t.Fatal("Stream() returned no error for a cancelled call")
	}

	if got := store.completed[0].completion.Outcome; got != OutcomeCancelled {
		t.Errorf("Outcome = %q, want %q", got, OutcomeCancelled)
	}
}

// A record that cannot be opened stops the call. The alternative is a model call
// that happened and left no row, which is the failure §2.7 is written against —
// and it is invisible from the outside, because the call itself works.
func TestARecordThatCouldNotBeOpenedStopsTheCall(t *testing.T) {
	store := &scriptStore{openErr: errors.New("the database is gone")}
	provider := &scriptProvider{response: aResponse()}
	recorder := newRecorder(t, provider, store, Options{})

	ctx := WithCall(context.Background(), aCall())
	_, err := recorder.Stream(ctx, aRequest(), collect(new([]string)))

	if err == nil {
		t.Fatal("Stream() made a call it could not record")
	}
	if !errors.Is(err, store.openErr) {
		t.Errorf("error = %v, want the store's own failure", err)
	}
	if provider.calls != 0 {
		t.Error("the model was called even though the call could not be recorded")
	}
	if len(store.completed) != 0 {
		t.Error("a completion was written for a call that was never made")
	}
}

// The other direction, and it is the other direction on purpose: by the time the
// numbers are written the answer is in hand and already paid for. Throwing it
// away because a column could not be updated would charge the account twice.
func TestAFailureToCompleteDoesNotThrowAwayTheAnswer(t *testing.T) {
	var log strings.Builder
	store := &scriptStore{completeErr: errors.New("the database is gone")}
	provider := &scriptProvider{response: aResponse()}
	recorder := newRecorder(t, provider, store, Options{
		Logger: slog.New(slog.NewTextHandler(&log, nil)),
	})

	ctx := WithCall(context.Background(), aCall())
	response, err := recorder.Stream(ctx, aRequest(), collect(new([]string)))

	if err != nil {
		t.Fatalf("Stream() error = %v, want the answer anyway", err)
	}
	if response.Content != "a rate of change" {
		t.Errorf("Content = %q, want the answer the model gave", response.Content)
	}
	// It is not swallowed either: the row is left open, and the log is the only
	// thing that says why.
	if !strings.Contains(log.String(), "could not complete the record of a model call") {
		t.Errorf("nothing was logged about the failure: %s", log.String())
	}
	if !strings.Contains(log.String(), "call_1") {
		t.Errorf("the log does not say which call it is about: %s", log.String())
	}
}
