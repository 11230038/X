package usage

import (
	"context"
	"errors"
	"testing"

	"backend/internal/llm"
)

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
