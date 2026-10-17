// Package chat is the general conversation capability: the one a request gets
// when it does not name another.
//
// It owns its turn — the stages it walks and when it stops — but it does not own
// how a model is called. Every call goes through the runtime's loop, which is
// what the capability seam exists for (RT-07).
package chat

import (
	"context"

	"backend/internal/capability"
	"backend/internal/event"
)

// Name is the capability's identifier: what a request asks for, what its events
// are published under, and the key its tuning is configured by.
const Name = "chat"

// The stages, in the order the capability walks them.
//
// Both exist from the start even though V1 calls a model in only one of them,
// because the stage names are what the per-stage token limits are configured
// against (CFG-01): a stage that appears later would need a configuration
// change, not just code.
const (
	// StageExploring is where a capability works out what it is answering.
	// V1 does no model call here: the context is assembled and the budget
	// applied inside the loop, and a second call that produced nothing the
	// user sees would double the cost of every turn for no output. It is the
	// seam V2's context pre-survey lands in, without a protocol change.
	StageExploring = "exploring"
	// StageResponding is where the answer is produced.
	StageResponding = "responding"
)

// Manifest returns what the runtime registers for this capability.
//
// It declares no tools: the always-on set already mounts ask_user, and listing
// a tool here as well would make the manifest say something the mount policy
// decides. Tools it needs beyond the always-on set go here.
func Manifest() capability.Manifest {
	return capability.Manifest{
		Name:        Name,
		Description: "通用对话：理解问题后作答，信息不足时先向用户确认",
		Stages:      []string{StageExploring, StageResponding},
	}
}

// Capability runs conversations.
type Capability struct {
	settings capability.Settings
}

var _ capability.Capability = (*Capability)(nil)

// New returns the capability for the given tuning.
//
// It takes no logger: what goes wrong inside a turn is either the loop's to log
// or the runtime's to turn into a terminal state, and logging it again here
// would report the same failure twice.
func New(settings capability.Settings) (*Capability, error) {
	if err := settings.Validate(Manifest()); err != nil {
		return nil, err
	}
	return &Capability{settings: settings}, nil
}

// Manifest implements capability.Capability.
func (c *Capability) Manifest() capability.Manifest { return Manifest() }

// Run plays a turn out: work out what is being asked, then answer it.
//
// The survey goes in front of the history as part of the answering call's
// material. It is not a thing anyone said, so it is not stored with the
// conversation — and it is a system message, so the loop's trimming keeps it.
func (c *Capability) Run(ctx context.Context, rc capability.RunContext) error {
	grounding, err := c.explore(ctx, rc)
	if err != nil {
		return err
	}
	return capability.Answer(ctx, rc, c.settings, StageResponding,
		capability.Messages(systemPrompt, rc.Messages(),
			capability.PersonaMessage(rc.Persona()), grounding.Message))
}

// emit publishes a stage boundary of this capability's own.
func (c *Capability) emit(ctx context.Context, rc capability.RunContext, kind event.Type, stage string) error {
	return capability.EmitStage(ctx, rc, kind, stage)
}
