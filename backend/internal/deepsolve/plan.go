package deepsolve

import (
	"context"
	"fmt"
	"strings"

	"backend/internal/event"
	"backend/internal/tool"
)

// tracker is one turn's plan: the steps, how far they have got, and whether the
// turn has said it is finished.
//
// It is created by Run and never kept on the Capability. That is not a detail of
// this struct but the whole reason it is a struct: the capability is registered
// once for the process (assemble.go), so a plan held on a field would be one
// plan shared by every turn running at the same time, and two users would watch
// each other's steps.
type tracker struct {
	emitter event.Emitter
	// stage is what the progress events are published under. It is the
	// capability's one stage, handed in rather than assumed.
	stage string
	// steps is the plan and done counts how many of them are finished, which is
	// also the index of the step in progress.
	steps []string
	done  int
	// closed is what solve_finish sets: the plan is over, whatever step it
	// reached. It is separate from done == len(steps), because a turn may well
	// finish a plan without having marked the last step.
	closed bool
}

var _ tool.Solver = (*tracker)(nil)

// newTracker returns the plan for one turn, reporting progress through that
// turn's emitter.
func newTracker(emitter event.Emitter, stage string) *tracker {
	return &tracker{emitter: emitter, stage: stage}
}

// Plan records a plan and opens it at its first step.
func (t *tracker) Plan(ctx context.Context, steps []string) (tool.Progress, error) {
	cleaned := cleanSteps(steps)
	if len(cleaned) == 0 {
		return tool.Progress{}, &tool.Refusal{Note: "计划里至少要写一步：把打算怎么做写下来，再调用一次"}
	}
	t.set(cleaned)
	return t.publish(ctx)
}

// Step marks the step in progress as done.
//
// Every way of getting it wrong is a refusal rather than a quiet success: the
// model is told which of the three states it is in — no plan, a plan already
// finished, a plan with nothing left — and each of those has a different thing
// to do next. A silent no-op would leave it stepping through a plan that is not
// moving.
func (t *tracker) Step(ctx context.Context) (tool.Progress, error) {
	switch {
	case len(t.steps) == 0:
		return tool.Progress{}, &tool.Refusal{Note: "还没有计划，先用 solve_plan 写下打算怎么做"}
	case t.closed:
		return tool.Progress{}, &tool.Refusal{Note: "这次计划已经结束了，可以直接给出最终答案"}
	case t.done >= len(t.steps):
		return tool.Progress{}, &tool.Refusal{Note: "计划里的每一步都完成了，可以给出答案，或者用 solve_replan 调整计划"}
	}
	t.done++
	return t.publish(ctx)
}

// Replan replaces the plan and opens the new one at its first step.
func (t *tracker) Replan(ctx context.Context, steps []string) (tool.Progress, error) {
	if len(t.steps) == 0 {
		return tool.Progress{}, &tool.Refusal{Note: "还没有计划，直接用 solve_plan 写下打算怎么做"}
	}
	cleaned := cleanSteps(steps)
	if len(cleaned) == 0 {
		return tool.Progress{}, &tool.Refusal{Note: "新计划里至少要写一步，再调用一次"}
	}
	t.set(cleaned)
	return t.publish(ctx)
}

// Finish closes the plan.
func (t *tracker) Finish(ctx context.Context) (tool.Progress, error) {
	switch {
	case len(t.steps) == 0:
		return tool.Progress{}, &tool.Refusal{Note: "还没有计划：不记计划就直接给出答案"}
	case t.closed:
		return tool.Progress{}, &tool.Refusal{Note: "这次计划已经结束了，直接给出最终答案"}
	}
	t.closed = true
	return t.publish(ctx)
}

// set starts a new plan. Both the first plan and a replacement begin at the
// first step: a plan is what is left to do, not a continuation of the one it
// replaced.
func (t *tracker) set(steps []string) {
	t.steps, t.done, t.closed = steps, 0, false
}

// progress is where the plan stands. It is called only with a plan in hand —
// every path that reaches it has refused an empty one — because the alternative
// is reporting a step of a plan that has no steps.
func (t *tracker) progress() tool.Progress {
	index := t.done
	if index > len(t.steps)-1 {
		index = len(t.steps) - 1
	}
	return tool.Progress{
		Step:  index + 1,
		Total: len(t.steps),
		Label: t.steps[index],
		Done:  t.closed || t.done >= len(t.steps),
	}
}

// publish reports where the plan now stands and tells whoever is watching.
//
// This is where the progress event (type 8, first used here) is published, and
// it is published from the state rather than from the capability because a plan
// moves inside a tool call the capability is blocked on: by the time its run
// continues, the turn may have walked three steps. Publishing here also puts the
// event where a reader wants it, between the call and its result.
//
// An event that cannot be recorded ends the turn rather than becoming a failed
// tool result, which is the rule the capability's own events follow: a turn whose
// record is not being written is not one to carry on with, and a result reading
// "the tool could not be run" would have the model try the same step again.
func (t *tracker) publish(ctx context.Context) (tool.Progress, error) {
	progress := t.progress()
	record := event.Event{
		Type:  event.TypeProgress,
		Stage: t.stage,
		Metadata: map[string]any{
			event.MetaStep:  progress.Step,
			event.MetaTotal: progress.Total,
			event.MetaLabel: progress.Label,
		},
	}
	if err := t.emitter.Emit(ctx, record); err != nil {
		return tool.Progress{}, fmt.Errorf("%w: could not record the plan: %w", tool.ErrFatal, err)
	}
	return progress, nil
}

// cleanSteps drops blank steps and the whitespace around the others.
//
// A blank step would be a step whose label is empty, and the label is the only
// thing the model and the user are told about it — a plan of three empty strings
// is a progress bar that moves through nothing.
func cleanSteps(steps []string) []string {
	cleaned := make([]string, 0, len(steps))
	for _, step := range steps {
		if text := strings.TrimSpace(step); text != "" {
			cleaned = append(cleaned, text)
		}
	}
	return cleaned
}
