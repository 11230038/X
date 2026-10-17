package capability

import (
	"context"

	"backend/internal/agentloop"
	"backend/internal/event"
	"backend/internal/llm"
	"backend/internal/tool"
)

// Capability is a way of holding a conversation: it owns a whole turn, from the
// stages it runs to the point it decides the turn is over.
//
// Run is the capability's own code, in order, deciding what each stage does and
// when to move between them. What it cannot do is call a model any other way
// than through RunContext.RunLoop, which is how RT-07 is enforced: there is one
// execution engine, and a capability has no way to bring its own (RT-07).
type Capability interface {
	Manifest() Manifest
	// Run plays the turn out. The context is the turn's, not a request's: it is
	// cancelled when the turn is cancelled, and not when a client disconnects
	// (AC-04). A returned error is the capability saying the turn failed; a
	// cancelled context is not an error the capability should convert.
	Run(ctx context.Context, rc RunContext) error
}

// RunContext is what a turn gives a capability while it runs.
//
// It is an interface the runtime satisfies rather than a struct the capability
// receives, so the runtime keeps the parts a capability must not reach: how
// events are persisted, whether the turn is still active, and what a model call
// costs. Everything exposed here is either the capability's own material or a
// request to run something under the runtime's control.
type RunContext interface {
	// Emitter publishes an event into the turn's stream. The capability's own
	// events — stage boundaries, results — go here; the loop publishes the
	// model's output and the tool traffic itself, so a capability does not
	// repeat them.
	Emitter() event.Emitter
	// Tools is the plane this turn may call, as the model sees it. It is already
	// composed by the mount policy: the capability declares what it needs in its
	// manifest, and the runtime decides what the deployment actually mounts.
	Tools() []tool.Definition
	// Messages is the conversation so far, oldest first.
	Messages() []llm.Message
	// Persona is the instruction this turn was started under, or empty when it
	// named none.
	//
	// It is the account's own text, resolved by the runtime before the turn
	// existed. What it is not is authority: a capability is given it to put in
	// front of the model, and nothing in the runtime reads it to decide what the
	// turn may do. A capability that used it for anything else would be making
	// a permission decision out of a prompt.
	Persona() string
	// RunLoop runs one stage through the shared engine. The request's stage,
	// messages and tools normally come from this context; the settings that vary
	// per stage — the model and its sampling — are the capability's to supply,
	// because only it knows what the stage is for.
	RunLoop(ctx context.Context, req agentloop.Request) (agentloop.Result, error)
	// Ground runs the read-only context pre-survey for this turn's material.
	// It reads sources, writes nothing, and returns the findings as both a
	// citation-ready list and a message ready to prepend to a model call.
	//
	// It is here rather than absent because the survey's product has to reach the
	// model, and a capability can build messages but cannot read material. The
	// only other way to give it one would be for a capability to import the
	// knowledge base package, which would make the layer that decides what a turn
	// does depend on the layer that stores what it reads.
	Ground(ctx context.Context, query string) (Grounding, error)
}

// Grounding is what a turn's own material offers for one question.
//
// It is what the runtime makes of the material, handed to a capability that has
// no way to reach the material itself: scope stays the turn's (KB-07), and a
// capability decides only whether and when to survey, never what.
type Grounding struct {
	// Sources are the passages the survey found, in the turn's own numbering and
	// ready to cite. They are the same list a sources event carries.
	Sources []event.Source
	// Message is the folded conclusion, ready to put in front of a model call:
	// what this turn carries, what was found in it, and what could not be read.
	//
	// It is a system message, because it is the service's account of the turn's
	// material rather than anything anyone said. A turn that carries nothing gets
	// a zero Message, and that is what "there was nothing to survey" looks like:
	// a capability that sent the empty message anyway would put a blank system
	// turn in front of every conversation.
	Message llm.Message
	// Unavailable says what this turn was to read and could not, with the reason:
	// an attachment that is a picture, a document that failed to parse, a
	// collection that has been deleted. It has to reach both sides — the model,
	// which has to know what it does not have, and the user, who has to be told
	// which material is missing (ATT-04, KB-07).
	Unavailable []string
}
