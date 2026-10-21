package turn

import (
	"backend/internal/authz"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"backend/internal/agentloop"
	"backend/internal/capability"
	"backend/internal/event"
	"backend/internal/llm"
	"backend/internal/session"
)

// memoryRepository is storage a test can read back: turns in a map, events in a
// slice, with the same refusals the real one gives.
//
// It keeps the rules that a turn's behaviour depends on — one active turn per
// session, a state change guarded by the version it was prepared from, a
// sequence number never reused — so a test observes the runtime's behaviour
// rather than a storage adapter that always agrees.
//
// What it does not reproduce is how the real one achieves any of that: locking
// reads, unique indexes and check constraints are the database's, and only the
// end-to-end run exercises those.
type memoryRepository struct {
	*watcher
	turns  map[string]Turn
	events map[string][]event.Event
	// saves counts state writes, so a test can tell "the turn ended once" from
	// "the turn ended".
	saves int
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{
		watcher: newWatcher(),
		turns:   make(map[string]Turn),
		events:  make(map[string][]event.Event),
	}
}

func (m *memoryRepository) Start(_ context.Context, record Turn) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, stored := range m.turns {
		if stored.SessionID == record.SessionID && stored.Status.Active() {
			return fmt.Errorf("%w: session %s", ErrActiveTurnExists, record.SessionID)
		}
	}
	m.turns[record.ID] = record
	m.touch()
	return nil
}

func (m *memoryRepository) Turn(_ context.Context, turnID string) (Turn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.turns[turnID]
	if !ok {
		return Turn{}, fmt.Errorf("%w: %s", ErrNotFound, turnID)
	}
	return record, nil
}

func (m *memoryRepository) ActiveTurn(_ context.Context, sessionID string) (Turn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, stored := range m.turns {
		if stored.SessionID == sessionID && stored.Status.Active() {
			return stored, nil
		}
	}
	return Turn{}, fmt.Errorf("%w: session %s has no active turn", ErrNotFound, sessionID)
}

func (m *memoryRepository) Save(_ context.Context, record Turn, from Status) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	stored, ok := m.turns[record.ID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, record.ID)
	}
	if stored.Status != from || stored.StateVersion != record.StateVersion-1 {
		return fmt.Errorf("%w: %s", ErrStateConflict, record.ID)
	}
	m.turns[record.ID] = record
	m.saves++
	m.touch()
	return nil
}

func (m *memoryRepository) AppendEvent(_ context.Context, record event.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, stored := range m.events[record.TurnID] {
		if stored.Seq == record.Seq {
			return fmt.Errorf("%w: %s seq %d", ErrSeqTaken, record.TurnID, record.Seq)
		}
	}
	m.events[record.TurnID] = append(m.events[record.TurnID], record)
	m.touch()
	return nil
}

func (m *memoryRepository) Events(_ context.Context, turnID string, afterSeq int64) ([]event.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var history []event.Event
	for _, stored := range m.events[turnID] {
		if stored.Seq > afterSeq {
			history = append(history, stored)
		}
	}
	return history, nil
}

func (m *memoryRepository) LastEvent(_ context.Context, turnID string) (event.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	highest, ok := m.highestSeq(turnID)
	if !ok {
		return event.Event{}, fmt.Errorf("%w: %s has no events", ErrNotFound, turnID)
	}
	return highest, nil
}

func (m *memoryRepository) LastSeq(_ context.Context, turnID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var highest int64
	for _, stored := range m.events[turnID] {
		if stored.Seq > highest {
			highest = stored.Seq
		}
	}
	return highest, nil
}

// highestSeq returns the last stored event. The caller holds the lock.
func (m *memoryRepository) highestSeq(turnID string) (event.Event, bool) {
	var (
		highest event.Event
		found   bool
	)
	for _, stored := range m.events[turnID] {
		if !found || stored.Seq > highest.Seq {
			highest, found = stored, true
		}
	}
	return highest, found
}

// awaitStatus waits until a turn has reached a status, and returns it as it
// then stands.
func (m *memoryRepository) awaitStatus(t *testing.T, turnID string, want Status) Turn {
	t.Helper()
	m.await(t, func() bool {
		stored, ok := m.turns[turnID]
		return ok && stored.Status == want
	}, fmt.Sprintf("turn %s to be %s", turnID, want))
	return m.mustTurn(t, turnID)
}

// mustTurn reads a stored turn, failing the test if it is not there.
func (m *memoryRepository) mustTurn(t *testing.T, turnID string) Turn {
	t.Helper()
	record, err := m.Turn(context.Background(), turnID)
	if err != nil {
		t.Fatalf("Turn(%s) error = %v", turnID, err)
	}
	return record
}

// storedEvents returns a turn's events, oldest first. They are stored in the
// order the emitter wrote them, which is the order of their sequence numbers:
// one writer numbers and writes each event under the same lock.
func (m *memoryRepository) storedEvents(turnID string) []event.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]event.Event(nil), m.events[turnID]...)
}

// stubSessions is the conversation domain in memory.
type stubSessions struct {
	mu           sync.Mutex
	created      int
	createdKinds []session.Kind
	sessions     map[string]session.Session
	messages     map[string][]session.Message
}

func newStubSessions() *stubSessions {
	return &stubSessions{
		sessions: make(map[string]session.Session),
		messages: make(map[string][]session.Message),
	}
}

