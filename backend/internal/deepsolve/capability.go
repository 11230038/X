// Package deepsolve is the capability that plans before it answers: it works the
// problem out as steps, records them, and reports where it has got to as it goes
// (CAP-02).
//
// It ships no executor. The steps it records are what the model is doing in its
// own head and in the messages it writes, which is why the tools that record
// them have no side effects to speak of: planning is not an action, and a plan
// is only as good as the model that follows it.
package deepsolve

import (
	"context"

	"backend/internal/capability"
	"backend/internal/tool"
)

// Name is the capability's identifier: what a request asks for, what its events
// are published under, and the key its tuning is configured by.
const Name = "deep_solve"

// StageResponding is the capability's only stage.
//
// One stage, because the steps are not stages: a capability's stages are what
// the environment configures a token limit for (CFG-01), and every step of a
// plan spends tokens out of the same answering budget. Where the turn has got to
// is a progress event, not a stage boundary — the manifest stays the shape the
// reference implementation declares, and the user-facing detail is carried by an
// event meant for it.
const StageResponding = "responding"

// Manifest returns what the runtime registers for this capability.
//
// The four solve tools are declared here rather than mounted by the policy: they
// are meaningless outside this capability's prompt, and a turn running anything
// else should not be offered a plan to manage. ask_user is not declared — the
// always-on set mounts it, and declaring it again would make the manifest say
// something the policy decides.
func Manifest() capability.Manifest {
	return capability.Manifest{
		Name:        Name,
		Description: "分步解决：先写计划再逐步推进，用户可以看到走到哪一步",
		Stages:      []string{StageResponding},
		Tools: []string{
			tool.SolvePlanName,
			tool.SolveStepName,
			tool.SolveReplanName,
			tool.SolveFinishName,
		},
	}
}

// Capability runs turns that plan.
type Capability struct {
	settings capability.Settings
}

var _ capability.Capability = (*Capability)(nil)

// New returns the capability for the given tuning.
func New(settings capability.Settings) (*Capability, error) {
	if err := settings.Validate(Manifest()); err != nil {
		return nil, err
	}
	return &Capability{settings: settings}, nil
}

// Manifest implements capability.Capability.
func (c *Capability) Manifest() capability.Manifest { return Manifest() }

// Run plays the turn out.
//
// The plan is created here, on the first line, and it is the one thing in this
// package that must not move to New: a capability is one per process while a
// plan is one per turn, so a tracker on a field would be every concurrent turn
// writing into the same plan.
//
// Putting it in the context rather than passing it is what lets the four tools
// find it — they are built once for the process, like ask_user and the source
// tools, and a plan is found the same way the turn's material is: in the context
// the call arrives on. The context goes to the loop, which hands it to each tool
// it dispatches.
func (c *Capability) Run(ctx context.Context, rc capability.RunContext) error {
	ctx = tool.WithSolver(ctx, newTracker(rc.Emitter(), StageResponding))
	return capability.Answer(ctx, rc, c.settings, StageResponding,
		capability.Messages(systemPrompt, rc.Messages(),
			capability.PersonaMessage(rc.Persona())))
}
