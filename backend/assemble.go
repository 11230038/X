package main

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"backend/internal/askquestions"
	"backend/internal/capability"
	"backend/internal/chat"
	"backend/internal/config"
	"backend/internal/deepsolve"
	"backend/internal/embed"
	"backend/internal/kb"
	"backend/internal/llm"
	"backend/internal/parse"
	"backend/internal/session"
	"backend/internal/tool"
	"backend/internal/turn"
	"backend/internal/ws"
	"gorm.io/gorm"
)

// ai is the assembled AI side of the process: the runtime that runs turns, the
// endpoint that shows them to a client, and the background work that catches the
// knowledge base up with what has been added to it.
type ai struct {
	turns        *turn.Runner
	socket       *ws.Handler
	capabilities *capability.Registry
	// materials is the knowledge base the turns read through, kept here because
	// the HTTP endpoints that manage it are built beside the rest of the
	// handlers rather than in here: it is the same service, reached two ways.
	materials *kb.Service
	// indexing is closed when the index worker has stopped, which is what makes
	// a shutdown able to wait for the document it is in the middle of.
	indexing <-chan struct{}
}

// assemble builds the turn runtime from configuration.
//
// Everything that can be checked before the first request is checked here: the
// cost of a drift that starts the process anyway is a turn that fails with a
// message about a tool nobody registered, an hour later, in front of a user.
//
// ctx is the process's own context, and it is what the background worker runs
// on: the worker stops when the process is asked to stop, not when a request is.
func assemble(
	ctx context.Context,
	cfg config.Config,
	logger *slog.Logger,
	db *gorm.DB,
	sessions *session.Service,
	files kb.FileSource,
) (*ai, error) {
	tools := tool.NewRegistry()
	policy := tool.DefaultPolicy()

	// Every tool here is bound to the runtime, not to a turn: ask_user is
	// always on, the two source tools are mounted by what a turn carries, and
	// the four solve tools are mounted by the capability that plans. Each finds
	// the turn it is being called for in the context of the call, so none of
	// them can reach past what that turn was given.
	for _, register := range []struct {
		name string
		tool tool.Tool
	}{
		{tool.AskUserName, tool.NewAskUserTool(turn.Asker{})},
		{tool.ReadSourceName, tool.NewReadSourceTool(turn.Sourcer{})},
		{tool.RetrieveName, tool.NewRetrieveTool(turn.Sourcer{})},
		{tool.SolvePlanName, tool.NewSolvePlanTool()},
		{tool.SolveStepName, tool.NewSolveStepTool()},
		{tool.SolveReplanName, tool.NewSolveReplanTool()},
		{tool.SolveFinishName, tool.NewSolveFinishTool()},
	} {
		if err := tools.Register(register.tool); err != nil {
			return nil, fmt.Errorf("register the %s tool: %w", register.name, err)
		}
	}

	capabilities, err := buildCapabilities(cfg)
	if err != nil {
		return nil, err
	}

	if err := checkDrift(capabilities, policy, tools.Names(), capabilityConfig(cfg)); err != nil {
		return nil, err
	}

	parsers, err := parse.NewRegistry(parse.Options{
		DefaultEngine:  cfg.Parser.DefaultEngine,
		DoclingBaseURL: cfg.Parser.DoclingBaseURL,
		Timeout:        cfg.Parser.Timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize the parser registry: %w", err)
	}
	// One repository and one embedding client, shared by the two things that use
	// them: the runtime, which reads the material a turn carries, and the worker,
	// which embeds what was added to a collection. Two clients would be two pools
	// and two independent rates against the same provider.
	documents := kb.NewGORMRepository(db)
	embedder, err := newEmbedder(cfg)
	if err != nil {
		return nil, err
	}
	materials, err := kb.NewService(kbSettings(cfg, logger), documents, parsers, files, embedder)
	if err != nil {
		return nil, fmt.Errorf("initialize the knowledge base service: %w", err)
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
		Materials:         materials,
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

	// The index worker is what embeds documents that are already readable by
	// the full-text path. It is built here and started on the process's own
	// context, so that it stops when the process is asked to stop and not when
	// the last request is.
	worker, err := indexWorker(ctx, cfg, logger, documents, embedder)
	if err != nil {
		return nil, err
	}

	return &ai{
		turns: runner, socket: socket, capabilities: capabilities,
		materials: materials, indexing: worker,
	}, nil
}

// newEmbedder builds the provider this process embeds with.
//
// It is built once, for the two things that embed: a document as it is indexed,
// and a question as it is searched for. Both have to be the same model — the
// base they meet in is the one that recorded which model its vectors came from.
func newEmbedder(cfg config.Config) (embed.Embedder, error) {
	embedder, err := embed.NewClient(embed.Options{
		BaseURL:   cfg.Embedding.BaseURL,
		APIKey:    cfg.Embedding.APIKey,
		Model:     cfg.Embedding.Model,
		Dimension: cfg.Embedding.Dimension,
		Timeout:   cfg.Embedding.Timeout,
		BatchSize: cfg.Embedding.BatchSize,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize the embedding provider: %w", err)
	}
	return embedder, nil
}

// indexWorker builds the embedding queue's worker and starts it, returning the
// channel that closes when it has stopped.
//
// The channel rather than the worker itself, because nothing outside this file
// has anything to say to a worker: it has no state to read and no method to
// call, only a job to do and a way to ask it to stop.
func indexWorker(
	ctx context.Context,
	cfg config.Config,
	logger *slog.Logger,
	documents kb.Repository,
	embedder embed.Embedder,
) (<-chan struct{}, error) {
	worker, err := kb.NewWorker(kb.WorkerOptions{
		Lease:          cfg.KB.VectorLease,
		Workers:        cfg.KB.IndexWorkers,
		EmbedBatchSize: cfg.Embedding.BatchSize,
		Logger:         logger,
	}, documents, embedder)
	if err != nil {
		return nil, fmt.Errorf("initialize the index worker: %w", err)
	}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		worker.Run(ctx)
	}()
	return stopped, nil
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

// close stops the socket, then the turns, then the index worker.
//
// The order matters: a turn that is ending publishes its ending, and a client
// that is still connected is told what it ended as. Closing the runtime first
// would leave whoever is watching a stream that stops without saying why.
//
// The worker goes last and its wait is bounded by ctx, which is the shutdown
// timeout: a document half embedded is one whose claim the lease will hand back
// on the next start, so giving up on it is a delay rather than a loss.
func (a *ai) close(ctx context.Context) error {
	if err := a.socket.Close(ctx); err != nil {
		return fmt.Errorf("close websocket connections: %w", err)
	}
	if err := a.turns.Close(ctx); err != nil {
		return fmt.Errorf("stop running turns: %w", err)
	}
	if a.indexing == nil {
		return nil
	}
	select {
	case <-a.indexing:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("stop the index worker: %w", ctx.Err())
	}
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

// buildCapabilities registers every capability this build offers.
//
// It is a function rather than a block inside assemble so that the list of
// capabilities, and the tuning each is built from, can be checked without a
// database, a socket and a knowledge base service: what a process offers is a
// property of the build, and a test can ask about it on its own.
func buildCapabilities(cfg config.Config) (*capability.Registry, error) {
	registry := capability.NewRegistry()
	for _, register := range []struct {
		name  string
		build func(capability.Settings) (capability.Capability, error)
	}{
		{chat.Name, func(s capability.Settings) (capability.Capability, error) { return chat.New(s) }},
		{askquestions.Name, func(s capability.Settings) (capability.Capability, error) {
			return askquestions.New(s)
		}},
		{deepsolve.Name, func(s capability.Settings) (capability.Capability, error) { return deepsolve.New(s) }},
	} {
		built, err := register.build(capabilitySettings(cfg, register.name))
		if err != nil {
			return nil, fmt.Errorf("initialize the %s capability: %w", register.name, err)
		}
		if err := registry.Register(built); err != nil {
			return nil, fmt.Errorf("register the %s capability: %w", register.name, err)
		}
	}
	return registry, nil
}

// capabilitySettings is one capability's tuning, resolved from configuration.
//
// A capability the deployment has not named runs on the general assistant's
// tuning. The two others are the same model answering the same user in the same
// language with one stage instead of two, so a deployment that never tunes them
// should not have to write the same numbers out again under two more names —
// and a capability being registered should not be a reason for a process that
// started yesterday to refuse to start today. Naming one in
// LLM_CAPABILITY_CONFIG_JSON overrides it, which is also how it is tuned.
//
// The stage limits are the one part that is not shared blindly: they are keyed
// by stage name, and the manifest is what says which names are right.
func capabilitySettings(cfg config.Config, name string) capability.Settings {
	params, ok := cfg.LLM.Capabilities[name]
	if !ok {
		params = cfg.LLM.Capabilities[chat.Name]
	}
	stages := make(map[string]int, len(params.Stages))
	for stage, tuning := range params.Stages {
		stages[stage] = tuning.MaxTokens
	}
	return capability.Settings{
		Model:               cfg.LLM.Model,
		Temperature:         params.Temperature,
		ContextBudgetTokens: params.ContextBudgetTokens,
		MaxRounds:           cfg.Turn.MaxRounds,
		StageMaxTokens:      stages,
	}
}

// kbSettings is the knowledge base domain's tuning, resolved from configuration.
func kbSettings(cfg config.Config, logger *slog.Logger) kb.Options {
	return kb.Options{
		Chunking:            kb.Chunking{Size: cfg.KB.ChunkSize, Overlap: cfg.KB.ChunkOverlap},
		RetrievalTopK:       cfg.KB.RetrievalTopK,
		Candidates:          cfg.KB.RetrievalCandidates,
		RRFK:                cfg.KB.RRFK,
		MaxCharsPerDocument: cfg.KB.MaxCharsPerDocument,
		EmbeddingModel:      cfg.Embedding.Model,
		Logger:              logger,
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
