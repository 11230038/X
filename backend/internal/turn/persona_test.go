package turn

import (
	"context"
	"errors"
	"strings"
	"testing"

	"backend/internal/agentloop"
	"backend/internal/authz"
	"backend/internal/capability"
	"backend/internal/event"
	"backend/internal/llm"
	"backend/internal/session"
)

// stubPersonas is the persona store the runtime talks to: one instruction, or
// the answer that there is none. It records who asked and for what, because the
// caller is part of the contract — another account's persona must never be
// resolved by id alone.
type stubPersonas struct {
	instruction string
	found       bool
	err         error
	asked       []string
	callers     []authz.Caller
}

var _ Personas = (*stubPersonas)(nil)

func (p *stubPersonas) Instruction(_ context.Context, personaID string, caller authz.Caller) (string, bool, error) {
	p.asked = append(p.asked, personaID)
	p.callers = append(p.callers, caller)
	if p.err != nil {
		return "", false, p.err
	}
	return p.instruction, p.found, nil
}

// withPersonas installs a persona store on the fixture, which is what a
// deployment with saved prompts has and what a deployment without one does not.
func withPersonas(personas Personas) func(*Options) {
	return func(options *Options) { options.Personas = personas }
}

// personified is a capability that puts the turn's persona in front of the
// history, the way every real capability does. It is the smallest shape that
// still exercises the whole seam: Start resolves the text, the run carries it,
// and the model reads it.
func personified(name string, request agentloop.Request) stubCapability {
	return stubCapability{
		manifest: capability.Manifest{Name: name, Description: "answers", Stages: []string{"responding"}},
		run: func(ctx context.Context, rc capability.RunContext) error {
			if err := rc.Emitter().Emit(ctx, event.Event{
				Type: event.TypeStageStart, Source: name, Stage: "responding",
			}); err != nil {
				return err
			}
			request.Messages = capability.Messages("you are a tutor", rc.Messages(),
				capability.PersonaMessage(rc.Persona()))
			request.Tools = rc.Tools()
			result, err := rc.RunLoop(ctx, request)
			if err != nil {
				return err
			}
			return rc.Emitter().Emit(ctx, event.Event{
				Type: event.TypeResult, Source: name, Stage: "responding", Content: result.Content,
			})
		},
	}
}

func TestANamedPersonaNobodyHasIsRefusedBeforeTheTurnExists(t *testing.T) {
	personas := &stubPersonas{found: false}
	fixture := newRunnerFixture(t, withPersonas(personas))

	_, err := fixture.runner.Start(context.Background(), StartRequest{
		OwnerID: 42, Content: "帮我写一段", PersonaID: "per_elsewhere",
	})

	if !errors.Is(err, ErrPersonaNotFound) {
		t.Fatalf("Start() error = %v, want ErrPersonaNotFound", err)
	}
	// It is asked with the caller, so "not found" can mean "not yours".
	if len(personas.asked) != 1 || personas.asked[0] != "per_elsewhere" {
		t.Errorf("the store was asked for %v, want the named persona", personas.asked)
	}
	if len(personas.callers) != 1 || personas.callers[0].UserID != 42 {
		t.Errorf("the store was asked as %+v, want the turn's account", personas.callers)
	}
	// And nothing was written: no conversation, no turn. A request that could
	// never be served leaves no half of one behind.
	if fixture.sessions.created != 0 {
		t.Errorf("Start() created %d sessions, want none", fixture.sessions.created)
	}
	if turns := fixture.repository.turnCount(); turns != 0 {
		t.Errorf("Start() stored %d turns, want none", turns)
	}
}

func TestANamedPersonaWithNoStoreIsRefusedRatherThanRunWithoutIt(t *testing.T) {
	fixture := newRunnerFixture(t)

	_, err := fixture.runner.Start(context.Background(), StartRequest{Content: "你好", PersonaID: "per_1"})

	// Silently running the turn under no persona would answer in the wrong
	// voice while reporting success, which is the one outcome a client cannot
	// detect.
	if !errors.Is(err, ErrPersonaNotFound) {
		t.Fatalf("Start() error = %v, want ErrPersonaNotFound", err)
	}
	if fixture.sessions.created != 0 {
		t.Errorf("Start() created %d sessions, want none", fixture.sessions.created)
	}
}

