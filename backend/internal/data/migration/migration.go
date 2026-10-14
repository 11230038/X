package migration

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
)

//go:embed sql/*.sql
var migrationFiles embed.FS

const (
	latestVersion    int64 = 13
	versionTableName       = "goose_db_version"
)

// Provider creates a migration provider over the embedded SQL files. seekdb
// speaks the MySQL protocol and has no schema namespace separate from the
// database, so the version table is unqualified and the connection's database
// is the one that gets migrated.
func Provider(db *sql.DB) (*goose.Provider, error) {
	files, err := fs.Sub(migrationFiles, "sql")
	if err != nil {
		return nil, fmt.Errorf("load embedded migrations")
	}
	store, err := database.NewStore(database.DialectMySQL, versionTableName)
	if err != nil {
		return nil, fmt.Errorf("create migration store")
	}
	provider, err := goose.NewProvider(
		"",
		db,
		files,
		goose.WithStore(store),
		goose.WithLogger(goose.NopLogger()),
	)
	if err != nil {
		return nil, fmt.Errorf("create migration provider")
	}
	return provider, nil
}

// Up applies all pending migrations.
func Up(ctx context.Context, db *sql.DB) error {
	provider, err := Provider(db)
	if err != nil {
		return err
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

// Down rolls back one migration.
func Down(ctx context.Context, db *sql.DB) error {
	provider, err := Provider(db)
	if err != nil {
		return err
	}
	if _, err := provider.Down(ctx); err != nil {
		return fmt.Errorf("roll back migration: %w", err)
	}
	return nil
}

// Status returns the current migration status.
func Status(ctx context.Context, db *sql.DB) ([]*goose.MigrationStatus, error) {
	provider, err := Provider(db)
	if err != nil {
		return nil, err
	}
	statuses, err := provider.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("read migration status: %w", err)
	}
	return statuses, nil
}

// CheckLatest performs a read-only check of the migration version table.
func CheckLatest(ctx context.Context, db *sql.DB) error {
	var found string
	// seekdb has no to_regclass equivalent; ask information_schema for the table
	// in whichever database the connection is using.
	err := db.QueryRowContext(
		ctx,
		`SELECT table_name FROM information_schema.tables
		 WHERE table_schema = DATABASE() AND table_name = ?`,
		versionTableName,
	).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("migration version table does not exist")
	}
	if err != nil {
		return fmt.Errorf("check migration version table")
	}

	query := fmt.Sprintf(
		`SELECT COALESCE(MAX(version_id), 0) FROM %s WHERE is_applied = TRUE`,
		quoteIdentifier(versionTableName),
	)
	var current int64
	if err := db.QueryRowContext(ctx, query).Scan(&current); err != nil {
		return fmt.Errorf("read migration version")
	}
	if current != latestVersion {
		return fmt.Errorf("database migration is not current")
	}
	return nil
}

func quoteIdentifier(value string) string {
	return "`" + value + "`"
}

// LatestVersion returns the schema version embedded in this binary.
func LatestVersion() int64 {
	return latestVersion
}
