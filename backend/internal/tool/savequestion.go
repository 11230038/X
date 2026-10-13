package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"backend/internal/event"
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

// option is one choice as the model wrote it.
type option struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}

// readOptions reads the choices of a question, refusing the shapes that would
// make one unreachable.
//
// A question whose options are not there, or whose option keys are not distinct
// under the fold the judge uses, is a question with an option nobody can choose:
// the judge builds its set of offered keys from folded keys, so two options that
// fold together are one option to it, and the answer can only ever name one of
// the rows.
func readOptions(given []option, questionType notebook.QuestionType) ([]notebook.Option, error) {
	if questionType != notebook.QuestionChoice {
		if len(given) > 0 {
			return nil, &Refusal{Note: fmt.Sprintf(
				"%s 题不需要 options，把 options 去掉；只有 choice 题有选项。", questionType)}
		}
		return nil, nil
	}
	if len(given) < MinOptions || len(given) > MaxOptions {
		return nil, &Refusal{Note: fmt.Sprintf(
			"选择题的 options 要 %d–%d 个，收到 %d 个。",
			MinOptions, MaxOptions, len(given))}
	}
	options := make([]notebook.Option, 0, len(given))
	seen := make(map[string]bool, len(given))
	for _, choice := range given {
		key := strings.TrimSpace(choice.Key)
		text := strings.TrimSpace(choice.Text)
		if key == "" {
			return nil, &Refusal{Note: "options 里有一项没写 key，每一项都要有 key（如 \"A\"）和 text。"}
		}
		if text == "" {
			return nil, &Refusal{Note: fmt.Sprintf(
				"选项「%s」的 text 是空的，每个选项都要写明内容。", key)}
		}
		folded := foldKey(key)
		if seen[folded] {
			return nil, &Refusal{Note: fmt.Sprintf(
				"options 里有两项的 key 是同一个（都是「%s」），选项键必须互不相同。", key)}
		}
		seen[folded] = true
		options = append(options, notebook.Option{Key: key, Text: text})
	}
	return options, nil
}

// readAnswer reads the correct answer in the form the question's type stores it,
// which is the form the judge reads it back in.
func readAnswer(answer string, questionType notebook.QuestionType, options []notebook.Option) (string, error) {
	switch questionType {
	case notebook.QuestionChoice:
		return readChoiceAnswer(answer, options)
	case notebook.QuestionFillBlank:
		alternatives := cleanParts(answer, "|")
		if len(alternatives) == 0 {
			return "", &Refusal{Note: "correct_answer 是空的。" +
				"填空题写答案文本，有几个可接受的写法就写成 \"2 年|两年\"（用 | 分隔）。"}
		}
		// Stored with the alternatives joined back up, one spelling each and no
		// empties: the judge reads this column by splitting it on "|", so what is
		// written here is the whole of what it will accept.
		return strings.Join(alternatives, "|"), nil
	default:
		trimmed := strings.TrimSpace(answer)
		if trimmed == "" {
			return "", &Refusal{Note: "correct_answer 是空的，主观题要写参考答案。"}
		}
		return trimmed, nil
	}
}

