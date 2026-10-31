//go:build !desktop

package main

// The ordinary entry point: settings come from backend/.env and the process
// serves the API. A desktop build has its own main — one that carries its
// settings inside the executable — in sources that are not part of this
// repository, so the two entry points are separated by a build tag rather than
// by a branch at run time. Everything they share lives in app.go.

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"backend/internal/config"
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

	if err := run(ctx, cfg, logger, nil); err != nil {
		logger.Error("server stopped with error", "error", err)
		os.Exit(1)
	}
}
