package practice

// The objective half of judging, pinned. Two claims are under test and they are
// the reason this branch exists at all: an answer to a choice or fill-in
// question is compared against what the question stores and never reaches a
// model, and the comparison is the fixed one — trim, folded whitespace,
// half-width, ASCII case — with no semantic leniency anywhere in it.

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"backend/internal/llm"
	"backend/internal/notebook"
)

func choiceQuestion(correct string, keys ...string) notebook.Question {
	options := make([]notebook.Option, 0, len(keys))
	for _, key := range keys {
		options = append(options, notebook.Option{Key: key, Text: "选项 " + key})
	}
	return notebook.Question{
		QuestionID:    "qn_choice",
		Text:          "下面哪一个是对的？",
		Type:          notebook.QuestionChoice,
		Options:       options,
		CorrectAnswer: correct,
		Explanation:   "因为 B 是对的。",
	}
}

func fillQuestion(correct string) notebook.Question {
	return notebook.Question{
		QuestionID:    "qn_fill",
		Text:          "闭包是什么",
		Type:          notebook.QuestionFillBlank,
		CorrectAnswer: correct,
		Explanation:   "闭包是能读取外部变量的函数。",
	}
}

func essayQuestion() notebook.Question {
	question := fillQuestion("闭包")
	question.Type = "essay"
	return question
}

// countingProvider is a provider that answers nothing and counts what it was
// asked. It is how "no model was involved" is stated as a test rather than as a
// reading of the code.
type countingProvider struct{ calls int }

func (p *countingProvider) Stream(_ context.Context, _ llm.Request, _ func(llm.Delta) error) (llm.Response, error) {
	p.calls++
	return llm.Response{}, errors.New("this test has no model to ask")
}

// judgeFor builds a judge over the given provider.
func judgeFor(t *testing.T, provider llm.Provider) *Judge {
	t.Helper()
	judge, err := NewJudge(provider, "judge-model", 512)
	if err != nil {
		t.Fatalf("NewJudge() error = %v", err)
	}
	return judge
}

// judged judges one answer, insists that no model was consulted, and returns
// the judgement.
func judged(t *testing.T, question notebook.Question, answer string) Judgment {
	t.Helper()
	provider := &countingProvider{}
	judgement, err := judgeFor(t, provider).Judge(context.Background(), question, answer)
	if err != nil {
		t.Fatalf("Judge(%q, %q) error = %v", question.Type, answer, err)
	}
	if provider.calls != 0 {
		t.Fatalf("judging a %s question asked a model %d times", question.Type, provider.calls)
	}
	return judgement
}

func TestNewJudgeNeedsItsParts(t *testing.T) {
	provider := &countingProvider{}
	cases := map[string]struct {
		provider  llm.Provider
		model     string
		maxTokens int
	}{
		"no provider":   {provider: nil, model: "judge-model", maxTokens: 512},
		"no model":      {provider: provider, model: "  ", maxTokens: 512},
		"no token room": {provider: provider, model: "judge-model", maxTokens: 0},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewJudge(test.provider, test.model, test.maxTokens); err == nil {
				t.Error("NewJudge() error = nil, want a rejection")
			}
		})
	}
}

// TestAnObjectiveQuestionNeverReachesAModel is the centre of §2.5.
//
// Asking a model whether "B" equals "B" costs a call to get an answer that was
// already known, and the answer it gets back can be wrong and can differ twice
// for the same input. The provider here fails loudly if it is used, so this is
// the claim stated as a fact about the code rather than as an intention.
func TestAnObjectiveQuestionNeverReachesAModel(t *testing.T) {
	provider := &countingProvider{}
	judge := judgeFor(t, provider)

	questions := map[string]notebook.Question{
		"a choice question":  choiceQuestion("A", "A", "B"),
		"a fill-in question": fillQuestion("闭包"),
	}
	for name, question := range questions {
		t.Run(name, func(t *testing.T) {
			judgement, err := judge.Judge(context.Background(), question, "A")
			if err != nil {
				t.Fatalf("Judge() error = %v", err)
			}
			if judgement.Kind != KindObjective {
				t.Errorf("Kind = %q, want %q", judgement.Kind, KindObjective)
			}
			if judgement.Model != nil {
				t.Errorf("Model = %q, want nil: no model judged this", *judgement.Model)
			}
			if judgement.Confidence != nil {
				t.Errorf("Confidence = %v, want nil: nobody estimated anything", *judgement.Confidence)
			}
			if judgement.Boundary != objectiveBoundary {
				t.Errorf("Boundary = %q, want %q", judgement.Boundary, objectiveBoundary)
			}
			if judgement.Reason == "" {
				t.Error("the judgement gives no reason")
			}
			// Empty rather than absent: a caller iterating the rubric should not
			// have to tell a list that is empty from a field nobody wrote.
			if judgement.Rubric == nil {
				t.Error("the rubric is nil, want an empty list")
			}
		})
	}
	if provider.calls != 0 {
		t.Errorf("judging two objective questions asked a model %d times", provider.calls)
	}
}

