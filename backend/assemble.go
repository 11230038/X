package main

import (
	"context"
	"fmt"
	"log/slog"

	"backend/internal/audit"
	"backend/internal/authz"
	"backend/internal/capability"
	"backend/internal/chat"
	"backend/internal/config"
	"backend/internal/dashboard"
	"backend/internal/kb"
	"backend/internal/keypool"
	"backend/internal/llm"
	"backend/internal/mastery"
	"backend/internal/memory"
	"backend/internal/notebook"
	"backend/internal/parse"
	"backend/internal/practice"
	"backend/internal/routing"
	"backend/internal/session"
	"backend/internal/tool"
	"backend/internal/turn"
	"backend/internal/usage"
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
	// questions is the question bank, and reviews is reviewing what is in it.
	// Both are here because this is where the things they are built from are:
	// the bank is a second read of the same database the runtime writes to, and
	// the judge is the same provider and the same model the turns run on.
	questions *notebook.Service
	reviews   *practice.Service
	// mastery is the same service the turns claim their path from, kept for the
	// reason materials is: reading a path, renaming it and dropping a claim are
	// the same domain the capability works in, reached by a person instead of by
	// a model.
	mastery *mastery.Service
	// dashboard is the read that fans in over the domains above. It is built last
	// because it is the one thing built from the others.
	dashboard *dashboard.Service
	// memory is what the system has been told about an account and what it
	// admitted from it (§2.6). It is built from the database alone, because this
	// version wires no surface to it yet: nothing in a turn observes anything, so
	// what a person reaches through the routes is the whole of what is in here.
	memory *memory.Service
	// immersive is the immersion layer: the same service the turns resolve a
	// selection through, kept here because the routes that open a material, save
	// a position and run an extension are built beside the other handlers.
	immersive *readingLayer
	// authorization is the one place the four layers are combined (§2.1). It is
	// here rather than built beside the router because the routes are not its
	// only consumer: the audit records what it decided, and the governance
	// routes write the rows it reads.
	authorization *authz.Service
	// auditLog is the append-only record of what was done to whom (§2.6). It is
	// built beside the authorization for the same reason: the two are one
	// subject seen twice — the policy answers, and the record says it answered
	// — and the refusal middleware writes to this on every denied request.
	auditLog *audit.Service
	// usage is the reading half of what the recorder writes (§2.7): the same
	// table, reached the other way. It is here rather than beside the handler
	// for the reason the audit is: the recorder and the report are one subject
	// seen twice, and the window the report is retained by is a deployment's.
	usage *usage.Service
}

