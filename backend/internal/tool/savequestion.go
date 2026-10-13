package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"backend/internal/notebook"
)

// SaveQuestionName is the name save_question mounts and is called under.
const SaveQuestionName = "save_question"

// The bounds the question contract is made of.
//
// They are written here and not read from configuration (§5, §0.2 第 8 条): how
// many options a choice question may offer is the shape of the interface, not a
// knob a deployment turns. A bank written under two different rules would be two
// banks — a question that was valid yesterday refused today by a service that
// had not changed.
const (
	// MinOptions and MaxOptions bound a choice question's list. Two is the least
	// there can be a choice between; eight is as many as a card can show before
	// the list stops being a list of answers and becomes the question.
	MinOptions = 2
	MaxOptions = 8
	// MinQuestionRunes is the shortest text that is a question rather than a
	// fragment or a stray character. It is deliberately low: the job is to catch
	// a blank or a "答", not to have an opinion about how a question is phrased.
	MinQuestionRunes = 4
	// MinExplanationRunes is the shortest explanation worth storing. The
	// explanation carries both jobs this version gives it: it is what a short
	// answer is judged against (§2.5) and it is what the card shows after the
	// verdict, so an explanation that says nothing is a question that cannot be
	// judged and cannot be learnt from.
	MinExplanationRunes = 2
)

// The two closed sets a question is written from, listed the way a refusal
// names them. The question types are the three the answer area has (§4.3) and
// the three judging dispatches on (§2.5) — a fourth type would be a type nothing
// can judge.
const (
	questionTypes    = "choice、fill_blank、short_answer"
	difficulties     = "easy、medium、hard"
	sourceAttachment = "attachment"
)

// QuestionBank is the turn runtime's side of save_question: what this turn may
// file a question under, and where the question goes.
//
// It is an interface because the tool must not know how a turn stores anything —
// the runtime owns that, and a test can stand in for it. Neither method takes a
// scope: which materials exist is the turn's to say and never the model's to
// choose, which is the same boundary read_source and retrieve sit behind (KB-07).
type QuestionBank interface {
	// Carried lists the attachments this turn holds, by the label the model
	// names them with — the numbers read_source takes.
	Carried(ctx context.Context) []Carried
	// Save stores one question and reports what that did.
	//
	// The store fills in the two facts only the turn knows — the conversation
	// and the turn this question was first asked in — because there is no
	// argument for them a model could supply and no reason it should.
	Save(ctx context.Context, question notebook.Question) (Saved, error)
}

// Carried is one attachment this turn holds.
type Carried struct {
	// Label is the attachment's position in the turn's own list, counted from
	// one.
	Label string
	// MaterialID is what a question written from this attachment records as its
	// material: the stored file's id.
	//
	// The file rather than the parsed document, because a material is what the
	// user attached — it exists whether or not the parse succeeded, and an
	// attachment that could not be read is exactly the one whose questions would
	// otherwise lose the one fact we do know about them. The two have to be
	// chosen between rather than mixed: material_id is one column, and which id
	// space it is in is what `source` says.
	MaterialID string
}

// Saved is what storing one question did, and where the set it belongs to stands.
type Saved struct {
	// EntryID is the row's own key. It is reported rather than kept inside
	// because it is what the tool result says a question landed on, which is how
	// a conversation can be read back against the bank.
	EntryID string
	// QuestionID is the content address, which is what the outside world calls
	// this question.
	QuestionID string
	// Created reports whether this call wrote a row or found the one already
	// there. A question written twice is one question (§2.2), and the model is
	// told which happened rather than left to reason about it.
	Created bool
	// Filed is how many questions this turn has filed, counting this one.
	Filed int
	// Planned is how many the turn set out to produce, zero when its plan said
	// nothing — a set with no denominator rather than a set of none. It comes
	// from WithQuestionSet, because the number is the plan the turn laid out
	// before the model was called and nothing about the calls themselves knows
	// it.
	Planned int
}

// questionSetKey is the context key WithQuestionSet puts the planned count
// under.
type questionSetKey struct{}

