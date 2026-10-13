package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// The tools a capability uses to manage a plan. They are named together because
// they are one contract: a plan, a step of it, a new plan, and the end.
const (
	// SolvePlanName records what the turn intends to do.
	SolvePlanName = "solve_plan"
	// SolveStepName marks the step in progress as done.
	SolveStepName = "solve_step"
	// SolveReplanName replaces the plan.
	SolveReplanName = "solve_replan"
	// SolveFinishName closes the plan.
	SolveFinishName = "solve_finish"
)

// ErrNoSolver reports a solve tool called for a turn that has no plan to manage.
//
// It is a defect rather than a model's mistake: the tools are mounted by a
// capability that has a plan, so a call arriving without one means the plane and
// the capability disagree.
var ErrNoSolver = errors.New("tool: no plan belongs to this turn")

// Solver is the plan one turn is following.
//
// The state is the capability's — it belongs to a turn, and a capability is one
// per process — so this is an interface the capability implements and these
// tools only use. Each method returns where the plan stands afterwards, and the
// implementation is the one that publishes it: the plan moves inside a tool call
// the capability cannot see, so nothing else is in a position to say so.
type Solver interface {
	// Plan records a plan. An empty one is refused.
	Plan(ctx context.Context, steps []string) (Progress, error)
	// Step marks the step in progress as done and moves to the next.
	Step(ctx context.Context) (Progress, error)
	// Replan replaces the plan with a new one.
	Replan(ctx context.Context, steps []string) (Progress, error)
	// Finish closes the plan, whatever step it reached.
	Finish(ctx context.Context) (Progress, error)
}

// Progress is where a turn stands in its plan, which is what a progress event
// carries and what a solve tool reports back to the model.
type Progress struct {
	// Step is the step the turn is on, counting from one. A plan that is closed
	// or finished reports its last step rather than one past it.
	Step int
	// Total is how many steps the plan has.
	Total int
	// Label is that step's text.
	Label string
	// Done reports that there is nothing left: the last step is finished, or the
	// plan was closed.
	Done bool
}

// solverKey is the context key a turn puts its plan under.
type solverKey struct{}

// WithSolver returns a context carrying the plan of the turn being run.
//
// It is here rather than in the capability for the same reason the source tools
// declare their port here: the tools are built once for the process while a plan
// belongs to one turn, so the plan has to be found in the context of the call.
// What is different is who puts it there — a turn's material is the runtime's,
// and its plan is the capability's own — so the capability installs it and this
// package only says how.
func WithSolver(ctx context.Context, solver Solver) context.Context {
	return context.WithValue(ctx, solverKey{}, solver)
}

// solverFrom returns the plan of the turn this call belongs to.
func solverFrom(ctx context.Context) (Solver, error) {
	solver, ok := ctx.Value(solverKey{}).(Solver)
	if !ok {
		return nil, ErrNoSolver
	}
	return solver, nil
}

// SolvePlanTool writes down what the turn intends to do.
type SolvePlanTool struct{}

var _ Tool = (*SolvePlanTool)(nil)

// NewSolvePlanTool returns the tool.
func NewSolvePlanTool() *SolvePlanTool { return &SolvePlanTool{} }

// Definition declares the steps as required. The description is written for the
// model, and it says what the tool does *not* do as plainly as what it does: a
// model that thought solve_plan would carry the work out for it would plan and
// then wait for something to happen.
func (t *SolvePlanTool) Definition() Definition {
	return Definition{
		Name: SolvePlanName,
		Description: "把接下来打算怎么解决这个问题的步骤写下来。" +
			"这个工具只记录计划，不会替你执行任何一步——推理和计算都由你自己完成。" +
			"一次调用写一整份计划，之后用 solve_step 逐步推进。",
		Parameters: solveStepsSchema(),
	}
}

// Invoke records the plan.
func (t *SolvePlanTool) Invoke(ctx context.Context, call Call) (Result, error) {
	solver, err := solverFrom(ctx)
	if err != nil {
		return Result{}, err
	}
	steps, err := planSteps(call.Arguments, SolvePlanName)
	if err != nil {
		return Result{}, err
	}

	progress, err := solver.Plan(ctx, steps)
	if err != nil {
		return refused(err)
	}
	return Result{
		Content:  fmt.Sprintf("计划已记下，共 %d 步。现在第 1 步：%s", progress.Total, progress.Label),
		Success:  true,
		Metadata: progressMetadata(progress),
	}, nil
}

// SolveStepTool marks the step in progress as done.
type SolveStepTool struct{}

var _ Tool = (*SolveStepTool)(nil)

// NewSolveStepTool returns the tool.
func NewSolveStepTool() *SolveStepTool { return &SolveStepTool{} }

// Definition describes one step, and it takes no arguments at all.
//
// Two things could have been arguments: which step, and what happened in it.
// Neither is here. The turn is always on exactly one step, so a number the model
// could get wrong would only be a way for the plan and the record to disagree;
// and what happened in a step is the model's own narration, which is already in
// the stream as the content it wrote. What a reader wants from a step event is
// where the turn has got to, and the progress event says that.
func (t *SolveStepTool) Definition() Definition {
	return Definition{
		Name: SolveStepName,
		Description: "把计划里当前这一步标记为完成，然后继续下一步。" +
			"它不接受参数：做完一步就调用一次，不要一次标记多步。",
	}
}