// assemble builds the turn runtime from configuration.
//
// Everything that can be checked before the first request is checked here: the
// cost of a drift that starts the process anyway is a turn that fails with a
// message about a tool nobody registered, an hour later, in front of a user.
//
// ctx is the process's own context, and it is what the background worker runs
// on: the worker stops when the process is asked to stop, not when a request is.
// assemble builds every domain that needs a database, and it is handed the two
// things that had to exist before it: the audit, because the file library is
// built from it and the file library is built first, and the sessions.
//
// The audit used to be constructed here. It moved out because a domain cannot
// record something through a recorder that does not exist yet, and the file
// library — the one domain built before this function runs, since the knowledge
// base needs it as its file source — is such a domain (§2.14).
func assemble(
	ctx context.Context,
	cfg config.Config,
	logger *slog.Logger,
	db *gorm.DB,
	sessions *session.Service,
	files kb.FileSource,
	auditLog *audit.Service,
) (*ai, error) {
	tools := tool.NewRegistry()
	policy := tool.DefaultPolicy()

	// Every tool here is bound to the runtime, not to a turn: ask_user is
	// always on, the two source tools are mounted by what a turn carries, the
	// four solve tools are mounted by the capability that plans, and
	// save_question is mounted by the capability that writes questions. Each
	// finds the turn it is being called for in the context of the call, so none
	// of them can reach past what that turn was given.
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
		{tool.SaveQuestionName, tool.NewSaveQuestionTool(turn.Banker{})},
		// The nine mastery tools are the same kind of thing as save_question:
		// built once for the process, and answered by the turn they are called
		// for, through the path that turn put in its own context. The path is
		// installed by the runtime from what masteryPath.Claim returned, so a
		// call that arrives without one is a plane that mounted a tool the turn
		// was never given a path for — which the tool reports as the defect it is
		// (tool.ErrNoMasteryPath).
		{tool.MasteryStateName, tool.NewMasteryStateTool()},
		{tool.MasteryQuestionName, tool.NewMasteryQuestionTool()},
		{tool.MasteryGradeName, tool.NewMasteryGradeTool()},
		{tool.MasterySkipName, tool.NewMasterySkipTool()},
		{tool.MasteryAssessName, tool.NewMasteryAssessTool()},
		{tool.MasteryMapName, tool.NewMasteryMapTool()},
		{tool.MasteryEditName, tool.NewMasteryEditTool()},
		{tool.MasterySwitchName, tool.NewMasterySwitchTool()},
		{tool.MasteryLeaveName, tool.NewMasteryLeaveTool()},
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

	// The authorization service is the one place the four layers are combined
	// (§2.1), and the baseline it is built with is where the deployment's two
	// lists meet the floor that is not configurable. Both are checked against
	// this build before anything is served: a capability or surface name that
	// this process has no implementation for has to fail here, where the message
	// can name it, rather than at a learner's first request.
	if err := checkAuthzDrift(cfg.Authz, capabilities); err != nil {
		return nil, err
	}
	authorization, err := authz.NewService(authz.NewGORMStore(db), authz.New(
		authz.NewBaseline(authz.DefaultFloor(), cfg.Authz.LearnerCapabilities, cfg.Authz.LearnerSurfaces),
	))
	if err != nil {
		return nil, fmt.Errorf("initialize authorization: %w", err)
	}

	// The authorization is built here and the audit is not: the audit arrives as
	// a parameter because the file library is built from it and the file library
	// is built before this function runs (see the signature). Both are still built
	// before the domains that write to them, which is the rule that matters — a
	// domain that records something is built from the recorder, and an audit and
	// an authorization that disagreed about who an account is would be an audit of
	// a different instance.
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

	// The client is built here and wrapped here, and nothing downstream ever
	// sees it unwrapped (§2.7). That is the whole of the structural claim: every
	// model call in this process goes through the recorder because there is one
	// provider and it is inside it, rather than because each caller remembered
	// to write a row — a caller that forgets does not fail, it just makes the
	// usage numbers quietly small.
	// The credentials arrive as a pool rather than as a key: one provider with
	// several keys is what keeps a rate-limited credential from slowing every
	// call (§17.3). The pool is built here and handed over as the client's key
	// source, and that handover is also the type check — llm declares what it
	// needs and knows nothing about this type (§1.2).
	keys, err := keypool.New(keypool.Options{
		Keys:             cfg.LLM.APIKeys,
		FailureThreshold: cfg.LLM.KeyFailureThreshold,
		Cooldown:         cfg.LLM.KeyCooldown,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize the key pool: %w", err)
	}
	client, err := llm.NewClient(cfg.LLM.BaseURL, keys, cfg.LLM.Timeout)
	if err != nil {
		return nil, fmt.Errorf("initialize the model client: %w", err)
	}
	// One ledger over this table, read by both halves: the report a client asks
	// for, and the quota it is refused on. They are the same read on purpose
	// (§2.9) — an account close to its limit can compare the two numbers, so
	// the two numbers have to be one answer.
	ledger := usage.NewGORMLedger(db)
	quota, err := usage.NewQuota(ledger, usage.Limits{
		DailyCalls:    int64(cfg.Usage.DailyCallsPerAccount),
		MonthlyTokens: int64(cfg.Usage.MonthlyTokensPerAccount),
	})
	if err != nil {
		return nil, fmt.Errorf("initialize the usage quota: %w", err)
	}
	provider, err := usage.New(
		client,
		usage.NewGORMStore(db),
		usage.Options{Provider: cfg.LLM.Provider, Quota: quota, Logger: logger},
	)
	if err != nil {
		return nil, fmt.Errorf("initialize the usage record: %w", err)
	}
	// The reading half is built over the same table and its own type, and it is
	// built here rather than where the handler is: the window it is retained by
	// is checked at startup like every other configured number, so a deployment
	// that wrote a window nobody could serve is refused before the first
	// request rather than at the first report.
	usageReport, err := usage.NewService(ledger, cfg.Usage.RetentionDays)
	if err != nil {
		return nil, fmt.Errorf("initialize the usage report: %w", err)
	}
	// The question bank and the review path share this database with everything
	// else, and each gets its own service over it: a question is written by the
	// turns that generate it, read by the bank's endpoints, and advanced by a
	// review somebody submits.
	questions, err := notebook.NewService(notebook.NewGORMRepository(db))
	if err != nil {
		return nil, fmt.Errorf("initialize the question bank: %w", err)
	}
	// The judge runs on the conversation model rather than the task one, and its
	// output budget is the one key that is only about judging: judging is a
	// fixed question with a written answer, not a role, so it is not a
	// capability and has no entry in the capability configuration.
	judge, err := practice.NewJudge(provider, cfg.LLM.Model, cfg.Practice.JudgeMaxTokens)
	if err != nil {
		return nil, fmt.Errorf("initialize the answer judge: %w", err)
	}
	reviews, err := practice.NewService(practice.NewGORMRepository(db), judge)
	if err != nil {
		return nil, fmt.Errorf("initialize the review service: %w", err)
	}
	// The two are joined here rather than imported into each other, and the
	// direction is why: the review path reads the bank to judge an answer, so the
	// bank reading the review tables back would be a cycle. What the bank says it
	// needs is one method (notebook.DueReader), and this is the line that says
	// these two are the same question.
	questions.WithDueReader(reviews)

	titler, err := turn.NewTitler(provider, cfg.LLM.TaskModel, cfg.LLM.TaskMaxTokens)
	if err != nil {
		return nil, fmt.Errorf("initialize session naming: %w", err)
	}
	// The mastery domain reads the same database and the same question bank the
	// rest of the process does, and it judges with the same judge: a question
	// filed on a path is a question in the bank, and judging an answer to it is
	// the act practice already performs (§2.5).
	//
	// paths is the join the runtime claims a turn's path through, and the service
	// inside it goes into ai as well: one domain, reached by a turn and by a
	// person.
	paths, err := newMasteryPaths(cfg, db, questions, judge)
	if err != nil {
		return nil, err
	}
	// The reading layer reads the same knowledge base the turns read through, so
	// a passage a turn is handed and a passage a reader opens are read by the
	// same code, at the same offsets, out of the same revision (§2.8). It is
	// named for the capability rather than for the package, because the package
	// is in scope here and one name for two things is how a shadowed import gets
	// written by accident.
	immersive, err := newReadingLayer(cfg, db, materials, provider)
	if err != nil {
		return nil, err
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
		Questions:         questions,
		Router:            questionRouting(),
		Paths:             paths,
		Readings:          immersive.selection,
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

	// Memory is built from the database and the deployment's six numbers (§5) and
	// nothing else. It is built here, beside the other domains, even though this
	// version joins none of them to it: the routes that read a person's memory
	// are built beside every other route, over the same service.
	memories, err := newMemory(cfg, db)
	if err != nil {
		return nil, err
	}

	// The dashboard reads three of the domains above and is read by nothing, so
	// it is built last and from what is already assembled. Its one number is the
	// deployment's (§5): how many entries a category carries.
	overview, err := dashboard.NewService(dashboard.Options{
		RecentLimit: cfg.Dashboard.RecentLimit,
		Sessions:    sessions,
		Paths:       paths.service,
		Reviews:     reviews,
	})
	if err != nil {
		return nil, err
	}

	return &ai{
		turns: runner, socket: socket, capabilities: capabilities,
		materials: materials, indexing: worker,
		questions: questions, reviews: reviews, mastery: paths.service,
		dashboard: overview, immersive: immersive, memory: memories,
		authorization: authorization, auditLog: auditLog,
		usage: usageReport,
	}, nil
}

// questionRouting is the join between the rule table and the runtime.
//
// The two ends are kept apart on purpose — the table is a leaf that knows the
// language of the product's users and nothing about turns, and the runtime knows
// what runs a turn and nothing about how a sentence is read — so somebody has to
// say that these two are the same decision, and this is that place. It is the
// same seam as the source and question tools: what a turn calls is declared in
// the turn package, implemented elsewhere, and joined here.
func questionRouting() turn.RouterFunc {
	return func(content string) (turn.Routing, bool) {
		decision, matched := routing.Route(content)
		if !matched {
			return turn.Routing{}, false
		}
		return turn.Routing{
			Capability: decision.Capability,
			By:         decision.By,
			Reason:     decision.Reason,
			Confidence: decision.Confidence,
		}, true
	}
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
