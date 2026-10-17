package turn

import (
	"context"
	"fmt"
	"strings"

	"backend/internal/agentloop"
	"backend/internal/authz"
	"backend/internal/capability"
	"backend/internal/event"
	"backend/internal/kb"
	"backend/internal/llm"
	"backend/internal/notebook"
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
	persona  string
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
func (c runContext) Persona() string          { return c.persona }

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

// Paths is the mastery path a turn is about to work on, as the runtime sees it.
//
// The runtime knows that *some* capabilities work on a path and nothing else
// about them: which capabilities those are, what a path is, and whether this
// turn may write it belong to the capability and to the domain, and they are
// joined by whoever assembled the process. So the runtime asks once, before it
// says anything, and the implementation answers both questions at once — the
// context the turn's tools will find their path in, and the claim the turn
// announces in its first event.
//
// It is optional, and a nil one is a build with no mastery path in it: every
// turn then runs as every turn ran before this version.
type Paths interface {
	// Claim settles which path this turn is on and prepares it for the turn's
	// tools.
	//
	// A path another turn is writing is not an error. The turn runs — reading a
	// path is not writing it — and what the claim reports is who holds it, which
	// the tools then say in whatever they refuse (§2.3).
	Claim(ctx context.Context, request ClaimRequest) (Claimed, error)
}

// Readings is the reading layer as the runtime reaches it: the passage a reader
// had chosen when this turn started (§2.8).
//
// It is here for the same reason Paths is: the runtime has to put a reader's
// selection in front of the model, and turning a material and a coordinate into
// text is not something it can do — that is the reading layer's, and it reads
// through the knowledge base the way every other read does. The two ends speak
// their own vocabularies and the join is in assemble, so the runtime learns
// nothing about reading beyond "a selection produces a source".
//
// What it resolves is one selection, and it is asked once per turn: a selection
// is part of one turn's material, and a turn's material is settled before its
// capability runs.
type Readings interface {
	// Passage resolves a declared selection into the source this turn may cite
	// it as — the text at that coordinate, and the document it lives in.
	//
	// A selection that cannot be resolved is an error, and the runtime reports
	// it as material that could not be read rather than failing the turn: the
	// reader's question is still a question, and losing the passage it was about
	// costs the answer its grounding and not its existence (§2.3).
	Passage(ctx context.Context, selection Selection, caller authz.Caller) (event.Source, error)
}

// Personas is the saved prompts an account may run a turn under.
//
// It is one method over one id, and it is declared here rather than in the
// package that stores personas for the same reason Readings is: the runtime
// knows that a turn may carry a prompt and nothing about what a persona is,
// where it is stored, or who may edit it. The join is in assemble.
//
// It is optional in the way Paths and Readings are: a nil one is a build with
// no persona store, where a turn that named one is refused rather than run
// without it.
type Personas interface {
	// Instruction returns the text of one persona, and whether this account has
	// it. A persona belonging to somebody else is not found, because there is no
	// operation in the product for which it would be visible.
	Instruction(ctx context.Context, personaID string, caller authz.Caller) (string, bool, error)
}

// Selection is a passage a client said the reader had chosen, in the vocabulary
// the runtime has for a place in a text: the material, the coordinate, and the
// text the client believes is there.
//
// The client's copy of the text is carried and never trusted. What a model is
// given is what the coordinate resolves to, because a client can be a stale tab
// and a document can be re-parsed underneath one; the text it sent is what the
// two can be compared against when that matters.
type Selection struct {
	MaterialID string
	Locator    event.Locator
	Text       string
}

// Validate checks a selection before a turn is started with it.
//
// The span rules are the locator's own, said again here because this is where a
// bad one stops the request: a negative start, or an end before the start, is a
// coordinate no text has, and it is the client's to fix (ErrInvalidSelection).
// A zero-width span is *not* refused here. Where the reader is looking and what
// they selected are one field on the wire, and a reader who asked a question
// with nothing selected is asking about where they are — which the reading
// layer resolves to the text at that place. Refusing that would make the
// ordinary way to ask about the current unit impossible to send.
func (s Selection) Validate() error {
	if strings.TrimSpace(s.MaterialID) == "" {
		return fmt.Errorf("%w: the selection names no material", ErrInvalidSelection)
	}
	if s.Locator.CharStart < 0 {
		return fmt.Errorf("%w: the selection starts before the text does", ErrInvalidSelection)
	}
	if s.Locator.CharEnd < s.Locator.CharStart {
		return fmt.Errorf("%w: the selection ends before it starts", ErrInvalidSelection)
	}
	return nil
}

// ClaimRequest is what the runtime knows about the turn being started.
type ClaimRequest struct {
	// Capability is the capability this turn resolved to, and it is what the
	// implementation reads to decide whether this turn is on a path at all. That
	// the runtime does not know which capability works on a path is the point:
	// the name travels as the string it already is, and the knowledge stays at
	// the end that has it.
	Capability string
	SessionID  string
	TurnID     string
	// Goal is what this turn is about. It is used only when the conversation has
	// no path yet, where it becomes the new path's goal — and it is the user's
	// own words rather than a summary of them, because a path's goal is read
	// back to the learner as the thing they set out to learn.
	Goal string
	// Caller is who is running this turn. It is here rather than derived from the
	// session because the two are not the same fact: a turn started in a session
	// that has no owner — one written before this version, which only the
	// administrator can see — has an account it is being run on behalf of, and
	// deriving would lose it. A path created for that turn is created for this
	// account, which is the one that will come back for it.
	Caller authz.Caller
}

// Claimed is a turn's path, as the runtime holds it.
type Claimed struct {
	// Context carries the path for the tools this turn dispatches. For a turn on
	// no path it is the context the runtime handed over, unchanged.
	Context context.Context
	// Lease is the claim as the turn's first event reports it, or nil for a turn
	// with no path. It stays opaque here on purpose: §4's four fields are the
	// domain's reading of who holds a path, and the runtime's job is to publish
	// them in the place a client looks, not to have an opinion about them.
	Lease map[string]any
}

// Notebook is the part of the question bank domain the runtime uses: the bank a
// turn writes the questions it generated into.
//
// It is an interface for the same reason Materials is one — a turn is testable
// without a database behind it — and it is held by the runtime rather than by a
// capability, because the two facts a stored question is stamped with are the
// turn's: the conversation it was asked in and the turn that asked it. A
// capability that could reach the bank would be a capability deciding which
// conversation a question belongs to, and it has no way to know.
type Notebook interface {
	// Save stores one question and reports what that did. A question written
	// twice is one question: the bank finds the row by content address, so the
	// second call rewrites the first rather than adding a copy.
	//
	// The caller is the turn's. A question is written in a conversation's name
	// and belongs to the account running it, and the session id is not a
	// substitute: the session may have no owner, and the bank files the row
	// under the account either way.
	Save(ctx context.Context, question notebook.Question, caller authz.Caller) (notebook.Upserted, error)
}

var _ Notebook = (*notebook.Service)(nil)

// Banker routes save_question to the turn it is being called for.
//
// It is the same seam Asker and Sourcer are, and it answers the same question in
// two parts: what material this turn can file a question under, and where the
// question goes. Neither is an argument the model supplies — a file id would be
// a material from anywhere, and a session id would be somebody else's
// conversation — so both are read off the turn the call arrived on.
type Banker struct{}

var _ tool.QuestionBank = Banker{}

// Carried lists the attachments this turn holds, under the labels the source
// tools number them by.
//
// The id is the stored file rather than the parsed document, partly because a
// material is what the user attached and the file exists whether or not the
// parse worked, and partly because the two cannot be told apart later: a
// question records one id and one word saying which id space it is in, so every
// question this turn writes has to agree about which of the two it means.
func (Banker) Carried(ctx context.Context) []tool.Carried {
	run, ok := ctx.Value(turnKey{}).(*turnRun)
	if !ok {
		return nil
	}
	carried := make([]tool.Carried, 0, len(run.material.Attachments))
	for _, attachment := range run.material.Attachments {
		carried = append(carried, tool.Carried{Label: attachment.Label, MaterialID: attachment.FileID})
	}
	return carried
}

// Save stores one question in this turn's name.
func (Banker) Save(ctx context.Context, question notebook.Question) (tool.Saved, error) {
	run, ok := ctx.Value(turnKey{}).(*turnRun)
	if !ok {
		return tool.Saved{}, fmt.Errorf("turn: save_question was called outside a turn")
	}
	return run.saveQuestion(ctx, question)
}

// saveQuestion stamps a question with the turn it came from and writes it down.
//
// The two stamps are written once and never rewritten (the bank's upsert leaves
// them alone on a second write), because a question outlives the conversation
// that produced it: a later turn that regenerates the same question does not
// take it over.
//
// Nothing here is locked, and nothing needs to be: a turn's tool calls are
// dispatched one at a time by the goroutine running the turn, so the count below
// is only ever touched by that one goroutine. The mutex on the run is there for
// the calls that come from outside it.
func (r *turnRun) saveQuestion(ctx context.Context, question notebook.Question) (tool.Saved, error) {
	question.SessionID, question.TurnID = r.session.ID, r.turn.ID
	upserted, err := r.runner.questions.Save(ctx, question, r.caller)
	if err != nil {
		return tool.Saved{}, err
	}
	r.filed++
	saved := tool.Saved{
		EntryID:    upserted.ID,
		QuestionID: upserted.QuestionID,
		Created:    upserted.Created,
		Filed:      r.filed,
	}
	// A turn that never laid out a plan has no denominator, and the count is absent
	// rather than zero so that "第 1 道" is what is said instead of "第 1/0 道".
	if total, ok := tool.QuestionSetTotal(ctx); ok {
		saved.Planned = total
	}
	return saved, nil
}
