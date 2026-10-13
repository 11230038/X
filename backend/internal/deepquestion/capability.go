// Package deepquestion is the capability that writes questions: it works out
// what set the user asked for, lays that out, and then writes the questions into
// the question bank one call at a time (CAP-03).
//
// It is the first capability here whose product is not a message. Every other
// one answers in words; this one answers by storing rows, and the answer the
// user reads is the set of questions that appeared while the turn ran. That is
// why its second stage is mostly a tool plane, and why the questions are visible
// one by one as they land rather than all at the end.
package deepquestion

import (
	"context"
	"fmt"

	"backend/internal/capability"
	"backend/internal/tool"
)

// Name is the capability's identifier: what a request asks for, what its events
// are published under, and the key its tuning is configured by.
const Name = "deep_question"

// The two stages, in the order the capability walks them.
//
// The names are fixed by the product's stage table, and they cannot be changed
// without changing the deployment's configuration with them: the per-stage token
// limits are looked up by stage name, and a capability whose stage names differ
// from the general assistant's does not inherit its tuning (CFG-01).
const (
	// StageIdeation is where the turn works out what it is going to write. No
	// model is called here — see ideate.
	StageIdeation = "ideation"
	// StageGeneration is where the model writes the questions, one save_question
	// call each.
	StageGeneration = "generation"
)

// Limits is how many questions one turn may write.
//
// These two are the deployment's numbers and come from configuration, which is
// the opposite of the bounds the save_question tool enforces: how many options a
// choice question may offer is the shape of the interface, and how many
// questions a person wants is a property of the deployment. Both are numbers,
// and only one of them belongs in a settings file.
type Limits struct {
	// Default is how many questions are written when the request does not say.
	Default int
	// Max is the ceiling on one request. It is not arithmetic tidiness: the count
	// is a multiplication, and one request for two hundred questions is two
	// hundred model calls.
	Max int
}

// Validate reports limits a turn could not run under.
func (l Limits) Validate() error {
	if l.Default <= 0 || l.Max <= 0 {
		return fmt.Errorf("%w: %s needs a positive default and maximum, got %d and %d",
			capability.ErrInvalidSettings, Name, l.Default, l.Max)
	}
	if l.Max < l.Default {
		// Written the other way round the turn would refuse the default it hands
		// out itself, and the request that asked for nothing in particular is the
		// one that gets the error.
		return fmt.Errorf("%w: %s has a maximum of %d below its default of %d",
			capability.ErrInvalidSettings, Name, l.Max, l.Default)
	}
	return nil
}

// Manifest returns what the runtime registers for this capability.
//
// save_question is declared here rather than mounted by the policy: it is
// meaningless outside this capability's instruction, and a turn doing anything
// else should not be offered a way to write into the question bank. The source
// tools are not declared — the mount policy brings them when the turn carries
// material, so a question turn that was given a chapter can read it closely
// before writing about it, and one that was given nothing is not offered a
// search over nothing.
func Manifest() capability.Manifest {
	return capability.Manifest{
		Name:        Name,
		Description: "出题：先摊开题组规格，再逐题写进题库",
		Stages:      []string{StageIdeation, StageGeneration},
		Tools:       []string{tool.SaveQuestionName},
	}
}

// Capability runs turns that generate questions.
type Capability struct {
	settings capability.Settings
	limits   Limits
}

var _ capability.Capability = (*Capability)(nil)

// New returns the capability for the given tuning and limits.
//
// Neither is kept per turn: a capability is one per process, and everything a
// turn decides for itself — the plan, how many questions it has written — is
// made inside Run or found in the context of a call. The two fields here are the
// same for every turn this process will ever run.
func New(settings capability.Settings, limits Limits) (*Capability, error) {
	if err := settings.Validate(Manifest()); err != nil {
		return nil, err
	}
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	return &Capability{settings: settings, limits: limits}, nil
}

// Manifest implements capability.Capability.
func (c *Capability) Manifest() capability.Manifest { return Manifest() }

// Run plays the turn out: work out the set, then write it.
//
// The plan goes into the context before the loop runs, which is how the count
// reaches a tool that was built once for the process. It is the same seam the
// solver uses: what one turn knows and a process-wide tool needs travels on the
// context of the call, and never on the capability, which two turns share.
func (c *Capability) Run(ctx context.Context, rc capability.RunContext) error {
	plan, grounding, err := c.ideate(ctx, rc)
	if err != nil {
		return err
	}
	ctx = tool.WithQuestionSet(ctx, plan.Count)
	return capability.Answer(ctx, rc, c.settings, StageGeneration,
		capability.Messages(generationPrompt(plan), rc.Messages(), grounding.Message))
}
