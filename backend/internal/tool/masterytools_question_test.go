package tool

// Asking a question on a mastery path: what the tool checks before it hands the
// question over, and what it tells the model when it will not.
//
// The claim being pinned is the order of the two steps. A question is filed on
// the way into the bank and the coordinate is written once (§2.2), so a question
// filed against a knowledge point that is not in the path would keep that
// coordinate for good. The path below is a double in the way every double in this
// package is — it answers the questions the tool asks it — and what it is for
// here is the negative: a tool that refused before reaching it can only be shown
// to have done so by a path that would have noticed.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"backend/internal/mastery"
	"backend/internal/notebook"
)

// stubPath is one turn's path, with the tree the model has been shown and a note
// of every question it was asked to file.
type stubPath struct {
	tree    mastery.Tree
	open    *mastery.Interaction
	asked   []notebook.Question
	refusal error
}

var _ MasteryPath = (*stubPath)(nil)

func (p *stubPath) Carried(context.Context) []Carried { return nil }

func (p *stubPath) Current(context.Context) (mastery.View, error) {
	return mastery.View{Path: mastery.Path{ID: "mp_1", Tree: p.tree}, Open: p.open}, nil
}

func (p *stubPath) Ask(_ context.Context, question notebook.Question,
	_ string) (Saved, error) {
	if p.refusal != nil {
		return Saved{}, p.refusal
	}
	p.asked = append(p.asked, question)
	return Saved{EntryID: "nbe_1", QuestionID: "qn_1", Created: true, Filed: 1}, nil
}

func (p *stubPath) Grade(context.Context, string, string) (Graded, error) {
	return Graded{}, nil
}

func (p *stubPath) Observe(context.Context, Observation) (mastery.View, error) {
	return mastery.View{}, nil
}

func (p *stubPath) Skip(context.Context, string, string) error { return nil }

func (p *stubPath) Map(context.Context, []mastery.Module) (mastery.View, error) {
	return mastery.View{}, nil
}

func (p *stubPath) Edit(context.Context, KnowledgePointEdit) (mastery.View, error) {
	return mastery.View{}, nil
}

func (p *stubPath) Switch(context.Context, string) (mastery.View, error) {
	return mastery.View{}, nil
}

func (p *stubPath) Leave(context.Context) error { return nil }

// twoPoints is a path with two knowledge points, so that a call can name one that
// is there and one that is not.
func twoPoints() mastery.Tree {
	return mastery.Tree{Modules: []mastery.Module{{
		ID: "m_1", Name: "闭包",
		KnowledgePoints: []mastery.KnowledgePoint{
			{ID: "kp_001", Name: "闭包捕获的是变量", ModuleID: "m_1"},
			{ID: "kp_002", Name: "循环里的闭包", ModuleID: "m_1"},
		},
	}}}
}

func askOn(path *stubPath, knowledgePointID string) (Result, error) {
	ctx := WithMasteryPath(context.Background(), path)
	arguments, err := json.Marshal(map[string]any{
		"question":           "闭包捕获的是变量还是值？",
		"question_type":      "fill_blank",
		"correct_answer":     "变量",
		"explanation":        "循环里的函数看到的是同一个变量。",
		"knowledge_point_id": knowledgePointID,
	})
	if err != nil {
		return Result{}, err
	}
	return NewMasteryQuestionTool().Invoke(ctx, Call{Name: MasteryQuestionName, Arguments: arguments})
}

func TestMasteryQuestionFilesTheQuestionOnThePath(t *testing.T) {
	path := &stubPath{tree: twoPoints()}

	result, err := askOn(path, "kp_001")
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("the call did not succeed: %s", result.Content)
	}
	if len(path.asked) != 1 {
		t.Fatalf("the path was asked to file %d questions, want one", len(path.asked))
	}
	// The knowledge point is named in what comes back, because it is the one fact
	// the model cannot check by reading the sentence: a question filed against the
	// wrong coordinate looks exactly like one filed against the right one.
	if result.Metadata["knowledge_point_id"] != "kp_001" {
		t.Errorf("the result says the question is about %v", result.Metadata["knowledge_point_id"])
	}
	if !strings.Contains(result.Content, "kp_001") {
		t.Errorf("the model was told %q, want the knowledge point named", result.Content)
	}
}

func TestMasteryQuestionRefusesAKnowledgePointThatIsNotInThePath(t *testing.T) {
	path := &stubPath{tree: twoPoints()}

	result, err := askOn(path, "kp_404")
	if err != nil {
		t.Fatalf("Invoke() error = %v, want a refusal the model reads", err)
	}
	if result.Success {
		t.Fatal("a question about a knowledge point that is not in the path succeeded")
	}
	// The sentence has to carry the way out: the ids that are there, and the tool
	// that adds one. A refusal that only said "not found" would leave the model
	// guessing at ids it has never seen.
	for _, want := range []string{"kp_404", "kp_001", "kp_002", "mastery_map"} {
		if !strings.Contains(result.Content, want) {
			t.Errorf("the refusal %q does not mention %q", result.Content, want)
		}
	}
	// And the question was never handed over: the coordinate is written on the way
	// into the bank, so a refusal after the fact would leave a row filed on an id
	// nothing resolves.
	if len(path.asked) != 0 {
		t.Errorf("the question reached the path anyway: %+v", path.asked)
	}
}

