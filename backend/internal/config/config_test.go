package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	testPassword        = "unit-test-password"
	testJWTSecret       = "unit-test-jwt-secret-with-enough-bytes"
	testJWTIssuer       = "x-backend-test"
	testTokenTTLKey     = "AUTH_TOKEN_TTL"
	testLLMAPIKey       = "unit-test-llm-api-key"
	testEmbeddingKey    = "EMBEDDING_API_KEY"
	testEmbeddingModel  = "qwen3.7-text-embedding-flash"
	testEmbeddingAPIKey = "unit-test-embedding-api-key"
)

func TestLoadReadsCompleteDotenv(t *testing.T) {
	path := writeDotenv(t, validValues())

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.HTTPAddr != "127.0.0.1:9090" {
		t.Errorf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.AppEnv != "test" {
		t.Errorf("AppEnv = %q", cfg.AppEnv)
	}
	if cfg.ShutdownTimeout != 2*time.Second {
		t.Errorf("ShutdownTimeout = %s", cfg.ShutdownTimeout)
	}
	if cfg.HTTPTimeouts.ReadHeader != 5*time.Second || cfg.HTTPTimeouts.Read != 2*time.Minute {
		t.Errorf("HTTP timeouts were not loaded: %+v", cfg.HTTPTimeouts)
	}
	if cfg.HTTPTimeouts.Write != 2*time.Minute || cfg.HTTPTimeouts.Idle != time.Minute {
		t.Errorf("HTTP timeouts were not loaded: %+v", cfg.HTTPTimeouts)
	}
	if cfg.Upload.MaxFileBytes != 20*1024*1024 || cfg.Upload.MaxRequestBytes != 21*1024*1024 {
		t.Errorf("upload limits were not loaded: %+v", cfg.Upload)
	}
	wantRoot := filepath.Join(filepath.Dir(path), "data", "upload")
	if cfg.Upload.Root != wantRoot {
		t.Errorf("Upload.Root = %q, want %q", cfg.Upload.Root, wantRoot)
	}
	if cfg.Database.Port != 2881 || cfg.Database.Password != testPassword {
		t.Errorf("Database configuration was not loaded")
	}
	if cfg.Database.Charset != "utf8mb4" || cfg.Database.Collation != "utf8mb4_bin" {
		t.Errorf("Database character set was not loaded: %+v", cfg.Database)
	}
	if cfg.Database.TLS {
		t.Error("Database.TLS = true, want false")
	}
	if cfg.Database.TimeZone != "UTC" {
		t.Errorf("Database.TimeZone = %q, want UTC", cfg.Database.TimeZone)
	}
	if cfg.Database.MaxOpenConns != 10 || cfg.Database.MaxIdleConns != 5 {
		t.Errorf("connection pool = %d/%d", cfg.Database.MaxOpenConns, cfg.Database.MaxIdleConns)
	}
	if cfg.Auth.Secret != testJWTSecret || cfg.Auth.Issuer != testJWTIssuer {
		t.Errorf("auth configuration was not loaded")
	}
	if cfg.Auth.TTL != 24*time.Hour {
		t.Errorf("Auth.TTL = %s, want 24h", cfg.Auth.TTL)
	}
	if cfg.LLM.Provider != "deepseek" || cfg.LLM.Model != "deepseek-chat" {
		t.Errorf("LLM provider = %q model = %q", cfg.LLM.Provider, cfg.LLM.Model)
	}
	if cfg.LLM.BaseURL != "https://api.deepseek.com" {
		t.Errorf("LLM.BaseURL = %q", cfg.LLM.BaseURL)
	}
	if cfg.LLM.TaskModel != "deepseek-chat" || cfg.LLM.TaskMaxTokens != 512 {
		t.Errorf("task model = %q max tokens = %d", cfg.LLM.TaskModel, cfg.LLM.TaskMaxTokens)
	}
	if cfg.LLM.Timeout != 2*time.Minute {
		t.Errorf("LLM.Timeout = %s, want 2m", cfg.LLM.Timeout)
	}
	if cfg.Turn.InputTimeout != 30*time.Minute || cfg.Turn.MaxRounds != 12 {
		t.Errorf("turn bounds were not loaded: %+v", cfg.Turn)
	}
	if cfg.Turn.MaxAttachmentBytes != 26214400 {
		t.Errorf("Turn.MaxAttachmentBytes = %d, want 26214400", cfg.Turn.MaxAttachmentBytes)
	}
	if cfg.Embedding.BaseURL != "https://dashscope.aliyuncs.com/compatible-mode/v1" {
		t.Errorf("Embedding.BaseURL = %q", cfg.Embedding.BaseURL)
	}
	if cfg.Embedding.Model != testEmbeddingModel || cfg.Embedding.Dimension != 1024 {
		t.Errorf("embedding model = %q dimension = %d", cfg.Embedding.Model, cfg.Embedding.Dimension)
	}
	if cfg.Embedding.Timeout != 30*time.Second || cfg.Embedding.BatchSize != 16 {
		t.Errorf("embedding request shape was not loaded: %+v", cfg.Embedding)
	}
	if cfg.Parser.DefaultEngine != ParserEngineText || cfg.Parser.Timeout != 2*time.Minute {
		t.Errorf("parser settings were not loaded: %+v", cfg.Parser)
	}
	if cfg.KB.ChunkSize != 512 || cfg.KB.ChunkOverlap != 50 {
		t.Errorf("chunking settings were not loaded: %+v", cfg.KB)
	}
	if cfg.KB.RetrievalTopK != 5 || cfg.KB.RetrievalCandidates != 20 || cfg.KB.RRFK != 60 {
		t.Errorf("retrieval settings were not loaded: %+v", cfg.KB)
	}
	if cfg.KB.MaxCharsPerDocument != 200000 || cfg.KB.MaxCharsPerTurn != 150000 {
		t.Errorf("text budgets were not loaded: %+v", cfg.KB)
	}
	if cfg.Audit.RetentionDays != 180 || cfg.Audit.MaxPageSize != 200 {
		t.Errorf("audit settings were not loaded: %+v", cfg.Audit)
	}
	if cfg.KB.VectorLease != 5*time.Minute || cfg.KB.IndexWorkers != 2 {
		t.Errorf("index worker settings were not loaded: %+v", cfg.KB)
	}
	chat, ok := cfg.LLM.Capabilities["chat"]
	if !ok {
		t.Fatalf("capability parameters were not loaded: %v", cfg.LLM.Capabilities)
	}
	if chat.Temperature != 0.7 {
		t.Errorf("chat temperature = %v, want 0.7", chat.Temperature)
	}
	if chat.ContextBudgetTokens != 32000 {
		t.Errorf("chat context budget = %d, want 32000", chat.ContextBudgetTokens)
	}
	if chat.Stages["responding"].MaxTokens != 4096 || chat.Stages["exploring"].MaxTokens != 1024 {
		t.Errorf("chat stage limits were not loaded: %+v", chat.Stages)
	}
}

func TestLoadNeverEchoesTheLLMAPIKey(t *testing.T) {
	for _, tt := range []struct {
		name  string
		key   string
		value string
	}{
		{name: "empty key", key: "LLM_API_KEY", value: " "},
		{name: "unsupported provider", key: "LLM_PROVIDER", value: "openai"},
		{name: "invalid base URL", key: "LLM_BASE_URL", value: "api.deepseek.com"},
		{name: "invalid capability config", key: "LLM_CAPABILITY_CONFIG_JSON", value: "not json"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			values := validValues()
			values[tt.key] = tt.value

			_, err := Load(writeDotenv(t, values))
			if err == nil {
				t.Fatalf("Load() error = nil, want an error for %s", tt.key)
			}
			if strings.Contains(err.Error(), testLLMAPIKey) {
				t.Fatal("configuration error exposed the LLM API key")
			}
		})
	}
}

