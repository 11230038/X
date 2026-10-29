// Package directchat answers one question with one model call.
//
// It is the one place in the product where a model is asked something with no
// conversation around it: no session, no history, no tools, and nothing kept
// afterwards. The answer is returned to the caller and forgotten, which is why
// this package has no repository and no interface to one.
//
// It is not a second way to talk to a model: the provider it is handed is the
// same recorder every other call goes through, so a direct question is counted,
// charged against the account's quota and visible in the usage report exactly
// like a turn's call. What is missing is the conversation, not the accounting.
package directchat

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"backend/internal/llm"
)

// Errors this package answers with.
var (
	// ErrEmptyQuestion is a request with nothing to ask.
	ErrEmptyQuestion = errors.New("directchat: the question is empty")
	// ErrNoAnswer is a call that came back without any text. It is a failure
	// rather than an empty answer: the caller asked one question and there is
	// nothing on the other side of it.
	ErrNoAnswer = errors.New("directchat: the model returned no answer")
	// ErrUnavailable is a model call that did not come back. The provider's own
	// error stays in the chain, so a quota refusal or a cancelled request is
	// still recognisable to whoever handles it.
	ErrUnavailable = errors.New("directchat: the model call failed")
)

// Options is what a direct chat service runs with.
type Options struct {
	// Provider is the shared model provider. It is required.
	Provider llm.Provider
	// Model, Temperature and MaxTokens are the general assistant's tuning: the
	// deployment already chose them for the chat capability, and a direct
	// question should not run on a second, differently-tuned model.
	Model       string
	Temperature float64
	MaxTokens   int
}

// Service makes one question and one answer out of a model call.
type Service struct {
	provider    llm.Provider
	model       string
	temperature float64
	maxTokens   int
}

// New builds the service, refusing tuning it could not run with.
func New(options Options) (*Service, error) {
	if options.Provider == nil {
		return nil, fmt.Errorf("directchat: a provider is required")
	}
	if strings.TrimSpace(options.Model) == "" {
		return nil, fmt.Errorf("directchat: a model is required")
	}
	if options.Temperature < 0 || options.Temperature > 2 {
		return nil, fmt.Errorf("directchat: temperature %v is outside 0 to 2", options.Temperature)
	}
	if options.MaxTokens <= 0 {
		return nil, fmt.Errorf("directchat: max tokens must be positive")
	}
	return &Service{
		provider:    options.Provider,
		model:       options.Model,
		temperature: options.Temperature,
		maxTokens:   options.MaxTokens,
	}, nil
}

// Ask sends one question and returns the answer.
//
// The request is exactly one user message. There is no system prompt and no
// history, which is what "direct" means here: the model sees the question and
// nothing else. The reply is assembled from the stream and returned whole,
// because a caller with one question in hand has no use for it in fragments.
func (s *Service) Ask(ctx context.Context, question string) (string, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return "", ErrEmptyQuestion
	}
	request := llm.Request{
		Model:       s.model,
		Messages:    []llm.Message{{Role: llm.RoleUser, Content: question}},
		Temperature: s.temperature,
		MaxTokens:   s.maxTokens,
	}

	var reply strings.Builder
	response, err := s.provider.Stream(ctx, request, func(delta llm.Delta) error {
		reply.WriteString(delta.Content)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	// The provider assembles the same text it hands over as deltas. Falling back
	// to it costs nothing and covers a provider that answers with a single
	// chunk-shaped Response rather than a stream.
	if reply.Len() == 0 {
		reply.WriteString(response.Content)
	}
	answer := strings.TrimSpace(reply.String())
	if answer == "" {
		return "", ErrNoAnswer
	}
	return answer, nil
}
