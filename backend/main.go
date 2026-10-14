package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"backend/internal/audit"
	"backend/internal/auth"
	"backend/internal/config"
	"backend/internal/data"
	"backend/internal/data/migration"
	"backend/internal/handler"
	"backend/internal/library"
	"backend/internal/limit"
	"backend/internal/server"
	"backend/internal/session"
)

func main() {
	cfg, err := config.Load(".env")
	if err != nil {
		logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	logger := newLogger(cfg.AppEnv)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg, logger); err != nil {
		logger.Error("server stopped with error", "error", err)
		os.Exit(1)
	}
}

func newLogger(appEnv string) *slog.Logger {
	options := &slog.HandlerOptions{Level: slog.LevelInfo}
	if appEnv == "development" {
		return slog.New(slog.NewTextHandler(os.Stderr, options))
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, options))
}

func run(ctx context.Context, cfg config.Config, logger *slog.Logger) (runErr error) {
	database, err := data.Open(cfg.Database)
	if err != nil {
		return err
	}
	defer func() {
		if err := database.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close database: %w", err))
		}
	}()

	pingCtx, cancelPing := context.WithTimeout(ctx, cfg.Database.PingTimeout)
	if err := database.Ping(pingCtx); err != nil {
		cancelPing()
		return fmt.Errorf("connect to seekdb")
	}
	cancelPing()
	if err := migration.CheckLatest(ctx, database.SQLDB()); err != nil {
		return fmt.Errorf("database schema is not current; run migration command")
	}

	system := handler.NewSystemHandler(database, cfg.Database.PingTimeout, logger)
	// The audit is built here rather than inside assemble, and the ordering is the
	// reason: the file library is built from it (§2.14), and the file library is
	// built before assemble runs because the knowledge base takes it as its file
	// source. Nothing else about the audit is main's business — assemble is handed
	// it and passes it to the domains that record.
	auditLog, err := audit.NewService(audit.NewGORMStore(database.GORM()), audit.Options{
		MaxPageSize:   cfg.Audit.MaxPageSize,
		RetentionDays: cfg.Audit.RetentionDays,
	})
	if err != nil {
		return fmt.Errorf("initialize audit: %w", err)
	}
	libraryRepository := library.NewGORMRepository(database.GORM())
	libraryService, err := library.NewService(library.Options{
		Root:         cfg.Upload.Root,
		MaxFileBytes: cfg.Upload.MaxFileBytes,
		Auditor:      auditLog,
	}, libraryRepository)
	if err != nil {
		return fmt.Errorf("initialize file library: %w", err)
	}
	libraryFiles := handler.NewLibraryFilesHandler(
		libraryService,
		cfg.Upload.MaxRequestBytes,
		logger,
	)
	authRepository := auth.NewGORMRepository(database.GORM())
	authService, err := auth.NewService(auth.Options{
		Secret: cfg.Auth.Secret,
		Issuer: cfg.Auth.Issuer,
		TTL:    cfg.Auth.TTL,
	}, authRepository)
	if err != nil {
		return fmt.Errorf("initialize authentication: %w", err)
	}
	sessionService, err := session.NewService(session.NewGORMRepository(database.GORM()))
	if err != nil {
		return fmt.Errorf("initialize sessions: %w", err)
	}
	// The file source is the one place that knows both the library and the
	// knowledge base domain, so neither has to know the other: the knowledge
	// base reads a file by id through this, and never learns where bytes live.
	ai, err := assemble(ctx, cfg, logger, database.GORM(), sessionService,
		fileSource{library: libraryService}, auditLog)
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := ai.close(closeCtx); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}()
	// The operations routes read the process rather than a domain: what this
	// build is, and what the process running it is doing. They are built here
	// rather than inside assemble because three of their four sources — the
	// environment, the schema this binary requires, the database the readiness
	// probe already pings — are the process's, and only the fourth is the
	// runtime's.
	ops, err := handler.NewOpsHandler(handler.OpsConfig{
		Env:              cfg.AppEnv,
		SchemaVersion:    migration.LatestVersion(),
		Readiness:        database,
		ReadinessTimeout: cfg.Database.PingTimeout,
		Runtime:          ai,
	}, logger)
	if err != nil {
		return err
	}
	// The limiter is built here rather than inside assemble, because it is not a
	// domain service: it has no store behind it and nothing to close, and it is
	// one process's buckets — which is exactly what §2.10 says a rate limit is,
	// and why a restart starting over is correct rather than a gap.
	limiter, err := limit.New(limit.Options{
		ModelPerMinute:     cfg.Limit.ModelPerMinute,
		RetrievalPerMinute: cfg.Limit.RetrievalPerMinute,
		UploadPerMinute:    cfg.Limit.UploadPerMinute,
		Burst:              cfg.Limit.Burst,
	})
	if err != nil {
		return fmt.Errorf("initialize the rate limiter: %w", err)
	}
	router := server.NewRouter(logger, cfg.AppEnv, server.Handlers{
		System:       system,
		Ops:          ops,
		LibraryFiles: libraryFiles,
		LibraryReads: handler.NewLibraryReadHandler(libraryService, ai.materials, logger),
		// One service answering both halves of the library: the bytes it hands over
		// for a download are the same bytes it hands the parsers, and the listing
		// and the recycle bin are the same table read two ways (§2.14).
		LibraryLifecycle: handler.NewLibraryLifecycleHandler(libraryService, libraryService, logger),
		KnowledgeBase:    handler.NewKBHandler(ai.materials, logger),
		Auth:             handler.NewAuthHandler(authService, logger),
		Sessions:         handler.NewSessionHandler(sessionService, logger),
		Questions:        handler.NewQuestionHandler(ai.questions, logger),
		Practice:         handler.NewPracticeHandler(ai.reviews, cfg.Practice.QueueSize, logger),
		Mastery:          handler.NewMasteryHandler(ai.mastery, logger),
		Reading:          handler.NewReadingHandler(ai.immersive.service, ai.immersive.extensions, logger),
		Dashboard:        handler.NewDashboardHandler(ai.dashboard, logger),
		Memory:           handler.NewMemoryHandler(ai.memory, logger),
		Capabilities:     handler.NewCapabilityHandler(ai.capabilities),
		Permissions:      handler.NewPermissionsHandler(ai.authorization, logger),
		// Governance and the guardian's own view are the same service asked two
		// questions: the administrator reads every account and every
		// relationship, and a guardian reads the ones it holds. Both are mounted
		// behind the surface that decides who may ask at all.
		Admin:     handler.NewAdminHandler(ai.authorization, ai.authorization, logger),
		Guardians: handler.NewGuardianHandler(ai.authorization, logger),
		// The audit is its own surface's read: it is where the decisions the
		// other routes make are read back, and it is mounted behind the
		// administrative surface rather than beside them.
		Audit: handler.NewAuditHandler(ai.auditLog, logger),
		// A guardian's report is the learner's own dashboard read by somebody
		// else, so it is the dashboard service with the audit beside it: the
		// answer is the one the learner would get, and the entry is what says
		// somebody else looked at it.
		Reports: handler.NewReportsHandler(ai.dashboard, ai.auditLog, logger),
		// The usage report reads the same table the recorder writes, through a
		// different type: the recorder cannot read a window and the ledger
		// cannot write a call, which is what keeps "how much have I spent" from
		// being a question the writing path is able to answer for anybody.
		Usage:         handler.NewUsageHandler(ai.usage, logger),
		WebSocket:     ai.socket,
		TokenVerifier: authService,
		Authorizer:    ai.authorization,
		// Every refusal is written down from here rather than from a handler,
		// because the middleware is the one place a refusal is produced: a
		// record kept per handler is a record omitted per handler, and an
		// omitted record does not fail — it makes the audit say nothing
		// happened.
		Refusals: refusalRecorder{audit: ai.auditLog},
		Limiter:  limiter,
	})
	httpServer := server.NewHTTPServer(cfg, router)

	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.HTTPAddr, err)
	}

	logger.Info("server started", "addr", cfg.HTTPAddr, "env", cfg.AppEnv)
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- httpServer.Serve(listener)
	}()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()

		logger.Info("shutting down server", "reason", ctx.Err())
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			if closeErr := httpServer.Close(); closeErr != nil {
				return fmt.Errorf("shutdown HTTP server: %w", errors.Join(err, closeErr))
			}
			return fmt.Errorf("shutdown HTTP server: %w", err)
		}
		return nil
	}
}
