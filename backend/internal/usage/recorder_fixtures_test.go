package usage

import (
	"context"
	"errors"
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
