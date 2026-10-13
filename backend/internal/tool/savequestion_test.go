package tool

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"backend/internal/event"
	"backend/internal/notebook"
)

// stubQuestionBank is a question bank as the tool sees it: it answers with the
// attachments a test put there, and it keeps the questions it was asked to store.
//
// It addresses questions by their content, the way the real bank does, rather
// than by counting calls — a stub that reported "created" from a flag would let a
// test assert the flag and never the rule, and the rule is that the same question
// written twice is one row.
type stubQuestionBank struct {
	// carried are the attachments this turn holds.
	carried []Carried
	// rows maps a content address to the row key it was first stored under.
	rows map[string]string
	// saved is every question this bank was asked to store, in order.
	saved []notebook.Question
	// planned is what the turn's plan said, reported back on every save.
	planned int
	// err is what every save fails with, for the failures a turn raises itself.
	err error
}

var _ QuestionBank = (*stubQuestionBank)(nil)

func (b *stubQuestionBank) Carried(context.Context) []Carried { return b.carried }

func (b *stubQuestionBank) Save(_ context.Context, question notebook.Question) (Saved, error) {
	if b.err != nil {
		return Saved{}, b.err
	}
	b.saved = append(b.saved, question)
	questionID := notebook.QuestionID(question.Text, question.Type)
	entryID, existed := b.rows[questionID]
	if !existed {
		entryID = "ent_" + questionID[:8]
		if b.rows == nil {
			b.rows = map[string]string{}
		}
		b.rows[questionID] = entryID
	}
	return Saved{
		EntryID:    entryID,
		QuestionID: questionID,
		Created:    !existed,
		Filed:      len(b.rows),
		Planned:    b.planned,
	}, nil
}

// save writes one call's arguments and returns what the model is given back.
func save(t *testing.T, bank *stubQuestionBank, arguments string) Result {
	t.Helper()
	result, err := NewSaveQuestionTool(bank).Invoke(context.Background(), call(t, arguments))
	if err != nil {
		t.Fatalf("Invoke(%s) error = %v, want a result the model can read", arguments, err)
	}
	return result
}

// only returns the one question the bank was asked to store.
func only(t *testing.T, bank *stubQuestionBank) notebook.Question {
	t.Helper()
	if len(bank.saved) != 1 {
		t.Fatalf("the bank was asked to store %d questions, want one", len(bank.saved))
	}
	return bank.saved[0]
}

const fullCall = `{
	"question": "  一个三角形的内角和是多少度？  ",
	"question_type": "choice",
	"options": [
		{"key": "A", "text": "90 度"},
		{"key": "B", "text": "180 度"},
		{"key": "C", "text": "360 度"}
	],
	"correct_answer": "b",
	"explanation": "  平面上三角形的三个内角加起来是一个平角。  ",
	"difficulty": "easy",
	"source_label": "2",
	"knowledge_points": ["三角形", " 三角形 ", "", "内角和"],
	"categories": ["第一章"]
}`

func TestSaveQuestionStoresTheQuestionTheModelDescribed(t *testing.T) {
	bank := &stubQuestionBank{carried: []Carried{
		{Label: "1", MaterialID: "file_1"},
		{Label: "2", MaterialID: "file_2"},
	}}
	if result := save(t, bank, fullCall); !result.Success {
		t.Fatalf("a well-formed question was refused: %q", result.Content)
	}

	want := notebook.Question{
		Text:          "一个三角形的内角和是多少度？",
		Type:          notebook.QuestionChoice,
		Options:       []notebook.Option{{Key: "A", Text: "90 度"}, {Key: "B", Text: "180 度"}, {Key: "C", Text: "360 度"}},
		CorrectAnswer: "B",
		Explanation:   "平面上三角形的三个内角加起来是一个平角。",
		Difficulty:    "easy",
		Source:        "attachment",
		MaterialID:    "file_2",
		// The repeats and the blank are dropped: a tag is a label, not a count,
		// and both places it is read would show it twice.
		KnowledgePoints: []string{"三角形", "内角和"},
		Categories:      []string{"第一章"},
	}
	if got := only(t, bank); !reflect.DeepEqual(got, want) {
		t.Errorf("the bank was given\n  %+v\nwant\n  %+v", got, want)
	}
	// The two facts only the turn knows are left to the turn: there is no
	// argument for them, and a model that supplied one would be naming a
	// conversation it is not in.
	if got := only(t, bank); got.SessionID != "" || got.TurnID != "" {
		t.Errorf("the tool named a session or a turn: %+v", got)
	}
}

