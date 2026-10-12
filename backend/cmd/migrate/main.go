package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"backend/internal/config"
	"backend/internal/data"
	"backend/internal/data/migration"
)

const commandTimeout = 5 * time.Minute

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: go run ./cmd/migrate <up|down|status>")
	}

	cfg, err := config.Load(".env")
	if err != nil {
		log.Fatal("load configuration")
	}

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	// The connection that runs migrations cannot create the database it points
	// at, so create it first over a connection that selects no database.
	if err := data.EnsureDatabase(ctx, cfg.Database); err != nil {
		log.Fatal("ensure database exists")
	}

	resource, err := data.Open(cfg.Database)
	if err != nil {
		log.Fatal("open database")
	}
	defer resource.Close()

	if err := resource.Ping(ctx); err != nil {
		log.Fatal("database is unavailable")
	}

	switch os.Args[1] {
	case "up":
		err = migration.Up(ctx, resource.SQLDB())
	case "down":
		err = migration.Down(ctx, resource.SQLDB())
	case "status":
		err = printStatus(ctx, resource.SQLDB())
	default:
		log.Fatalf("unknown migration command %q", os.Args[1])
	}
	if err != nil {
		log.Fatal(err)
	}
}

func printStatus(ctx context.Context, db *sql.DB) error {
	statuses, err := migration.Status(ctx, db)
	if err != nil {
		return err
	}
	for _, status := range statuses {
		fmt.Printf("%-8s %05d %s\n", status.State, status.Source.Version, filepath.Base(status.Source.Path))
	}
	return nil
}
