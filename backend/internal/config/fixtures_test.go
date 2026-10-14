package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func validValues() map[string]string {
	return map[string]string{
		"HTTP_ADDR":                 "127.0.0.1:9090",
		"APP_ENV":                   "test",
		"SHUTDOWN_TIMEOUT":          "2s",
		"HTTP_READ_HEADER_TIMEOUT":  "5s",
		"HTTP_READ_TIMEOUT":         "2m",
		"HTTP_WRITE_TIMEOUT":        "2m",
		"HTTP_IDLE_TIMEOUT":         "1m",
		"UPLOAD_ROOT":               "data/upload",
		"UPLOAD_MAX_FILE_BYTES":     "20971520",
		"UPLOAD_MAX_REQUEST_BYTES":  "22020096",
		"AUTH_JWT_SECRET":           testJWTSecret,
		"AUTH_JWT_ISSUER":           testJWTIssuer,
		testTokenTTLKey:             "24h",
		"DB_HOST":                   "localhost",
		"DB_PORT":                   "2881",
		"DB_NAME":                   "x_test",
		"DB_USER":                   "x_test_user",
		"DB_PASSWORD":               testPassword,
		"DB_CHARSET":                "utf8mb4",
		"DB_COLLATION":              "utf8mb4_bin",
		"DB_TLS":                    "false",
		"DB_TIMEZONE":               "UTC",
		"DB_PING_TIMEOUT":           "1s",
		"DB_MAX_OPEN_CONNS":         "10",
		"DB_MAX_IDLE_CONNS":         "5",
		"DB_CONN_MAX_LIFETIME":      "30m",
		"DB_CONN_MAX_IDLE_TIME":     "5m",
		"LLM_PROVIDER":              "deepseek",
		"LLM_BASE_URL":              "https://api.deepseek.com",
		"LLM_MODEL":                 "deepseek-chat",
		"LLM_API_KEYS":              testLLMAPIKey,
		"LLM_KEY_FAILURE_THRESHOLD": "3",
		"LLM_KEY_COOLDOWN":          "60s",
		"LLM_TASK_MODEL":            "deepseek-chat",
		"LLM_TASK_MAX_TOKENS":       "512",
		"LLM_TIMEOUT":               "2m",
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
		"USAGE_RETENTION_DAYS":         "365",
		// The two quota keys at zero, which is the shape .env.example ships: a
		// deployment that bounds nothing starts, and the keys are required all
		// the same (§2.9, and see the case in usage_test.go that pins the two).
		"USAGE_DAILY_CALLS_PER_ACCOUNT":    "0",
		"USAGE_MONTHLY_TOKENS_PER_ACCOUNT": "0",
		"RATE_LIMIT_MODEL_PER_MINUTE":      "60",
		"RATE_LIMIT_RETRIEVAL_PER_MINUTE":  "120",
		"RATE_LIMIT_UPLOAD_PER_MINUTE":     "30",
		"RATE_LIMIT_BURST":                 "10",
		"WORKSPACE_ARCHIVE_ROOT":           "../data/archive",
		"WORKSPACE_MAX_ARCHIVE_BYTES":      "536870912",
		"WORKSPACE_JOB_TIMEOUT":            "15m",
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
