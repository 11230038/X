package practice

// The model half of judging, driven through a real provider talking HTTP to a
// server this file stands up.
//
// The provider is the real one rather than a struct that returns a canned
// Response, because what is under test is what the judge makes of a *wire*
// reply: the temperature and the budget it asks for, the messages it sends, and
// whether an answer assembled from stream fragments is read as a judgement. A
// double would answer the last of those questions by construction.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// judgeKey is the credential these cases call with. It is a key *source* rather
// than a key because that is what the client takes (§2.11): what a pool does
// with several is internal/keypool's business, and a judge only ever needs one.
func TestAShortAnswerIsJudgedByAModel(t *testing.T) {
	judge, _ := scriptedJudge(t, replyWith(nil))

	judgement, err := judge.Judge(context.Background(), shortAnswerQuestion(), "闭包是函数里能读到外面变量的东西")
	if err != nil {
		t.Fatalf("Judge() error = %v", err)
	}

	if judgement.Kind != KindModelJudgment {
		t.Errorf("Kind = %q, want %q", judgement.Kind, KindModelJudgment)
	}
	if !judgement.IsCorrect {
		t.Error("IsCorrect = false, want the model's verdict")
	}
	if judgement.Model == nil || *judgement.Model != "conversation-model" {
		t.Errorf("Model = %v, want the model that judged", judgement.Model)
	}
	if judgement.Confidence == nil || *judgement.Confidence != 0.8 {
		t.Errorf("Confidence = %v, want the model's own estimate", judgement.Confidence)
	}
	if want := []string{"能读取外部变量的函数", "变量在函数定义外"}; !slices.Equal(judgement.Rubric, want) {
		t.Errorf("Rubric = %q, want %q", judgement.Rubric, want)
	}
	if judgement.Reason == "" || judgement.Boundary == "" || judgement.Suggestion == "" {
		t.Errorf("the judgement left something out: %+v", judgement)
	}
}

// TestTheJudgeSaysWhatAndHowItIsAsking pins the call itself: which model, at
// which temperature, with how much room, and with what in the messages.
//
// The model is the conversation model and not the task model — the task channel
// is for calls the user did not ask for, and this is a user pressing submit.
func TestTheJudgeSaysWhatAndHowItIsAsking(t *testing.T) {
	judge, calls := scriptedJudge(t, replyWith(nil))
	if _, err := judge.Judge(context.Background(), shortAnswerQuestion(), "闭包是函数"); err != nil {
		t.Fatalf("Judge() error = %v", err)
	}

	asked := calls.only(t)
	if asked.Model != "conversation-model" {
		t.Errorf("Model = %q, want the conversation model rather than the task model", asked.Model)
	}
	if asked.Temperature != judgeTemperature {
		t.Errorf("Temperature = %v, want %v written into the code", asked.Temperature, judgeTemperature)
	}
	if asked.MaxTokens != 512 {
		t.Errorf("MaxTokens = %d, want the judge's own budget", asked.MaxTokens)
	}
	if len(asked.Messages) != 2 || asked.Messages[0].Role != "system" {
		t.Fatalf("the judge sent %+v", asked.Messages)
	}
	if strings.Contains(calls.raw[0], `"tools"`) {
		t.Error("the judge offered the model tools")
	}

	// The question, the basis and the answer all travel, labelled: the answer is
	// free text and the model has to be told where it starts.
	message := asked.Messages[1].Content
	for _, wanted := range []string{
		"什么是闭包？",
		"能读取外部变量的函数",
		"闭包是函数",
	} {
		if !strings.Contains(message, wanted) {
			t.Errorf("the judge did not show the model %q:\n%s", wanted, message)
		}
	}
}

