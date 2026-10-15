package practice

// What the model-judgement cases are made of: the one-key credential source, the
// HTTP server that stands in for a provider, the recorder that reads what the
// judge sent, and the reply builders.
//
// They are a file of their own because they are the fixture and not the claims.
// Every case in judge_model_test.go builds its judge out of scriptedJudge and
// says what it wants the model to have replied in a call to replyWith or
// sseReply; none of them is about how that server works, and moving one of these
// helpers would move all of them.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"backend/internal/llm"
	"backend/internal/notebook"
	"backend/internal/outbound"
)

type judgeKey string

func (k judgeKey) Next() string       { return string(k) }
func (k judgeKey) Succeeded(string)   {}
func (k judgeKey) RateLimited(string) {}

// newJudgeClient is the provider these cases hand to the judge.
//
// The provider is served by an httptest server on this machine, so the client is
// built behind the policy that admits loopback: which policy the judge's model
// gets in a deployment is the assembly point's decision (§2.6), and what is
// under test here is what the judge makes of a wire reply.
func newJudgeClient(t *testing.T, baseURL string) llm.Provider {
	t.Helper()
	dial, err := outbound.DialContext(outbound.LANServicePolicy{})
	if err != nil {
		t.Fatalf("build the test dialer: %v", err)
	}
	client, err := llm.NewClient(baseURL, judgeKey("judge-test-key"), 5*time.Second, dial)
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
