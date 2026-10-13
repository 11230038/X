package capability

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"backend/internal/agentloop"
	"backend/internal/event"
	"backend/internal/llm"
)

// ErrInvalidSettings reports tuning that would let a capability run wrongly.
var ErrInvalidSettings = errors.New("capability: invalid settings")

// Settings is the tuning a capability runs with: the model, its sampling, and a
// token limit for each stage it walks.
//
// It is one type for every capability because it is one shape in the
// environment — the deployment writes exactly these fields under each
// capability's own name — and because which stages a capability has is its
// manifest's answer, not this type's. That is why Validate takes the manifest:
// the check that the tuning covers the stages is the check that two
// hand-written lists agree.
type Settings struct {
	Model       string
	Temperature float64
	// ContextBudgetTokens bounds the conversation sent in one call.
	ContextBudgetTokens int
	// MaxRounds bounds the model calls one stage may make.
	MaxRounds int
	// StageMaxTokens is the output limit per stage, keyed by stage name.
	StageMaxTokens map[string]int
}

// Validate reports tuning this capability could not run with.
//
// Missing tuning fails here rather than at the first turn: a zero token limit
// or an unconfigured stage would otherwise surface as a rejected request hours
// later, with nothing to say about which key was wrong.
func (s Settings) Validate(manifest Manifest) error {
	if strings.TrimSpace(s.Model) == "" {
		return fmt.Errorf("%w: %s has no model", ErrInvalidSettings, manifest.Name)
	}
	if s.Temperature < 0 || s.Temperature > 2 {
		return fmt.Errorf("%w: %s has temperature %v, outside 0 to 2", ErrInvalidSettings, manifest.Name, s.Temperature)
	}
	if s.ContextBudgetTokens <= 0 {
		return fmt.Errorf("%w: %s has no positive context budget", ErrInvalidSettings, manifest.Name)
	}
	if s.MaxRounds <= 0 {
		return fmt.Errorf("%w: %s has no positive round limit", ErrInvalidSettings, manifest.Name)
	}
	for _, stage := range manifest.Stages {
		if s.StageMaxTokens[stage] <= 0 {
			return fmt.Errorf("%w: %s stage %s has no positive max_tokens", ErrInvalidSettings, manifest.Name, stage)
		}
	}
	return nil
}

// Answer runs one stage of a capability: opens it, calls the model through the
// shared loop, publishes the answer and what it cost, and closes the stage.
//
// It is here rather than written out in each capability because a stage that
// produces an answer is the same stage in all of them — chat's, the clarifying
// one's, the multi-step one's. What differs is the instruction and the
// messages, which are arguments, and the stages themselves, which are the
// capability's own.
//
// The loop's error is returned unchanged: a cancelled turn, an exhausted round
// limit and a provider failure are the runtime's to turn into a terminal state,
// and translating them here would lose which one it was (RT-07). A stage that
// failed publishes no result and no end — it did not finish, and saying it did
// would tell a client the answer was complete.
func Answer(ctx context.Context, rc RunContext, settings Settings, stage string, messages []llm.Message) error {
	// Read before anything is published: a stage the tuning does not cover would
	// otherwise be sent to the provider as a limit of zero, which some providers
	// read as "no output" rather than as the configuration mistake it is.
	maxTokens, ok := settings.StageMaxTokens[stage]
	if !ok || maxTokens <= 0 {
		return fmt.Errorf("%w: stage %s has no positive max_tokens", ErrInvalidSettings, stage)
	}

	if err := EmitStage(ctx, rc, event.TypeStageStart, stage); err != nil {
		return err
	}
	result, err := rc.RunLoop(ctx, agentloop.Request{
		Stage:               stage,
		Model:               settings.Model,
		Temperature:         settings.Temperature,
		MaxTokens:           maxTokens,
		Messages:            messages,
		Tools:               rc.Tools(),
		MaxRounds:           settings.MaxRounds,
		ContextBudgetTokens: settings.ContextBudgetTokens,
	})
	if err != nil {
		return err
	}
	if err := publishResult(ctx, rc, stage, result); err != nil {
		return err
	}
	return EmitStage(ctx, rc, event.TypeStageEnd, stage)
}

// publishResult reports the answer and what it cost (RT-05).
//
// The token counts are reported here because a run is what they belong to; the
// runtime above only sees the turn end, and the turn's own accounting — how many
// sources it handed out — is added to this same event on the way past (see the
// turn's result watch).
func publishResult(ctx context.Context, rc RunContext, stage string, result agentloop.Result) error {
	return rc.Emitter().Emit(ctx, event.Event{
		Type:    event.TypeResult,
		Stage:   stage,
		Content: result.Content,
		Metadata: map[string]any{
			event.MetaUsageSummary: map[string]any{
				"prompt_tokens":     result.Usage.PromptTokens,
				"completion_tokens": result.Usage.CompletionTokens,
				"total_tokens":      result.Usage.TotalTokens,
				"rounds":            result.Rounds,
			},
		},
	})
}

// Messages is what a capability sends the model: its own instruction first,
// then whatever this turn has to add, then the conversation.
//
// The instruction is prepended rather than stored with the history, so what is
// persisted stays a record of what was said. A prompt that changed between
// versions would otherwise be frozen into older sessions, and a branch would
// inherit the prompt of the day it was forked.
//
// It is a system message, which is also what keeps it: the loop trims a
// conversation that overruns its budget from the oldest end, and a system
// message is never what gets dropped. The same goes for anything passed in
// between — a turn's survey of its own material is the last thing that should
// go, because it is what the answer is standing on.
func Messages(instruction string, history []llm.Message, supplied ...llm.Message) []llm.Message {
	messages := make([]llm.Message, 0, len(history)+1+len(supplied))
	messages = append(messages, llm.Message{Role: llm.RoleSystem, Content: instruction})
	for _, message := range supplied {
		// A zero message is what "this turn has nothing to add" looks like, and
		// sending it anyway would put a blank system turn in front of every
		// conversation that arrived without material.
		if message.Role == "" && message.Content == "" {
			continue
		}
		messages = append(messages, message)
	}
	return append(messages, history...)
}

// EmitStage publishes one stage boundary.
//
// Stage boundaries are the capability's own events — the loop publishes the
// model's output and the tool traffic — and they are emitted in pairs, so a
// client can tell a stage that ended from one that was cut off.
func EmitStage(ctx context.Context, rc RunContext, kind event.Type, stage string) error {
	return rc.Emitter().Emit(ctx, event.Event{Type: kind, Stage: stage})
}
