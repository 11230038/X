package tool

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"backend/internal/mastery"
	"backend/internal/notebook"
)

// The nine tools a mastery path is worked with.
//
// They are named together because they are one contract, the way the solve tools
// are: a turn on a path reads it, asks about it, is judged against it, observes
// it, builds it, edits it, switches away from it and leaves it. Every one of
// those is a call the model makes rather than a paragraph it writes, and the
// reason is the same one save_question was made a tool for — a call gets an
// answer, and a model that got something wrong hears about it while it still has
// the thing in front of it.
//
// There are nine of them. §2.5's heading says eight and its table lists nine,
// and the design's own prose settles it — "原文九个词……我们按九个工具实现" — so nine
// is what is here. Leaving out mastery_leave to make the heading true would
// leave the one action by which a turn says it is finished with a path to the
// runtime, and the runtime cannot know that: releasing the lease is a decision
// a turn makes, not something that happens to it.
const (
	// MasteryStateName reads the path's state and levels.
	MasteryStateName = "mastery_state"
	// MasteryQuestionName files a question against one knowledge point.
	MasteryQuestionName = "mastery_question"
	// MasteryGradeName judges an answer to a question.
	MasteryGradeName = "mastery_grade"
	// MasterySkipName records that the learner moved past a knowledge point.
	MasterySkipName = "mastery_skip"
	// MasteryAssessName records one observation about a knowledge point.
	MasteryAssessName = "mastery_assess"
	// MasteryMapName adds modules and knowledge points to the tree.
	MasteryMapName = "mastery_map"
	// MasteryEditName rewords or moves one knowledge point.
	MasteryEditName = "mastery_edit"
	// MasterySwitchName moves this turn to another path.
	MasterySwitchName = "mastery_switch"
	// MasteryLeaveName releases this turn's claim on the path.
	MasteryLeaveName = "mastery_leave"
)

// ErrNoMasteryPath reports a mastery tool called for a turn that has no path.
//
// It is a defect rather than a model's mistake, and it is the same one
// ErrNoSolver marks: the tools are mounted by a capability that has a path, so a
// call arriving without one means the plane and the capability disagree — which
// is a thing to report, not a sentence to hand the model.
var ErrNoMasteryPath = errors.New("tool: no mastery path belongs to this turn")

// MasteryPath is the turn runtime's side of the mastery tools: which path this
// turn is working on, what it may ask about it, and what it may write.
//
// Every method is about *this turn's* path and takes no path id, because a path
// id in an argument would be the model's to choose — a path from anywhere, in a
// session that never opened it. The one exception is Switch, whose whole subject
// is naming another path, and Leave, whose whole subject is the one it is on.
// This is read_source's boundary applied to a second kind of scope (KB-07).
//
// The interface is the runtime's rather than a capability's because three of the
// things every call needs — the actor, the lease, and the version the turn last
// read — belong to a turn and not to a capability: the capability is one per
// process, and a lease is one per turn.
type MasteryPath interface {
	// Carried lists the attachments this turn holds, under the labels the source
	// tools number them by. It is the same list save_question reads, because
	// mastery_question is that call in this context.
	Carried(ctx context.Context) []Carried
	// Current reads the path this turn is on: its tree, its version, and what it
	// is for.
	Current(ctx context.Context) (mastery.View, error)
	// Ask files one question against one knowledge point of this turn's path.
	Ask(ctx context.Context, question notebook.Question, knowledgePointID string) (Saved, error)
	// Grade judges one answer to one question.
	Grade(ctx context.Context, questionID, userAnswer string) (Graded, error)
	// Observe records one observation about one knowledge point.
	Observe(ctx context.Context, observation Observation) (mastery.View, error)
	// Skip records that the learner moved past one knowledge point.
	Skip(ctx context.Context, knowledgePointID, reason string) error
	// Map adds modules and knowledge points to the tree, and returns it as it now
	// stands.
	//
	// The modules arrive as the model wrote them — names, and a blank id for
	// anything new — and the implementation issues the missing ids, fills in each
	// knowledge point's ModuleID from the module it was written under, and merges
	// what is already there. The tree it returns carries those ids, which is how
	// the model learns them: it names things, and the ids are the runtime's to
	// issue.
	Map(ctx context.Context, modules []mastery.Module) (mastery.View, error)
	// Edit rewords one knowledge point, moves it, or both.
	Edit(ctx context.Context, edit KnowledgePointEdit) (mastery.View, error)
	// Switch moves this turn onto another path, and returns that path's state.
	Switch(ctx context.Context, pathID string) (mastery.View, error)
	// Leave releases this turn's claim on the path it is on.
	Leave(ctx context.Context) error
}

// Observation is one thing seen about one knowledge point.
//
// It is not mastery.Evidence: that is the stored row, and it carries the id, the
// path and the actor, none of which a model has any business supplying. What a
// model knows is what it saw, about what, and why it thinks so.
type Observation struct {
	// KnowledgePointID is the coordinate, as mastery_state reports it.
	KnowledgePointID string
	// Result is the outcome, and it is one of the three the gate is built on
	// (§2.4). A fourth value would be a value the gate has no rule for.
	Result mastery.Result
	// Detail is the model's own sentence about what happened. It is stored and it
	// is not read by the gate: the gate counts results, and a gate that read prose
	// would be a gate nothing could pin down.
	Detail string
}