func TestLoadRejectsIncompleteAuthConfiguration(t *testing.T) {
	for _, tt := range []struct {
		name   string
		key    string
		remove bool
	}{
		{name: "missing secret", key: "AUTH_JWT_SECRET", remove: true},
		{name: "empty issuer", key: "AUTH_JWT_ISSUER"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			values := validValues()
			if tt.remove {
				delete(values, tt.key)
			} else {
				values[tt.key] = " "
			}

			_, err := Load(writeDotenv(t, values))
			if err == nil || !strings.Contains(err.Error(), tt.key) {
				t.Fatalf("Load() error = %v, want error naming %s", err, tt.key)
			}
		})
	}
}

func TestLoadRejectsInvalidTokenTTL(t *testing.T) {
	for _, value := range []string{"0s", "-1h", "soon"} {
		values := validValues()
		values[testTokenTTLKey] = value

		if _, err := Load(writeDotenv(t, values)); err == nil {
			t.Fatalf("Load() error = nil for %s=%s", testTokenTTLKey, value)
		}
	}
}

func TestLoadNeverEchoesTheJWTSecret(t *testing.T) {
	values := validValues()
	values[testTokenTTLKey] = "not-a-duration"

	_, err := Load(writeDotenv(t, values))
	if err == nil {
		t.Fatal("Load() error = nil, want token TTL error")
	}
	if strings.Contains(err.Error(), testJWTSecret) {
		t.Fatal("configuration error exposed the JWT secret")
	}
}

func TestLoadRequiresDotenvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if _, err := Load(path); err == nil {
		t.Fatal("Load() error = nil, want missing file error")
	}
}

func TestLoadRejectsMissingAndEmptyValues(t *testing.T) {
	for _, tt := range []struct {
		name   string
		key    string
		remove bool
	}{
		{name: "missing database password", key: "DB_PASSWORD", remove: true},
		{name: "empty database name", key: "DB_NAME"},
		{name: "empty HTTP address", key: "HTTP_ADDR"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			values := validValues()
			if tt.remove {
				delete(values, tt.key)
			} else {
				values[tt.key] = " "
			}

			_, err := Load(writeDotenv(t, values))
			if err == nil || !strings.Contains(err.Error(), tt.key) {
				t.Fatalf("Load() error = %v, want error naming %s", err, tt.key)
			}
		})
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
	}{
		{name: "HTTP address", key: "HTTP_ADDR", value: "localhost"},
		{name: "shutdown timeout", key: "SHUTDOWN_TIMEOUT", value: "0s"},
		{name: "HTTP read timeout", key: "HTTP_READ_TIMEOUT", value: "0s"},
		{name: "HTTP write timeout", key: "HTTP_WRITE_TIMEOUT", value: "soon"},
		{name: "upload file limit", key: "UPLOAD_MAX_FILE_BYTES", value: "0"},
		{name: "upload request limit", key: "UPLOAD_MAX_REQUEST_BYTES", value: "-1"},
		{name: "database port", key: "DB_PORT", value: "70000"},
		{name: "uppercase database name", key: "DB_NAME", value: "AppData"},
		{name: "charset with a space", key: "DB_CHARSET", value: "utf8 mb4"},
		{name: "collation with a dash", key: "DB_COLLATION", value: "utf8mb4-bin"},
		{name: "TLS flag", key: "DB_TLS", value: "yes"},
		{name: "non-UTC time zone", key: "DB_TIMEZONE", value: "Asia/Shanghai"},
		{name: "ping timeout", key: "DB_PING_TIMEOUT", value: "soon"},
		{name: "max open", key: "DB_MAX_OPEN_CONNS", value: "0"},
		{name: "max idle", key: "DB_MAX_IDLE_CONNS", value: "-1"},
		{name: "connection lifetime", key: "DB_CONN_MAX_LIFETIME", value: "-1s"},
		{name: "unsupported LLM provider", key: "LLM_PROVIDER", value: "openai"},
		{name: "relative LLM base URL", key: "LLM_BASE_URL", value: "api.deepseek.com"},
		{name: "LLM base URL without a host", key: "LLM_BASE_URL", value: "https://"},
		{name: "non-HTTP LLM scheme", key: "LLM_BASE_URL", value: "ftp://api.deepseek.com"},
		{name: "LLM timeout", key: "LLM_TIMEOUT", value: "soon"},
		{name: "task max tokens", key: "LLM_TASK_MAX_TOKENS", value: "0"},
		{name: "capability config as an array", key: "LLM_CAPABILITY_CONFIG_JSON", value: "[]"},
		{name: "capability config with an unknown field", key: "LLM_CAPABILITY_CONFIG_JSON",
			value: `{"chat":{"temperatur":0.7,"stages":{"responding":{"max_tokens":1}}}}`},
		{name: "capability temperature above the range", key: "LLM_CAPABILITY_CONFIG_JSON",
			value: `{"chat":{"temperature":3,"context_budget_tokens":32000,"stages":{"responding":{"max_tokens":1}}}}`},
		{name: "capability temperature below the range", key: "LLM_CAPABILITY_CONFIG_JSON",
			value: `{"chat":{"temperature":-1,"context_budget_tokens":32000,"stages":{"responding":{"max_tokens":1}}}}`},
		{name: "capability stage without max tokens", key: "LLM_CAPABILITY_CONFIG_JSON",
			value: `{"chat":{"temperature":0.7,"context_budget_tokens":32000,"stages":{"responding":{}}}}`},
		{name: "capability without stages", key: "LLM_CAPABILITY_CONFIG_JSON",
			value: `{"chat":{"temperature":0.7}}`},
		{name: "capability without a context budget", key: "LLM_CAPABILITY_CONFIG_JSON",
			value: `{"chat":{"temperature":0.7,"stages":{"responding":{"max_tokens":1}}}}`},
		{name: "capability with a zero context budget", key: "LLM_CAPABILITY_CONFIG_JSON",
			value: `{"chat":{"temperature":0.7,"context_budget_tokens":0,"stages":{"responding":{"max_tokens":1}}}}`},
		{name: "turn input timeout", key: "TURN_INPUT_TIMEOUT", value: "0s"},
		{name: "turn rounds", key: "TURN_MAX_ROUNDS", value: "0"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			values := validValues()
			values[tt.key] = tt.value
			if _, err := Load(writeDotenv(t, values)); err == nil {
				t.Fatalf("Load() error = nil, want error for %s", tt.key)
			}
		})
	}
}

