package data

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"backend/internal/config"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Resource owns the GORM handle and its underlying PostgreSQL connection pool.
type Resource struct {
	gormDB *gorm.DB
	sqlDB  *sql.DB
}

// Open creates a PostgreSQL resource without performing a network ping.
func Open(cfg config.DatabaseConfig) (*Resource, error) {
	dsn := postgresDSN(cfg)
	gormDB, err := gorm.Open(postgres.New(postgres.Config{
		DSN:                  dsn,
		PreferSimpleProtocol: true,
	}), &gorm.Config{
		DisableAutomaticPing: true,
		TranslateError:       true,
		Logger:               gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL database")
	}

	sqlDB, err := gormDB.DB()
	if err != nil {
		return nil, fmt.Errorf("get PostgreSQL connection pool")
	}
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	return &Resource{gormDB: gormDB, sqlDB: sqlDB}, nil
}

// GORM returns the handle for persistence adapters.
func (r *Resource) GORM() *gorm.DB {
	return r.gormDB
}

// SQLDB returns the underlying pool for migrations and health checks.
func (r *Resource) SQLDB() *sql.DB {
	return r.sqlDB
}

// Ping checks database connectivity with the caller's context deadline.
func (r *Resource) Ping(ctx context.Context) error {
	if r == nil || r.sqlDB == nil {
		return fmt.Errorf("database resource is not initialized")
	}
	return r.sqlDB.PingContext(ctx)
}

// Close releases the underlying PostgreSQL connection pool.
func (r *Resource) Close() error {
	if r == nil || r.sqlDB == nil {
		return nil
	}
	return r.sqlDB.Close()
}

func quoteSearchPath(schema string) string {
	return `"` + schema + `"`
}

func postgresDSN(cfg config.DatabaseConfig) string {
	query := url.Values{}
	query.Set("sslmode", cfg.SSLMode)
	query.Set("timezone", cfg.TimeZone)
	query.Set("search_path", quoteSearchPath(cfg.Schema))
	return (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(cfg.User, cfg.Password),
		Host:     cfg.Host + ":" + strconv.Itoa(int(cfg.Port)),
		Path:     "/" + strings.TrimPrefix(cfg.Name, "/"),
		RawQuery: query.Encode(),
	}).String()
}

// Ensure Resource satisfies the readiness and lifecycle contract used by callers.
var _ interface {
	Ping(context.Context) error
	Close() error
} = (*Resource)(nil)
