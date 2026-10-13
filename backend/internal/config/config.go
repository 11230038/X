package config

import (
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const multipartHeadroomBytes int64 = 64 * 1024

// databaseIdentifier matches the database names this service will accept. seekdb
// runs with lower_case_table_names=1, so a mixed-case name would be folded anyway.
var databaseIdentifier = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// charsetIdentifier matches the character-set and collation names accepted in the
// connection string, keeping arbitrary text out of the DSN.
var charsetIdentifier = regexp.MustCompile(`^[a-z0-9_]+$`)

// utcZone is the only session time zone this service accepts.
const utcZone = "UTC"

var requiredKeys = []string{
	"HTTP_ADDR",
	"APP_ENV",
	"SHUTDOWN_TIMEOUT",
	"HTTP_READ_HEADER_TIMEOUT",
	"HTTP_READ_TIMEOUT",
	"HTTP_WRITE_TIMEOUT",
	"HTTP_IDLE_TIMEOUT",
	"UPLOAD_ROOT",
	"UPLOAD_MAX_FILE_BYTES",
	"UPLOAD_MAX_REQUEST_BYTES",
	"AUTH_JWT_SECRET",
	"AUTH_JWT_ISSUER",
	"AUTH_TOKEN_TTL",
	"DB_HOST",
	"DB_PORT",
	"DB_NAME",
	"DB_USER",
	"DB_PASSWORD",
	"DB_CHARSET",
	"DB_COLLATION",
	"DB_TLS",
	"DB_TIMEZONE",
	"DB_PING_TIMEOUT",
	"DB_MAX_OPEN_CONNS",
	"DB_MAX_IDLE_CONNS",
	"DB_CONN_MAX_LIFETIME",
	"DB_CONN_MAX_IDLE_TIME",
	"LLM_PROVIDER",
	"LLM_BASE_URL",
	"LLM_MODEL",
	"LLM_API_KEY",
	"LLM_TASK_MODEL",
	"LLM_TASK_MAX_TOKENS",
	"LLM_TIMEOUT",
	"LLM_CAPABILITY_CONFIG_JSON",
	"TURN_INPUT_TIMEOUT",
	"TURN_MAX_ROUNDS",
	"TURN_MAX_ATTACHMENT_BYTES",
	"EMBEDDING_BASE_URL",
	"EMBEDDING_MODEL",
	"EMBEDDING_API_KEY",
	"EMBEDDING_DIMENSION",
	"EMBEDDING_TIMEOUT",
	"EMBEDDING_BATCH_SIZE",
	"PARSER_DEFAULT_ENGINE",
	"PARSER_DOCLING_BASE_URL",
	"PARSER_TIMEOUT",
	"KB_CHUNK_SIZE",
	"KB_CHUNK_OVERLAP",
	"KB_RETRIEVAL_TOP_K",
	"KB_RETRIEVAL_CANDIDATES",
	"KB_RRF_K",
	"KB_MAX_CHARS_PER_DOCUMENT",
	"KB_MAX_CHARS_PER_TURN",
	"KB_VECTOR_LEASE",
	"KB_INDEX_WORKERS",
}

// Config contains all settings required to run the service.
type Config struct {
	HTTPAddr        string
	AppEnv          string
	ShutdownTimeout time.Duration
	HTTPTimeouts    HTTPTimeoutConfig
	Upload          UploadConfig
	Auth            AuthConfig
	Database        DatabaseConfig
	LLM             LLMConfig
	Embedding       EmbeddingConfig
	Parser          ParserConfig
	KB              KBConfig
	Turn            TurnConfig
}

// AuthConfig contains access-token signing settings.
type AuthConfig struct {
	Secret string
	Issuer string
	TTL    time.Duration
}

// HTTPTimeoutConfig contains network deadlines for the HTTP server.
type HTTPTimeoutConfig struct {
	ReadHeader time.Duration
	Read       time.Duration
	Write      time.Duration
	Idle       time.Duration
}

// UploadConfig contains local file-library storage limits and location.
type UploadConfig struct {
	Root            string
	MaxFileBytes    int64
	MaxRequestBytes int64
}

// DatabaseConfig contains seekdb connection and pool settings. seekdb speaks the
// MySQL protocol, so the field set follows MySQL rather than PostgreSQL: there is
// no separate schema namespace (the database is the schema) and TLS is either on
// or off rather than a libpq sslmode ladder.
type DatabaseConfig struct {
	Host            string
	Port            uint16
	Name            string
	User            string
	Password        string
	Charset         string
	Collation       string
	TLS             bool
	TimeZone        string
	PingTimeout     time.Duration
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// Load reads configuration exclusively from the dotenv file at path.
func Load(path string) (Config, error) {
	values, err := godotenv.Read(path)
	if err != nil {
		return Config{}, fmt.Errorf("read configuration file %q", path)
	}
	if err := requireValues(values); err != nil {
		return Config{}, err
	}

	shutdownTimeout, err := parsePositiveDuration(values, "SHUTDOWN_TIMEOUT")
	if err != nil {
		return Config{}, err
	}
	httpTimeouts, err := loadHTTPTimeouts(values)
	if err != nil {
		return Config{}, err
	}
	upload, err := loadUploadConfig(path, values)
	if err != nil {
		return Config{}, err
	}
	authConfig, err := loadAuthConfig(values)
	if err != nil {
		return Config{}, err
	}
	database, err := loadDatabaseConfig(values)
	if err != nil {
		return Config{}, err
	}
	llm, err := loadLLMConfig(values)
	if err != nil {
		return Config{}, err
	}
	embedding, err := loadEmbeddingConfig(values)
	if err != nil {
		return Config{}, err
	}
	parser, err := loadParserConfig(values)
	if err != nil {
		return Config{}, err
	}
	kb, err := loadKBConfig(values)
	if err != nil {
		return Config{}, err
	}
	turn, err := loadTurnConfig(values)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		HTTPAddr:        strings.TrimSpace(values["HTTP_ADDR"]),
		AppEnv:          strings.TrimSpace(values["APP_ENV"]),
		ShutdownTimeout: shutdownTimeout,
		HTTPTimeouts:    httpTimeouts,
		Upload:          upload,
		Auth:            authConfig,
		Database:        database,
		LLM:             llm,
		Embedding:       embedding,
		Parser:          parser,
		KB:              kb,
		Turn:            turn,
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate checks values that would otherwise cause a late startup failure.
func (c Config) Validate() error {
	if err := validateHTTPAddr(c.HTTPAddr); err != nil {
		return fmt.Errorf("invalid HTTP_ADDR: %w", err)
	}
	if strings.TrimSpace(c.AppEnv) == "" {
		return fmt.Errorf("APP_ENV must not be empty")
	}
	if c.ShutdownTimeout <= 0 {
		return fmt.Errorf("SHUTDOWN_TIMEOUT must be a positive duration")
	}
	if err := c.HTTPTimeouts.Validate(); err != nil {
		return err
	}
	if err := c.Upload.Validate(); err != nil {
		return err
	}
	if err := c.Auth.Validate(); err != nil {
		return err
	}
	if err := c.Database.Validate(); err != nil {
		return err
	}
	if err := c.LLM.Validate(); err != nil {
		return err
	}
	if err := c.Embedding.Validate(); err != nil {
		return err
	}
	if err := c.Parser.Validate(); err != nil {
		return err
	}
	if err := c.KB.Validate(); err != nil {
		return err
	}
	return c.Turn.Validate()
}

// Validate checks HTTP server timeout settings.
func (c HTTPTimeoutConfig) Validate() error {
	for key, value := range map[string]time.Duration{
		"HTTP_READ_HEADER_TIMEOUT": c.ReadHeader,
		"HTTP_READ_TIMEOUT":        c.Read,
		"HTTP_WRITE_TIMEOUT":       c.Write,
		"HTTP_IDLE_TIMEOUT":        c.Idle,
	} {
		if value <= 0 {
			return fmt.Errorf("%s must be a positive duration", key)
		}
	}
	return nil
}

// Validate checks upload storage and request limits.
func (c UploadConfig) Validate() error {
	if strings.TrimSpace(c.Root) == "" {
		return fmt.Errorf("UPLOAD_ROOT must not be empty")
	}
	if c.MaxFileBytes <= 0 {
		return fmt.Errorf("UPLOAD_MAX_FILE_BYTES must be a positive integer")
	}
	if c.MaxRequestBytes <= 0 {
		return fmt.Errorf("UPLOAD_MAX_REQUEST_BYTES must be a positive integer")
	}
	if c.MaxRequestBytes < multipartHeadroomBytes || c.MaxFileBytes > c.MaxRequestBytes-multipartHeadroomBytes {
		return fmt.Errorf("UPLOAD_MAX_REQUEST_BYTES must exceed UPLOAD_MAX_FILE_BYTES by at least %d bytes", multipartHeadroomBytes)
	}
	return nil
}

// Validate checks the access-token configuration. The secret is only checked for
// presence here; its minimum strength is enforced when the signer is built, so
// the requirement lives in one place. Failures never echo the secret value.
func (c AuthConfig) Validate() error {
	for key, value := range map[string]string{
		"AUTH_JWT_SECRET": c.Secret,
		"AUTH_JWT_ISSUER": c.Issuer,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s must not be empty", key)
		}
	}
	if c.TTL <= 0 {
		return fmt.Errorf("AUTH_TOKEN_TTL must be a positive duration")
	}
	return nil
}

// Validate checks seekdb connection and pool settings.
func (c DatabaseConfig) Validate() error {
	if strings.TrimSpace(c.Host) == "" {
		return fmt.Errorf("DB_HOST must not be empty")
	}
	if c.Port == 0 {
		return fmt.Errorf("DB_PORT must be between 1 and 65535")
	}
	for key, value := range map[string]string{
		"DB_NAME":      c.Name,
		"DB_USER":      c.User,
		"DB_CHARSET":   c.Charset,
		"DB_COLLATION": c.Collation,
		"DB_TIMEZONE":  c.TimeZone,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s must not be empty", key)
		}
	}
	if !databaseIdentifier.MatchString(c.Name) {
		return fmt.Errorf("DB_NAME must be a lowercase identifier")
	}
	if !charsetIdentifier.MatchString(c.Charset) {
		return fmt.Errorf("DB_CHARSET must be a character-set name")
	}
	if !charsetIdentifier.MatchString(c.Collation) {
		return fmt.Errorf("DB_COLLATION must be a collation name")
	}
	if c.TimeZone != utcZone {
		return fmt.Errorf("DB_TIMEZONE must be %s", utcZone)
	}
	if c.PingTimeout <= 0 {
		return fmt.Errorf("DB_PING_TIMEOUT must be a positive duration")
	}
	if c.MaxOpenConns <= 0 {
		return fmt.Errorf("DB_MAX_OPEN_CONNS must be positive")
	}
	if c.MaxIdleConns < 0 {
		return fmt.Errorf("DB_MAX_IDLE_CONNS must not be negative")
	}
	if c.MaxIdleConns > c.MaxOpenConns {
		return fmt.Errorf("DB_MAX_IDLE_CONNS must not exceed DB_MAX_OPEN_CONNS")
	}
	if c.ConnMaxLifetime < 0 {
		return fmt.Errorf("DB_CONN_MAX_LIFETIME must not be negative")
	}
	if c.ConnMaxIdleTime < 0 {
		return fmt.Errorf("DB_CONN_MAX_IDLE_TIME must not be negative")
	}
	return nil
}

func loadHTTPTimeouts(values map[string]string) (HTTPTimeoutConfig, error) {
	readHeader, err := parsePositiveDuration(values, "HTTP_READ_HEADER_TIMEOUT")
	if err != nil {
		return HTTPTimeoutConfig{}, err
	}
	read, err := parsePositiveDuration(values, "HTTP_READ_TIMEOUT")
	if err != nil {
		return HTTPTimeoutConfig{}, err
	}
	write, err := parsePositiveDuration(values, "HTTP_WRITE_TIMEOUT")
	if err != nil {
		return HTTPTimeoutConfig{}, err
	}
	idle, err := parsePositiveDuration(values, "HTTP_IDLE_TIMEOUT")
	if err != nil {
		return HTTPTimeoutConfig{}, err
	}
	return HTTPTimeoutConfig{ReadHeader: readHeader, Read: read, Write: write, Idle: idle}, nil
}

func loadUploadConfig(path string, values map[string]string) (UploadConfig, error) {
	root := strings.TrimSpace(values["UPLOAD_ROOT"])
	if !filepath.IsAbs(root) {
		absoluteEnv, err := filepath.Abs(path)
		if err != nil {
			return UploadConfig{}, fmt.Errorf("resolve configuration file path")
		}
		root = filepath.Join(filepath.Dir(absoluteEnv), root)
	}
	root, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return UploadConfig{}, fmt.Errorf("resolve UPLOAD_ROOT")
	}
	maxFile, err := parsePositiveInt64(values, "UPLOAD_MAX_FILE_BYTES")
	if err != nil {
		return UploadConfig{}, err
	}
	maxRequest, err := parsePositiveInt64(values, "UPLOAD_MAX_REQUEST_BYTES")
	if err != nil {
		return UploadConfig{}, err
	}
	return UploadConfig{Root: root, MaxFileBytes: maxFile, MaxRequestBytes: maxRequest}, nil
}

