package directchat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"backend/internal/llm"
)

// scriptedProvider is the model as these cases need it: it records the request
// it was handed and answers with whatever the case told it to.
type scriptedProvider struct {
	asked  llm.Request
	reply  string
	answer string
	err    error
}

func (p *scriptedProvider) Stream(
	_ context.Context, request llm.Request, onDelta func(llm.Delta) error,
) (llm.Response, error) {
	p.asked = request
	if p.err != nil {
		return llm.Response{}, p.err
	}
	if p.reply != "" {
		if err := onDelta(llm.Delta{Content: p.reply}); err != nil {
			return llm.Response{}, err
		}
	}
	return llm.Response{Content: p.answer + p.reply}, nil
}

func newService(t *testing.T, provider llm.Provider) *Service {
	t.Helper()
	service, err := New(Options{
		Provider:    provider,
		Model:       "deepseek-chat",
		Temperature: 0.7,
		MaxTokens:   4096,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return service
}

func TestAskSendsOneUserMessageAndReturnsTheAnswer(t *testing.T) {
	provider := &scriptedProvider{reply: "  三  "}
	service := newService(t, provider)

	answer, err := service.Ask(context.Background(), "  1+2 等于几  ")
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if answer != "三" {
		t.Errorf("answer = %q, want the trimmed reply", answer)
	}
	if len(provider.asked.Messages) != 1 {
		t.Fatalf("messages = %+v, want exactly one", provider.asked.Messages)
	}
	message := provider.asked.Messages[0]
	if message.Role != llm.RoleUser || message.Content != "1+2 等于几" {
		t.Errorf("message = %+v, want the trimmed question as the only user turn", message)
	}
	if provider.asked.Model != "deepseek-chat" ||
		provider.asked.Temperature != 0.7 || provider.asked.MaxTokens != 4096 {
		t.Errorf("request = %+v, want the general assistant's tuning", provider.asked)
	}
	if len(provider.asked.Tools) != 0 {
		t.Errorf("tools = %+v, want none", provider.asked.Tools)
	}
}

func TestAskRefusesAnEmptyQuestionWithoutCallingTheModel(t *testing.T) {
	provider := &scriptedProvider{reply: "不该被问"}
	service := newService(t, provider)

	if _, err := service.Ask(context.Background(), "   "); !errors.Is(err, ErrEmptyQuestion) {
		t.Fatalf("Ask(blank) error = %v, want ErrEmptyQuestion", err)
	}
	if provider.asked.Model != "" {
		t.Error("the model was called for an empty question")
	}
}

func TestAskTreatsAnEmptyReplyAsNoAnswer(t *testing.T) {
	service := newService(t, &scriptedProvider{})

	if _, err := service.Ask(context.Background(), "问题"); !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("Ask() error = %v, want ErrNoAnswer", err)
	}
}

func TestAskKeepsTheProviderErrorInTheChain(t *testing.T) {
	cause := errors.New("provider is down")
	service := newService(t, &scriptedProvider{err: cause})

	_, err := service.Ask(context.Background(), "问题")
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, cause) {
		t.Fatalf("Ask() error = %v, want ErrUnavailable wrapping the cause", err)
	}
	if strings.TrimSpace(err.Error()) == "" {
		t.Error("the error says nothing")
	}
}
