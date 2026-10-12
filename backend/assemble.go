package main

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"backend/internal/capability"
	"backend/internal/chat"
	"backend/internal/config"
	"backend/internal/llm"
	"backend/internal/session"
	"backend/internal/tool"
	"backend/internal/turn"
	"backend/internal/ws"
	"gorm.io/gorm"
)

// ai is the assembled AI side of the process: the runtime that runs turns, and
// the endpoint that shows them to a client.
type ai struct {
	turns        *turn.Runner
	socket       *ws.Handler
	capabilities *capability.Registry
}

// assemble builds the turn runtime from configuration.
//
// Everything that can be checked before the first request is checked here: the
// cost of a drift that starts the process anyway is a turn that fails with a
// message about a tool nobody registered, an hour later, in front of a user.
func assemble(cfg config.Config, logger *slog.Logger, db *gorm.DB, sessions *session.Service) (*ai, error) {
	capabilities := capability.NewRegistry()
	tools := tool.NewRegistry()
	policy := tool.DefaultPolicy()

	// ask_user is always on, and the runtime is its asker: the tool does not
	// know how a turn is paused, so the thing that does is what it is bound to.
	if err := tools.Register(tool.NewAskUserTool(turn.Asker{})); err != nil {
		return nil, fmt.Errorf("register the ask_user tool: %w", err)
	}

	chatCapability, err := chat.New(chatSettings(cfg))
	if err != nil {
		return nil, fmt.Errorf("initialize the chat capability: %w", err)
	}
	if err := capabilities.Register(chatCapability); err != nil {
		return nil, fmt.Errorf("register the chat capability: %w", err)
	}

	if err := checkDrift(capabilities, policy, tools.Names(), capabilityConfig(cfg)); err != nil {
		return nil, err
	}

	provider := llm.NewClient(cfg.LLM.BaseURL, cfg.LLM.APIKey, cfg.LLM.Timeout)
	titler, err := turn.NewTitler(provider, cfg.LLM.TaskModel, cfg.LLM.TaskMaxTokens)
	if err != nil {
		return nil, fmt.Errorf("initialize session naming: %w", err)
	}
	repository := turn.NewGORMRepository(db)
	hub, err := turn.NewHub(repository)
	if err != nil {
		return nil, fmt.Errorf("initialize the turn hub: %w", err)
	}
	runner, err := turn.NewRunner(turn.Options{
		Repository:        repository,
		Hub:               hub,
		Sessions:          sessions,
		Capabilities:      capabilities,
		Tools:             tools,
		Policy:            policy,
		Provider:          provider,
		Titler:            titler,
		Logger:            logger,
		DefaultCapability: chat.Name,
		InputTimeout:      cfg.Turn.InputTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize the turn runtime: %w", err)
	}

	socket, err := ws.NewHandler(turnRuntime{runner}, logger)
	if err != nil {
		return nil, fmt.Errorf("initialize the websocket endpoint: %w", err)
	}
	return &ai{turns: runner, socket: socket, capabilities: capabilities}, nil
}

// turnRuntime adapts the turn runtime to what the protocol layer drives.
//
// The two differ in one method: a subscription is the hub's own type, and a
// connection needs only the events, the reason they stopped and the way to stop
// them. Narrowing it here is what keeps the protocol layer from depending on
// how a turn is watched, and what lets its tests drive a stream by hand.
type turnRuntime struct {
	*turn.Runner
}

var _ ws.Turns = turnRuntime{}

func (t turnRuntime) Subscribe(ctx context.Context, turnID string, afterSeq int64) (ws.Stream, error) {
	subscription, err := t.Runner.Subscribe(ctx, turnID, afterSeq)
	if err != nil {
		return nil, err
	}
	return subscription, nil
}

// close stops the socket and then the turns.
//
// The order matters: a turn that is ending publishes its ending, and a client
// that is still connected is told what it ended as. Closing the runtime first
// would leave whoever is watching a stream that stops without saying why.
func (a *ai) close(ctx context.Context) error {
	if err := a.socket.Close(ctx); err != nil {
		return fmt.Errorf("close websocket connections: %w", err)
	}
	if err := a.turns.Close(ctx); err != nil {
		return fmt.Errorf("stop running turns: %w", err)
	}
	return nil
}

// checkDrift refuses a configuration that does not describe this build.
//
// Each check is about a pair of things that are written down twice: the tools a
// manifest names and the tools that were registered, and the capabilities and
// stages the tuning names and the ones the manifests declare. A mismatch means
// the process would start and then fail on the first turn that touched it.
func checkDrift(capabilities *capability.Registry, policy tool.Policy, registered []string, configured capability.Config) error {
	if err := capabilities.CheckToolDrift(registered); err != nil {
		return fmt.Errorf("capability manifests name tools that are not registered: %w", err)
	}
	if err := capabilities.CheckConfigDrift(configured); err != nil {
		return err
	}
	known := make(map[string]bool, len(registered))
	for _, name := range registered {
		known[name] = true
	}
	for _, name := range policy.Referenced() {
		if !known[name] {
			return fmt.Errorf("the tool policy names %s, which is not a registered tool", name)
		}
	}
	return nil
}

// chatSettings is the chat capability's tuning, resolved from configuration.
func chatSettings(cfg config.Config) chat.Settings {
	params := cfg.LLM.Capabilities[chat.Name]
	stages := make(map[string]int, len(params.Stages))
	for stage, tuning := range params.Stages {
		stages[stage] = tuning.MaxTokens
	}
	return chat.Settings{
		Model:               cfg.LLM.Model,
		Temperature:         params.Temperature,
		ContextBudgetTokens: params.ContextBudgetTokens,
		MaxRounds:           cfg.Turn.MaxRounds,
		StageMaxTokens:      stages,
	}
}

// capabilityConfig is which stages each configured capability declares, which
// is what the drift check compares against the manifests.
func capabilityConfig(cfg config.Config) capability.Config {
	configured := make(capability.Config, len(cfg.LLM.Capabilities))
	for name, params := range cfg.LLM.Capabilities {
		stages := make([]string, 0, len(params.Stages))
		for stage := range params.Stages {
			stages = append(stages, stage)
		}
		sort.Strings(stages)
		configured[name] = stages
	}
	return configured
}
