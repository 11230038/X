// Package askquestions is the capability that clarifies before it answers: it
// asks the user the one question the answer turns on, and answers with what came
// back (CAP-04).
//
// It is a capability rather than a mode of the general assistant because it is a
// different bargain with the user — a turn that may stop and ask — and because
// being one makes it selectable (CFG-03) instead of a paragraph in a prompt
// whose effect nobody can predict from the outside.
package askquestions

import (
	"context"

	"backend/internal/capability"
)

// Name is the capability's identifier: what a request asks for, what its events
// are published under, and the key its tuning is configured by.
const Name = "ask_questions"

// StageResponding is the capability's only stage.
//
// One stage, because the asking is not a phase of the turn — it happens inside
// the answering one, as a tool call the model makes, and the turn pauses there
// and carries on (WS-09). A stage of its own would be a boundary nothing crosses
// differently.
const StageResponding = "responding"

// Manifest returns what the runtime registers for this capability.
//
// It declares no tools. What it needs to ask a question is the always-on
// ask_user, and naming it here as well would make the manifest claim a mount the
// policy decides.
func Manifest() capability.Manifest {
	return capability.Manifest{
		Name:        Name,
		Description: "先澄清再作答：信息不足时只问一个最关键的问题，拿到答复再回答",
		Stages:      []string{StageResponding},
	}
}

// Capability runs turns that may stop to ask.
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
// There is nothing between the stage opening and the answer: the clarifying
// question is a tool call the model makes from inside the answering stage, and
// the runtime carries the user's reply back to it as that call's result. So this
// capability's entire behaviour is the instruction it puts in front of the
// conversation — which is the design's claim about it, visible here as the
// absence of anything else.
func (c *Capability) Run(ctx context.Context, rc capability.RunContext) error {
	return capability.Answer(ctx, rc, c.settings, StageResponding,
		capability.Messages(systemPrompt, rc.Messages(),
			capability.PersonaMessage(rc.Persona())))
}
