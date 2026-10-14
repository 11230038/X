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

	"backend/internal/auth"
	"backend/internal/config"
	"backend/internal/data"
	"backend/internal/data/migration"
	"backend/internal/handler"
	"backend/internal/library"
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
	libraryRepository := library.NewGORMRepository(database.GORM())
	libraryService, err := library.NewService(library.Options{
		Root:         cfg.Upload.Root,
		MaxFileBytes: cfg.Upload.MaxFileBytes,
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
		fileSource{library: libraryService})
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
	router := server.NewRouter(logger, cfg.AppEnv, server.Handlers{
		System:        system,
		LibraryFiles:  libraryFiles,
		LibraryReads:  handler.NewLibraryReadHandler(libraryService, ai.materials, logger),
		KnowledgeBase: handler.NewKBHandler(ai.materials, logger),
		Auth:          handler.NewAuthHandler(authService, logger),
		Sessions:      handler.NewSessionHandler(sessionService, logger),
		Questions:     handler.NewQuestionHandler(ai.questions, logger),
		Practice:      handler.NewPracticeHandler(ai.reviews, cfg.Practice.QueueSize, logger),
		Mastery:       handler.NewMasteryHandler(ai.mastery, logger),
		Reading:       handler.NewReadingHandler(ai.immersive.service, ai.immersive.extensions, logger),
		Dashboard:     handler.NewDashboardHandler(ai.dashboard, logger),
		Memory:        handler.NewMemoryHandler(ai.memory, logger),
		Capabilities:  handler.NewCapabilityHandler(ai.capabilities),
		Permissions:   handler.NewPermissionsHandler(ai.authorization, logger),
		// Governance and the guardian's own view are the same service asked two
		// questions: the administrator reads every account and every
		// relationship, and a guardian reads the ones it holds. Both are mounted
		// behind the surface that decides who may ask at all.
		Admin:         handler.NewAdminHandler(ai.authorization, ai.authorization, logger),
		Guardians:     handler.NewGuardianHandler(ai.authorization, logger),
		WebSocket:     ai.socket,
		TokenVerifier: authService,
		Authorizer:    ai.authorization,
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