func (s *stubSessions) Create(_ context.Context, _ authz.Caller, kind session.Kind) (session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.created++
	s.createdKinds = append(s.createdKinds, kind)
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	record := session.Session{
		ID:        fmt.Sprintf("sess_%d", s.created),
		Kind:      kind,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.sessions[record.ID] = record
	return record, nil
}

func (s *stubSessions) Get(_ context.Context, sessionID string, _ authz.Caller) (session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.sessions[sessionID]
	if !ok {
		return session.Session{}, fmt.Errorf("%w: %s", session.ErrNotFound, sessionID)
	}
	return record, nil
}

func (s *stubSessions) Rename(_ context.Context, sessionID, title string, _ authz.Caller) (session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.sessions[sessionID]
	if !ok {
		return session.Session{}, fmt.Errorf("%w: %s", session.ErrNotFound, sessionID)
	}
	record.Title = title
	s.sessions[sessionID] = record
	return record, nil
}

func (s *stubSessions) AppendMessage(_ context.Context, input session.NewMessage, _ authz.Caller) (session.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[input.SessionID]; !ok {
		return session.Message{}, fmt.Errorf("%w: %s", session.ErrNotFound, input.SessionID)
	}
	record := session.Message{
		ID:         fmt.Sprintf("msg_%d", len(s.messages[input.SessionID])+1),
		SessionID:  input.SessionID,
		Role:       input.Role,
		Content:    input.Content,
		Capability: input.Capability,
	}
	s.messages[input.SessionID] = append(s.messages[input.SessionID], record)
	return record, nil
}

func (s *stubSessions) Messages(_ context.Context, sessionID string, _ authz.Caller) ([]session.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]session.Message(nil), s.messages[sessionID]...), nil
}

// stubCapability runs whatever a test tells it to.
type stubCapability struct {
	manifest capability.Manifest
	run      func(ctx context.Context, rc capability.RunContext) error
}

func (c stubCapability) Manifest() capability.Manifest { return c.manifest }

func (c stubCapability) Run(ctx context.Context, rc capability.RunContext) error {
	if c.run == nil {
		return nil
	}
	return c.run(ctx, rc)
}

// answering is a capability that says one thing and reports the result, which
// is the shortest turn that is still a whole turn.
func answering(capabilityName, text string) stubCapability {
	return stubCapability{
		manifest: capability.Manifest{Name: capabilityName, Description: "answers", Stages: []string{"responding"}},
		run: func(ctx context.Context, rc capability.RunContext) error {
			if err := rc.Emitter().Emit(ctx, event.Event{
				Type: event.TypeStageStart, Source: capabilityName, Stage: "responding",
			}); err != nil {
				return err
			}
			if err := rc.Emitter().Emit(ctx, event.Event{
				Type: event.TypeContent, Source: capabilityName, Stage: "responding", Content: text,
			}); err != nil {
				return err
			}
			return rc.Emitter().Emit(ctx, event.Event{
				Type: event.TypeResult, Source: capabilityName, Stage: "responding", Content: text,
			})
		},
	}
}

// scriptedProvider answers with one scripted response per call.
//
// It is the seam that lets a whole turn be run without a model: everything
// above it — the loop, the tool plane, the runtime — is the real thing.
type scriptedProvider struct {
	mu        sync.Mutex
	responses []llm.Response
	requests  []llm.Request
	err       error
	// block, when set, is waited on before answering, so a test can hold a turn
	// in the middle of a model call.
	block chan struct{}
	// failAfter, when set, makes every call past the first that many fail. It
	// is how a test makes a model stop mid-conversation.
	failAfter int
	// stuck, when set, is asked for on every call: a model that keeps calling
	// the same tool and never answers.
	stuck llm.ToolCall
}

func (p *scriptedProvider) Stream(ctx context.Context, req llm.Request, onDelta func(llm.Delta) error) (llm.Response, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	block, err, stuck := p.block, p.err, p.stuck
	if p.failAfter > 0 && len(p.requests) > p.failAfter {
		err = errors.New("the provider stopped answering")
	}
	var response llm.Response
	if len(p.responses) > 0 {
		response, p.responses = p.responses[0], p.responses[1:]
	}
	if stuck.Name != "" {
		response = llm.Response{ToolCalls: []llm.ToolCall{stuck}}
	}
	p.mu.Unlock()

	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return llm.Response{}, ctx.Err()
		}
	}
	if err != nil {
		return llm.Response{}, err
	}
	if response.Content != "" {
		if err := onDelta(llm.Delta{Content: response.Content}); err != nil {
			return llm.Response{}, err
		}
	}
	return response, nil
}

// asked returns the requests the provider was given.
func (p *scriptedProvider) asked() []llm.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]llm.Request(nil), p.requests...)
}

// looping is a capability that answers through the shared agent loop, which is
// how every capability in this version answers.
func looping(name string, request agentloop.Request) stubCapability {
	return stubCapability{
		manifest: capability.Manifest{Name: name, Description: "answers", Stages: []string{"responding"}},
		run: func(ctx context.Context, rc capability.RunContext) error {
			if err := rc.Emitter().Emit(ctx, event.Event{
				Type: event.TypeStageStart, Source: name, Stage: "responding",
			}); err != nil {
				return err
			}
			request.Messages = rc.Messages()
			request.Tools = rc.Tools()
			result, err := rc.RunLoop(ctx, request)
			if err != nil {
				return err
			}
			if err := rc.Emitter().Emit(ctx, event.Event{
				Type: event.TypeStageEnd, Source: name, Stage: "responding",
			}); err != nil {
				return err
			}
			return rc.Emitter().Emit(ctx, event.Event{
				Type: event.TypeResult, Source: name, Stage: "responding", Content: result.Content,
			})
		},
	}
}

// agentRequest is the model call a capability makes: the smallest one that
// still reaches the loop.
func agentRequest() agentloop.Request {
	return agentloop.Request{Stage: "responding", Model: "chat-model", MaxRounds: 3}
}
