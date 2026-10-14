package usage

// The decorator, without a database.
//
// What is pinned here is the shape of the write and what each failure does: the
// row exists before the call and is completed after it, an identity the context
// does not carry stops the call rather than losing the record of it, and the two
// failures of the two halves are answered in opposite directions — an open that
// failed stops the call, a completion that failed does not throw away an answer
// that has already been paid for.
//
// The store's own contract is in repository_db_test.go, against a real seekdb.

import (
	"context"
	"testing"
	"time"

	"backend/internal/llm"
)

// The row is written before the call and completed after it, and this is the
// case that can see it: the provider looks at the store from inside the call.
func TestACallIsRecordedInTwoHalvesAroundIt(t *testing.T) {
	store := &scriptStore{}
	var seenAtCall []string
	provider := &scriptProvider{
		response: aResponse(),
		during:   func(context.Context) { seenAtCall = append([]string{}, store.writes...) },
	}
	recorder := newRecorder(t, provider, store, Options{})

	ctx := WithCall(context.Background(), aCall())
	if _, err := recorder.Stream(ctx, aRequest(), collect(new([]string))); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	if len(seenAtCall) != 1 || seenAtCall[0] != "open" {
		t.Errorf("the store held %v when the call was made, want the open write alone", seenAtCall)
	}
	if want := []string{"open", "complete"}; !equalStrings(store.writes, want) {
		t.Errorf("the writes were %v, want %v", store.writes, want)
	}
	if len(store.completed) != 1 || store.completed[0].callID != store.opened[0].CallID {
		t.Errorf("the completion is not about the row that was opened: %+v", store.completed)
	}
}

// The first half says who, where and from which part of the product; the second
// half says what it cost. Neither carries the other's fields.
func TestTheTwoHalvesCarryWhatTheyAreFor(t *testing.T) {
	store := &scriptStore{}
	// The provider streams the answer's own text as the one fragment it is: the
	// time to first token is measured to the first fragment a provider hands over,
	// so a fixture that answered with a whole response and no fragment would be
	// asserting timings about a fragment that never arrived.
	provider := &scriptProvider{
		response: aResponse(),
		deltas:   []llm.Delta{{Content: "a rate of change"}},
	}
	recorder := newRecorder(t, provider, store, Options{})

	ctx := WithCall(context.Background(), aCall())
	if _, err := recorder.Stream(ctx, aRequest(), collect(new([]string))); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	open := store.opened[0]

	if open.CallID != "call_1" {
		t.Errorf("CallID = %q, want the id the recorder made", open.CallID)
	}
	if open.Call != aCall() {
		t.Errorf("Call = %+v, want the identity the context carried", open.Call)
	}
	if open.Provider != "deepseek" || open.Model != "deepseek-chat" {
		t.Errorf("the open write does not say what was called: %+v", open)
	}
	if want := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC); !open.StartedAt.Equal(want) {
		t.Errorf("StartedAt = %s, want %s", open.StartedAt, want)
	}

	done := store.completed[0].completion
	if done.Usage != aResponse().Usage {
		t.Errorf("Usage = %+v, want the provider's own numbers", done.Usage)
	}
	if done.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want what the provider said", done.FinishReason)
	}
	if done.Outcome != OutcomeOK {
		t.Errorf("Outcome = %q, want %q", done.Outcome, OutcomeOK)
	}
	// Two reads of the clock went to the first fragment and the end of the call,
	// and the clock moves a hundred milliseconds each time.
	if done.TTFTMs != 100 || done.DurationMs != 200 {
		t.Errorf("the timings are %d/%d, want 100/200", done.TTFTMs, done.DurationMs)
	}
	if want := time.Date(2026, 10, 1, 9, 0, 0, 200000000, time.UTC); !done.FinishedAt.Equal(want) {
		t.Errorf("FinishedAt = %s, want %s", done.FinishedAt, want)
	}
}

// The structural claim, and the reason the recorder is worth having at all: a
// call that does not go through it leaves nothing behind. The provider is the
// same one; only the wrapper is missing.
func TestACallThatBypassesTheRecorderLeavesNoRow(t *testing.T) {
	store := &scriptStore{}
	provider := &scriptProvider{response: aResponse()}
	ctx := WithCall(context.Background(), aCall())
	fragments := new([]string)

	// Straight to the provider: an identity in the context is not enough, and
	// this is what a second provider built somewhere else would look like.
	if _, err := provider.Stream(ctx, aRequest(), collect(fragments)); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if len(store.writes) != 0 {
		t.Fatalf("a call that did not go through the recorder was recorded: %v", store.writes)
	}

	recorder := newRecorder(t, provider, store, Options{})
	if _, err := recorder.Stream(ctx, aRequest(), collect(fragments)); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if len(store.writes) != 2 {
		t.Errorf("the same call through the recorder wrote %v, want both halves", store.writes)
	}
}

// The recorder is a provider: what it does not record it does not change. The
// answer comes back whole, every fragment reaches the caller's own callback, and
// an error from that callback stops the call and is returned — the three things
// the provider contract promises.
func TestTheRecorderChangesNothingAboutTheCall(t *testing.T) {
	store := &scriptStore{}
	provider := &scriptProvider{response: aResponse(), deltas: []llm.Delta{
		{Content: "a rate "}, {Content: "of change"},
	}}
	recorder := newRecorder(t, provider, store, Options{})

	var fragments []string
	response, err := recorder.Stream(WithCall(context.Background(), aCall()),
		aRequest(), collect(&fragments))
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if !equalStrings(fragments, []string{"a rate ", "of change"}) {
		t.Errorf("the fragments were %v, want both of them in order", fragments)
	}
	if response.Content != "a rate of change" || response.Usage != aResponse().Usage ||
		response.FinishReason != "stop" {
		t.Errorf("Response = %+v, want the provider's own", response)
	}
	if got := provider.requests[0]; got.Model != "deepseek-chat" || len(got.Messages) != 1 {
		t.Errorf("the request the provider saw is %+v, want the one that was handed in", got)
	}
}