func TestMasteryQuestionRefusesACoordinateWithoutAQuestion(t *testing.T) {
	path := &stubPath{tree: twoPoints()}

	// The coordinate is required, and the refusal says where the id comes from —
	// the model has to go and look it up, so "which id" is worth more than "which
	// field".
	result, err := askOn(path, "")
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if result.Success || !strings.Contains(result.Content, "kp_") {
		t.Errorf("a call with no knowledge point came back as %q", result.Content)
	}
	if len(path.asked) != 0 {
		t.Errorf("a call with no coordinate reached the path: %+v", path.asked)
	}
}

func TestMasteryQuestionHandsOverWhatThePathRefused(t *testing.T) {
	// A path that refuses — a lease another turn holds, a question already waiting
	// — is refused to the model, and the tool adds nothing to the sentence the
	// domain wrote.
	//
	// It has to come back as a *result*, not as an error. The loop converts every
	// error a tool returns into "The tool could not be run.", so a refusal sent
	// that way would reach the model with the one part it cannot act on and lose
	// who holds the path and what to do about it.
	path := &stubPath{tree: twoPoints(), refusal: &Refusal{Note: "这条路径现在由另一个 turn 在写。"}}

	result, err := askOn(path, "kp_001")
	if err != nil {
		t.Fatalf("Invoke() error = %v, want the refusal reported to the model", err)
	}
	if result.Success {
		t.Fatal("a refused question was reported as a success")
	}
	if !strings.Contains(result.Content, "另一个 turn") {
		t.Errorf("the refusal reads %q", result.Content)
	}
	if len(path.asked) != 0 {
		t.Errorf("the refused question reached the path anyway: %+v", path.asked)
	}
}

func TestMasteryQuestionWithoutAPathIsADefect(t *testing.T) {
	arguments, err := json.Marshal(map[string]any{"question": "闭包是什么？", "question_type": "fill_blank"})
	if err != nil {
		t.Fatalf("build the arguments: %v", err)
	}
	// No WithMasteryPath: the tool is mounted by a capability that has one, so a
	// call arriving without it means the plane and the capability disagree. That is
	// a fault to report, not a sentence to hand the model.
	if _, err := NewMasteryQuestionTool().Invoke(context.Background(),
		Call{Name: MasteryQuestionName, Arguments: arguments}); !errors.Is(err, ErrNoMasteryPath) {
		t.Errorf("Invoke() error = %v, want %v", err, ErrNoMasteryPath)
	}
}

func TestMasteryQuestionRefusesASubjectiveQuestion(t *testing.T) {
	// Same policy as save_question, and stated here as well because this is the
	// door the path uses: a proof-shaped question has to become a blank or a
	// choice, not a short answer the popup cannot offer options for.
	path := &stubPath{tree: twoPoints()}
	arguments, err := json.Marshal(map[string]any{
		"question":           "用元素法证明 (A∩B)ᶜ = Aᶜ ∪ Bᶜ。",
		"question_type":      "short_answer",
		"correct_answer":     "两个方向各证一次。",
		"explanation":        "元素法。",
		"knowledge_point_id": "kp_001",
	})
	if err != nil {
		t.Fatalf("build the arguments: %v", err)
	}
	result, err := NewMasteryQuestionTool().Invoke(WithMasteryPath(context.Background(), path),
		Call{Name: MasteryQuestionName, Arguments: arguments})
	if err != nil {
		t.Fatalf("Invoke() error = %v, want a refusal the model reads", err)
	}
	if result.Success {
		t.Fatalf("a subjective question was filed: %+v", result)
	}
	if !strings.Contains(result.Content, "主观题") {
		t.Errorf("the refusal reads %q", result.Content)
	}
	if len(path.asked) != 0 {
		t.Errorf("the refused question reached the path anyway: %+v", path.asked)
	}
}

func TestMasteryStateNamesTheQuestionThatIsWaiting(t *testing.T) {
	// While a question is open, nothing else can be asked and mastery_grade is the
	// only way on — and it takes the *question* id, which exists in exactly two
	// places: the asking tool's own result and this read. A state that showed the
	// open interaction without that id left the model nothing to call the judge
	// with; what it reached for instead was the interaction id, which is not a
	// question, so the call was a fault rather than a verdict.
	open := &mastery.Interaction{
		ID: "min_1", PathID: "mp_1", KnowledgePointID: "kp_001", QuestionID: "qn_1",
	}
	path := &stubPath{tree: twoPoints(), open: open}
	ctx := WithMasteryPath(context.Background(), path)

	result, err := NewMasteryStateTool().Invoke(ctx, Call{Name: MasteryStateName})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("the state read failed: %q", result.Content)
	}
	for _, want := range []string{"qn_1", "min_1", "kp_001", MasteryGradeName} {
		if !strings.Contains(result.Content, want) {
			t.Errorf("the state does not mention %q:\n%s", want, result.Content)
		}
	}
	if result.Metadata["open_question_id"] != "qn_1" {
		t.Errorf("metadata[open_question_id] = %v, want qn_1", result.Metadata["open_question_id"])
	}

	// Nothing waiting is the ordinary state and stays unsaid: a line about a
	// question that is not there would be read as one that is.
	plain := &stubPath{tree: twoPoints()}
	result, err = NewMasteryStateTool().Invoke(
		WithMasteryPath(context.Background(), plain), Call{Name: MasteryStateName})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if strings.Contains(result.Content, "等用户作答") {
		t.Errorf("a path with nothing waiting reads:\n%s", result.Content)
	}
	if _, present := result.Metadata["open_question_id"]; present {
		t.Errorf("metadata carries an open question on a path with none: %+v", result.Metadata)
	}
}
