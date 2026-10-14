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
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"backend/internal/llm"
)

// scriptProvider is a provider under the test's control. It answers with what
// it was told to, and it can be asked what the record looked like at the moment
// it was called — which is the only way to see that the first half of the write
// happened before the call and not after it.
type scriptProvider struct {
	response llm.Response
	err      error
	deltas   []llm.Delta
	// during runs inside the call. It is where a test cancels the context or
	// looks at what has been written so far.
	during func(context.Context)
	// seen is the store's state when the provider was called, captured by the
	// test through during.
	calls    int
	requests []llm.Request
}

func (p *scriptProvider) Stream(
	ctx context.Context, req llm.Request, onDelta func(llm.Delta) error,
) (llm.Response, error) {
	p.calls++
	p.requests = append(p.requests, req)
	if p.during != nil {
		p.during(ctx)
	}
	for _, delta := range p.deltas {
		if err := onDelta(delta); err != nil {
			return llm.Response{}, err
		}
	}
	if p.err != nil {
		return llm.Response{}, p.err
	}
	return p.response, nil
}

// scriptStore keeps the two writes, in order, so that a case can assert on the
// order as well as on the contents.
type scriptStore struct {
	writes      []string
	opened      []OpenCall
	completed   []completedCall
	openErr     error
	completeErr error
}

type completedCall struct {
	callID     string
	completion Completion
}

func (s *scriptStore) Open(_ context.Context, open OpenCall) error {
	s.writes = append(s.writes, "open")
	if s.openErr != nil {
		return s.openErr
	}
	s.opened = append(s.opened, open)
	return nil
}

func (s *scriptStore) Complete(_ context.Context, callID string, done Completion) error {
	s.writes = append(s.writes, "complete")
	if s.completeErr != nil {
		return s.completeErr
	}
	s.completed = append(s.completed, completedCall{callID: callID, completion: done})
	return nil
}

// ticking hands out a time and moves forward by step on every read, so that a
// duration is a number a test names rather than a number it hopes for.
func ticking(start time.Time, step time.Duration) func() time.Time {
	current := start
	return func() time.Time {
		now := current
		current = current.Add(step)
		return now
	}
}

// naming hands out ids in order, for a test that wants to say which call it is
// looking at.
func naming(ids ...string) func() (string, error) {
	index := 0
	return func() (string, error) {
		if index >= len(ids) {
			return "", errors.New("the test ran out of call ids")
		}
		id := ids[index]
		index++
		return id, nil
	}
}

// aCall is the identity a test's context carries: an account, a conversation,
// and which part of the product asked.
func aCall() Call {
	return Call{OwnerID: 7, SessionID: "ses_1", TurnID: "turn_1", Source: "chat"}
}

func newRecorder(t *testing.T, provider llm.Provider, store Store, options Options) *Recorder {
	t.Helper()
	if options.Provider == "" {
		options.Provider = "deepseek"
	}
	// A gate that refuses nothing and reads nothing, so that the cases in this
	// file stay about the write. What the quota does when it does have limits is
	// in quota_test.go, and the two cases at the end of this file are about the
	// one thing only this file can see: that a refusal is handed out before the
	// call is made and the row is written.
	if options.Quota == nil {
		options.Quota = freeQuota(t)
	}
	if options.Now == nil {
		options.Now = ticking(time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC), 100*time.Millisecond)
	}
	if options.NewID == nil {
		options.NewID = naming("call_1")
	}
	recorder, err := New(provider, store, options)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return recorder
}

func aRequest() llm.Request {
	return llm.Request{Model: "deepseek-chat", Messages: []llm.Message{
		{Role: llm.RoleUser, Content: "what is a derivative"},
	}}
}

func aResponse() llm.Response {
	return llm.Response{
		Content:      "a rate of change",
		Usage:        llm.Usage{PromptTokens: 120, CompletionTokens: 45, TotalTokens: 165},
		FinishReason: "stop",
	}
}

// collect is a caller's callback: it keeps what it was handed.
func collect(into *[]string) func(llm.Delta) error {
	return func(delta llm.Delta) error {
		*into = append(*into, delta.Content)
		return nil
	}
}

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

