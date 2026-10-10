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

	"backend/internal/config"
	"backend/internal/data"
	"backend/internal/data/migration"
	"backend/internal/handler"
	"backend/internal/library"
	"backend/internal/server"
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
		return fmt.Errorf("connect to PostgreSQL")
	}
	cancelPing()
	if err := migration.CheckLatest(ctx, database.SQLDB(), cfg.Database.Schema); err != nil {
		return fmt.Errorf("database schema is not current; run migration command")
	}

	system := handler.NewSystemHandler(database, cfg.Database.PingTimeout, logger)
	libraryRepository := library.NewPostgresRepository(database.GORM())
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
	router := server.NewRouter(logger, cfg.AppEnv, server.Handlers{
		System:       system,
		LibraryFiles: libraryFiles,
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
