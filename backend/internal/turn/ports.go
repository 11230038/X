package turn

import (
	"context"
	"fmt"

	"backend/internal/agentloop"
	"backend/internal/capability"
	"backend/internal/event"
	"backend/internal/kb"
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
	w.grounded(&record)
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

// grounded adds to a result event the accounting that belongs to the turn.
//
// A capability reports the answer and what it cost; what it cannot report is how
// much material the turn handed over in total, because a turn hands material out
// in several places — the pre-survey before the first call, a tool read after it
// — and only the runtime counts them. Two things are read from that count: how
// many sources the answer could be citing, and how much of the material could
// not be read at all, which is how "the material does not answer this" is told
// apart from "the material was never looked at" (AC-06).
//
// The citation numbers are read against that same count, and the answer is not
// touched: a number naming no source is recorded for diagnosis and rendered as
// plain text, because editing the model's words is a different act from
// measuring them (AIQ-02). A turn that handed out no source at all has no list
// for a number to fail against — a bracketed one there is a list marker or a
// footnote, not a citation.
func (w *resultWatch) grounded(record *event.Event) {
	if record.Type != event.TypeResult {
		return
	}
	if record.Metadata == nil {
		record.Metadata = make(map[string]any, 2)
	}
	if _, set := record.Metadata[event.MetaGrounding]; !set {
		record.Metadata[event.MetaGrounding] = map[string]any{
			"source_count":      w.run.sources,
			"unavailable_count": w.run.unavailable,
		}
	}
	if w.run.sources == 0 {
		return
	}
	if citations := kb.ParseCitations(record.Content, w.run.sources); len(citations.Unresolved) > 0 {
		record.Metadata[event.MetaUnresolvedCitations] = citations.Unresolved
	}
}

// runContext is what a capability is given while it runs. It is deliberately
// narrow: everything the runtime keeps to itself stays here (RT-07).
type runContext struct {
	emitter  event.Emitter
	tools    []tool.Definition
	messages []llm.Message
	loop     *agentloop.Loop
	// run is the turn this context belongs to. It is the one field that is not
	// already a plain value, and it is here for the one method whose answer is
	// about this turn's material rather than about the conversation.
	run *turnRun
}

var _ capability.RunContext = runContext{}

func (c runContext) Emitter() event.Emitter   { return c.emitter }
func (c runContext) Tools() []tool.Definition { return c.tools }
func (c runContext) Messages() []llm.Message  { return c.messages }

func (c runContext) RunLoop(ctx context.Context, req agentloop.Request) (agentloop.Result, error) {
	return c.loop.Run(ctx, req)
}

// Ground surveys this turn's material. The capability asks the question and gets
// what the runtime made of it — it never sees a document id, a collection id or
// a file, which is the same reason the source tools take no scope of their own
// (KB-07).
func (c runContext) Ground(ctx context.Context, query string) (capability.Grounding, error) {
	return c.run.ground(ctx, query)
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

// Sourcer routes the source tools to the turn they are being called for.
//
// It is the same seam Asker uses, and for the same reason: the tools are built
// once for the process while the material they may read belongs to one turn. So
// the turn is found in the context the call arrives on, and there is no way for
// a call to name a file or a collection of its own — which is what makes the
// scope rule a property of the code rather than of the model's restraint.
type Sourcer struct{}

var _ tool.Sources = Sourcer{}

// ReadSource reads part of one attachment this turn carries.
func (Sourcer) ReadSource(ctx context.Context, request tool.ReadRequest) (tool.Reading, error) {
	run, ok := ctx.Value(turnKey{}).(*turnRun)
	if !ok {
		return tool.Reading{}, fmt.Errorf("turn: read_source was called outside a turn")
	}
	return run.readSource(ctx, request)
}

// Retrieve searches the collections this turn carries.
func (Sourcer) Retrieve(ctx context.Context, query string) (tool.Reading, error) {
	run, ok := ctx.Value(turnKey{}).(*turnRun)
	if !ok {
		return tool.Reading{}, fmt.Errorf("turn: retrieve was called outside a turn")
	}
	return run.retrieve(ctx, query)
}
