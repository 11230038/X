package turn

import (
	"context"
	"fmt"

	"backend/internal/agentloop"
	"backend/internal/capability"
	"backend/internal/event"
	"backend/internal/llm"
	"backend/internal/tool"
)

// resultWatch is the runtime's view of what a capability produced.
//
// The runtime persists the answer, so it has to see the event that carries it,
// and it has to know whether the client was shown any of it — a turn that fails
// halfway says so differently from one that fails before it starts.
type resultWatch struct {
	inner event.Emitter
	run   *turnRun
	// answer and answered are written only by the goroutine running the turn,
	// which is the only thing that emits through this.
	answer   string
	answered bool
}

var _ event.Emitter = (*resultWatch)(nil)

func (w *resultWatch) Emit(ctx context.Context, record event.Event) error {
	if err := w.inner.Emit(ctx, record); err != nil {
		return err
	}
	switch record.Type {
	case event.TypeContent:
		w.answered = true
	case event.TypeResult:
		w.answer = record.Content
		w.answered = true
	}
	return nil
}

// runContext is what a capability is given while it runs. It is deliberately
// narrow: everything the runtime keeps to itself stays here (RT-07).
type runContext struct {
	emitter  event.Emitter
	tools    []tool.Definition
	messages []llm.Message
	loop     *agentloop.Loop
}

var _ capability.RunContext = runContext{}

func (c runContext) Emitter() event.Emitter   { return c.emitter }
func (c runContext) Tools() []tool.Definition { return c.tools }
func (c runContext) Messages() []llm.Message  { return c.messages }

func (c runContext) RunLoop(ctx context.Context, req agentloop.Request) (agentloop.Result, error) {
	return c.loop.Run(ctx, req)
}

// mountedPlane answers what the model may call for one turn.
//
// It is asked at dispatch time, not composed into a list when the plane was
// built, which is the difference between "the model was shown the schema" and
// "the model may call it" (TOOL-07): a name outside this turn's plane is
// refused however the model came by it.
type mountedPlane struct {
	registry *tool.Registry
	mounted  []string
}

var _ agentloop.Plane = mountedPlane{}

func (p mountedPlane) Tool(name string) (tool.Tool, bool) {
	for _, mounted := range p.mounted {
		if mounted == name {
			return p.registry.Lookup(name)
		}
	}
	return nil, false
}

// Asker routes ask_user to the turn it is being called for.
//
// The tool is registered once for the process while the turn it has to pause is
// per-turn, so the turn is found in the context the call arrives on rather than
// captured when the tool was built.
type Asker struct{}

var _ tool.Asker = Asker{}

// AskUser pauses the calling turn to ask the user a question.
func (Asker) AskUser(ctx context.Context, question string) (string, error) {
	run, ok := ctx.Value(turnKey{}).(*turnRun)
	if !ok {
		return "", fmt.Errorf("turn: ask_user was called outside a turn")
	}
	return run.askUser(ctx, question)
}