// readChoiceAnswer reads a choice question's answer as the option keys it names,
// spelled the way the options spell them and in ascending order.
//
// The order is the storage convention (notebook.Question.CorrectAnswer), and
// sorting here is what keeps the same answer written two ways one stored value:
// without it, "A,C" and "C,A" would be two answers to one question, and a
// question saved again would flicker between them. Stray separators are read the
// way the judge reads them — an empty piece between two commas says nothing, so
// "A,,C" is the answer "A,C" and is not worth a round trip.
func readChoiceAnswer(answer string, options []notebook.Option) (string, error) {
	offered := make(map[string]string, len(options))
	spelling := make([]string, 0, len(options))
	for _, choice := range options {
		offered[foldKey(choice.Key)] = choice.Key
		spelling = append(spelling, choice.Key)
	}
	parts := cleanParts(answer, ",")
	keys := make([]string, 0, len(parts))
	for _, part := range parts {
		key, ok := offered[foldKey(part)]
		if !ok {
			return "", &Refusal{Note: fmt.Sprintf(
				"correct_answer 里的「%s」不在 options 里，options 提供的是 %s。",
				strings.TrimSpace(part), strings.Join(spelling, "、"))}
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return "", &Refusal{Note: "correct_answer 是空的。" +
			"选择题写选项键，多选写成 \"A,C\"（逗号分隔）。"}
	}
	sort.Strings(keys)
	return strings.Join(keys, ","), nil
}

// cleanParts splits an answer the way the judge's reader does: on the separator,
// with the whitespace around each piece taken off and the pieces that say
// nothing dropped.
func cleanParts(answer, separator string) []string {
	parts := make([]string, 0, 2)
	for _, chunk := range strings.Split(answer, separator) {
		if trimmed := strings.TrimSpace(chunk); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return parts
}

// materialFor decides which material a question came from, and refuses a label
// this turn does not carry.
//
// The scope is the turn's and not the argument's: a label is looked up in the
// list the turn gave and nowhere else, so a question can only be filed under
// material the user put in front of this conversation (KB-07). A question that
// named no label but has exactly one attachment to have come from is recorded
// against it — the turn knows which material this was about even when the model
// did not say, and "the one file I gave you" is not a guess. Several attachments
// and no label is a question with no material: an arbitrary pick would be a
// citation of material the model never claimed.
func materialFor(carried []Carried, label string) (string, string, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		if len(carried) == 1 {
			return sourceAttachment, carried[0].MaterialID, nil
		}
		return "", "", nil
	}
	for _, attachment := range carried {
		if attachment.Label == label {
			return sourceAttachment, attachment.MaterialID, nil
		}
	}
	return "", "", &Refusal{Note: fmt.Sprintf(
		"本轮没有编号为「%s」的附件，携带的附件是 %s", label, labelsOf(carried))}
}

// labelsOf lists the numbers this turn's attachments carry, in the sentence the
// source tools refuse with, so that one mistake reads the same way wherever the
// model makes it.
func labelsOf(carried []Carried) string {
	if len(carried) == 0 {
		return "本轮没有附件"
	}
	labels := make([]string, 0, len(carried))
	for _, attachment := range carried {
		labels = append(labels, attachment.Label)
	}
	return strings.Join(labels, "、")
}

// foldKey folds an option key the way the identity of a key is decided.
//
// It is the judging path's fold (practice/objective.go's foldKey) written again,
// deliberately and not by accident: the two have to agree about which keys are
// the same key, and they cannot share the code because the judge is a domain
// package and this is the tool plane. It is narrower than the judge's in exactly
// one place — a full-width character is not folded to its half-width form — and
// the difference is only ever a refusal here: a model that wrote "Ａ" is told the
// key is not offered, and writes "A". On the judge's side the same character
// arrives from a user who cannot be told anything, which is why that side folds
// it and this one does not.
func foldKey(key string) string {
	var folded strings.Builder
	folded.Grow(len(key))
	for _, character := range strings.TrimSpace(key) {
		if unicode.IsSpace(character) {
			continue
		}
		folded.WriteRune(unicode.ToUpper(character))
	}
	return folded.String()
}

// knownDifficulty reports whether the difficulty is one of the three.
func knownDifficulty(difficulty string) bool {
	switch difficulty {
	case "easy", "medium", "hard":
		return true
	default:
		return false
	}
}

// tags cleans the free-text knowledge points: trimmed, with the blanks and the
// repeats dropped.
//
// The repeats are dropped because a tag is a label and not a count: the two
// places they are read — the filter that selects on them and the list shown
// beside a question — would both show "一元一次方程" twice, and neither could
// tell that from a user who really did mean it twice. Categories are left alone
// here because the bank cleans them itself, by name and by key.
func tags(names []string) []string {
	cleaned := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		cleaned = append(cleaned, trimmed)
	}
	return cleaned
}

// savedText is what the model reads back after a question lands.
//
// It says the two things the model cannot see: whether the question was new —
// the same question written twice is one question (§2.2), so "我再补一道" that
// repeats one already there has not added anything — and where the set stands,
// so that the model knows how many are left without having to count its own
// calls.
func savedText(saved Saved) string {
	what := "已存进题库"
	if !saved.Created {
		what = "题库里已经有这道题了，这次更新了它的题干"
	}
	if saved.Planned > 0 {
		return fmt.Sprintf("%s。这是第 %d/%d 道。", what, saved.Filed, saved.Planned)
	}
	return fmt.Sprintf("%s。这是第 %d 道。", what, saved.Filed)
}

// savedMetadata is what the tool result carries beside its text.
//
// Three of these keys are the progress keys (event/progress.go) and not a
// fourth set of names invented here: a client reads a question arriving and a
// plan advancing through one shape, which is what keeps "第 3/10 题" from being
// a thing the interface had to be taught separately. The label is the question's
// own text, since that is what a progress line names the step after.
func savedMetadata(saved Saved, question notebook.Question) map[string]any {
	return map[string]any{
		"notebook_entries_id": saved.EntryID,
		"question_id":         saved.QuestionID,
		"created":             saved.Created,
		event.MetaStep:        saved.Filed,
		event.MetaTotal:       saved.Planned,
		event.MetaLabel:       question.Text,
	}
}