func TestTheSameQuestionSavedTwiceIsOneRow(t *testing.T) {
	bank := &stubQuestionBank{}
	first := save(t, bank, `{"question": "勾股定理说的是什么？", "question_type": "short_answer", "correct_answer": "直角三角形两直角边的平方和等于斜边的平方。", "explanation": "直角边上的两个正方形面积之和等于斜边上的正方形。"}`)
	// The same question again, differently spaced and with a different
	// explanation: it is the same question, and the second call rewrites it.
	second := save(t, bank, `{"question": "  勾股定理说的是什么？", "question_type": "short_answer", "correct_answer": "直角三角形两直角边的平方和等于斜边的平方。", "explanation": "改过的解析。"}`)

	if !asBool(t, first.Metadata["created"]) {
		t.Errorf("the first save reported created = %v, want true", first.Metadata["created"])
	}
	if asBool(t, second.Metadata["created"]) {
		t.Errorf("the second save reported created = true, want it to have found the row")
	}
	if first.Metadata["notebook_entries_id"] != second.Metadata["notebook_entries_id"] {
		t.Errorf("the two saves landed on %v and %v, want one row",
			first.Metadata["notebook_entries_id"], second.Metadata["notebook_entries_id"])
	}
	if len(bank.rows) != 1 {
		t.Errorf("the bank holds %d rows, want one", len(bank.rows))
	}
	// The second call was still written down: rewriting the row is the point,
	// not skipping it.
	if len(bank.saved) != 2 {
		t.Errorf("the bank was asked to store %d questions, want both", len(bank.saved))
	}
	if !strings.Contains(second.Content, "已经有这道题") {
		t.Errorf("content = %q, want it to say the question was already there", second.Content)
	}
}

func TestSaveQuestionReportsWhereTheSetStands(t *testing.T) {
	bank := &stubQuestionBank{planned: 10}
	saved := save(t, bank, `{"question": "什么是单调函数？", "question_type": "short_answer", "correct_answer": "定义域上随自变量增大而函数值不减的函数。", "explanation": "按定义比较任意两点的函数值。"}`)
	if !strings.Contains(saved.Content, "1/10") {
		t.Errorf("content = %q, want the position in the set the turn planned", saved.Content)
	}

	// A turn whose plan said nothing has no denominator, and a set of one out of
	// zero would read as a set of none.
	planless := save(t, &stubQuestionBank{}, `{"question": "什么是单调函数？", "question_type": "short_answer", "correct_answer": "不减。", "explanation": "按定义。"}`)
	if strings.Contains(planless.Content, "/") {
		t.Errorf("content = %q, want no total when the plan named none", planless.Content)
	}
}

func TestSaveQuestionMetadataSpeaksTheProgressKeys(t *testing.T) {
	bank := &stubQuestionBank{planned: 4}
	result := save(t, bank, `{"question": "什么是单调函数？", "question_type": "short_answer", "correct_answer": "不减。", "explanation": "按定义。"}`)

	// A question arriving and a plan advancing are one shape to a client, so
	// these three keys are the progress keys and not a fourth set of names
	// invented here (§5, event/progress.go).
	question := only(t, bank)
	for key, want := range map[string]any{
		event.MetaStep:  1,
		event.MetaTotal: 4,
		event.MetaLabel: question.Text,
	} {
		if got := result.Metadata[key]; got != want {
			t.Errorf("metadata[%q] = %v, want %v", key, got, want)
		}
	}
	if result.Metadata["question_id"] != notebook.QuestionID(question.Text, question.Type) {
		t.Errorf("metadata[question_id] = %v, want the content address the bank stored under",
			result.Metadata["question_id"])
	}
	if _, ok := result.Metadata["notebook_entries_id"]; !ok {
		t.Error("metadata carries no notebook_entries_id, so nothing says which row was written")
	}
}