func loadAuthConfig(values map[string]string) (AuthConfig, error) {
	ttl, err := parsePositiveDuration(values, "AUTH_TOKEN_TTL")
	if err != nil {
		return AuthConfig{}, err
	}
	// The secret is used verbatim: trimming it would silently change the key.
	return AuthConfig{
		Secret: values["AUTH_JWT_SECRET"],
		Issuer: strings.TrimSpace(values["AUTH_JWT_ISSUER"]),
		TTL:    ttl,
	}, nil
}

func loadDatabaseConfig(values map[string]string) (DatabaseConfig, error) {
	port, err := parsePort(values, "DB_PORT")
	if err != nil {
		return DatabaseConfig{}, err
	}
	pingTimeout, err := parsePositiveDuration(values, "DB_PING_TIMEOUT")
	if err != nil {
		return DatabaseConfig{}, err
	}
	maxOpen, err := parseNonNegativeInt(values, "DB_MAX_OPEN_CONNS")
	if err != nil {
		return DatabaseConfig{}, err
	}
	maxIdle, err := parseNonNegativeInt(values, "DB_MAX_IDLE_CONNS")
	if err != nil {
		return DatabaseConfig{}, err
	}
	maxLifetime, err := parseNonNegativeDuration(values, "DB_CONN_MAX_LIFETIME")
	if err != nil {
		return DatabaseConfig{}, err
	}
	maxIdleTime, err := parseNonNegativeDuration(values, "DB_CONN_MAX_IDLE_TIME")
	if err != nil {
		return DatabaseConfig{}, err
	}
	tls, err := parseBool(values, "DB_TLS")
	if err != nil {
		return DatabaseConfig{}, err
	}
	return DatabaseConfig{
		Host: strings.TrimSpace(values["DB_HOST"]), Port: port,
		Name: strings.TrimSpace(values["DB_NAME"]), User: strings.TrimSpace(values["DB_USER"]),
		Password: values["DB_PASSWORD"], Charset: strings.TrimSpace(values["DB_CHARSET"]),
		Collation: strings.TrimSpace(values["DB_COLLATION"]), TLS: tls,
		TimeZone:    strings.TrimSpace(values["DB_TIMEZONE"]),
		PingTimeout: pingTimeout, MaxOpenConns: maxOpen, MaxIdleConns: maxIdle,
		ConnMaxLifetime: maxLifetime, ConnMaxIdleTime: maxIdleTime,
	}, nil
}

