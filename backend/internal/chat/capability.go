// Package chat is the general conversation capability: the one a request gets
// when it does not name another.
//
// It owns its turn — the stages it walks and when it stops — but it does not own
// how a model is called. Every call goes through the runtime's loop, which is
// what the capability seam exists for (RT-07).
package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"backend/internal/agentloop"
	"backend/internal/capability"
	"backend/internal/event"
	"backend/internal/llm"
)

// Name is the capability's identifier: what a request asks for, what its events
// are published under, and the key its tuning is configured by.
const Name = "chat"

// The stages, in the order the capability walks them.
//
// Both exist from the start even though V1 calls a model in only one of them,
// because the stage names are what the per-stage token limits are configured
// against (CFG-01): a stage that appears later would need a configuration
// change, not just code.
const (
	// StageExploring is where a capability works out what it is answering.
	// V1 does no model call here: the context is assembled and the budget
	// applied inside the loop, and a second call that produced nothing the
	// user sees would double the cost of every turn for no output. It is the
	// seam V2's context pre-survey lands in, without a protocol change.
	StageExploring = "exploring"
	// StageResponding is where the answer is produced.
	StageResponding = "responding"
)

// ErrInvalidSettings reports tuning that would let the capability run wrongly.
var ErrInvalidSettings = errors.New("chat: invalid settings")

// Manifest returns what the runtime registers for this capability.
//
// It declares no tools: the always-on set already mounts ask_user, and listing
// a tool here as well would make the manifest say something the mount policy
// decides. Tools it needs beyond the always-on set go here.
func Manifest() capability.Manifest {
	return capability.Manifest{
		Name:        Name,
		Description: "通用对话：理解问题后作答，信息不足时先向用户确认",
		Stages:      []string{StageExploring, StageResponding},
	}
}

// Settings is the environment-supplied tuning this capability runs with.
//
// It is resolved by the caller and passed in rather than read here, so this
// package does not depend on how configuration is stored or shaped.
type Settings struct {
	Model       string
	Temperature float64
	// ContextBudgetTokens bounds the conversation sent in one call.
	ContextBudgetTokens int
	// MaxRounds bounds the model calls one turn may make.
	MaxRounds int
	// StageMaxTokens is the output limit per stage, keyed by stage name.
	StageMaxTokens map[string]int
}

// Validate reports tuning this capability could not run with.
//
// Missing tuning fails here rather than at the first turn: a zero token limit
// or an unconfigured stage would otherwise surface as a rejected request hours
// later, with nothing to say about which key was wrong.
func (s Settings) Validate() error {
	if strings.TrimSpace(s.Model) == "" {
		return fmt.Errorf("%w: a model is required", ErrInvalidSettings)
	}
	if s.Temperature < 0 || s.Temperature > 2 {
		return fmt.Errorf("%w: temperature %v is outside 0 to 2", ErrInvalidSettings, s.Temperature)
	}
	if s.ContextBudgetTokens <= 0 {
		return fmt.Errorf("%w: context budget must be positive", ErrInvalidSettings)
	}
	if s.MaxRounds <= 0 {
		return fmt.Errorf("%w: the round limit must be positive", ErrInvalidSettings)
	}
	for _, stage := range Manifest().Stages {
		if s.StageMaxTokens[stage] <= 0 {
			return fmt.Errorf("%w: stage %s has no positive max_tokens", ErrInvalidSettings, stage)
		}
	}
	return nil
}

// Capability runs conversations.
type Capability struct {
	settings Settings
}

var _ capability.Capability = (*Capability)(nil)

// New returns the capability for the given tuning.
//
// It takes no logger: what goes wrong inside a turn is either the loop's to log
// or the runtime's to turn into a terminal state, and logging it again here
// would report the same failure twice.
func New(settings Settings) (*Capability, error) {
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	return &Capability{settings: settings}, nil
}

// Manifest implements capability.Capability.
func (c *Capability) Manifest() capability.Manifest { return Manifest() }

// Run plays a turn out: work out what is being asked, then answer it.
func (c *Capability) Run(ctx context.Context, rc capability.RunContext) error {
	if err := c.explore(ctx, rc); err != nil {
		return err
	}
	return c.respond(ctx, rc)
}

// explore marks the stage the runtime uses to assemble context.
func (c *Capability) explore(ctx context.Context, rc capability.RunContext) error {
	if err := c.emit(ctx, rc, event.TypeStageStart, StageExploring); err != nil {
		return err
	}
	return c.emit(ctx, rc, event.TypeStageEnd, StageExploring)
}

// respond calls the model and reports what it cost.
//
// The loop's error is returned unchanged: a cancelled turn, an exhausted round
// limit and a provider failure are the runtime's to turn into a terminal state,
// and translating them here would lose which one it was.
func (c *Capability) respond(ctx context.Context, rc capability.RunContext) error {
	if err := c.emit(ctx, rc, event.TypeStageStart, StageResponding); err != nil {
		return err
	}

	result, err := rc.RunLoop(ctx, agentloop.Request{
		Stage:               StageResponding,
		Model:               c.settings.Model,
		Temperature:         c.settings.Temperature,
		MaxTokens:           c.settings.StageMaxTokens[StageResponding],
		Messages:            c.messages(rc),
		Tools:               rc.Tools(),
		MaxRounds:           c.settings.MaxRounds,
		ContextBudgetTokens: c.settings.ContextBudgetTokens,
	})
	if err != nil {
		return err
	}

	if err := c.publishResult(ctx, rc, result); err != nil {
		return err
	}
	return c.emit(ctx, rc, event.TypeStageEnd, StageResponding)
}

// messages is the conversation the model is shown: this capability's prompt,
// then the turn's history.
//
// The prompt is prepended rather than stored with the history, so that what is
// persisted stays a record of what was said. A prompt that changed between
// versions would otherwise be frozen into older sessions, and a branch would
// inherit the prompt of the day it was forked.
func (c *Capability) messages(rc capability.RunContext) []llm.Message {
	history := rc.Messages()
	messages := make([]llm.Message, 0, len(history)+1)
	messages = append(messages, llm.Message{Role: llm.RoleSystem, Content: systemPrompt})
	return append(messages, history...)
}

// publishResult reports the answer and what it cost (RT-05).
//
// The token counts are the capability's to report because it is the level that
// knows which run they belong to; the runtime only sees the turn end.
func (c *Capability) publishResult(ctx context.Context, rc capability.RunContext, result agentloop.Result) error {
	return rc.Emitter().Emit(ctx, event.Event{
		Type:    event.TypeResult,
		Stage:   StageResponding,
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

func (c *Capability) emit(ctx context.Context, rc capability.RunContext, kind event.Type, stage string) error {
	return rc.Emitter().Emit(ctx, event.Event{Type: kind, Stage: stage})
}
