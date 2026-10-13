package chat

import (
	"context"
	"fmt"

	"backend/internal/capability"
	"backend/internal/event"
	"backend/internal/llm"
)

// explore works out what this turn is answering, and no model is called to do
// it: the capability asks the runtime to survey this turn's material and
// publishes what came back (CHAT-09).
//
// It returns the survey because the answering stage has to put it in front of
// the model. Nothing about it is kept on the capability: there is one capability
// per process and one survey per turn, so a field would be a turn's material
// under a name every other turn reads.
func (c *Capability) explore(ctx context.Context, rc capability.RunContext) (capability.Grounding, error) {
	if err := c.emit(ctx, rc, event.TypeStageStart, StageExploring); err != nil {
		return capability.Grounding{}, err
	}

	grounding, err := rc.Ground(ctx, question(rc.Messages()))
	if err != nil {
		return capability.Grounding{}, err
	}
	// A turn carrying nothing comes back with a zero Grounding, and a turn with
	// material always comes back with a message: the two events below are
	// published exactly when there was something to survey, so a plain
	// conversation produces the stream it produced before any of this existed.
	if grounding.Message.Content != "" {
		if err := c.publishSurvey(ctx, rc, grounding); err != nil {
			return capability.Grounding{}, err
		}
	}

	if err := c.emit(ctx, rc, event.TypeStageEnd, StageExploring); err != nil {
		return capability.Grounding{}, err
	}
	return grounding, nil
}

// publishSurvey tells whoever is watching what the survey saw: the sources
// themselves, so the material a turn is standing on is visible while the answer
// is still being written, and then what they amount to.
//
// The two are one event apart rather than one event because they have different
// readers: the sources are the citation list, with the locators a client opens
// them by, and the observation is the sentence a client shows. Neither carries
// the survey message itself, which is a block of material written for a model
// and not for a person.
func (c *Capability) publishSurvey(ctx context.Context, rc capability.RunContext, grounding capability.Grounding) error {
	if err := rc.Emitter().Emit(ctx, event.Event{
		Type:     event.TypeSources,
		Stage:    StageExploring,
		Metadata: map[string]any{event.MetaSources: orEmpty(grounding.Sources)},
	}); err != nil {
		return err
	}
	return rc.Emitter().Emit(ctx, event.Event{
		Type:  event.TypeObservation,
		Stage: StageExploring,
		Metadata: map[string]any{event.MetaGrounding: map[string]any{
			"summary":      summaryOf(grounding),
			"source_count": len(grounding.Sources),
			"unavailable":  orEmpty(grounding.Unavailable),
		}},
	})
}

// orEmpty keeps an empty list a list. A payload that serialises an empty list as
// null makes every reader tell the two apart, and the one that forgets reads a
// turn that looked and found nothing as a turn that said nothing at all.
func orEmpty[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

// summaryOf is the survey in one line, for a client that shows what a turn
// looked at without showing it the material.
func summaryOf(grounding capability.Grounding) string {
	summary := fmt.Sprintf("查看了本轮携带的材料，找到 %d 条相关片段", len(grounding.Sources))
	if len(grounding.Sources) == 0 {
		summary = "查看了本轮携带的材料，没有找到相关片段"
	}
	if len(grounding.Unavailable) > 0 {
		summary += fmt.Sprintf("；有 %d 项材料没有读到", len(grounding.Unavailable))
	}
	return summary
}

// question is what this turn is answering: the newest thing the user said.
//
// The runtime appends the message that started a turn before the turn runs, so
// the last user message is this turn's own question. It is read from the history
// rather than handed in, because the history is what the runtime already gives
// a capability and a second way to say the same thing would be a second thing to
// keep in step.
func question(history []llm.Message) string {
	for index := len(history) - 1; index >= 0; index-- {
		if history[index].Role == llm.RoleUser {
			return history[index].Content
		}
	}
	return ""
}