// WithQuestionSet says how many questions this turn set out to write, so that a
// question written into the bank can report where it stands in the set.
//
// It is the same seam WithSolver is, and for the same reason: the tool is built
// once for the process while the plan belongs to one turn, so the number is
// found in the context the call arrives on. The tool does not read it — the
// bank's implementation does, when it fills in what storing the question did —
// because the tool has no idea what a plan is and does not need one: all it
// sends back is whatever the bank reports.
//
// A count of zero or less is stored as "no set": a plan of none is a plan whose
// denominator is unknown, and a progress line that said "第 3/0 道" would be
// arithmetic the user has to interpret.
func WithQuestionSet(ctx context.Context, total int) context.Context {
	return context.WithValue(ctx, questionSetKey{}, total)
}

// QuestionSetTotal reports how many questions this turn set out to write, and
// whether the turn said at all.
func QuestionSetTotal(ctx context.Context) (int, bool) {
	total, ok := ctx.Value(questionSetKey{}).(int)
	if !ok || total <= 0 {
		return 0, false
	}
	return total, true
}

// SaveQuestionTool files one question into the question bank.
//
// This is how a question leaves a turn. A model asked to write JSON into its
// reply and a service that picked it out with a regular expression would lose
// the loop: a question that failed a check would fail silently, or half of it
// would be stored. As a call, every question gets an answer — and a question the
// model got wrong comes back to it while it still has the question in front of
// it, which is the whole reason the loop exists.
type SaveQuestionTool struct {
	questions QuestionBank
}

var _ Tool = (*SaveQuestionTool)(nil)

// NewSaveQuestionTool returns the tool bound to a question bank.
func NewSaveQuestionTool(questions QuestionBank) *SaveQuestionTool {
	return &SaveQuestionTool{questions: questions}
}

