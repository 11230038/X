package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// stubSolver is one turn's plan as the tools see it: it answers out of what a
// test put in and records what it was asked for. The plan's own rules — what
// counts as a step, what happens to a step marked twice — belong to the
// capability that keeps the plan, and are tested there.
type stubSolver struct {
	progress Progress
	err      error
	// asked is every operation the tools requested, in order.
	asked []string
	// steps is what the last plan handed over looked like.
	steps []string
}

var _ Solver = (*stubSolver)(nil)

func (s *stubSolver) Plan(_ context.Context, steps []string) (Progress, error) {
	s.asked = append(s.asked, "plan")
	s.steps = steps
	return s.progress, s.err
}

func (s *stubSolver) Step(_ context.Context) (Progress, error) {
	s.asked = append(s.asked, "step")
	return s.progress, s.err
}

func (s *stubSolver) Replan(_ context.Context, steps []string) (Progress, error) {
	s.asked = append(s.asked, "replan")
	s.steps = steps
	return s.progress, s.err
}

func (s *stubSolver) Finish(_ context.Context) (Progress, error) {
	s.asked = append(s.asked, "finish")
	return s.progress, s.err
}

// solveCall builds the invocation a model would make of one solve tool.
func solveCall(t *testing.T, name, arguments string) Call {
	t.Helper()
	if arguments == "" {
		arguments = "{}"
	}
	if !json.Valid([]byte(arguments)) {
		t.Fatalf("the test's arguments are not JSON: %s", arguments)
	}
	return Call{ID: "call_1", Name: name, Arguments: json.RawMessage(arguments)}
}

// bound returns a context carrying a plan, which is what the capability of a
// turn that plans puts in front of every call.
func bound(solver Solver) context.Context {
	return WithSolver(context.Background(), solver)
}

func TestEverySolveToolExplainsItselfToTheModel(t *testing.T) {
	// The description is the only thing telling the model what these tools do,
	// and the one thing they do not do — carry the work out — is the mistake
	// worth spelling out: a model that planned and then waited for something to
	// happen would produce a plan and no answer.
	for _, tool := range []Tool{NewSolvePlanTool(), NewSolveStepTool(), NewSolveReplanTool(), NewSolveFinishTool()} {
		definition := tool.Definition()
		if definition.Name == "" {
			t.Fatal("a solve tool has a blank name")
		}
		if len(definition.Description) < 20 {
			t.Errorf("%s says almost nothing: %q", definition.Name, definition.Description)
		}
	}
	if !strings.Contains(NewSolvePlanTool().Definition().Description, "不会替你执行") {
		t.Error("solve_plan does not say that it only records the plan")
	}
}

func TestASolveToolCalledWithoutAPlanIsADefectNotARefusal(t *testing.T) {
	// No plan in the context: the tools are mounted by a capability that keeps
	// one, so this is the plane and the capability disagreeing. The model is not
	// told how to ask differently, because there is no way to ask differently.
	for _, invoke := range []func(context.Context, Call) (Result, error){
		NewSolvePlanTool().Invoke,
		NewSolveStepTool().Invoke,
		NewSolveReplanTool().Invoke,
		NewSolveFinishTool().Invoke,
	} {
		if _, err := invoke(context.Background(), solveCall(t, SolveStepName, "")); !errors.Is(err, ErrNoSolver) {
			t.Errorf("a call with no plan returned %v, want ErrNoSolver", err)
		}
	}
}

func TestSolvePlanRecordsTheStepsAndReportsTheFirst(t *testing.T) {
	solver := &stubSolver{progress: Progress{Step: 1, Total: 3, Label: "读题目"}}
	ctx := bound(solver)

	result, err := NewSolvePlanTool().Invoke(ctx, solveCall(t, SolvePlanName, `{"steps": ["读题目", "算一下", "写结论"]}`))
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}

	if len(solver.asked) != 1 || solver.asked[0] != "plan" {
		t.Fatalf("the plan was asked for %v", solver.asked)
	}
	if len(solver.steps) != 3 || solver.steps[2] != "写结论" {
		t.Errorf("the plan recorded was %v", solver.steps)
	}
	if !result.Success {
		t.Errorf("a recorded plan was reported as failed: %+v", result)
	}
	// The model is told where it now stands. A tool that only said "ok" would
	// leave it to remember, which is what the plan exists to spare it.
	for _, want := range []string{"3", "读题目"} {
		if !strings.Contains(result.Content, want) {
			t.Errorf("the result does not mention %q: %s", want, result.Content)
		}
	}
	if result.Metadata["step"] != 1 || result.Metadata["total"] != 3 || result.Metadata["label"] != "读题目" {
		t.Errorf("the result reports %+v", result.Metadata)
	}
}

func TestSolvePlanRefusesAPlanWithNothingInIt(t *testing.T) {
	solver := &stubSolver{err: &Refusal{Note: "计划里至少要写一步：把打算怎么做写下来，再调用一次"}}
	ctx := bound(solver)

	result, err := NewSolvePlanTool().Invoke(ctx, solveCall(t, SolvePlanName, `{"steps": []}`))
	if err != nil {
		t.Fatalf("Invoke() error = %v, want the state's refusal as a result", err)
	}

	// The sentence is the whole point of a refusal: the loop's generic failure
	// text would have the model try the same empty plan again.
	if result.Success || !strings.Contains(result.Content, "至少要写一步") {
		t.Errorf("an empty plan produced %+v", result)
	}
}