// KnowledgePointEdit is what mastery_edit asks for: a knowledge point's new
// wording, its new place, or both.
//
// Edit's zero value is "name nothing", which is why the three fields are
// pointers — the request has to be able to tell "leave the name alone" from
// "call it an empty string", and only one of those is a thing anybody wants.
type KnowledgePointEdit struct {
	ID   string
	Edit mastery.Edit
	// ToModule, when set, moves the knowledge point into that module.
	ToModule *string
	// Position is where in that module, counting from zero. A negative value
	// appends, which is what a caller that did not think about it meant.
	Position int
}

// Graded is what judging one answer produced, in the shape the model reads.
//
// It is two fields and not the judge's whole verdict because the rest of the
// verdict is not the model's to use: the rubric, the confidence and the boundary
// are the surface's, and a model that was handed them would start explaining a
// judgement it did not make.
type Graded struct {
	// Correct is the verdict.
	Correct bool
	// Reason says why, in a sentence. For an objective question it names both
	// answers — the comparison is the whole of its reason — and for a judged one
	// it points at what the answer turned on.
	Reason string
}

// masteryPathKey is the context key a turn puts its path under.
type masteryPathKey struct{}

// WithMasteryPath returns a context carrying the path of the turn being run.
//
// It is the same seam WithSolver is and it exists for the same reason: the tools
// are built once for the process while the path, the lease and the actor belong
// to one turn, so the path is found in the context the call arrives on.
func WithMasteryPath(ctx context.Context, path MasteryPath) context.Context {
	return context.WithValue(ctx, masteryPathKey{}, path)
}

// masteryPathFrom returns the path of the turn this call belongs to.
func masteryPathFrom(ctx context.Context) (MasteryPath, error) {
	path, ok := ctx.Value(masteryPathKey{}).(MasteryPath)
	if !ok || path == nil {
		return nil, ErrNoMasteryPath
	}
	return path, nil
}

// levelText is how a level is read out. The words are the surface's and they are
// here rather than in the domain because they are a rendering: what the domain
// decided is `unknown`, and "未学" is what a reader of a tool result should see.
//
// 未学 and 薄弱 are deliberately two different words — an untested knowledge
// point is not a weak one (§2.4), and a model given one word for both would tell
// a learner they are bad at something they have never met.
func levelText(level mastery.Level) string {
	switch level {
	case mastery.LevelMastered:
		return "已掌握"
	case mastery.LevelLearning:
		return "学习中"
	case mastery.LevelWeak:
		return "薄弱"
	case mastery.LevelUnknown:
		return "未学"
	default:
		return string(level)
	}
}

// resultText is how an observation's outcome is read back to the model.
func resultText(result mastery.Result) string {
	switch result {
	case mastery.ResultCorrect:
		return "答对"
	case mastery.ResultIncorrect:
		return "答错"
	case mastery.ResultPartial:
		return "答对一部分"
	default:
		return string(result)
	}
}

// masteryText renders a path the way the model is shown it: what it is for, how
// far it has got, and every knowledge point with its id and its level.
//
// The ids are in it because they are the only way to name a knowledge point
// afterwards, and a model that had to guess one would file a question against a
// coordinate that does not exist — which the domain refuses, so the guess costs a
// round trip rather than a wrong row, but it costs a round trip either way.
func masteryText(view mastery.View) string {
	var text strings.Builder
	fmt.Fprintf(&text, "路径 %s（目标：%s）\n", view.Path.ID, goalText(view.Meta.Goal))

	points := 0
	for _, module := range view.Path.Tree.Modules {
		points += len(module.KnowledgePoints)
	}
	fmt.Fprintf(&text, "版本 %d · %d 个模块 · %d 条知识点\n",
		view.Path.Version, len(view.Path.Tree.Modules), points)

	if len(view.Path.Tree.Modules) == 0 {
		text.WriteString("树还是空的：用 mastery_map 把要学的知识点按模块列出来。\n")
		return strings.TrimSpace(text.String())
	}
	for _, module := range view.Path.Tree.Modules {
		fmt.Fprintf(&text, "- 模块 %s %s\n", module.ID, module.Name)
		if len(module.KnowledgePoints) == 0 {
			// A module with nothing in it yet is said out loud rather than left as
			// a heading with a blank under it, which reads as a rendering fault.
			text.WriteString("  （还没有知识点）\n")
			continue
		}
		for _, point := range module.KnowledgePoints {
			fmt.Fprintf(&text, "  - %s %s［%s］\n", point.ID, point.Name, levelText(point.Mastery))
		}
	}
	return strings.TrimSpace(text.String())
}

// pointText says how one knowledge point stands after a write, which is what a
// caller that just observed or moved it wants to know. A coordinate that is no
// longer in the tree gives back the id alone rather than an invented level.
func pointText(view mastery.View, knowledgePointID string) string {
	point, _, ok := view.Path.Tree.KnowledgePoint(knowledgePointID)
	if !ok {
		return knowledgePointID
	}
	return fmt.Sprintf("%s 现在是「%s」", point.Name, levelText(point.Mastery))
}

// goalText is what a path with no stated goal reads as. A blank in the middle of
// a sentence reads as a rendering fault, and the honest reading — nobody has said
// what this path is for — is worth the four characters.
func goalText(goal string) string {
	if strings.TrimSpace(goal) == "" {
		return "还没有写目标"
	}
	return strings.TrimSpace(goal)
}

// masteryMetadata is what every mastery tool reports beside its text, in the
// shape the surface draws: which path, which version, and how much is in it.
func masteryMetadata(view mastery.View) map[string]any {
	points := 0
	for _, module := range view.Path.Tree.Modules {
		points += len(module.KnowledgePoints)
	}
	return map[string]any{
		"path_id":          view.Path.ID,
		"version":          view.Path.Version,
		"modules":          len(view.Path.Tree.Modules),
		"knowledge_points": points,
	}
}
