package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// fakeAsker stands in for the turn runtime and records what it was asked.
type fakeAsker struct {
	question string
	answer   string
	err      error
	calls    int
}

func (f *fakeAsker) AskUser(_ context.Context, question string) (string, error) {
	f.calls++
	f.question = question
	return f.answer, f.err
}

func askUserCall(t *testing.T, arguments string) Call {
	t.Helper()
	if !json.Valid([]byte(arguments)) {
		t.Fatalf("test arguments are not valid JSON: %s", arguments)
	}
	return Call{ID: "call_1", Name: AskUserName, Arguments: json.RawMessage(arguments)}
}

func TestAskUserReturnsTheAnswer(t *testing.T) {
	asker := &fakeAsker{answer: "用 Redis 做缓存"}
	tool := NewAskUserTool(asker)

	result, err := tool.Invoke(context.Background(), askUserCall(t, `{"question":"你想问什么？"}`))
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if !result.Success {
		t.Error("Success = false, want true")
	}
	if result.Content != "用 Redis 做缓存" {
		t.Errorf("Content = %q, want the answer", result.Content)
	}
	if asker.calls != 1 || asker.question != "你想问什么？" {
		t.Errorf("asker saw %d calls and question %q", asker.calls, asker.question)
	}
	if got := result.Metadata["question"]; got != "你想问什么？" {
		t.Errorf("Metadata[question] = %v", got)
	}
}

func TestAskUserRejectsCallsWithoutAQuestion(t *testing.T) {
	for _, tt := range []struct {
		name      string
		arguments string
		want      error
	}{
		{name: "empty object", arguments: `{}`, want: ErrMissingQuestion},
		{name: "blank question", arguments: `{"question":"   "}`, want: ErrMissingQuestion},
		{name: "empty question", arguments: `{"question":""}`, want: ErrMissingQuestion},
		{name: "question is not a string", arguments: `{"question":42}`, want: nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			asker := &fakeAsker{answer: "should not be reached"}
			_, err := NewAskUserTool(asker).Invoke(context.Background(), askUserCall(t, tt.arguments))
			if err == nil {
				t.Fatal("Invoke() error = nil, want a rejection")
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Errorf("Invoke() error = %v, want %v", err, tt.want)
			}
			// A rejected call must not have paused the turn.
			if asker.calls != 0 {
				t.Errorf("the asker was called %d times for a rejected call", asker.calls)
			}
		})
	}
}

func TestAskUserToleratesExtraArguments(t *testing.T) {
	// The schema asks for one field; a model that adds another has still asked a
	// usable question, and failing the call would cost the user a turn.
	asker := &fakeAsker{answer: "ok"}
	_, err := NewAskUserTool(asker).Invoke(context.Background(), askUserCall(t, `{"question":"继续吗？","options":["是","否"]}`))
	if err != nil {
		t.Fatalf("Invoke() error = %v, want the extra field to be ignored", err)
	}
	if asker.question != "继续吗？" {
		t.Errorf("asker question = %q", asker.question)
	}
}

func TestAskUserTrimsTheQuestion(t *testing.T) {
	asker := &fakeAsker{answer: "ok"}
	if _, err := NewAskUserTool(asker).Invoke(context.Background(), askUserCall(t, `{"question":"  继续吗？ "}`)); err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if asker.question != "继续吗？" {
		t.Errorf("asker question = %q, want it trimmed", asker.question)
	}
}

func TestAskUserReportsAMalformedArgumentObject(t *testing.T) {
	// The model does not always emit an object, so this must fail cleanly
	// rather than panic inside the decode.
	asker := &fakeAsker{}
	_, err := NewAskUserTool(asker).Invoke(context.Background(), Call{Name: AskUserName, Arguments: json.RawMessage(`"just a string"`)})
	if err == nil {
		t.Fatal("Invoke() error = nil, want a decode failure")
	}
	if asker.calls != 0 {
		t.Errorf("the asker was called %d times", asker.calls)
	}
}

func TestAskUserPassesThroughTheAskerFailure(t *testing.T) {
	// A turn that stopped waiting is not the tool's failure to reinterpret.
	wantErr := errors.New("input timed out")
	asker := &fakeAsker{err: wantErr}

	_, err := NewAskUserTool(asker).Invoke(context.Background(), askUserCall(t, `{"question":"继续吗？"}`))
	if !errors.Is(err, wantErr) {
		t.Errorf("Invoke() error = %v, want %v", err, wantErr)
	}
	// A question the runtime could not deliver ends the turn: the deadline for
	// an answer passed, or the turn was cancelled, and in neither case is there
	// an answer the model could be given instead.
	if !errors.Is(err, ErrFatal) {
		t.Errorf("Invoke() error = %v, want a fatal failure", err)
	}
}

func TestAMissingQuestionIsTheModelsToFix(t *testing.T) {
	// The model can ask again with a question in it, so this one stays the
	// call's failure rather than ending the turn over it.
	asker := &fakeAsker{}
	_, err := NewAskUserTool(asker).Invoke(context.Background(), askUserCall(t, `{}`))
	if !errors.Is(err, ErrMissingQuestion) {
		t.Fatalf("Invoke() error = %v, want ErrMissingQuestion", err)
	}
	if errors.Is(err, ErrFatal) {
		t.Error("a missing question ended the turn")
	}
	if asker.calls != 0 {
		t.Error("the runtime was asked a question with nothing in it")
	}
}

func TestAskUserHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	asker := &fakeAsker{err: context.Canceled}
	_, err := NewAskUserTool(asker).Invoke(ctx, askUserCall(t, `{"question":"继续吗？"}`))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Invoke() error = %v, want context.Canceled", err)
	}
}

func TestAskUserDefinition(t *testing.T) {
	definition := NewAskUserTool(&fakeAsker{}).Definition()
	if definition.Name != AskUserName {
		t.Errorf("Name = %q, want %q", definition.Name, AskUserName)
	}
	if strings.TrimSpace(definition.Description) == "" {
		t.Error("Description is blank; the model has nothing to decide with")
	}

	var schema struct {
		Type       string   `json:"type"`
		Required   []string `json:"required"`
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(definition.Parameters, &schema); err != nil {
		t.Fatalf("Parameters is not valid JSON: %v", err)
	}
	if schema.Type != "object" {
		t.Errorf("schema type = %q, want object", schema.Type)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "question" {
		t.Errorf("schema required = %v, want [question]", schema.Required)
	}
	if question, ok := schema.Properties["question"]; !ok || question.Type != "string" {
		t.Errorf("schema properties = %v, want a string question", schema.Properties)
	}
}