// TestAReplyThatIsNotTheShapeIsNotAVerdict walks §2.5's failure list.
//
// None of these becomes an answer. Guessing at what an unreadable reply meant
// would be inventing the one thing the user is asking about, so each is a
// retryable error instead — and the value returned alongside it is not a
// judgement either, which is the half that stops a caller from reading a
// zero-valued struct as "wrong".
func TestAReplyThatIsNotTheShapeIsNotAVerdict(t *testing.T) {
	cases := []struct {
		name  string
		reply string
	}{
		{"prose", "这道题答得不错，基本说到了要点。"},
		{"broken JSON", `{"is_correct": true, "reason": "说到要点了"`},
		{"nothing at all", ""},
		{"a JSON array", `[{"is_correct": true}]`},
		{"JSON then a courtesy", "{\"is_correct\": true}\n\n希望有帮助！"},
		{"the JSON word null", "null"},
		{"a fenced code block", "```json\n" + replyWith(nil) + "\n```"},
	}
	for _, field := range []string{
		"is_correct", "reason", "rubric", "feedback", "suggestion", "confidence", "boundary",
	} {
		cases = append(cases, struct {
			name  string
			reply string
		}{"a reply with no " + field, replyWith(map[string]any{field: nil})})
	}

	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			judge, _ := scriptedJudge(t, scenario.reply)
			judgement, err := judge.Judge(context.Background(), shortAnswerQuestion(), "闭包是函数")
			if !errors.Is(err, ErrJudgeUnavailable) {
				t.Fatalf("Judge() error = %v, want an unavailable judgement", err)
			}
			if judgement.Kind != "" || judgement.IsCorrect || judgement.Model != nil {
				t.Errorf("a refused reply came back as the judgement %+v", judgement)
			}
		})
	}
}

// TestAModelThatCannotJudgeDoesNotAnswerCorrect is AIQ-05's floor.
//
// "I cannot judge this" has two shapes and neither is a pass: a reply that does
// not carry the shape at all is refused outright, and one that says so in the
// fields it was asked for is a verdict of false with a reason. There is no
// third state for it to hide in, which is what makes the guarantee structural
// rather than a matter of trusting the wording.
func TestAModelThatCannotJudgeDoesNotAnswerCorrect(t *testing.T) {
	cannotJudge := replyWith(map[string]any{
		"is_correct": false,
		"reason":     "作答只有一句话，看不出是否理解闭包。",
		"boundary":   "信息不足以判断，我没有把它当成答对。",
		"rubric":     []string{},
	})

	judge, _ := scriptedJudge(t, cannotJudge)
	judgement, err := judge.Judge(context.Background(), shortAnswerQuestion(), "不知道")
	if err != nil {
		t.Fatalf("Judge() error = %v", err)
	}
	if judgement.IsCorrect {
		t.Error("a model that said it could not judge was read as saying the answer was right")
	}
	if judgement.Kind != KindModelJudgment || judgement.Boundary == "" {
		t.Errorf("the judgement does not carry the model's own boundary: %+v", judgement)
	}
	// An empty rubric is allowed where a missing one is not: the model judged
	// against nothing in particular and said so.
	if judgement.Rubric == nil || len(judgement.Rubric) != 0 {
		t.Errorf("Rubric = %#v, want an empty list", judgement.Rubric)
	}

	// The same thing said in prose is not a verdict at all.
	judge, _ = scriptedJudge(t, "这道题我无法判断。")
	if _, err := judge.Judge(context.Background(), shortAnswerQuestion(), "不知道"); !errors.Is(err, ErrJudgeUnavailable) {
		t.Errorf("a prose refusal came back as %v, want an unavailable judgement", err)
	}
}

// TestThePartsOfAJudgementThatCarryItsMeaningAreRequired covers the fields
// whose absence is not a formatting slip.
//
// A reason is what makes a verdict arguable and a boundary is what makes it
// trustworthy; a judgement missing either is not worth storing, whatever the
// rest of the reply says.
func TestThePartsOfAJudgementThatCarryItsMeaningAreRequired(t *testing.T) {
	cases := []struct {
		name  string
		reply string
	}{
		{"a blank reason", replyWith(map[string]any{"reason": "   "})},
		{"a blank boundary", replyWith(map[string]any{"boundary": ""})},
		{"confidence below the range", replyWith(map[string]any{"confidence": -0.1})},
		{"confidence above the range", replyWith(map[string]any{"confidence": 1.5})},
		{"confidence that is a word", replyWith(map[string]any{"confidence": "很高"})},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			judge, _ := scriptedJudge(t, scenario.reply)
			if _, err := judge.Judge(context.Background(), shortAnswerQuestion(), "闭包"); !errors.Is(err, ErrJudgeUnavailable) {
				t.Errorf("Judge() error = %v, want an unavailable judgement", err)
			}
		})
	}
}

