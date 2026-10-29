package main

import (
	"context"
	"fmt"
	"log/slog"

	"backend/internal/audit"
	"backend/internal/authz"
	"backend/internal/book"
	"backend/internal/capability"
	"backend/internal/chat"
	"backend/internal/config"
	"backend/internal/dashboard"
	"backend/internal/directchat"
	"backend/internal/kb"
	"backend/internal/keypool"
	"backend/internal/llm"
	"backend/internal/mastery"
	"backend/internal/memory"
	"backend/internal/notebook"
	"backend/internal/outbound"
	"backend/internal/parse"
	"backend/internal/persona"
	"backend/internal/practice"
	"backend/internal/presence"
	"backend/internal/session"
	"backend/internal/tool"
	"backend/internal/turn"
	"backend/internal/usage"
	"backend/internal/video"
	"backend/internal/workspace"
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
	// personas is the saved prompt store, reached two ways: the runtime resolves
	// one turn's persona through it, and the routes beside the composer read and
	// write the rows. It is the same domain in both cases, which is why it is
	// built here rather than twice.
	personas *persona.Service
	// dashboard is the read that fans in over the domains above. It is built last
	// because it is the one thing built from the others.
	dashboard *dashboard.Service
	// presence is the attendance record behind the home page's two numbers: how
	// long today has been spent here and how many days in a row. It is built
	// from the database alone and joined to nothing — a minute being lived is
	// not a fact about any of the domains above.
	presence *presence.Service
	// memory is what the system has been told about an account and what it
	// admitted from it (§2.6). It is built from the database alone, because this
	// version wires no surface to it yet: nothing in a turn observes anything, so
	// what a person reaches through the routes is the whole of what is in here.
	memory *memory.Service
	// immersive is the immersion layer: the same service the turns resolve a
	// selection through, kept here because the routes that open a material, save
	// a position and run an extension are built beside the other handlers.
	immersive *readingLayer
	// books is generated books: the shelf, the outline and the chapter bodies.
	// It reads four domains — conversations, collections, parsed documents and
	// questions — through one loader built beside it, and calls the same provider
	// the turns do.
	books *book.Service
	// videoSearch is the public video site's search. It has no store and no
	// background work; the player belongs to the other site, so what is kept here
	// is the client the search route calls.
	videoSearch *video.Client
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
	// packages is the export and import of a selection of an account's work
	// (§2.13). It is here for the reason materials is: it is a service over the
	// domains above, reached by a person through the routes rather than by a
	// turn.
	packages *workspace.Service
	// directChat is the one-shot model call: one question in, one answer out,
	// with no session or history. It is built here because it runs on the
	// general assistant's tuning and the shared provider, which are both decided
	// in this function.
	directChat *directchat.Service
	// tools is the tool plane itself, kept so the capability catalogue can say
	// what each tool does: a manifest names its tools, and the sentence the
	// model reads about one belongs to the tool, not to the declaration.
	tools *tool.Registry
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
	sessionStore session.Repository,
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

	// The document engine is a service beside this deployment and is normally
	// reached at the loopback address the default names, so its policy is the
	// LAN one — the widening §2.6 makes for exactly this client.
	engineDial, err := outbound.DialContext(outbound.LANServicePolicy{})
	if err != nil {
		return nil, fmt.Errorf("initialize the document engine's boundary: %w", err)
	}
	parsers, err := parse.NewRegistry(parse.Options{
		DefaultEngine:  cfg.Parser.DefaultEngine,
		DoclingBaseURL: cfg.Parser.DoclingBaseURL,
		Timeout:        cfg.Parser.Timeout,
		Dial:           engineDial,
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
	// The model provider is somewhere on the internet, so the policy is the one
	// for an address that came from outside (§2.6). The address itself came from
	// configuration rather than from a request — what this closes is the door a
	// mistyped LLM_BASE_URL walks through, not a door a user can open.
	modelDial, err := outbound.DialContext(outbound.ExternalMediaPolicy{})
	if err != nil {
		return nil, fmt.Errorf("initialize the model client's boundary: %w", err)
	}
	client, err := llm.NewClient(cfg.LLM.BaseURL, keys, cfg.LLM.Timeout, modelDial)
	if err != nil {
		return nil, fmt.Errorf("initialize the model client: %w", err)
	}
	// Video learning asks a different public service, so it has its own client,
	// its own cache and its own dial boundary — the same external policy as the
	// model provider, because the address is another site's rather than this
	// deployment's. It is built here for the reason every outbound client is:
	// where the boundary is chosen is one file, not each call site (§2.6).
	videoDial, err := outbound.DialContext(outbound.ExternalMediaPolicy{})
	if err != nil {
		return nil, fmt.Errorf("initialize the video search boundary: %w", err)
	}
	videoSearch, err := video.NewClient(video.Options{
		BaseURL:  cfg.Video.BaseURL,
		Timeout:  cfg.Video.Timeout,
		CacheTTL: cfg.Video.CacheTTL,
		Dial:     videoDial,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize the video search client: %w", err)
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
	// A direct question is not a turn, so it takes no router, no tools and no
	// repository — only the model and the tuning the general assistant already
	// runs with. Resolving that tuning through capabilitySettings rather than
	// reading the environment a second way is what keeps the two from drifting.
	general, err := capabilitySettings(cfg, chat.Manifest())
	if err != nil {
		return nil, err
	}
	direct, err := directchat.New(directchat.Options{
		Provider:    provider,
		Model:       general.Model,
		Temperature: general.Temperature,
		MaxTokens:   general.StageMaxTokens[chat.StageResponding],
	})
	if err != nil {
		return nil, fmt.Errorf("initialize direct chat: %w", err)
	}

	// The question bank and the review path share this database with everything
	// else, and each gets its own service over it: a question is written by the
	// turns that generate it, read by the bank's endpoints, and advanced by a
	// review somebody submits.
	questionStore := notebook.NewGORMRepository(db)
	questions, err := notebook.NewService(questionStore)
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
	// A persona is one account's prompt, read by its own turns and by nothing
	// else. The service is built from the same database as the rest and handed to
	// the runtime as the narrow interface it implements, which is what keeps the
	// runtime from learning where a prompt is stored.
	// A book is generated from a source snapshotted when it is created, so the
	// loader reads the four domains once and never again: what the outline and
	// every chapter are written from is the copy in the book's own row.
	books, err := book.NewService(book.Options{
		Repository: book.NewGORMRepository(db),
		Sources:    newBookSources(sessions, materials, questions),
		Provider:   provider,
		Model:      cfg.LLM.Model,
		Logger:     logger,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize books: %w", err)
	}
	personas, err := persona.NewService(persona.NewGORMRepository(db))
	if err != nil {
		return nil, fmt.Errorf("initialize personas: %w", err)
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
		Personas:          personas,
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

	// Presence is the database and the clock, and nothing above: a minute spent
	// in the app is not a conclusion drawn from any domain, it is the thing
	// itself. It is built here because this is where the database is in hand.
	presenceService, err := presence.NewService(presence.NewRepository(db), presence.Options{})
	if err != nil {
		return nil, fmt.Errorf("initialize presence: %w", err)
	}

	// The package service reads four of the domains above and is read by
	// nothing. It is the one thing here built from the reading layer rather than
	// from the services under it, because a material is the reading layer's
	// subject and asking the knowledge base for one would be a second answer to
	// what a material is (§2.8).
	packages, err := newWorkspace(cfg, db, sessions, materials, questions, immersive, auditLog,
		workspaceStores{
			sessions: sessionStore, materials: immersive.repository,
			questions: questionStore, documents: documents,
		})
	if err != nil {
		return nil, err
	}

	return &ai{
		turns: runner, socket: socket, capabilities: capabilities, tools: tools,
		materials: materials, indexing: worker,
		questions: questions, reviews: reviews, mastery: paths.service,
		personas:  personas,
		dashboard: overview, presence: presenceService,
		immersive: immersive, books: books, videoSearch: videoSearch,
		memory:        memories,
		authorization: authorization, auditLog: auditLog,
		usage: usageReport, packages: packages, directChat: direct,
	}, nil
}