func TestLoadRejectsRequestLimitWithoutMultipartHeadroom(t *testing.T) {
	values := validValues()
	values["UPLOAD_MAX_REQUEST_BYTES"] = values["UPLOAD_MAX_FILE_BYTES"]

	if _, err := Load(writeDotenv(t, values)); err == nil {
		t.Fatal("Load() error = nil, want upload limit validation error")
	}
}

func TestLoadRejectsOverflowingUploadLimits(t *testing.T) {
	values := validValues()
	values["UPLOAD_MAX_FILE_BYTES"] = "9223372036854775807"
	values["UPLOAD_MAX_REQUEST_BYTES"] = "1"

	if _, err := Load(writeDotenv(t, values)); err == nil {
		t.Fatal("Load() error = nil, want upload limit overflow rejection")
	}
}

func TestLoadResolvesUploadRootFromDotenvDirectory(t *testing.T) {
	values := validValues()
	values["UPLOAD_ROOT"] = "../runtime/upload"
	path := writeDotenv(t, values)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want, err := filepath.Abs(filepath.Join(filepath.Dir(path), "..", "runtime", "upload"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Upload.Root != want {
		t.Fatalf("Upload.Root = %q, want %q", cfg.Upload.Root, want)
	}
}

// Every required key must appear in the template. A key added here and not
// there stops the first deployment that copies the template, and the message it
// stops with names a key the operator has no way to look up.
//
// Only this direction is checked. The template may hold keys that are not
// required — VISION_* ships ahead of the code that reads it, by decision — so
// the reverse would fail on purpose.
func TestEnvExampleListsEveryRequiredKey(t *testing.T) {
	path := filepath.Join("..", "..", ".env.example")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	listed := make(map[string]bool)
	for _, line := range strings.Split(string(content), "\n") {
		if key, _, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			listed[strings.TrimSpace(key)] = true
		}
	}
	for _, key := range requiredKeys {
		if !listed[key] {
			t.Errorf("%s is required but missing from .env.example", key)
		}
	}
}

func TestLoadRejectsIdleConnectionsAboveOpenConnections(t *testing.T) {
	values := validValues()
	values["DB_MAX_OPEN_CONNS"] = "2"
	values["DB_MAX_IDLE_CONNS"] = "3"

	if _, err := Load(writeDotenv(t, values)); err == nil {
		t.Fatal("Load() error = nil, want pool validation error")
	}
}

func TestLoadIgnoresProcessEnvironment(t *testing.T) {
	values := validValues()
	values["DB_HOST"] = "file-host"
	path := writeDotenv(t, values)
	t.Setenv("DB_HOST", "environment-host")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Database.Host != "file-host" {
		t.Fatalf("Database.Host = %q, want file-host", cfg.Database.Host)
	}

	delete(values, "DB_NAME")
	t.Setenv("DB_NAME", "environment-database")
	if _, err := Load(writeDotenv(t, values)); err == nil {
		t.Fatal("Load() used process environment to fill a missing file value")
	}
}

func TestLoadDoesNotExposeSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := "DB_PASSWORD=" + testPassword + "\nINVALID LINE WITH " + testPassword
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want dotenv parse error")
	}
	if strings.Contains(err.Error(), testPassword) {
		t.Fatal("configuration error exposed the database password")
	}
}