// Definition describes the question to write.
//
// The descriptions of `correct_answer` and `options` are the only place the
// writer is told the storage conventions the judging path reads — a choice
// answer as comma-separated option keys, a fill-in answer with "|" between the
// spellings it accepts. They are the judge's conventions (practice/objective.go
// reads them and nothing else), so a model that is not told them writes a
// question that can be answered in exactly one way, or one whose answer the
// judge cannot find at all.
func (t *SaveQuestionTool) Definition() Definition {
	return Definition{
		Name: SaveQuestionName,
		Description: "把一道题存进题库。每道题单独调用一次，题目会立刻出现在用户面前，" +
			"所以不要在正文里再重复一遍题目内容。题目写得不合格会退回给你，改好再存一次。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"question": {
					"type": "string",
					"description": "题干，一句话说清要问什么"
				},
				"question_type": {
					"type": "string",
					"enum": ["choice", "fill_blank", "short_answer"],
					"description": "choice 选择题、fill_blank 填空题、short_answer 主观题"
				},
				"options": {
					"type": "array",
					"description": "选择题的选项，2–8 个；其他题型不要给这个字段",
					"items": {
						"type": "object",
						"properties": {
							"key": {"type": "string", "description": "选项键，如 \"A\""},
							"text": {"type": "string", "description": "选项内容"}
						},
						"required": ["key", "text"],
						"additionalProperties": false
					}
				},
				"correct_answer": {
					"type": "string",
					"description": "选择题写选项键，多选写成 \"A,C\"（逗号分隔）；填空题写答案文本，有几个可接受的写法就写成 \"2 年|两年\"（用 | 分隔）；主观题写参考答案"
				},
				"explanation": {
					"type": "string",
					"description": "解析：为什么是这个答案。主观题就是照它判分的，作答之后也要展示给用户，所以要把道理说清"
				},
				"difficulty": {
					"type": "string",
					"enum": ["easy", "medium", "hard"],
					"description": "难度，不确定就不给"
				},
				"source_label": {
					"type": "string",
					"description": "这道题出自本轮哪一份附件，写该附件在材料清单里的编号（与 read_source 同一套编号）；题目不是从某一份附件来的就不要给"
				},
				"knowledge_points": {
					"type": "array",
					"items": {"type": "string"},
					"description": "这道题考的知识点，几个短标签"
				},
				"categories": {
					"type": "array",
					"items": {"type": "string"},
					"description": "分类名，如 \"第三章\"，按名字归档"
				}
			},
			"required": ["question", "question_type", "correct_answer", "explanation"],
			"additionalProperties": false
		}`),
	}
}

// Invoke writes the question down.
//
// Every refusal here is a sentence the model can act on rather than a bare
// failure, and the set of them is not a matter of taste: the tool refuses, at
// write time, exactly what the judge would refuse the question for at answer
// time. A choice question naming an option it does not offer, a question with no
// options, a question whose answer reads as nothing — each of those is a
// question the judge reports as unusable (practice/objective.go), and a model
// that is told now can fix it; a question stored without being told becomes a
// row the user meets as an unanswerable question.
func (t *SaveQuestionTool) Invoke(ctx context.Context, call Call) (Result, error) {
	question, err := parseQuestion(call.Arguments, t.questions.Carried(ctx))
	if err != nil {
		return refused(err)
	}
	saved, err := t.questions.Save(ctx, question)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Content:  savedText(saved),
		Success:  true,
		Metadata: savedMetadata(saved, question),
	}, nil
}

// parseQuestion reads one call's arguments as a question the bank can store, or
// refuses with a sentence that says which part to change.
func parseQuestion(arguments json.RawMessage, carried []Carried) (notebook.Question, error) {
	var args struct {
		Question        string   `json:"question"`
		QuestionType    string   `json:"question_type"`
		Options         []option `json:"options"`
		CorrectAnswer   string   `json:"correct_answer"`
		Explanation     string   `json:"explanation"`
		Difficulty      string   `json:"difficulty"`
		SourceLabel     string   `json:"source_label"`
		KnowledgePoints []string `json:"knowledge_points"`
		Categories      []string `json:"categories"`
	}
	if err := json.Unmarshal(arguments, &args); err != nil {
		return notebook.Question{}, fmt.Errorf("tool: save_question arguments are not a JSON object: %w", err)
	}

	text := strings.TrimSpace(args.Question)
	if runes := utf8.RuneCountInString(text); runes < MinQuestionRunes {
		return notebook.Question{}, &Refusal{Note: fmt.Sprintf(
			"题干太短了：至少 %d 个字，现在只有 %d 个。把要问的东西写完整再存一次。",
			MinQuestionRunes, runes)}
	}
	questionType := notebook.QuestionType(strings.TrimSpace(args.QuestionType))
	if !questionType.Valid() {
		return notebook.Question{}, &Refusal{Note: fmt.Sprintf(
			"question_type 只能是 %s 之一，收到的是「%s」。", questionTypes, args.QuestionType)}
	}
	explanation := strings.TrimSpace(args.Explanation)
	if runes := utf8.RuneCountInString(explanation); runes < MinExplanationRunes {
		return notebook.Question{}, &Refusal{Note: fmt.Sprintf(
			"explanation 是空的或太短（现在 %d 个字）：主观题照它判分，用户也要靠它看懂这道题，"+
				"把为什么是这个答案说清楚。", runes)}
	}

	options, err := readOptions(args.Options, questionType)
	if err != nil {
		return notebook.Question{}, err
	}
	answer, err := readAnswer(args.CorrectAnswer, questionType, options)
	if err != nil {
		return notebook.Question{}, err
	}
	difficulty := strings.TrimSpace(args.Difficulty)
	if difficulty != "" && !knownDifficulty(difficulty) {
		return notebook.Question{}, &Refusal{Note: fmt.Sprintf(
			"difficulty 只能是 %s 之一，收到的是「%s」；不确定就不给这个字段。",
			difficulties, args.Difficulty)}
	}
	source, materialID, err := materialFor(carried, args.SourceLabel)
	if err != nil {
		return notebook.Question{}, err
	}

	return notebook.Question{
		Text:            text,
		Type:            questionType,
		Options:         options,
		CorrectAnswer:   answer,
		Explanation:     explanation,
		Difficulty:      difficulty,
		Source:          source,
		MaterialID:      materialID,
		KnowledgePoints: tags(args.KnowledgePoints),
		Categories:      args.Categories,
	}, nil
}
