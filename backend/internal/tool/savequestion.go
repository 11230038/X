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
// names them. The bank's own set is three types — the ones the answer area has
// (§4.3) and judging dispatches on (§2.5); a fourth would be a type nothing can
// judge. What these two tools *generate* is narrower: 主观题 (short_answer) is
// paused by the product as of 2026-10-20, so writes are refused for it while the
// existing rows in the bank keep being judged. The refusal below and the schema's
// enum are the two places that policy is written; they have to say the same
// thing.
const (
	questionTypes    = "choice、fill_blank"
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
		Parameters: questionParameters(false),
	}
}

// questionSchemaTemplate is the argument schema of the two tools that write a
// question: save_question, and mastery_question, which is that same call made
// from inside a mastery path (§2.5).
//
// The two differ by exactly one property — the knowledge point a mastery
// question is filed against — and that is why this is a template with two slots
// rather than two schemas. The risk being avoided is not the typing, it is the
// drift: a rule that bound one tool and not the other would let the two write
// rows the other refuses, and the bank would then depend on which tool wrote a
// question rather than on the question.
const questionSchemaTemplate = `{
			"type": "object",
			"properties": {
				"question": {
					"type": "string",
					"description": "题干，一句话说清要问什么"
				},
				"question_type": {
					"type": "string",
					"enum": ["choice", "fill_blank"],
					"description": "choice 选择题、fill_blank 填空题；现在不出主观题，要考的开放表述请改写成这两种之一"
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
					"description": "选择题写选项键，多选写成 \"A,C\"（逗号分隔）；填空题写答案文本，有几个可接受的写法就写成 \"2 年|两年\"（用 | 分隔）"
				},
				"explanation": {
					"type": "string",
					"description": "解析：为什么是这个答案。作答之后也要展示给用户，所以要把道理说清"
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
				}%s
			},
			"required": ["question", "question_type", "correct_answer", "explanation"%s],
			"additionalProperties": false
		}`

// questionParameters returns that schema, with the mastery coordinate added when
// the caller is the mastery tool.
//
// The coordinate's description says "id and not name" in as many words, because
// that is the one part of the call the model can get wrong without noticing: a
// name would be a string that looks like a coordinate, and the domain would
// refuse it as a knowledge point that is not in the path — costing a round trip
// rather than a wrong row, but costing one either way.
func questionParameters(withKnowledgePoint bool) json.RawMessage {
	property, required := "", ""
	if withKnowledgePoint {
		property = `,
				"knowledge_point_id": {
					"type": "string",
					"description": "这道题考的那条知识点的 id（mastery_state 里 kp_ 开头的那一列），不是它的名字"
				}`
		required = `, "knowledge_point_id"`
	}
	return json.RawMessage(fmt.Sprintf(questionSchemaTemplate, property, required))
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

// questionArguments is one call's arguments, as both tools that write a question
// take them.
//
// It is a superset: save_question reads every field but KnowledgePointID, and
// mastery_question requires that one as well. The two share the type and the
// validation below rather than each reading their own copy, because the question
// they produce has to be the same question — the same row, the same judging, the
// same bank (§2.5).
type questionArguments struct {
	Question        string   `json:"question"`
	QuestionType    string   `json:"question_type"`
	Options         []option `json:"options"`
	CorrectAnswer   string   `json:"correct_answer"`
	Explanation     string   `json:"explanation"`
	Difficulty      string   `json:"difficulty"`
	SourceLabel     string   `json:"source_label"`
	KnowledgePoints []string `json:"knowledge_points"`
	Categories      []string `json:"categories"`
	// KnowledgePointID is the mastery coordinate. It is empty for save_question,
	// which is the whole of the difference between the two calls.
	KnowledgePointID string `json:"knowledge_point_id"`
}

// readQuestionArguments unmarshals one call's arguments.
func readQuestionArguments(arguments json.RawMessage) (questionArguments, error) {
	var args questionArguments
	if err := json.Unmarshal(arguments, &args); err != nil {
		return questionArguments{}, fmt.Errorf("tool: save_question arguments are not a JSON object: %w", err)
	}
	return args, nil
}

// parseQuestion reads one call's arguments as a question the bank can store, or
// refuses with a sentence that says which part to change.
func parseQuestion(arguments json.RawMessage, carried []Carried) (notebook.Question, error) {
	args, err := readQuestionArguments(arguments)
	if err != nil {
		return notebook.Question{}, err
	}
	return questionFrom(args, carried)
}

// questionFrom validates the fields both question-writing tools share.
func questionFrom(args questionArguments, carried []Carried) (notebook.Question, error) {
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
	// 主观题 is a type the bank still stores and judging still dispatches on, but
	// the product has paused generating it. The check is here rather than in each
	// tool because both tools build their question through this one function, so
	// "which tool wrote it" cannot decide whether the rule applies.
	if questionType == notebook.QuestionShortAnswer {
		return notebook.Question{}, &Refusal{Note: "现在只出选择题和填空题，不出主观题（short_answer）：" +
			"把这道题改成 choice 或 fill_blank 再存一次——要考开放的表述，就拆成一道答案唯一的填空题，或者给出选项。"}
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