func validValues() map[string]string {
	return map[string]string{
		"HTTP_ADDR":                "127.0.0.1:9090",
		"APP_ENV":                  "test",
		"SHUTDOWN_TIMEOUT":         "2s",
		"HTTP_READ_HEADER_TIMEOUT": "5s",
		"HTTP_READ_TIMEOUT":        "2m",
		"HTTP_WRITE_TIMEOUT":       "2m",
		"HTTP_IDLE_TIMEOUT":        "1m",
		"UPLOAD_ROOT":              "data/upload",
		"UPLOAD_MAX_FILE_BYTES":    "20971520",
		"UPLOAD_MAX_REQUEST_BYTES": "22020096",
		"AUTH_JWT_SECRET":          testJWTSecret,
		"AUTH_JWT_ISSUER":          testJWTIssuer,
		testTokenTTLKey:            "24h",
		"DB_HOST":                  "localhost",
		"DB_PORT":                  "2881",
		"DB_NAME":                  "x_test",
		"DB_USER":                  "x_test_user",
		"DB_PASSWORD":              testPassword,
		"DB_CHARSET":               "utf8mb4",
		"DB_COLLATION":             "utf8mb4_bin",
		"DB_TLS":                   "false",
		"DB_TIMEZONE":              "UTC",
		"DB_PING_TIMEOUT":          "1s",
		"DB_MAX_OPEN_CONNS":        "10",
		"DB_MAX_IDLE_CONNS":        "5",
		"DB_CONN_MAX_LIFETIME":     "30m",
		"DB_CONN_MAX_IDLE_TIME":    "5m",
		"LLM_PROVIDER":             "deepseek",
		"LLM_BASE_URL":             "https://api.deepseek.com",
		"LLM_MODEL":                "deepseek-chat",
		"LLM_API_KEY":              testLLMAPIKey,
		"LLM_TASK_MODEL":           "deepseek-chat",
		"LLM_TASK_MAX_TOKENS":      "512",
		"LLM_TIMEOUT":              "2m",
		// Single quotes keep godotenv from reading the braces and quotes, which
		// is also how the shipped dotenv files spell this value.
		"LLM_CAPABILITY_CONFIG_JSON":   `'{"chat":{"temperature":0.7,"context_budget_tokens":32000,"stages":{"exploring":{"max_tokens":1024},"responding":{"max_tokens":4096}}}}'`,
		"TURN_INPUT_TIMEOUT":           "30m",
		"TURN_MAX_ROUNDS":              "12",
		"TURN_MAX_ATTACHMENT_BYTES":    "26214400",
		"EMBEDDING_BASE_URL":           "https://dashscope.aliyuncs.com/compatible-mode/v1",
		"EMBEDDING_MODEL":              testEmbeddingModel,
		"EMBEDDING_API_KEY":            testEmbeddingAPIKey,
		"EMBEDDING_DIMENSION":          "1024",
		"EMBEDDING_TIMEOUT":            "30s",
		"EMBEDDING_BATCH_SIZE":         "16",
		"PARSER_DEFAULT_ENGINE":        "text",
		"PARSER_DOCLING_BASE_URL":      "http://127.0.0.1:5001",
		"PARSER_TIMEOUT":               "2m",
		"KB_CHUNK_SIZE":                "512",
		"KB_CHUNK_OVERLAP":             "50",
		"KB_RETRIEVAL_TOP_K":           "5",
		"KB_RETRIEVAL_CANDIDATES":      "20",
		"KB_RRF_K":                     "60",
		"KB_MAX_CHARS_PER_DOCUMENT":    "200000",
		"KB_MAX_CHARS_PER_TURN":        "150000",
		"KB_VECTOR_LEASE":              "5m",
		"KB_INDEX_WORKERS":             "2",
		"QUESTION_DEFAULT_COUNT":       "5",
		"QUESTION_MAX_COUNT":           "30",
		"PRACTICE_QUEUE_SIZE":          "20",
		"PRACTICE_JUDGE_MAX_TOKENS":    "1024",
		"MASTERY_LEASE_TTL":            "2m",
		"MASTERY_GATE_CONSECUTIVE":     "3",
		"MASTERY_GATE_MIN_EVIDENCE":    "2",
		"MASTERY_MAX_ACTIVE_PATHS":     "5",
		"DASHBOARD_RECENT_LIMIT":       "10",
		"READING_UNIT_MAX_CHARS":       "4000",
		"READING_TUTOR_MAX_TOKENS":     "2048",
		"READING_EXTENSION_TIMEOUT":    "3s",
		"MEMORY_CONFIDENCE_THRESHOLD":  "60",
		"MEMORY_MIN_CORROBORATION":     "2",
		"MEMORY_MIN_SURFACES":          "2",
		"MEMORY_SENSITIVE_KINDS":       "preference,health,relationship,credential",
		"MEMORY_SYNTHESIS_WINDOW_DAYS": "30",
		"MEMORY_MAX_FACTS_PER_SURFACE": "200",
		"AUTHZ_DEFAULT_PRESET":         "standard",
		"AUTHZ_LEARNER_CAPABILITIES":   "chat",
		"AUTHZ_LEARNER_SURFACES":       "chat",
		"AUDIT_RETENTION_DAYS":         "180",
		"AUDIT_MAX_PAGE_SIZE":          "200",
	}
}

func writeDotenv(t *testing.T, values map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	var lines []string
	for _, key := range requiredKeys {
		if value, ok := values[key]; ok {
			lines = append(lines, key+"="+value)
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
