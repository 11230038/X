package turn

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"backend/internal/capability"
	"backend/internal/event"
	"backend/internal/llm"
	"backend/internal/tool"
)

// runnerFixture is a whole runtime whose two outside worlds are fakes: the
// model and the database.
//
// Everything between them is the real thing — the runner, the emitter, the hub,
// the loop, the tool plane — which is the point: these tests are about how a
// turn is played out, not about a mock of it.
type runnerFixture struct {
	runner       *Runner
	repository   *memoryRepository
	sessions     *stubSessions
	materials    *stubMaterials
	hub          *Hub
	provider     *scriptedProvider
	naming       *scriptedProvider
	capabilities *capability.Registry
	tools        *tool.Registry
	logs         bytes.Buffer
}

func newRunnerFixture(t *testing.T, adjust ...func(*Options)) *runnerFixture {
	t.Helper()
	fixture := &runnerFixture{
		repository:   newMemoryRepository(),
		sessions:     newStubSessions(),
		materials:    newStubMaterials(),
		provider:     &scriptedProvider{},
		naming:       &scriptedProvider{responses: []llm.Response{{Content: "第一次对话"}}},
		capabilities: capability.NewRegistry(),
		tools:        tool.NewRegistry(),
	}
	hub, err := NewHub(fixture.repository)
	if err != nil {
		t.Fatalf("NewHub() error = %v", err)
	}
	fixture.hub = hub
	// The process registers ask_user once, bound to the runtime's own asker:
	// the turn it has to pause is found in the context of the call, not here.
	if err := fixture.tools.Register(tool.NewAskUserTool(Asker{})); err != nil {
		t.Fatalf("registering ask_user: %v", err)
	}
	titler, err := NewTitler(fixture.naming, "task-model", 64)
	if err != nil {
		t.Fatalf("NewTitler() error = %v", err)
	}

	options := Options{
		Repository:        fixture.repository,
		Hub:               hub,
		Sessions:          fixture.sessions,
		Materials:         fixture.materials,
		Capabilities:      fixture.capabilities,
		Tools:             fixture.tools,
		Policy:            tool.DefaultPolicy(),
		Provider:          fixture.provider,
		Titler:            titler,
		Logger:            slog.New(slog.NewTextHandler(&fixture.logs, nil)),
		DefaultCapability: "chat",
		InputTimeout:      time.Minute,
	}
	for _, apply := range adjust {
		apply(&options)
	}
	runner, err := NewRunner(options)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	fixture.runner = runner
	t.Cleanup(func() { fixture.stop(t) })
	return fixture
}

func (f *runnerFixture) stop(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := f.runner.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func (f *runnerFixture) start(t *testing.T, request StartRequest) Started {
	t.Helper()
	started, err := f.runner.Start(context.Background(), request)
	if err != nil {
		t.Fatalf("Start(%+v) error = %v", request, err)
	}
	return started
}

func (f *runnerFixture) watch(t *testing.T, turnID string) []event.Event {
	t.Helper()
	subscription, err := f.runner.Subscribe(context.Background(), turnID, 0)
	if err != nil {
		t.Fatalf("Subscribe(%s) error = %v", turnID, err)
	}
	defer subscription.Close()
	return drain(t, subscription)
}

func kindsOf(records []event.Event) []event.Type {
	kinds := make([]event.Type, 0, len(records))
	for _, record := range records {
		kinds = append(kinds, record.Type)
	}
	return kinds
}

func only(t *testing.T, records []event.Event, kind event.Type) event.Event {
	t.Helper()
	var found []event.Event
	for _, record := range records {
		if record.Type == kind {
			found = append(found, record)
		}
	}
	if len(found) != 1 {
		t.Fatalf("the stream has %d %s events: %v", len(found), kind, kindsOf(records))
	}
	return found[0]
}

func (f *runnerFixture) register(t *testing.T, capabilities ...capability.Capability) {
	t.Helper()
	for _, c := range capabilities {
		if err := f.capabilities.Register(c); err != nil {
			t.Fatalf("Register(%s) error = %v", c.Manifest().Name, err)
		}
	}
}

// titler is the fixture's namer, for tests that build Options by hand.
func (f *runnerFixture) titler() *Titler {
	titler, err := NewTitler(f.naming, "task-model", 64)
	if err != nil {
		panic(err)
	}
	return titler
}

// until reads a stream until an event of a kind arrives, which is how a test
// waits for a turn to reach a point it can act on.
func until(t *testing.T, subscription *Subscription, kind event.Type) event.Event {
	t.Helper()
	for {
		if record := next(t, subscription); record.Type == kind {
			return record
		}
	}
}

func countOf(records []event.Event, kind event.Type) int {
	count := 0
	for _, record := range records {
		if record.Type == kind {
			count++
		}
	}
	return count
}

func contains(kinds []event.Type, kind event.Type) bool {
	for _, candidate := range kinds {
		if candidate == kind {
			return true
		}
	}
	return false
}
