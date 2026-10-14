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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"backend/internal/llm"
	"backend/internal/notebook"
)

// judgeKey is the credential these cases call with. It is a key *source* rather
// than a key because that is what the client takes (§2.11): what a pool does
// with several is internal/keypool's business, and a judge only ever needs one.
type judgeKey string

func (k judgeKey) Next() string       { return string(k) }
func (k judgeKey) Succeeded(string)   {}
func (k judgeKey) RateLimited(string) {}

// newJudgeClient is the provider these cases hand to the judge.
func newJudgeClient(t *testing.T, baseURL string) llm.Provider {
	t.Helper()
	client, err := llm.NewClient(baseURL, judgeKey("judge-test-key"), 5*time.Second)
	if err != nil {
		t.Fatalf("llm.NewClient() error = %v", err)
	}
	return client
}

// judgeRequest is the parts of the wire request this file asserts on.
type judgeRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	Temperature float64 `json:"temperature"`
	MaxTokens   int     `json:"max_tokens"`
}

// judgeCalls is the provider's side of a test: the replies it was scripted
// with, and what it was asked for.
type judgeCalls struct {
	mu       sync.Mutex
	replies  []string
	requests []judgeRequest
	raw      []string
}

// reply records one request and returns the reply scripted for it.
func (c *judgeCalls) reply(t *testing.T, body []byte) (string, bool) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()

	var request judgeRequest
	if err := json.Unmarshal(body, &request); err != nil {
		t.Errorf("the judge sent a request that is not JSON: %v", err)
		return "", false
	}
	c.requests = append(c.requests, request)
	c.raw = append(c.raw, string(body))
	if len(c.requests) > len(c.replies) {
		t.Errorf("the judge asked for reply %d; the test scripted %d", len(c.requests), len(c.replies))
		return "", false
	}
	return c.replies[len(c.requests)-1], true
}

// only returns the one request the judge made.
func (c *judgeCalls) only(t *testing.T) judgeRequest {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.requests) != 1 {
		t.Fatalf("the judge made %d calls, want one", len(c.requests))
	}
	return c.requests[0]
}

// scriptedJudge is a judge over a provider that answers with the given replies
// in order.
func scriptedJudge(t *testing.T, replies ...string) (*Judge, *judgeCalls) {
	t.Helper()
	calls := &judgeCalls{replies: replies}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		reply, ok := calls.reply(t, body)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := io.WriteString(w, sseReply(reply)); err != nil {
			t.Errorf("write stream: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	judge, err := NewJudge(
		newJudgeClient(t, server.URL), "conversation-model", 512)
	if err != nil {
		t.Fatalf("NewJudge() error = %v", err)
	}
	return judge, calls
}

// sseReply renders a reply the way a provider streams one: in two fragments,
// with usage on the last of them, terminated.
//
// Two fragments rather than one so that the judgement being parsed was
// assembled from a stream. One is a shape this code has never seen.
func sseReply(reply string) string {
	runes := []rune(reply)
	half := len(runes) / 2
	fragments := []string{string(runes[:half]), string(runes[half:])}

	var builder strings.Builder
	for index, fragment := range fragments {
		payload := map[string]any{
			"choices": []any{map[string]any{
				"index": 0,
				"delta": map[string]any{"content": fragment},
			}},
		}
		if index == len(fragments)-1 {
			payload["usage"] = map[string]any{
				"prompt_tokens": 100, "completion_tokens": 20, "total_tokens": 120,
			}
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			panic(fmt.Sprintf("encode a test fragment: %v", err))
		}
		fmt.Fprintf(&builder, "data: %s\n\n", encoded)
	}
	builder.WriteString("data: [DONE]\n\n")
	return builder.String()
}

// replyWith builds the shape the prompt asks for, with the given overrides.
func replyWith(overrides map[string]any) string {
	fields := goodReply()
	for field, value := range overrides {
		if value == nil {
			delete(fields, field)
			continue
		}
		fields[field] = value
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		panic(fmt.Sprintf("encode a test reply: %v", err))
	}
	return string(encoded)
}

// goodReply is a reply that is the shape it should be.
func goodReply() map[string]any {
	return map[string]any{
		"is_correct": true,
		"reason":     "作答说到了「能读取外部变量的函数」，这是解析里的第一个采分点。",
		"rubric":     []string{"能读取外部变量的函数", "变量在函数定义外"},
		"feedback":   "要点答到了，但没说清变量的作用域。",
		"suggestion": "补一句变量是在哪定义的。",
		"confidence": 0.8,
		"boundary":   "这题没有唯一标准答案，我只按你给的解析判。",
	}
}

// shortAnswerQuestion is a question of the kind a model judges.
func shortAnswerQuestion() notebook.Question {
	return notebook.Question{
		QuestionID:    "qn_short",
		Text:          "什么是闭包？",
		Type:          notebook.QuestionShortAnswer,
		CorrectAnswer: "闭包是能读取外部变量的函数",
		Explanation:   "采分点：1. 能读取外部变量的函数；2. 变量在函数定义外。",
	}
}

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