// TestFillBlankComparisonIsFrozen runs the four folding rules and the floor
// under them.
//
// Every "same" case here is a way of writing the stored answer with different
// characters; every "different" case is a reason to be lenient that the design
// turned down. The lenient ones matter as much as the strict ones — an answer
// that is judged correct for being close is a judgement nobody can reproduce.
func TestFillBlankComparisonIsFrozen(t *testing.T) {
	const stored = "闭包 是 ABC"
	cases := []struct {
		name   string
		stored string
		answer string
		want   bool
	}{
		{"written exactly", stored, "闭包 是 ABC", true},
		{"padded with spaces", stored, "  闭包 是 ABC  ", true},
		{"a run of spaces where one was written", stored, "闭包   是   ABC", true},
		{"a tab where a space was written", stored, "闭包\t是 ABC", true},
		{"a newline where a space was written", stored, "闭包\n是 ABC", true},
		{"a full-width space where one was written", stored, "闭包　是 ABC", true},
		{"lower case for the stored capitals", stored, "闭包 是 abc", true},
		{"full-width letters for the stored ones", stored, "闭包 是 ＡＢＣ", true},

		// The floor: folding is about characters, never about meaning.
		{"a synonym", "闭包是函数", "闭包是一个函数", false},
		{"a missing character", stored, "闭包 是 AB", false},
		{"an extra character", stored, "闭包 是 ABCD", false},

		// Folding whitespace runs cannot put a space where none was written.
		{"a space nobody wrote", "什么是闭包", "什么是 闭包", false},
		{"no space where one was written", stored, "闭包是 ABC", false},

		// ASCII only, as written down: Unicode case folding would answer
		// questions this comparison has no business answering.
		{"a non-ASCII case difference", "café", "CAFÉ", false},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			judgement := judged(t, fillQuestion(scenario.stored), scenario.answer)
			if judgement.IsCorrect != scenario.want {
				t.Errorf("judging %q against %q = %v, want %v",
					scenario.answer, scenario.stored, judgement.IsCorrect, scenario.want)
			}
		})
	}
}

// TestAFillBlankQuestionMayStoreSeveralAnswers covers the one way this
// comparison is allowed to be forgiving.
//
// The design's answer to "what about a legitimate synonym" is a question that
// lists it, which keeps the acceptance in the bank where a person can read and
// change it rather than in a model that has to guess at it.
func TestAFillBlankQuestionMayStoreSeveralAnswers(t *testing.T) {
	question := fillQuestion("闭包|闭包函数| closure ")
	cases := map[string]bool{
		"闭包":      true,
		"闭包函数":    true,
		"CLOSURE": true,
		"闭包是函数":   false,
		"":        false,
	}
	for answer, want := range cases {
		if got := judged(t, question, answer).IsCorrect; got != want {
			t.Errorf("judging %q = %v, want %v", answer, got, want)
		}
	}

	// The stored spellings are what the reason shows, so a reader sees the
	// alternatives rather than an empty answer folded to nothing.
	reason := judged(t, question, "都不对").Reason
	for _, alternative := range []string{"闭包", "闭包函数", "closure"} {
		if !strings.Contains(reason, alternative) {
			t.Errorf("the reason %q does not name the accepted answer %q", reason, alternative)
		}
	}
}

// TestChoiceAnswersAreSets runs the same claim over option keys.
//
// The storage convention is a comma-separated sorted list of distinct keys, so
// the comparison is between sets: order is not something a client chose, and a
// key written twice is one selection.
func TestChoiceAnswersAreSets(t *testing.T) {
	const stored = "A,C"
	cases := []struct {
		name   string
		answer string
		want   bool
	}{
		{"in the stored order", "A,C", true},
		{"in the other order", "C,A", true},
		{"with spaces around the keys", " C , A ", true},
		{"with a key repeated", "A,A,C", true},
		{"in lower case", "a,c", true},
		{"with full-width letters", "Ａ，Ｃ", false}, // a full-width comma is not a separator
		{"missing one of the two", "A", false},
		{"one too many", "A,B,C", false},
		{"an option the question offers but the answer does not name", "B", false},
		{"nothing at all", "", false},
		{"a key the question never offered", "D", false},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			question := choiceQuestion(stored, "A", "B", "C")
			if got := judged(t, question, scenario.answer).IsCorrect; got != scenario.want {
				t.Errorf("judging %q against %q = %v, want %v",
					scenario.answer, stored, got, scenario.want)
			}
		})
	}
}

