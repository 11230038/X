package migration

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"regexp"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
)

//go:embed sql/*.sql
var migrationFiles embed.FS

const (
	latestVersion    int64 = 5
	versionTableName       = "goose_db_version"
)

var postgresIdentifier = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// Provider creates a migration provider over the embedded SQL files.
func Provider(db *sql.DB, schema string) (*goose.Provider, error) {
	files, err := fs.Sub(migrationFiles, "sql")
	if err != nil {
		return nil, fmt.Errorf("load embedded migrations")
	}
	if !postgresIdentifier.MatchString(schema) {
		return nil, fmt.Errorf("invalid migration schema")
	}
	store, err := database.NewStore(
		database.DialectPostgres,
		schema+"."+versionTableName,
	)
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
func Up(ctx context.Context, db *sql.DB, schema string) error {
	if err := ensureSchema(ctx, db, schema); err != nil {
		return err
	}
	provider, err := Provider(db, schema)
	if err != nil {
		return err
	}
	_, err = provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

// Down rolls back one migration.
func Down(ctx context.Context, db *sql.DB, schema string) error {
	provider, err := Provider(db, schema)
	if err != nil {
		return err
	}
	if _, err := provider.Down(ctx); err != nil {
		return fmt.Errorf("roll back migration: %w", err)
	}
	return nil
}

// Status returns the current migration status.
func Status(ctx context.Context, db *sql.DB, schema string) ([]*goose.MigrationStatus, error) {
	provider, err := Provider(db, schema)
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
func CheckLatest(ctx context.Context, db *sql.DB, schema string) error {
	if !postgresIdentifier.MatchString(schema) {
		return fmt.Errorf("invalid migration schema")
	}

	var tableExists bool
	err := db.QueryRowContext(
		ctx,
		`SELECT to_regclass($1) IS NOT NULL`,
		schema+"."+versionTableName,
	).Scan(&tableExists)
	if err != nil {
		return fmt.Errorf("check migration version table")
	}
	if !tableExists {
		return fmt.Errorf("migration version table does not exist")
	}

	query := fmt.Sprintf(
		`SELECT COALESCE(MAX(version_id), 0) FROM %s.%s WHERE is_applied = TRUE`,
		quoteIdentifier(schema),
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

func ensureSchema(ctx context.Context, db *sql.DB, schema string) error {
	if !postgresIdentifier.MatchString(schema) {
		return fmt.Errorf("invalid migration schema")
	}
	query := fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, quoteIdentifier(schema))
	if _, err := db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("ensure migration schema: %w", err)
	}
	return nil
}

func quoteIdentifier(value string) string {
	return `"` + value + `"`
}

// LatestVersion returns the schema version embedded in this binary.
func LatestVersion() int64 {
	return latestVersion
}
