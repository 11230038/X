package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// The question pair: asking one against a knowledge point, and judging the
// answer.
//
// Neither of them is new machinery, and that is the point of this file. V4 §2.5
// says a question inside a mastery path *is* a question in the bank — QN-01's
// one bank, with Mastery listed as one of its sources — so mastery_question is
// save_question's own call with a coordinate attached, and mastery_grade is the
// practice path's judging reached from a different door. Any implementation that
// gave a path questions of its own would take one whole source out of the bank's
// filters, which is why the reuse is a requirement rather than a convenience.

// MasteryQuestionTool files a question against one knowledge point.
type MasteryQuestionTool struct{}

var _ Tool = (*MasteryQuestionTool)(nil)

// NewMasteryQuestionTool returns the tool.
func NewMasteryQuestionTool() *MasteryQuestionTool { return &MasteryQuestionTool{} }

// Definition is save_question's argument schema with one property added.
//
// It is built from the same schema rather than copied (§2.5): the two tools
// write the same row, so a rule that bound one and not the other — a fourth
// question type, a ninth option — would produce questions the other refuses and
// a bank that depends on which tool wrote the row.
//
// The description tells the model the two things it cannot work out for itself:
// that the question is about *one* knowledge point, and that the coordinate is
// the id from mastery_state rather than the knowledge point's name.
func (t *MasteryQuestionTool) Definition() Definition {
	return Definition{
		Name: MasteryQuestionName,
		Description: "针对当前掌握路径里的一条知识点出一道题，存进同一个题库。" +
			"每道题单独调用一次，写得不合格会退回给你，改好再存一次。" +
			"knowledge_point_id 写 mastery_state 里那条知识点的 id（kp_ 开头），不要写它的名字。",
		Parameters: questionParameters(true),
	}
}

// Invoke parses the question and files it against the coordinate.
//
// The knowledge point is named in what comes back, because that is the one fact
// the model cannot check by reading the sentence: a question stored against the
// wrong coordinate looks exactly like one stored against the right one, and the
// next thing that happens is that the wrong evidence is counted.
//
// The coordinate is checked against the tree before the question is written, and
// that order is the point of the read below. A question is filed on the way into
// the bank (§2.2) and the coordinate is written once, so a question filed
// against an id that is not in the path would keep that id for good — the model
// would read "knowledge point kp_42 is not in path mp_1" and the row would go on
// saying it was. The domain refuses the same id a moment later (mastery.Ask
// checks it inside its transaction), and that check is what makes the rule true;
// this one is what keeps the refusal from leaving anything behind.
func (t *MasteryQuestionTool) Invoke(ctx context.Context, call Call) (Result, error) {
	path, err := masteryPathFrom(ctx)
	if err != nil {
		return Result{}, err
	}
	question, knowledgePointID, err := parseMasteryQuestion(call.Arguments, path.Carried(ctx))
	if err != nil {
		return refused(err)
	}
	view, err := path.Current(ctx)
	if err != nil {
		return Result{}, err
	}
	if _, _, ok := view.Path.Tree.KnowledgePoint(knowledgePointID); !ok {
		return refused(&Refusal{Note: fmt.Sprintf(
			"知识点 %s 不在这条路径里，这道题没有存。现在这条路径上有这些知识点：%s。"+
				"用其中一个 id 再出一次题；要加一条，先用 mastery_map。",
			knowledgePointID, strings.Join(view.Path.Tree.KnowledgePointIDs(), "、"))})
	}
	saved, err := path.Ask(ctx, question, knowledgePointID)
	if err != nil {
		return Result{}, err
	}

	content := fmt.Sprintf("%s 这道题算在知识点 %s 上。", savedText(saved), knowledgePointID)
	metadata := savedMetadata(saved, question)
	metadata["knowledge_point_id"] = knowledgePointID
	return Result{Content: content, Success: true, Metadata: metadata}, nil
}

// MasteryGradeTool judges an answer.
type MasteryGradeTool struct{}

var _ Tool = (*MasteryGradeTool)(nil)

// NewMasteryGradeTool returns the tool.
func NewMasteryGradeTool() *MasteryGradeTool { return &MasteryGradeTool{} }

// Definition asks for the question and the answer, and it does not ask whether
// the answer was right.
//
// That is the whole shape of this tool: a model that could state the verdict
// would be a model marking its own homework, and the judgement is exactly the
// thing this path refuses to take the model's word for. The question is named by
// its id so that the judge reads the row — the accepted spellings of a
// fill-in-the-blank are in that row and nowhere else (§2.5 of V3).
func (t *MasteryGradeTool) Definition() Definition {
	return Definition{
		Name: MasteryGradeName,
		Description: "判一次作答对不对，并把这次判卷记进复习曲线。" +
			"question_id 是题库存那道题的 id；user_answer 原样抄用户写的答案，不要替他改正、不要补全。" +
			"对不对由判卷决定，不要在正文里自己下结论；这道题是选择题还是填空题由题目本身决定。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"question_id": {
					"type": "string",
					"description": "题库里那道题的 id，从出题时的返回或题库里取"
				},
				"user_answer": {
					"type": "string",
					"description": "用户写的答案原文；选择题写选项键（如 \"A\" 或 \"A,C\"）"
				}
			},
			"required": ["question_id", "user_answer"],
			"additionalProperties": false
		}`),
	}
}

// Invoke judges the answer and says what the verdict was.
func (t *MasteryGradeTool) Invoke(ctx context.Context, call Call) (Result, error) {
	path, err := masteryPathFrom(ctx)
	if err != nil {
		return Result{}, err
	}
	grade, err := parseGrade(call.Arguments)
	if err != nil {
		return refused(err)
	}
	graded, err := path.Grade(ctx, grade.QuestionID, grade.UserAnswer)
	if err != nil {
		return refused(err)
	}

	verdict := "判错了"
	if graded.Correct {
		verdict = "判对了"
	}
	content := verdict
	if reason := strings.TrimSpace(graded.Reason); reason != "" {
		content = fmt.Sprintf("%s：%s", verdict, reason)
	}
	return Result{
		Content: content,
		Success: true,
		Metadata: map[string]any{
			"question_id": grade.QuestionID,
			"correct":     graded.Correct,
		},
	}, nil
}