// TestAnOptionKeyIsComparedTheWayAnAnswerIs keeps the two sides of a choice
// comparison folded the same way: the keys a question offers are as much a
// written text as the answer is, and a question whose keys were written in a
// full-width or lower-case form must still be answerable.
func TestAnOptionKeyIsComparedTheWayAnAnswerIs(t *testing.T) {
	// Both sides full-width and lower case in the bank, plain ASCII from the
	// user.
	written := choiceQuestion("ａ", "ａ", "B", "C")
	if !judged(t, written, "A").IsCorrect {
		t.Error("a full-width option key was not matched by its half-width form")
	}
	// And the other way round: the bank is plain and the answer is not.
	if !judged(t, choiceQuestion("C", "A", "B", "C"), "Ｃ").IsCorrect {
		t.Error("a full-width answer did not match the half-width key it stands for")
	}
}

// TestAnUnanswerableQuestionIsRefused walks the questions this package will not
// judge.
//
// Each of them is the bank's fault rather than the answer's, which is why the
// error is not the retryable one: a caller that retried would retry forever,
// and a caller that judged anyway would mark every answer to the question
// wrong.
func TestAnUnanswerableQuestionIsRefused(t *testing.T) {
	cases := []struct {
		name     string
		question notebook.Question
	}{
		{"a choice question whose answer names an option it does not offer",
			choiceQuestion("D", "A", "B")},
		{"a choice question offering no options", choiceQuestion("A")},
		{"a choice question storing no answer", choiceQuestion("", "A", "B")},
		{"a choice question storing only separators", choiceQuestion(" , ", "A", "B")},
		{"a fill-in question storing no answer", fillQuestion("")},
		{"a fill-in question storing only separators", fillQuestion(" | ")},
		{"a question of a kind this package does not know", essayQuestion()},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			provider := &countingProvider{}
			_, err := judgeFor(t, provider).Judge(context.Background(), scenario.question, "A")
			if !errors.Is(err, ErrUnusableQuestion) {
				t.Fatalf("Judge() error = %v, want an unusable question", err)
			}
			if errors.Is(err, ErrJudgeUnavailable) {
				t.Error("an unanswerable question was reported as a retryable failure")
			}
			if provider.calls != 0 {
				t.Errorf("an unanswerable question was sent to a model %d times", provider.calls)
			}
		})
	}
}

// TestTheJudgementIsTheWireShapeTheDesignWrites pins the field names.
//
// This value is stored verbatim in the review event's outcome_json and handed
// back unchanged when the same submission arrives again, so these names are
// data rather than an implementation detail, and renaming one of them is a
// migration. The two nulls are the same claim for the same reason: a reader
// tells an objective judgement from a model's by `model`, and a client iterating
// the rubric must not meet a null.
func TestTheJudgementIsTheWireShapeTheDesignWrites(t *testing.T) {
	encoded, err := json.Marshal(judged(t, choiceQuestion("A", "A", "B"), "A"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var shape map[string]any
	if err := json.Unmarshal(encoded, &shape); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	want := map[string]any{
		"kind":       "objective",
		"is_correct": true,
		"reason":     "你答的是「A」，题库的答案是「A」，两者一致。",
		"rubric":     []any{},
		"feedback":   "",
		"suggestion": "",
		"confidence": nil,
		"boundary":   objectiveBoundary,
		"model":      nil,
	}
	if len(shape) != len(want) {
		t.Errorf("the judgement has %d fields, want %d: %v", len(shape), len(want), shape)
	}
	for field, wanted := range want {
		got, present := shape[field]
		if !present {
			t.Errorf("the judgement has no field %q", field)
			continue
		}
		if !sameJSON(got, wanted) {
			t.Errorf("the judgement's %s = %#v, want %#v", field, got, wanted)
		}
	}
}

// TestAnUnansweredObjectiveQuestionSaysSo covers the one reason this branch
// writes two sentences rather than one: "you answered nothing" and "you
// answered the wrong thing" are different things to be told.
func TestAnUnansweredObjectiveQuestionSaysSo(t *testing.T) {
	for _, question := range []notebook.Question{
		choiceQuestion("A", "A", "B"),
		fillQuestion("闭包"),
	} {
		judgement := judged(t, question, "   ")
		if judgement.IsCorrect {
			t.Errorf("an empty answer to a %s question was judged correct", question.Type)
		}
		if !strings.Contains(judgement.Reason, "没有作答") {
			t.Errorf("the reason %q does not say the answer was empty", judgement.Reason)
		}
	}
}

// sameJSON compares two decoded JSON values, treating every number as text so
// that a float from the decoder is not a difference from an int in the
// expectation.
func sameJSON(got, want any) bool {
	gotJSON, gotErr := json.Marshal(got)
	wantJSON, wantErr := json.Marshal(want)
	if gotErr != nil || wantErr != nil {
		return false
	}
	return slices.Equal(gotJSON, wantJSON)
}
