// Package config reads the service's settings from its dotenv file. Each section
// of settings lives in the file named after it — http.go, upload.go, auth.go,
// database.go, llm.go, embedding.go, parse.go, kb.go, turn.go, practice.go,
// mastery.go — holding that section's struct, the loader that fills it, and the
// Validate that guards it. This file is the composition: what the whole
// configuration is, how the file is read into it, and the order the sections are
// checked in.
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

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
	"QUESTION_DEFAULT_COUNT",
	"QUESTION_MAX_COUNT",
	"PRACTICE_QUEUE_SIZE",
	"PRACTICE_JUDGE_MAX_TOKENS",
	"MASTERY_LEASE_TTL",
	"MASTERY_GATE_CONSECUTIVE",
	"MASTERY_GATE_MIN_EVIDENCE",
	"MASTERY_MAX_ACTIVE_PATHS",
	"DASHBOARD_RECENT_LIMIT",
	"READING_UNIT_MAX_CHARS",
	"READING_TUTOR_MAX_TOKENS",
	"READING_EXTENSION_TIMEOUT",
	"MEMORY_CONFIDENCE_THRESHOLD",
	"MEMORY_MIN_CORROBORATION",
	"MEMORY_MIN_SURFACES",
	"MEMORY_SENSITIVE_KINDS",
	"MEMORY_SYNTHESIS_WINDOW_DAYS",
	"MEMORY_MAX_FACTS_PER_SURFACE",
	"AUTHZ_DEFAULT_PRESET",
	"AUTHZ_LEARNER_CAPABILITIES",
	"AUTHZ_LEARNER_SURFACES",
	"AUDIT_RETENTION_DAYS",
	"AUDIT_MAX_PAGE_SIZE",
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
	Practice        PracticeConfig
	Mastery         MasteryConfig
	Dashboard       DashboardConfig
	Reading         ReadingConfig
	Memory          MemoryConfig
	Authz           AuthzConfig
	Audit           AuditConfig
}

// Load reads configuration exclusively from the dotenv file at path. The path
// reaches the upload section as well, because a relative UPLOAD_ROOT is resolved
// against the file it was written in rather than the process's working directory.
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
	practice, err := loadPracticeConfig(values)
	if err != nil {
		return Config{}, err
	}
	mastery, err := loadMasteryConfig(values)
	if err != nil {
		return Config{}, err
	}
	dashboard, err := loadDashboardConfig(values)
	if err != nil {
		return Config{}, err
	}
	reading, err := loadReadingConfig(values)
	if err != nil {
		return Config{}, err
	}
	memory, err := loadMemoryConfig(values)
	if err != nil {
		return Config{}, err
	}
	authorization, err := loadAuthzConfig(values)
	if err != nil {
		return Config{}, err
	}
	audit, err := loadAuditConfig(values)
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
		Practice:        practice,
		Mastery:         mastery,
		Dashboard:       dashboard,
		Reading:         reading,
		Memory:          memory,
		Authz:           authorization,
		Audit:           audit,
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate checks values that would otherwise cause a late startup failure. The
// order is the order a failure would otherwise be met in, and every section's
// check belongs to that section.
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
	if err := c.Turn.Validate(); err != nil {
		return err
	}
	if err := c.Practice.Validate(); err != nil {
		return err
	}
	if err := c.Mastery.Validate(); err != nil {
		return err
	}
	if err := c.Dashboard.Validate(); err != nil {
		return err
	}
	if err := c.Reading.Validate(); err != nil {
		return err
	}
	if err := c.Memory.Validate(); err != nil {
		return err
	}
	if err := c.Authz.Validate(); err != nil {
		return err
	}
	return c.Audit.Validate()
}