func TestAPersonaStoreThatFailedStopsTheTurnAndIsNotReportedAsMissing(t *testing.T) {
	broken := errors.New("the persona store is unreachable")
	fixture := newRunnerFixture(t, withPersonas(&stubPersonas{err: broken}))

	_, err := fixture.runner.Start(context.Background(), StartRequest{Content: "你好", PersonaID: "per_1"})

	// The caller cannot fix a store that failed, so it must not be told the
	// persona is missing — the two send a person to different pages.
	if !errors.Is(err, broken) {
		t.Fatalf("Start() error = %v, want the store's failure", err)
	}
	if errors.Is(err, ErrPersonaNotFound) {
		t.Error("a storage failure was reported as a missing persona")
	}
	if fixture.sessions.created != 0 {
		t.Errorf("Start() created %d sessions, want none", fixture.sessions.created)
	}
}

func TestThePersonaReachesOnlyTheTurnThatNamedIt(t *testing.T) {
	personas := &stubPersonas{instruction: "用诗意的口吻回答", found: true}
	fixture := newRunnerFixture(t, withPersonas(personas))
	fixture.provider.responses = []llm.Response{{Content: "好的"}, {Content: "好的"}}
	fixture.register(t, personified("chat", agentRequest()))

	first := fixture.start(t, StartRequest{OwnerID: 42, Content: "写一句", PersonaID: "per_1"})
	fixture.watch(t, first.Turn.ID)
	// The same conversation, asked again without a persona: if the prompt had
	// been stored it would still be in force here.
	second := fixture.start(t, StartRequest{OwnerID: 42, SessionID: first.Session.ID, Content: "再写一句"})
	fixture.watch(t, second.Turn.ID)

	sent := fixture.provider.asked()
	if len(sent) != 2 {
		t.Fatalf("the model was called %d times, want twice", len(sent))
	}
	if !mentions(sent[0].Messages, "用诗意的口吻回答") {
		t.Errorf("the first turn's model call does not carry the persona:\n%+v", sent[0].Messages)
	}
	if mentions(sent[1].Messages, "用诗意的口吻回答") {
		t.Errorf("the second turn ran under a persona it never named:\n%+v", sent[1].Messages)
	}
	// The persona is a system message and not something anyone said, so it is
	// not part of the conversation that is handed to the next turn either.
	if mentions(sent[1].Messages, "人格设定") {
		t.Errorf("the frame leaked into the history:\n%+v", sent[1].Messages)
	}
	fixture.sessions.mu.Lock()
	stored := append([]session.Message(nil), fixture.sessions.messages[first.Session.ID]...)
	fixture.sessions.mu.Unlock()
	for _, message := range stored {
		if strings.Contains(message.Content, "诗意的口吻") || strings.Contains(message.Content, "人格设定") {
			t.Errorf("the persona was written into the conversation: %+v", message)
		}
	}
	// Two exchanges and no persona row between them.
	if len(stored) != 4 {
		t.Errorf("the conversation holds %d messages, want the two exchanges: %+v", len(stored), stored)
	}
	for i, want := range []string{"写一句", "再写一句"} {
		if stored[i*2].Content != want || stored[i*2].Role != session.RoleUser {
			t.Errorf("message %d is %+v, want the user's %q", i*2, stored[i*2], want)
		}
	}
}

// turnCount is how many turns storage holds, for the "nothing was written"
// assertions.
func (m *memoryRepository) turnCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.turns)
}

func mentions(messages []llm.Message, text string) bool {
	for _, message := range messages {
		if message.Role == llm.RoleSystem && strings.Contains(message.Content, text) {
			return true
		}
	}
	return false
}