// TestTheRubricLosesOnlyThePointsThatSayNothing checks that reading a list is
// reading a list: an empty entry is a blank line, and dropping it is not the
// same as supplying what the model left out.
func TestTheRubricLosesOnlyThePointsThatSayNothing(t *testing.T) {
	judge, _ := scriptedJudge(t, replyWith(map[string]any{
		"rubric": []string{" 能读取外部变量 ", "", "   ", "变量在函数定义外"},
	}))
	judgement, err := judge.Judge(context.Background(), shortAnswerQuestion(), "闭包")
	if err != nil {
		t.Fatalf("Judge() error = %v", err)
	}
	want := []string{"能读取外部变量", "变量在函数定义外"}
	if !slices.Equal(judgement.Rubric, want) {
		t.Errorf("Rubric = %q, want %q", judgement.Rubric, want)
	}
}

// TestExtraFieldsAreNotAFailureOnTheirOwn records one deliberate leniency: a
// reply that carries the shape and something else as well is read for the
// shape. An extra key cannot turn a judgement into a different one — every
// field this code reads has to be there and be right — so refusing it would
// only add a way for a good judgement to be lost.
func TestExtraFieldsAreNotAFailureOnTheirOwn(t *testing.T) {
	judge, _ := scriptedJudge(t, replyWith(map[string]any{"explanation": "顺手多写的一段"}))
	if _, err := judge.Judge(context.Background(), shortAnswerQuestion(), "闭包"); err != nil {
		t.Fatalf("Judge() error = %v", err)
	}
}

// TestAProviderFailureIsRetryableAndCancellationIsNot keeps the two apart.
//
// A provider that fails is a judgement the user can ask for again, which is
// what makes the error an exit rather than a dead end. A call the caller itself
// gave up on is not — the caller is the only one who can tell whether asking
// again is even meaningful, and it is told so by getting its own context's
// error back.
func TestAProviderFailureIsRetryableAndCancellationIsNot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	judge, err := NewJudge(
		newJudgeClient(t, server.URL), "conversation-model", 512)
	if err != nil {
		t.Fatalf("NewJudge() error = %v", err)
	}
	if _, err := judge.Judge(context.Background(), shortAnswerQuestion(), "闭包"); !errors.Is(err, ErrJudgeUnavailable) {
		t.Errorf("a rejected key came back as %v, want an unavailable judgement", err)
	}

	// The same call with a caller that has already given up.
	judge, calls := scriptedJudge(t, replyWith(nil))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = judge.Judge(ctx, shortAnswerQuestion(), "闭包")
	if errors.Is(err, ErrJudgeUnavailable) {
		t.Error("a cancelled call was reported as an unavailable judgement")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled call came back as %v, want the context's own error", err)
	}
	calls.mu.Lock()
	defer calls.mu.Unlock()
	if len(calls.requests) != 0 {
		t.Errorf("a cancelled call reached the provider %d times", len(calls.requests))
	}
}

// TestTheModelJudgementIsTheWireShapeTheDesignWrites pins the two fields that
// carry the difference between the branches: a model that judged, and its own
// estimate of how sure it is.
func TestTheModelJudgementIsTheWireShapeTheDesignWrites(t *testing.T) {
	judge, _ := scriptedJudge(t, replyWith(nil))
	judgement, err := judge.Judge(context.Background(), shortAnswerQuestion(), "闭包")
	if err != nil {
		t.Fatalf("Judge() error = %v", err)
	}
	encoded, err := json.Marshal(judgement)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var shape map[string]any
	if err := json.Unmarshal(encoded, &shape); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := map[string]any{
		"kind":       "model_judgment",
		"is_correct": true,
		"reason":     judgement.Reason,
		"rubric":     []any{judgement.Rubric[0], judgement.Rubric[1]},
		"feedback":   judgement.Feedback,
		"suggestion": judgement.Suggestion,
		"confidence": 0.8,
		"boundary":   judgement.Boundary,
		"model":      "conversation-model",
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