func parseBool(values map[string]string, key string) (bool, error) {
	value := strings.TrimSpace(values[key])
	if value != "true" && value != "false" {
		return false, fmt.Errorf("%s must be true or false", key)
	}
	return value == "true", nil
}

func requireValues(values map[string]string) error {
	for _, key := range requiredKeys {
		value, ok := values[key]
		if !ok || strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required in the configuration file", key)
		}
	}
	return nil
}

func parsePort(values map[string]string, key string) (uint16, error) {
	value, err := strconv.ParseUint(strings.TrimSpace(values[key]), 10, 16)
	if err != nil || value == 0 {
		return 0, fmt.Errorf("%s must be between 1 and 65535", key)
	}
	return uint16(value), nil
}

func parsePositiveInt64(values map[string]string, key string) (int64, error) {
	value, err := strconv.ParseInt(strings.TrimSpace(values[key]), 10, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return value, nil
}

func parseNonNegativeInt(values map[string]string, key string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(values[key]))
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", key)
	}
	return value, nil
}

func parsePositiveInt(values map[string]string, key string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(values[key]))
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return value, nil
}

func parsePositiveDuration(values map[string]string, key string) (time.Duration, error) {
	value, err := time.ParseDuration(strings.TrimSpace(values[key]))
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return value, nil
}

func parseNonNegativeDuration(values map[string]string, key string) (time.Duration, error) {
	value, err := time.ParseDuration(strings.TrimSpace(values[key]))
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s must be a non-negative duration", key)
	}
	return value, nil
}

func validateHTTPAddr(addr string) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return fmt.Errorf("address must not be empty")
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("must be host:port: %w", err)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	return nil
}