func TestSaveQuestionStoresTheAnswerTheJudgeWillReadBack(t *testing.T) {
	// These two spellings are the judging path's own conventions (the judge reads
	// the stored answer and nothing else), and the tool is the only place the
	// model is ever told about them. What is written is therefore what the judge
	// will read: sorted, deduplicated, without a stray separator.
	for _, test := range []struct {
		name      string
		arguments string
		want      string
	}{
		{
			name: "a multiple answer is sorted",
			arguments: `{"question": "下列哪些是质数？", "question_type": "choice", "correct_answer": "C,A",
				"options": [{"key": "A", "text": "2"}, {"key": "B", "text": "4"}, {"key": "C", "text": "5"}],
				"explanation": "2 和 5 只有 1 和自身两个因数。"}`,
			want: "A,C",
		},
		{
			name: "a repeated answer is one answer",
			arguments: `{"question": "下列哪些是质数？", "question_type": "choice", "correct_answer": "A, A",
				"options": [{"key": "A", "text": "2"}, {"key": "B", "text": "4"}],
				"explanation": "2 只有 1 和自身两个因数。"}`,
			want: "A",
		},
		{
			name: "an answer key is spelled the way the option spells it",
			arguments: `{"question": "下列哪些是质数？", "question_type": "choice", "correct_answer": " a ",
				"options": [{"key": "A", "text": "2"}, {"key": "B", "text": "4"}],
				"explanation": "2 只有 1 和自身两个因数。"}`,
			want: "A",
		},
		{
			name: "the spellings a blank accepts are kept apart",
			arguments: `{"question": "这个合同的有效期是多久？", "question_type": "fill_blank", "correct_answer": "2 年 | 两年 |",
				"explanation": "合同写明有效期为两年。"}`,
			want: "2 年|两年",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			bank := &stubQuestionBank{}
			if result := save(t, bank, test.arguments); !result.Success {
				t.Fatalf("a well-formed question was refused: %q", result.Content)
			}
			if got := only(t, bank).CorrectAnswer; got != test.want {
				t.Errorf("stored answer = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSaveQuestionRefusesWhatTheJudgeWouldRefuse(t *testing.T) {
	// Every branch here is a question the judge reports as unusable at answer
	// time (practice/objective.go). Refusing at write time is the whole reason
	// this call exists: the model still has the question in front of it, and the
	// user never meets a row that cannot be answered.
	for _, test := range []struct {
		name      string
		arguments string
		want      string // a word the refusal has to contain to be actionable
	}{
		{
			name:      "a question that is not a question",
			arguments: `{"question": " 答? ", "question_type": "short_answer", "correct_answer": "不减。", "explanation": "按定义。"}`,
			want:      "题干太短",
		},
		{
			name:      "a type nothing can judge",
			arguments: `{"question": "什么是单调函数？", "question_type": "multiple_choice", "correct_answer": "不减。", "explanation": "按定义。"}`,
			want:      "fill_blank",
		},
		{
			name:      "a choice with nothing to choose between",
			arguments: `{"question": "下列哪些是质数？", "question_type": "choice", "correct_answer": "A", "options": [{"key": "A", "text": "2"}], "explanation": "只有 1 和自身两个因数。"}`,
			want:      "2–8",
		},
		{
			name: "a choice with more options than a list can hold",
			arguments: `{"question": "下列哪些是质数？", "question_type": "choice", "correct_answer": "A", "explanation": "只有 1 和自身两个因数。",
				"options": [{"key":"A","text":"2"},{"key":"B","text":"3"},{"key":"C","text":"4"},{"key":"D","text":"5"},
					{"key":"E","text":"6"},{"key":"F","text":"7"},{"key":"G","text":"8"},{"key":"H","text":"9"},{"key":"I","text":"10"}]}`,
			want: "2–8",
		},
		{
			name:      "a choice with no options at all",
			arguments: `{"question": "下列哪些是质数？", "question_type": "choice", "correct_answer": "A", "explanation": "只有 1 和自身两个因数。"}`,
			want:      "2–8",
		},
		{
			name: "options on a question that has none",
			arguments: `{"question": "这个合同的有效期是多久？", "question_type": "fill_blank", "correct_answer": "两年",
				"options": [{"key": "A", "text": "两年"}, {"key": "B", "text": "三年"}],
				"explanation": "合同写明有效期为两年。"}`,
			want: "options 去掉",
		},
		{
			name:      "an option with no key",
			arguments: `{"question": "下列哪些是质数？", "question_type": "choice", "correct_answer": "A", "options": [{"key": "  ", "text": "2"}, {"key": "B", "text": "4"}], "explanation": "只有 1 和自身两个因数。"}`,
			want:      "没写 key",
		},
		{
			name:      "two options that are one option",
			arguments: `{"question": "下列哪些是质数？", "question_type": "choice", "correct_answer": "A", "options": [{"key": "A", "text": "2"}, {"key": " a ", "text": "4"}], "explanation": "只有 1 和自身两个因数。"}`,
			want:      "互不相同",
		},
		{
			name:      "an option with nothing written on it",
			arguments: `{"question": "下列哪些是质数？", "question_type": "choice", "correct_answer": "A", "options": [{"key": "A", "text": "2"}, {"key": "B", "text": " "}], "explanation": "只有 1 和自身两个因数。"}`,
			want:      "text 是空的",
		},
		{
			name:      "an answer naming something the question does not offer",
			arguments: `{"question": "下列哪些是质数？", "question_type": "choice", "correct_answer": "D", "options": [{"key": "A", "text": "2"}, {"key": "B", "text": "4"}], "explanation": "只有 1 和自身两个因数。"}`,
			want:      "A、B",
		},
		{
			name:      "a choice with no answer",
			arguments: `{"question": "下列哪些是质数？", "question_type": "choice", "correct_answer": " , ", "options": [{"key": "A", "text": "2"}, {"key": "B", "text": "4"}], "explanation": "只有 1 和自身两个因数。"}`,
			want:      "correct_answer",
		},
		{
			name:      "a blank whose only spelling says nothing",
			arguments: `{"question": "这个合同的有效期是多久？", "question_type": "fill_blank", "correct_answer": " | ", "explanation": "合同写明有效期为两年。"}`,
			want:      "correct_answer",
		},
		{
			name:      "an answer with no explanation",
			arguments: `{"question": "什么是单调函数？", "question_type": "short_answer", "correct_answer": "不减。", "explanation": " "}`,
			want:      "explanation",
		},
		{
			name:      "a difficulty outside the three",
			arguments: `{"question": "什么是单调函数？", "question_type": "short_answer", "correct_answer": "不减。", "explanation": "按定义。", "difficulty": "very hard"}`,
			want:      "medium",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			bank := &stubQuestionBank{}
			result := save(t, bank, test.arguments)
			if result.Success {
				t.Fatalf("the question was stored: %+v", result)
			}
			if !strings.Contains(result.Content, test.want) {
				t.Errorf("the refusal = %q, want it to mention %q", result.Content, test.want)
			}
			// Nothing reached the bank: a refusal that had already written
			// something would leave the row it refused behind.
			if len(bank.saved) != 0 {
				t.Errorf("a refused question was stored anyway: %+v", bank.saved)
			}
		})
	}
}

// asBool reads a metadata value the tool puts there as a bool.
func asBool(t *testing.T, value any) bool {
	t.Helper()
	flag, ok := value.(bool)
	if !ok {
		t.Fatalf("metadata value %v is a %T, want a bool", value, value)
	}
	return flag
}
