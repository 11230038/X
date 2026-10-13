package deepsolve

import (
	"context"
	"errors"
	"testing"

	"backend/internal/event"
	"backend/internal/tool"
)

// planning returns a tracker for one turn, and the emitter it reports through.
func planning(t *testing.T) (*tracker, *recordingEmitter) {
	t.Helper()
	emitter := &recordingEmitter{}
	return newTracker(emitter, StageResponding), emitter
}

func TestAPlanOpensAtItsFirstStep(t *testing.T) {
	plan, emitter := planning(t)

	progress, err := plan.Plan(context.Background(), []string{"读题目", "算一下", "写结论"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	if progress.Step != 1 || progress.Total != 3 || progress.Label != "读题目" || progress.Done {
		t.Errorf("the plan opened at %+v", progress)
	}
	// The event is published from here rather than from the capability: the plan
	// moves inside a tool call the capability is blocked on, so this is the only
	// place that knows a step just happened.
	published := emitter.all()
	if len(published) != 1 || published[0].Type != event.TypeProgress {
		t.Fatalf("the plan published %v", emitter.types())
	}
	if published[0].Stage != StageResponding {
		t.Errorf("the progress event is under stage %q", published[0].Stage)
	}
	if published[0].Metadata[event.MetaStep] != 1 ||
		published[0].Metadata[event.MetaTotal] != 3 ||
		published[0].Metadata[event.MetaLabel] != "读题目" {
		t.Errorf("the progress event carries %+v", published[0].Metadata)
	}
}

func TestStepsAdvanceAndTheLastOneFinishes(t *testing.T) {
	plan, emitter := planning(t)
	ctx := context.Background()
	if _, err := plan.Plan(ctx, []string{"读题目", "算一下"}); err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	first, err := plan.Step(ctx)
	if err != nil {
		t.Fatalf("Step() error = %v", err)
	}
	if first.Step != 2 || first.Label != "算一下" || first.Done {
		t.Errorf("after one step the plan is at %+v", first)
	}

	// The second step is the last: the count does not run past the plan, because
	// "第 3/2 步" is not a place a turn can be.
	last, err := plan.Step(ctx)
	if err != nil {
		t.Fatalf("Step() error = %v", err)
	}
	if last.Step != 2 || last.Total != 2 || !last.Done {
		t.Errorf("the last step put the plan at %+v", last)
	}
	// One event per move, in order, so a client that missed one is still told
	// where the turn is by the next.
	if len(emitter.all()) != 3 {
		t.Errorf("three moves published %d events", len(emitter.all()))
	}
}

func TestAStepInAPlanThatIsNotMovingIsRefused(t *testing.T) {
	tests := []struct {
		name   string
		arrive func(*tracker, context.Context)
		want   string
	}{
		{
			name:   "before there is a plan",
			arrive: func(*tracker, context.Context) {},
			want:   "还没有计划",
		},
		{
			name: "after the plan was closed",
			arrive: func(plan *tracker, ctx context.Context) {
				plan.Plan(ctx, []string{"读题目"})
				plan.Finish(ctx)
			},
			want: "已经结束",
		},
		{
			name: "when every step is done",
			arrive: func(plan *tracker, ctx context.Context) {
				plan.Plan(ctx, []string{"读题目"})
				plan.Step(ctx)
			},
			want: "每一步都完成",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, emitter := planning(t)
			ctx := context.Background()
			test.arrive(plan, ctx)
			before := len(emitter.all())

			_, err := plan.Step(ctx)
			var refusal *tool.Refusal
			if !errors.As(err, &refusal) || !contains(refusal.Note, test.want) {
				t.Fatalf("Step() error = %v, want a refusal saying %q", err, test.want)
			}
			// A refusal changes nothing: a plan that published a step nobody
			// took would show progress the turn never made.
			if len(emitter.all()) != before {
				t.Error("a refused step was published as progress")
			}
		})
	}
}

func TestAReplacementPlanStartsOver(t *testing.T) {
	plan, _ := planning(t)
	ctx := context.Background()
	plan.Plan(ctx, []string{"读题目", "算一下", "写结论"})
	plan.Step(ctx)

	progress, err := plan.Replan(ctx, []string{"换个问法", "再试一次"})
	if err != nil {
		t.Fatalf("Replan() error = %v", err)
	}

	// A plan is what is left to do rather than a continuation of the one it
	// replaced, so the count starts again — otherwise a shorter plan would open
	// on a step that does not exist.
	if progress.Step != 1 || progress.Total != 2 || progress.Label != "换个问法" || progress.Done {
		t.Errorf("the new plan opened at %+v", progress)
	}
}

func TestAReplacementWithNothingInItLeavesThePlanAlone(t *testing.T) {
	plan, emitter := planning(t)
	ctx := context.Background()
	plan.Plan(ctx, []string{"读题目"})
	before := len(emitter.all())

	_, err := plan.Replan(ctx, []string{"  ", ""})
	if err == nil {
		t.Fatal("a plan of blank steps was accepted")
	}
	// And it published nothing, because nothing happened: a progress event for a
	// plan that was never adopted would show the user a step the turn is not on.
	if len(emitter.all()) != before {
		t.Error("a refused replacement was published as progress")
	}
	// The old plan survives: a refused replacement is a call that did nothing,
	// and a turn left with no plan at all would be worse than one that kept the
	// plan it had.
	progress, err := plan.Step(ctx)
	if err != nil {
		t.Fatalf("Step() error = %v", err)
	}
	if progress.Step != 1 || progress.Total != 1 {
		t.Errorf("the turn is at %+v, want the plan it already had", progress)
	}
}

func TestPlanStepsAreTrimmedAndBlanksDropped(t *testing.T) {
	plan, _ := planning(t)

	progress, err := plan.Plan(context.Background(), []string{"  读题目 ", "", "  ", "算一下\n"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	// The label is the only thing anyone is told about a step, so a plan of
	// blank strings would be a progress bar moving through nothing.
	if progress.Total != 2 || progress.Label != "读题目" {
		t.Errorf("the plan opened at %+v", progress)
	}
	if got := plan.steps[1]; got != "算一下" {
		t.Errorf("a step kept its whitespace: %q", got)
	}
}

func TestAPlanWithNoStepsAtAllIsRefused(t *testing.T) {
	for _, steps := range [][]string{nil, {}, {"   "}} {
		plan, _ := planning(t)

		_, err := plan.Plan(context.Background(), steps)
		var refusal *tool.Refusal
		if !errors.As(err, &refusal) {
			t.Errorf("Plan(%v) error = %v, want a refusal", steps, err)
		}
	}
}

func TestFinishingClosesThePlanOnce(t *testing.T) {
	plan, _ := planning(t)
	ctx := context.Background()
	plan.Plan(ctx, []string{"读题目", "算一下"})
	plan.Step(ctx)

	progress, err := plan.Finish(ctx)
	if err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	// Closed where it stood, not where it could have got to: a turn that stops
	// early has finished, and saying it reached the last step would be a claim
	// about work nobody did.
	if !progress.Done || progress.Total != 2 {
		t.Errorf("the closed plan is %+v", progress)
	}

	if _, err := plan.Finish(ctx); err == nil {
		t.Error("the plan was closed twice")
	}
	if _, err := plan.Step(ctx); err == nil {
		t.Error("a step was taken in a closed plan")
	}
	// And a plan can be opened again: a turn that closed one and found more to
	// do is not stuck with the tools refusing it.
	if _, err := plan.Plan(ctx, []string{"再查一次"}); err != nil {
		t.Errorf("a new plan after a closed one: %v", err)
	}
}

func TestAPlanThatCannotBeRecordedEndsTheTurn(t *testing.T) {
	emitterErr := errors.New("the event store is unavailable")
	emitter := &recordingEmitter{err: emitterErr}
	plan := newTracker(emitter, StageResponding)

	_, err := plan.Plan(context.Background(), []string{"读题目"})
	if !errors.Is(err, tool.ErrFatal) {
		t.Fatalf("Plan() error = %v, want it to end the turn", err)
	}
	// The model must not be told the tool failed and left to try again: the turn
	// is running without a record of what it did, and that is the runtime's to
	// stop.
	if !errors.Is(err, emitterErr) {
		t.Errorf("the reason was lost: %v", err)
	}
}
