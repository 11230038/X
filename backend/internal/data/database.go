package data

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strconv"
	"time"

	"backend/internal/config"
	mysqldriver "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// sessionTimeZone pins the seekdb session time zone. CURRENT_TIMESTAMP is
// evaluated in the session zone, and a seekdb container defaults that to the host
// offset (+08:00 here) while the application treats every DATETIME as UTC.
// Without this, every creat_time/update_time would be stored hours off.
const sessionTimeZone = "'+00:00'"

// Resource owns the GORM handle and its underlying seekdb connection pool.
type Resource struct {
	gormDB *gorm.DB
	sqlDB  *sql.DB
}

// Open creates a seekdb resource without performing a network ping.
func Open(cfg config.DatabaseConfig) (*Resource, error) {
	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{
		DSN: mysqlDSN(cfg),
		// Opening must not touch the network, and the server-version probe is
		// only used to tune the GORM migrator, which this project never calls:
		// the schema comes from the versioned SQL files.
		SkipInitializeWithVersion: true,
	}), &gorm.Config{
		DisableAutomaticPing: true,
		TranslateError:       true,
		Logger:               gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("open seekdb database: %w", err)
	}

	sqlDB, err := gormDB.DB()
	if err != nil {
		return nil, fmt.Errorf("get seekdb connection pool: %w", err)
	}
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	return &Resource{gormDB: gormDB, sqlDB: sqlDB}, nil
}

// EnsureDatabase creates the configured database when it is missing. The
// connection used to run migrations cannot create the database it points at, so
// this opens a separate connection that selects no database at all.
func EnsureDatabase(ctx context.Context, cfg config.DatabaseConfig) error {
	driverCfg := mysqlConfig(cfg)
	driverCfg.DBName = ""

	connector, err := mysqldriver.NewConnector(driverCfg)
	if err != nil {
		return fmt.Errorf("build seekdb server connection: %w", err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()

	// cfg.Name is already restricted to a lowercase identifier by config.Validate.
	query := fmt.Sprintf(
		"CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET %s COLLATE %s",
		cfg.Name, cfg.Charset, cfg.Collation,
	)
	if _, err := db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("create database %q: %w", cfg.Name, err)
	}
	return nil
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

// Close releases the underlying seekdb connection pool.
func (r *Resource) Close() error {
	if r == nil || r.sqlDB == nil {
		return nil
	}
	return r.sqlDB.Close()
}

func mysqlConfig(cfg config.DatabaseConfig) *mysqldriver.Config {
	driverCfg := mysqldriver.NewConfig()
	driverCfg.User = cfg.User
	driverCfg.Passwd = cfg.Password
	driverCfg.Net = "tcp"
	driverCfg.Addr = net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port)))
	driverCfg.DBName = cfg.Name
	// DATETIME columns are scanned into time.Time, which requires parseTime.
	driverCfg.ParseTime = true
	driverCfg.Loc = time.UTC
	driverCfg.Collation = cfg.Collation
	driverCfg.Params = map[string]string{
		"charset":   cfg.Charset,
		"time_zone": sessionTimeZone,
	}
	if cfg.TLS {
		driverCfg.TLSConfig = "true"
	}
	return driverCfg
}

func mysqlDSN(cfg config.DatabaseConfig) string {
	return mysqlConfig(cfg).FormatDSN()
}

// Ensure Resource satisfies the readiness and lifecycle contract used by callers.
var _ interface {
	Ping(context.Context) error
	Close() error
} = (*Resource)(nil)