func TestSolvePlanArgumentsThatAreNotAnObjectAreAnError(t *testing.T) {
	// A mistake about the call rather than about the turn: there is nothing the
	// model could fix by planning differently, and the loop has a sentence for
	// exactly this.
	for _, arguments := range []string{`[]`, `"steps"`, `7`} {
		_, err := NewSolvePlanTool().Invoke(bound(&stubSolver{}), solveCall(t, SolvePlanName, arguments))
		if err == nil {
			t.Errorf("arguments %s were accepted", arguments)
		}
	}
}

func TestSolveStepTakesNoArgumentsAndReportsWhereTheTurnIs(t *testing.T) {
	solver := &stubSolver{progress: Progress{Step: 2, Total: 3, Label: "算一下"}}
	ctx := bound(solver)

	result, err := NewSolveStepTool().Invoke(ctx, solveCall(t, SolveStepName, ""))
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}

	if len(solver.asked) != 1 || solver.asked[0] != "step" {
		t.Fatalf("the step was asked for %v", solver.asked)
	}
	// Both numbers, because either alone reads as the other one: "第 2 步" is not
	// "还剩 2 步".
	for _, want := range []string{"2/3", "算一下"} {
		if !strings.Contains(result.Content, want) {
			t.Errorf("the result does not mention %q: %s", want, result.Content)
		}
	}
	if result.Metadata["step"] != 2 || result.Metadata["label"] != "算一下" {
		t.Errorf("the result reports %+v", result.Metadata)
	}
	// No parameters at all: the schema is what the model reads to decide how to
	// call it, and an argument nobody reads would be an invitation to guess.
	if definition := NewSolveStepTool().Definition(); len(definition.Parameters) != 0 {
		t.Errorf("solve_step declares parameters: %s", definition.Parameters)
	}
}

func TestSolveStepSaysWhenThereIsNothingLeft(t *testing.T) {
	solver := &stubSolver{progress: Progress{Step: 3, Total: 3, Label: "写结论", Done: true}}

	result, err := NewSolveStepTool().Invoke(bound(solver), solveCall(t, SolveStepName, ""))
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}

	// A finished plan and a plan that moved on are different instructions to the
	// model: one has an answer to write, the other has a step to do.
	if !strings.Contains(result.Content, "最终答案") {
		t.Errorf("the last step produced %q", result.Content)
	}
}

func TestSolveStepRefusesAStepInAPlanThatIsNotMoving(t *testing.T) {
	for _, note := range []string{"还没有计划", "已经结束"} {
		solver := &stubSolver{err: &Refusal{Note: note}}

		result, err := NewSolveStepTool().Invoke(bound(solver), solveCall(t, SolveStepName, ""))
		if err != nil {
			t.Fatalf("Invoke() error = %v", err)
		}
		if result.Success || result.Content != note {
			t.Errorf("a step with nothing to mark produced %+v", result)
		}
	}
}

func TestSolveReplanReplacesThePlan(t *testing.T) {
	solver := &stubSolver{progress: Progress{Step: 1, Total: 2, Label: "换个问法"}}

	result, err := NewSolveReplanTool().Invoke(
		bound(solver),
		solveCall(t, SolveReplanName, `{"steps": ["换个问法", "再试一次"], "reason": "这条路查不到公开数据"}`),
	)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}

	if len(solver.asked) != 1 || solver.asked[0] != "replan" {
		t.Fatalf("the replacement was asked for %v", solver.asked)
	}
	if len(solver.steps) != 2 || solver.steps[0] != "换个问法" {
		t.Errorf("the new plan was %v", solver.steps)
	}
	// A new plan begins at its first step, and the model is told so; the reason
	// is not read here because it is already in the call's own record.
	if !strings.Contains(result.Content, "换个问法") || !strings.Contains(result.Content, "1") {
		t.Errorf("the result = %q", result.Content)
	}
}

func TestSolveFinishClosesThePlan(t *testing.T) {
	solver := &stubSolver{progress: Progress{Step: 3, Total: 3, Label: "写结论", Done: true}}

	result, err := NewSolveFinishTool().Invoke(bound(solver), solveCall(t, SolveFinishName, `{"summary": "算出了答案"}`))
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}

	if len(solver.asked) != 1 || solver.asked[0] != "finish" {
		t.Fatalf("the end was asked for %v", solver.asked)
	}
	if !result.Success || !strings.Contains(result.Content, "最终答案") {
		t.Errorf("closing the plan produced %+v", result)
	}
}

func TestAFatalFailureFromTheStateIsNotTurnedIntoAResult(t *testing.T) {
	// A plan that cannot be recorded is the turn's failure, not the call's: the
	// state says so, and a tool that swallowed it would leave the model to carry
	// on with a plan nothing is keeping.
	fatal := fmt.Errorf("%w: could not record the plan", ErrFatal)
	solver := &stubSolver{err: fatal}

	_, err := NewSolveStepTool().Invoke(bound(solver), solveCall(t, SolveStepName, ""))
	if !errors.Is(err, ErrFatal) {
		t.Errorf("Invoke() error = %v, want it to stay fatal", err)
	}
}

func TestTheSolveToolsDeclareWhatAReaderOfTheEventsWouldAsk(t *testing.T) {
	// The two arguments that are kept are the two questions the progress events
	// cannot answer: why the plan changed, and what came of it. Both land in the
	// call's own record.
	for _, test := range []struct {
		definition Definition
		want       string
	}{
		{NewSolveReplanTool().Definition(), "reason"},
		{NewSolveFinishTool().Definition(), "summary"},
	} {
		if !strings.Contains(string(test.definition.Parameters), test.want) {
			t.Errorf("%s does not declare %q: %s", test.definition.Name, test.want, test.definition.Parameters)
		}
	}
}