// A call with no account behind it, or with no source, is not made. Refusing is
// the whole point: quota is summed per account (§2.9), so a call that happened
// and left nothing behind is a call nobody was charged for and nothing reports.
func TestACallWithoutAnIdentityIsNotMade(t *testing.T) {
	for _, refused := range []struct {
		name string
		ctx  context.Context
	}{
		{name: "no call at all", ctx: context.Background()},
		{name: "no account", ctx: WithCall(context.Background(), Call{Source: "chat"})},
		{name: "no source", ctx: WithCall(context.Background(), Call{OwnerID: 7})},
		{name: "a blank source", ctx: WithCall(context.Background(), Call{OwnerID: 7, Source: "   "})},
	} {
		t.Run(refused.name, func(t *testing.T) {
			store := &scriptStore{}
			provider := &scriptProvider{response: aResponse()}
			recorder := newRecorder(t, provider, store, Options{})

			_, err := recorder.Stream(refused.ctx, aRequest(), collect(new([]string)))

			if !errors.Is(err, ErrUnattributed) {
				t.Fatalf("Stream() error = %v, want %v", err, ErrUnattributed)
			}
			if provider.calls != 0 {
				t.Error("the model was called for a call nothing could be recorded about")
			}
			if len(store.writes) != 0 {
				t.Errorf("the store was written to: %v", store.writes)
			}
		})
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

// An id that was not made stops the call before it is made: a row cannot be
// written without one, and a call that cannot be written is not made.
func TestACallThatCouldNotBeNamedIsNotMade(t *testing.T) {
	store := &scriptStore{}
	provider := &scriptProvider{response: aResponse()}
	recorder := newRecorder(t, provider, store, Options{
		NewID: func() (string, error) { return "", errors.New("no randomness") },
	})

	ctx := WithCall(context.Background(), aCall())
	if _, err := recorder.Stream(ctx, aRequest(), collect(new([]string))); err == nil {
		t.Fatal("Stream() made a call it could not name")
	}
	if provider.calls != 0 || len(store.writes) != 0 {
		t.Error("a call that could not be named reached the provider or the store")
	}
}

// TestACallTheQuotaRefusesIsNotMade is §8 第 10 条, and it is here rather than in
// quota_test.go because it needs both halves: a real refusal, and a store that
// can say what was written. The refusal is handed out before anything at all
// happens — no id is spent, no row is opened, and the provider is never called —
// so the call the account was refused leaves nothing behind that a later count
// could find. A refused call that still wrote its row would be a limit that
// counts itself.
func TestACallTheQuotaRefusesIsNotMade(t *testing.T) {
	store := &scriptStore{}
	provider := &scriptProvider{response: aResponse()}
	quota := at(t, noon, Limits{DailyCalls: 2}, usedToday(2, 0, 0))
	recorder := newRecorder(t, provider, store, Options{Quota: quota})

	_, err := recorder.Stream(WithCall(context.Background(), aCall()), aRequest(), collect(new([]string)))

	var exceeded *Exceeded
	if !errors.As(err, &exceeded) {
		t.Fatalf("Stream() error = %v, want the quota's refusal to reach the caller", err)
	}
	if provider.calls != 0 {
		t.Error("a refused call reached the provider")
	}
	if len(store.writes) != 0 {
		t.Errorf("a refused call wrote %v, want nothing at all", store.writes)
	}
}

// TestACallThatCouldNotBeCountedIsNotMade is the other half of the same rule. A
// quota that cannot read the table is not a quota that allows the call, and it is
// not a refusal either: the account has done nothing, and what comes back is the
// read's own failure so that the surface answers it as one.
func TestACallThatCouldNotBeCountedIsNotMade(t *testing.T) {
	store := &scriptStore{}
	provider := &scriptProvider{response: aResponse()}
	broken := errors.New("the database is gone")
	quota := at(t, noon, Limits{DailyCalls: 2},
		&ledgerStub{answer: func(Query) (Report, error) { return Report{}, broken }})
	recorder := newRecorder(t, provider, store, Options{Quota: quota})

	_, err := recorder.Stream(WithCall(context.Background(), aCall()), aRequest(), collect(new([]string)))

	if !errors.Is(err, broken) {
		t.Fatalf("Stream() error = %v, want the read's own failure", err)
	}
	if provider.calls != 0 || len(store.writes) != 0 {
		t.Error("a call nobody could count was made anyway")
	}
}

func TestNewRefusesWhatItCannotRecordWith(t *testing.T) {
	store := &scriptStore{}
	cases := map[string]struct {
		provider llm.Provider
		store    Store
		quota    *Quota
		name     string
	}{
		"no provider": {provider: nil, store: store, quota: freeQuota(t), name: "deepseek"},
		"no store":    {provider: &scriptProvider{}, store: nil, quota: freeQuota(t), name: "deepseek"},
		"no name":     {provider: &scriptProvider{}, store: store, quota: freeQuota(t), name: "  "},
		// No quota is the one of the four that would fail invisibly: the calls
		// would work, the answers would be right, and nobody would be charged.
		"no quota": {provider: &scriptProvider{}, store: store, quota: nil, name: "deepseek"},
	}
	for name, refused := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := New(refused.provider, refused.store, Options{Provider: refused.name, Quota: refused.quota})
			if err == nil {
				t.Error("a recorder was built that could not record")
			}
		})
	}
}

// A recorder that was never built is a wiring mistake, and the one thing it must
// not do is answer that a call was made.
func TestAnUnwiredRecorderRefusesRatherThanCallingOut(t *testing.T) {
	var recorder *Recorder
	_, err := recorder.Stream(context.Background(), aRequest(), collect(new([]string)))

	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Stream() error = %v, want %v", err, ErrNotConfigured)
	}
}

// The ids are the primary key's whole story: distinct, so that §17.3's
// deduplication has nothing to do, and marked, so that a row can be recognised.
func TestTheCallIDsAreDistinctAndMarked(t *testing.T) {
	seen := make(map[string]bool, 200)
	for range 200 {
		id, err := newCallID()
		if err != nil {
			t.Fatalf("newCallID() error = %v", err)
		}
		if !strings.HasPrefix(id, callIDPrefix) || len(id) == len(callIDPrefix) {
			t.Fatalf("id = %q, want the prefix and something after it", id)
		}
		if seen[id] {
			t.Fatalf("the id %q was made twice", id)
		}
		seen[id] = true
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
