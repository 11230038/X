package main

// The retention windows, executed.
//
// Two things in this build have a configured window and a service method that
// enforces it — `audit.Service.Prune` (AUDIT_RETENTION_DAYS) and
// `usage.Service.Prune` (USAGE_RETENTION_DAYS) — and neither had a caller. They
// are here rather than on a `time.Ticker` inside the server, and §2.5 gives the
// reason: an in-process timer throws away the answers to two questions an
// operator has to be able to ask, which are "am I running this twice" and "did
// it fail". A subcommand answers both by being something a scheduler runs: cron
// can be told to keep one copy going, and a non-zero exit is what a scheduler
// reports — it is the difference between a cleanup that failed and a deployment
// whose logs nobody reads.
//
// So the shape of this file is deliberate and short: everything worth testing
// is behind the two `Prune` calls, and their cases are in `internal/audit` and
// `internal/usage`, against a real database. What is left here is reading the
// configuration, opening the database, and deciding what the process's exit
// code is — none of which a case in this package could say anything about that
// running the process does not say better.
//
// It deletes rows and nothing else. No window here is a delete of something a
// user can reach: an audit entry past its window is one the audit no longer
// answers with, and a `llm_calls` row past its window is spending that has
// already been reported. The counts are printed because "0" is information —
// it is the difference between a window that is working and one nobody ran.

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"backend/internal/audit"
	"backend/internal/config"
	"backend/internal/data"
	"backend/internal/usage"
)

// commandTimeout bounds the whole run. It is the same five minutes the
// migration command takes, for the same reason: a cleanup that has not finished
// by then is stuck on something a longer wait will not fix, and the scheduler is
// about to run this again anyway.
const commandTimeout = 5 * time.Minute

const usageText = `usage: go run ./cmd/cleanup

Deletes the audit entries and the recorded calls that are older than their
configured retention windows (AUDIT_RETENTION_DAYS, USAGE_RETENTION_DAYS) and
prints how many rows went. Takes no arguments.

Exit status is non-zero if either table could not be pruned.`

func main() {
	// Nothing is configured by argument: the two windows are environment keys
	// like every other deployment number, and a command that could be told a
	// different window on the command line would be a second way to decide how
	// far back an audit reaches.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-h", "--help", "help":
			fmt.Println(usageText)
			return
		default:
			log.Fatalf("unknown argument %q\n\n%s", os.Args[1], usageText)
		}
	}

	cfg, err := config.Load(".env")
	if err != nil {
		log.Fatalf("load configuration: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	// The database is opened the way the server opens it, and the two services
	// are built the way the server builds them — the same constructor with the
	// same configured numbers. A cleanup that decided the window itself would be
	// the second answer to how far back the audit reaches, which is the one thing
	// the window must not have.
	//
	// EnsureDatabase comes first because this is also the command an operator
	// reaches for on a deployment that has never started: creating the database
	// it points at changes nothing about the two tables below, and it keeps this
	// command's first line the same as the migration command's.
	if err := data.EnsureDatabase(ctx, cfg.Database); err != nil {
		log.Fatalf("ensure database exists: %v", err)
	}

	resource, err := data.Open(cfg.Database)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer resource.Close()

	if err := resource.Ping(ctx); err != nil {
		log.Fatalf("database is unavailable: %v", err)
	}

	auditLog, err := audit.NewService(audit.NewGORMStore(resource.GORM()), audit.Options{
		MaxPageSize:   cfg.Audit.MaxPageSize,
		RetentionDays: cfg.Audit.RetentionDays,
	})
	if err != nil {
		log.Fatalf("initialize audit: %v", err)
	}
	calls, err := usage.NewService(usage.NewGORMLedger(resource.GORM()), cfg.Usage.RetentionDays)
	if err != nil {
		log.Fatalf("initialize the usage record: %v", err)
	}

	// One table at a time, and a failure stops the run: the two windows are
	// independent, but an exit code is not — a scheduler that saw zero after the
	// second table failed would be told the deployment is tidy when it is not.
	pruned, err := auditLog.Prune(ctx)
	if err != nil {
		log.Fatalf("prune the audit: %v", err)
	}
	fmt.Printf("audit: %d rows older than %d days deleted\n", pruned, auditLog.RetentionDays())

	pruned, err = calls.Prune(ctx)
	if err != nil {
		log.Fatalf("prune the usage record: %v", err)
	}
	fmt.Printf("usage: %d rows older than %d days deleted\n", pruned, calls.RetentionDays())
}