// Invoke advances the plan.
func (t *SolveStepTool) Invoke(ctx context.Context, call Call) (Result, error) {
	solver, err := solverFrom(ctx)
	if err != nil {
		return Result{}, err
	}

	progress, err := solver.Step(ctx)
	if err != nil {
		return refused(err)
	}
	return Result{
		Content:  stepText(progress),
		Success:  true,
		Metadata: progressMetadata(progress),
	}, nil
}

// stepText says where the turn is now, which is the whole point of the call: an
// acknowledgement with no position in it leaves the model to remember how far it
// had got, and the plan is there precisely because it does not have to.
func stepText(progress Progress) string {
	if progress.Done {
		return fmt.Sprintf("计划里的 %d 步都已经完成，可以给出最终答案了。", progress.Total)
	}
	return fmt.Sprintf("这一步已完成。现在第 %d/%d 步：%s", progress.Step, progress.Total, progress.Label)
}

// SolveReplanTool replaces the plan.
type SolveReplanTool struct{}

var _ Tool = (*SolveReplanTool)(nil)

// NewSolveReplanTool returns the tool.
func NewSolveReplanTool() *SolveReplanTool { return &SolveReplanTool{} }

// Definition asks for the reason as well as the steps.
//
// The reason is not read by anything here, and it is not dead: a call's
// arguments are in the event stream, so the sentence lands in the record beside
// the plan it explains. What a plan changed to is position, which the progress
// event carries; why it changed is not, and it is the one thing a reader of the
// events afterwards cannot work out for themselves.
func (t *SolveReplanTool) Definition() Definition {
	return Definition{
		Name: SolveReplanName,
		Description: "原来的计划行不通时，用这份新的计划替换它。" +
			"reason 写一句为什么改：它会连同新计划一起记进这次调用里。" +
			"只有确实走不通时才改，能继续做下去就不要改。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"steps": {
					"type": "array",
					"items": {"type": "string"},
					"description": "新的计划，按先后顺序排列"
				},
				"reason": {
					"type": "string",
					"description": "为什么要改计划，一句话"
				}
			},
			"required": ["steps", "reason"],
			"additionalProperties": false
		}`),
	}
}

// Invoke replaces the plan.
func (t *SolveReplanTool) Invoke(ctx context.Context, call Call) (Result, error) {
	solver, err := solverFrom(ctx)
	if err != nil {
		return Result{}, err
	}
	steps, err := planSteps(call.Arguments, SolveReplanName)
	if err != nil {
		return Result{}, err
	}

	progress, err := solver.Replan(ctx, steps)
	if err != nil {
		return refused(err)
	}
	return Result{
		Content:  fmt.Sprintf("计划已改，共 %d 步。现在第 1 步：%s", progress.Total, progress.Label),
		Success:  true,
		Metadata: progressMetadata(progress),
	}, nil
}

// SolveFinishTool closes the plan.
type SolveFinishTool struct{}

var _ Tool = (*SolveFinishTool)(nil)

// NewSolveFinishTool returns the tool.
func NewSolveFinishTool() *SolveFinishTool { return &SolveFinishTool{} }

// Definition asks for the result in one sentence, for the same reason
// solve_replan asks why: the outcome is the one thing about a finished plan that
// the events do not already carry, and it lands in the call's own record.
func (t *SolveFinishTool) Definition() Definition {
	return Definition{
		Name: SolveFinishName,
		Description: "计划结束时调用，然后给出最终答案。" +
			"summary 用一句话写这次做到了什么，它会记进这次调用里。" +
			"还没走完计划就不要调用它。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"summary": {
					"type": "string",
					"description": "这次解出了什么，一句话"
				}
			},
			"required": ["summary"],
			"additionalProperties": false
		}`),
	}
}

// Invoke closes the plan.
func (t *SolveFinishTool) Invoke(ctx context.Context, call Call) (Result, error) {
	solver, err := solverFrom(ctx)
	if err != nil {
		return Result{}, err
	}

	progress, err := solver.Finish(ctx)
	if err != nil {
		return refused(err)
	}
	return Result{
		Content:  fmt.Sprintf("计划已结束（%d 步）。现在给出最终答案。", progress.Total),
		Success:  true,
		Metadata: progressMetadata(progress),
	}, nil
}

// progressMetadata is what a solve tool reports alongside its text, in the same
// shape a progress event carries: a client reading either sees the same three
// facts about where the turn is.
func progressMetadata(progress Progress) map[string]any {
	return map[string]any{
		"step":  progress.Step,
		"total": progress.Total,
		"label": progress.Label,
	}
}

// solveStepsSchema is the argument schema the two tools that take a plan share.
func solveStepsSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"steps": {
				"type": "array",
				"items": {"type": "string"},
				"description": "按先后顺序排列的步骤，每一步一句话"
			}
		},
		"required": ["steps"],
		"additionalProperties": false
	}`)
}

// planSteps reads a plan's steps out of a call's arguments.
//
// Missing steps are not checked for here: an empty or absent list is a thing the
// model can fix by calling again, so it is the state's to refuse with a sentence
// saying what to do. What is left is the mistake with no fix — arguments that
// are not a JSON object at all.
func planSteps(arguments json.RawMessage, tool string) ([]string, error) {
	var args struct {
		Steps []string `json:"steps"`
	}
	if err := json.Unmarshal(arguments, &args); err != nil {
		return nil, fmt.Errorf("tool: %s arguments are not a JSON object: %w", tool, err)
	}
	return args.Steps, nil
}
