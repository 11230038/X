package turn

import (
	"strings"
	"testing"

	"backend/internal/llm"
	"backend/internal/tool"
)

// TestTheSourceToolRefusesWhatTheTurnIsNotCarrying runs the whole path with
// nothing stubbed between the model and the material: the tool is the one this
// build registers, the turn is a real one, and the only fake is the model's
// scripted call.
//
// It is the acceptance criterion for the scope rule. A model that writes a label
// this turn never gave out is refused — the refusal reaches it as content it can
// act on, and the library is never reached.
func TestTheSourceToolRefusesWhatTheTurnIsNotCarrying(t *testing.T) {
	fixture := newRunnerFixture(t)
	if err := fixture.tools.Register(tool.NewReadSourceTool(Sourcer{})); err != nil {
		t.Fatalf("registering read_source: %v", err)
	}
	if err := fixture.tools.Register(tool.NewRetrieveTool(Sourcer{})); err != nil {
		t.Fatalf("registering retrieve: %v", err)
	}
	fixture.materials.add("lib_a", "doc_a", "笔记.md")
	// A model that keeps asking for an attachment the turn does not have, which
	// is the only way to see both the refusal and the turn's own ending.
	fixture.provider.stuck = llm.ToolCall{
		ID: "call_1", Name: tool.ReadSourceName, Arguments: `{"label": "2"}`,
	}
	fixture.register(t, looping("chat", agentRequest()))

	started := fixture.start(t, StartRequest{Content: "读第 2 个附件", Attachments: []string{"lib_a"}})
	results := toolResults(t, fixture.watch(t, started.Turn.ID))
	if len(results) == 0 {
		t.Fatal("the call produced no result at all")
	}
	if results[0].Metadata["success"] != false {
		t.Errorf("a read the turn does not carry was reported as served: %+v", results[0])
	}
	if !strings.Contains(results[0].Content, "2") {
		t.Errorf("the refusal = %q, want it to name what was asked for", results[0].Content)
	}

	// The turn carries one attachment, so the library was reached once — to
	// resolve it — and never for the label that does not exist.
	for _, asked := range fixture.materials.asked {
		if strings.HasPrefix(asked, "window") || strings.HasPrefix(asked, "locate") {
			t.Errorf("a refused read reached the library: %v", fixture.materials.asked)
		}
	}
}

// TestATurnWithNoMaterialIsOfferedNoSourceTool is the regression half: a turn
// that carries nothing is offered exactly the plane it was offered before there
// was any material, and the tools it is not offered cannot be called.
func TestATurnWithNoMaterialIsOfferedNoSourceTool(t *testing.T) {
	fixture := newRunnerFixture(t)
	if err := fixture.tools.Register(tool.NewReadSourceTool(Sourcer{})); err != nil {
		t.Fatalf("registering read_source: %v", err)
	}
	fixture.materials.add("lib_a", "doc_a", "笔记.md")
	fixture.provider.stuck = llm.ToolCall{
		ID: "call_1", Name: tool.ReadSourceName, Arguments: `{"label": "1"}`,
	}
	fixture.register(t, looping("chat", agentRequest()))

	started := fixture.start(t, StartRequest{Content: "读点东西"})
	results := toolResults(t, fixture.watch(t, started.Turn.ID))
	if len(results) == 0 {
		t.Fatal("the call produced no result at all")
	}
	if results[0].Metadata["success"] != false {
		t.Errorf("a tool this turn was not offered was allowed to run: %+v", results[0])
	}
	// Not even resolution: a turn carrying nothing has no material to look up,
	// and the library was never consulted.
	if len(fixture.materials.asked) != 0 {
		t.Errorf("a turn carrying nothing reached the library: %v", fixture.materials.asked)
	}
}
