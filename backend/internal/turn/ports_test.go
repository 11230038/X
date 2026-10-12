package turn

import (
	"context"
	"encoding/json"
	"testing"

	"backend/internal/capability"
	"backend/internal/event"
	"backend/internal/llm"
	"backend/internal/tool"
)

func TestTheToolPlaneIsWhatTheCapabilityWasGiven(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.provider.responses = []llm.Response{{Content: "好"}}
	fixture.register(t, looping("chat", agentRequest()))

	started := fixture.start(t, StartRequest{Content: "在吗"})
	fixture.repository.awaitStatus(t, started.Turn.ID, StatusCompleted)

	asked := fixture.provider.asked()
	offered := make([]string, 0, len(asked[0].Tools))
	for _, definition := range asked[0].Tools {
		offered = append(offered, definition.Name)
	}
	// The capability declares no tools of its own, so what the model may call
	// is the always-on set and nothing else.
	if len(offered) != 1 || offered[0] != tool.AskUserName {
		t.Errorf("the model was offered %v, want only %q", offered, tool.AskUserName)
	}
}

func TestAToolTheTurnWasNotOfferedCannotBeCalled(t *testing.T) {
	fixture := newRunnerFixture(t)
	// Registered, so the name resolves — but no capability declares it and no
	// policy mounts it, so this turn never offered it to the model.
	unoffered := &stubTool{name: "read_file"}
	if err := fixture.tools.Register(unoffered); err != nil {
		t.Fatalf("registering read_file: %v", err)
	}
	fixture.provider.stuck = llm.ToolCall{ID: "call_1", Name: "read_file", Arguments: `{}`}
	fixture.register(t, looping("chat", agentRequest()))

	started := fixture.start(t, StartRequest{Content: "读一下"})
	seen := fixture.watch(t, started.Turn.ID)

	// Being shown a schema and being allowed to call it are different things
	// (TOOL-07). This model was shown one; it is calling another.
	results := toolResults(t, seen)
	if len(results) == 0 {
		t.Fatal("the call produced no result at all")
	}
	if results[0].Metadata["success"] != false {
		t.Errorf("the call was allowed: %+v", results[0])
	}
	if unoffered.calls != 0 {
		t.Error("the tool ran despite not being part of this turn's plane")
	}
}

func TestEveryCallIsAnswered(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.provider.stuck = llm.ToolCall{ID: "call_1", Name: "missing_tool", Arguments: `{}`}
	fixture.register(t, looping("chat", agentRequest()))

	started := fixture.start(t, StartRequest{Content: "用一下工具"})
	seen := fixture.watch(t, started.Turn.ID)

	// A tool_call with no tool_result would leave a client drawing a call that
	// never came back, and the model waiting on it.
	calls := countOf(seen, event.TypeToolCall)
	results := countOf(seen, event.TypeToolResult)
	if calls == 0 || calls != results {
		t.Errorf("the stream has %d calls and %d results", calls, results)
	}
}

// toolResults returns the tool_result events of a stream.
func toolResults(t *testing.T, records []event.Event) []event.Event {
	t.Helper()
	var results []event.Event
	for _, record := range records {
		if record.Type == event.TypeToolResult {
			results = append(results, record)
		}
	}
	return results
}

// stubTool is a tool that records whether it was called.
type stubTool struct {
	name   string
	calls  int
	result tool.Result
}

var _ tool.Tool = (*stubTool)(nil)

func (s *stubTool) Definition() tool.Definition {
	return tool.Definition{Name: s.name, Description: "asked for by the test"}
}

func (s *stubTool) Invoke(context.Context, tool.Call) (tool.Result, error) {
	s.calls++
	return s.result, nil
}

func TestACapabilityIsGivenTheSameToolPlaneTheModelIs(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.provider.responses = []llm.Response{{Content: "好"}}

	var offered []tool.Definition
	fixture.register(t, stubCapability{
		manifest: capability.Manifest{Name: "chat", Description: "looks", Stages: []string{"responding"}},
		run: func(ctx context.Context, rc capability.RunContext) error {
			offered = rc.Tools()
			request := agentRequest()
			request.Messages = rc.Messages()
			request.Tools = rc.Tools()
			result, err := rc.RunLoop(ctx, request)
			if err != nil {
				return err
			}
			return rc.Emitter().Emit(ctx, event.Event{Type: event.TypeResult, Content: result.Content})
		},
	})

	started := fixture.start(t, StartRequest{Content: "部署"})
	fixture.repository.awaitStatus(t, started.Turn.ID, StatusCompleted)

	if len(offered) != 1 || offered[0].Name != tool.AskUserName {
		t.Fatalf("the capability was given %v, want the mounted plane", offered)
	}
	var schema map[string]any
	if err := json.Unmarshal(offered[0].Parameters, &schema); err != nil {
		t.Errorf("the tool schema is not a JSON object: %v", err)
	}
	// One composition, two readers: a capability working from a different list
	// than the model would be a capability describing a plane that is not
	// there.
	asked := fixture.provider.asked()
	if len(asked[0].Tools) != 1 || asked[0].Tools[0].Name != offered[0].Name {
		t.Errorf("the model was offered %+v, the capability %+v", asked[0].Tools, offered)
	}
}
