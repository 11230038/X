// Package agentloop is the single execution engine behind every capability
// (RT-07): it calls the model, runs the tools the model asks for, publishes
// events, and decides when to stop.
//
// Capabilities supply a stage name, a tool plane and messages; they do not get
// to implement their own loop. Keeping that in one place is what makes the
// context budget, the round limit and the cancellation checks apply uniformly
// instead of being re-derived per capability.
package agentloop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"backend/internal/event"
	"backend/internal/llm"
	"backend/internal/tool"
)

// ErrRoundsExhausted reports a model that kept asking for tools until the round
// limit. It is terminal: continuing would spend unbounded tokens on a model
// that is not converging.
var ErrRoundsExhausted = errors.New("agentloop: the model kept calling tools until the round limit")

// ErrToolCallWithoutID reports a tool call the provider sent with no id. The id
// is how the answer is matched to the call, so a call without one could not be
// answered, and sending it back would fail the next request instead.
var ErrToolCallWithoutID = errors.New("agentloop: the provider returned a tool call with no id")

// Plane resolves a tool name to the tool that may run it.
//
// It is asked once per call rather than captured when the plane was composed,
// which is what separates "the model was shown the schema" from "the model may
// call it" (TOOL-07): a name the plane does not resolve is refused even though
// the model was offered it.
type Plane interface {
	Tool(name string) (tool.Tool, bool)
}

// Request is one run of the loop: one stage, one model, one tool plane.
type Request struct {
	// Stage names the capability stage the run's events belong to.
	Stage string
	// Model and the two sampling settings come from the capability's tuning.
	Model       string
	Temperature float64
	MaxTokens   int
	// Messages is the conversation so far, oldest first.
	Messages []llm.Message
	// Tools is the plane as the model sees it.
	Tools []tool.Definition
	// MaxRounds bounds the model calls this run may make, so a model that keeps
	// asking for tools cannot spin forever.
	MaxRounds int
	// ContextBudgetTokens bounds the conversation sent in one call.
	ContextBudgetTokens int
}

// Result is what a run produced.
type Result struct {
	// Content is every content delta the run produced, in order. A run that
	// called tools and then answered carries both, which is also what the
	// content events published during the run carried.
	Content string
	Usage   llm.Usage
	// Rounds is how many model calls it took.
	Rounds int
}

// Loop runs one capability stage against a model.
type Loop struct {
	provider llm.Provider
	emitter  event.Emitter
	plane    Plane
	logger   *slog.Logger
	// source is the capability name the run's events are published under.
	source string
}

// New returns a loop for one capability. The plane may change between runs; the
// rest are fixed for the life of the loop.
func New(provider llm.Provider, emitter event.Emitter, plane Plane, logger *slog.Logger, source string) *Loop {
	return &Loop{
		provider: provider,
		emitter:  emitter,
		plane:    plane,
		logger:   logger,
		source:   source,
	}
}

// Run calls the model until it answers without asking for tools.
//
// The conversation grows as tools run, so the budget is re-checked before every
// call rather than once up front: one large tool result is enough to push a
// request past the model's window.
func (l *Loop) Run(ctx context.Context, req Request) (Result, error) {
	if req.MaxRounds <= 0 {
		return Result{}, fmt.Errorf("agentloop: a run needs at least one round")
	}

	messages := req.Messages
	var result Result
	for round := 1; round <= req.MaxRounds; round++ {
		// Cancellation is checked before every model call and every dispatch, so
		// a cancelled turn stops calling out even if it was already mid-loop.
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}

		messages = Trim(messages, req.ContextBudgetTokens)
		response, err := l.provider.Stream(ctx, l.modelRequest(req, messages), l.onDelta(ctx, req))
		if err != nil {
			return Result{}, err
		}

		result.Usage = addUsage(result.Usage, response.Usage)
		result.Content += response.Content
		result.Rounds = round

		if len(response.ToolCalls) == 0 {
			return result, nil
		}

		messages = append(messages, assistantMessage(response))
		for _, call := range response.ToolCalls {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			answer, err := l.dispatch(ctx, req, call)
			if err != nil {
				return Result{}, err
			}
			messages = append(messages, answer)
		}
	}
	return Result{}, ErrRoundsExhausted
}

func (l *Loop) modelRequest(req Request, messages []llm.Message) llm.Request {
	tools := make([]llm.ToolDefinition, 0, len(req.Tools))
	for _, definition := range req.Tools {
		tools = append(tools, llm.ToolDefinition{
			Name:        definition.Name,
			Description: definition.Description,
			Parameters:  definition.Parameters,
		})
	}
	return llm.Request{
		Model:       req.Model,
		Messages:    messages,
		Tools:       tools,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}
}

// onDelta publishes the model's output as it arrives. Reasoned text becomes a
// thinking event rather than content: it is shown while the model works, but it
// is not part of the answer.
func (l *Loop) onDelta(ctx context.Context, req Request) func(llm.Delta) error {
	return func(delta llm.Delta) error {
		if delta.Reasoning != "" {
			if err := l.publish(ctx, event.Event{
				Stage:   req.Stage,
				Type:    event.TypeThinking,
				Content: delta.Reasoning,
			}); err != nil {
				return err
			}
		}
		if delta.Content == "" {
			return nil
		}
		return l.publish(ctx, event.Event{
			Stage:   req.Stage,
			Type:    event.TypeContent,
			Content: delta.Content,
		})
	}
}

// publish sends one event. The source is the loop's, not the caller's: every
// event a run produces belongs to the capability that started it.
func (l *Loop) publish(ctx context.Context, e event.Event) error {
	e.Source = l.source
	return l.emitter.Emit(ctx, e)
}

// assistantMessage replays what the model said so the next call sees its own
// tool calls. The arguments go back exactly as they arrived: rewriting them
// would make the next request disagree with the call it is answering.
func assistantMessage(response llm.Response) llm.Message {
	message := llm.Message{Role: llm.RoleAssistant, Content: response.Content}
	for _, call := range response.ToolCalls {
		message.ToolCalls = append(message.ToolCalls, llm.ToolCall{
			ID:        call.ID,
			Name:      call.Name,
			Arguments: call.Arguments,
		})
	}
	return message
}

func addUsage(total, delta llm.Usage) llm.Usage {
	return llm.Usage{
		PromptTokens:     total.PromptTokens + delta.PromptTokens,
		CompletionTokens: total.CompletionTokens + delta.CompletionTokens,
		TotalTokens:      total.TotalTokens + delta.TotalTokens,
	}
}
