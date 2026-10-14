package reading

import (
	"context"
	"fmt"

	"backend/internal/capability"
	"backend/internal/event"
	"backend/internal/llm"
	"backend/internal/tool"
)

// immersive_reading as a capability: one stage, the reader's own material in
// front of the model, and the question bank at the end of it.
//
// The stage is single and that is the design's answer rather than a shortcut
// (§2.9, PRD §10.2): what a reading turn does — read the passage the reader
// selected, answer about it, file a question if they want one — is held by the
// selection the turn carries and by the conversation, not by phases. A second
// stage would be a second place for "which passage are we on" to live, and the
// two would disagree the first time the reader moved.
//
// It is the same mechanism READ-03's extensions are the side-panel entry to
// (§2.9, PRD §10.1): this capability is the turn, an extension is a panel
// button, and both are handed the passage and nothing else.

// Name is the capability's identifier: what a request asks for, what its events
// are published under, and the key its tuning is configured by.
const Name = "immersive_reading"

// StageResponding is the one stage this capability walks.
//
// The name is the general assistant's, and that is deliberate rather than
// convenient: the per-stage token limits and the sampling are configured against
// stage names, and a capability that invented a name for "answer the reader"
// would need its own entry in the deployment's tuning to say the same thing.
// PRD §10.2 names this stage for both of V4's new capabilities.
const StageResponding = "responding"

// Manifest returns what the runtime registers for this capability.
//
// It declares save_question and nothing else. Reading the passage is not a tool
// call here: the passage is the material the turn carries, and the two source
// tools are mounted by that material rather than by this declaration. What
// READ-04 asks for beyond answering is the ability to keep a question the
// reader asked for, and that is the bank's own tool — one implementation of
// "file a question", shared with the turn that writes questions and with
// mastery.
func Manifest() capability.Manifest {
	return capability.Manifest{
		Name:        Name,
		Description: "沉浸阅读：贴着划出来的原文回答，并把它存进题库",
		Stages:      []string{StageResponding},
		Tools:       []string{tool.SaveQuestionName},
	}
}

// Capability runs reading turns.
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

// Run plays a reading turn out: survey what this turn carries, then answer
// about it.
//
// The survey is the same one every grounded turn runs, and the reader's
// selection is the first thing in it (§2.8). That is the whole of how a
// selection reaches the model: it is material this turn carries, so it goes
// through the path material already goes through rather than through a field of
// its own — which is also why nothing here can be talked into reading a passage
// the turn was not given.
func (c *Capability) Run(ctx context.Context, rc capability.RunContext) error {
	grounding, err := c.survey(ctx, rc)
	if err != nil {
		return err
	}
	return capability.Answer(ctx, rc, c.settings, StageResponding,
		capability.Messages(systemPrompt, rc.Messages(), grounding.Message))
}

// survey runs this turn's pre-survey and publishes what it found.
//
// Unlike the general assistant's, there is no stage to bracket it with: this
// capability walks one stage, and opening an "exploring" stage for a survey
// would put a stage in the stream that no configuration and no manifest names.
// The two events go out under the answering stage, which is where the reader
// sees them anyway — the sources list has to arrive before the answer, and it
// does.
func (c *Capability) survey(ctx context.Context, rc capability.RunContext) (capability.Grounding, error) {
	grounding, err := rc.Ground(ctx, questionOf(rc.Messages()))
	if err != nil {
		return capability.Grounding{}, err
	}
	// A turn carrying nothing comes back with a zero Grounding, and the events
	// below are published exactly when there was something to survey: a reading
	// turn started without a selection is answered from the conversation alone,
	// and its stream stays as short as that says.
	if grounding.Message.Content == "" {
		return grounding, nil
	}
	if err := rc.Emitter().Emit(ctx, event.Event{
		Type:     event.TypeSources,
		Stage:    StageResponding,
		Metadata: map[string]any{event.MetaSources: orEmpty(grounding.Sources)},
	}); err != nil {
		return capability.Grounding{}, err
	}
	// The observation is what makes "there was a passage, and here is what it
	// came to" visible without putting the passage itself in the stream: the
	// material is written for a model, and the sentence is written for a person.
	if err := rc.Emitter().Emit(ctx, event.Event{
		Type:  event.TypeObservation,
		Stage: StageResponding,
		Metadata: map[string]any{event.MetaGrounding: map[string]any{
			"summary":      summaryOf(grounding),
			"source_count": len(grounding.Sources),
			"unavailable":  orEmpty(grounding.Unavailable),
		}},
	}); err != nil {
		return capability.Grounding{}, err
	}
	return grounding, nil
}

// orEmpty keeps an empty list a list, for the reason every other capability that
// publishes one does: a payload that serialises an empty list as null makes
// every reader tell the two apart, and the one that forgets reads a turn that
// looked and found nothing as a turn that said nothing at all.
func orEmpty[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

// summaryOf is the survey in one line, for a client that shows what a turn
// looked at without showing it the material.
func summaryOf(grounding capability.Grounding) string {
	summary := fmt.Sprintf("读了这一轮携带的材料，找到 %d 条相关片段", len(grounding.Sources))
	if len(grounding.Sources) == 0 {
		summary = "读了这一轮携带的材料，没有找到相关片段"
	}
	if len(grounding.Unavailable) > 0 {
		summary += fmt.Sprintf("；有 %d 项材料没有读到", len(grounding.Unavailable))
	}
	return summary
}

// questionOf is what this turn is answering: the newest thing the reader said.
//
// The runtime appends the message that started a turn before the turn runs, so
// the last user message is this turn's own question. It is read from the history
// rather than handed in, because the history is what the runtime already gives a
// capability and a second way to say the same thing would be a second thing to
// keep in step.
func questionOf(history []llm.Message) string {
	for index := len(history) - 1; index >= 0; index-- {
		if history[index].Role == llm.RoleUser {
			return history[index].Content
		}
	}
	return ""
}
