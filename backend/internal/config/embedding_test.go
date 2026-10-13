package config

import (
	"strings"
	"testing"
	"time"
)

func validEmbeddingConfig() EmbeddingConfig {
	return EmbeddingConfig{
		BaseURL:   "https://dashscope.aliyuncs.com/compatible-mode/v1",
		Model:     testEmbeddingModel,
		APIKey:    testEmbeddingAPIKey,
		Dimension: 1024,
		Timeout:   30 * time.Second,
		BatchSize: 16,
	}
}

func TestEmbeddingConfigValidateAcceptsTheShippedShape(t *testing.T) {
	if err := validEmbeddingConfig().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestEmbeddingConfigValidateRejectsInvalidSettings(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*EmbeddingConfig)
	}{
		{name: "relative base URL", mutate: func(c *EmbeddingConfig) { c.BaseURL = "dashscope.aliyuncs.com" }},
		{name: "base URL without a host", mutate: func(c *EmbeddingConfig) { c.BaseURL = "https://" }},
		{name: "non-HTTP scheme", mutate: func(c *EmbeddingConfig) {
			c.BaseURL = "ftp://dashscope.aliyuncs.com"
		}},
		{name: "blank model", mutate: func(c *EmbeddingConfig) { c.Model = "  " }},
		{name: "blank API key", mutate: func(c *EmbeddingConfig) { c.APIKey = "" }},
		{name: "zero dimension", mutate: func(c *EmbeddingConfig) { c.Dimension = 0 }},
		{name: "negative dimension", mutate: func(c *EmbeddingConfig) { c.Dimension = -1024 }},
		{name: "zero timeout", mutate: func(c *EmbeddingConfig) { c.Timeout = 0 }},
		{name: "zero batch size", mutate: func(c *EmbeddingConfig) { c.BatchSize = 0 }},
		{name: "negative batch size", mutate: func(c *EmbeddingConfig) { c.BatchSize = -1 }},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validEmbeddingConfig()
			tt.mutate(&cfg)

			err := cfg.Validate()
			if err == nil {
				t.Fatal("Validate() error = nil, want an error")
			}
			if strings.Contains(err.Error(), testEmbeddingAPIKey) {
				t.Fatal("validation error exposed the embedding API key")
			}
		})
	}
}

func TestEmbeddingConfigValidateNamesTheFieldThatIsMissing(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*EmbeddingConfig)
		want   string
	}{
		{name: "model", mutate: func(c *EmbeddingConfig) { c.Model = "" }, want: "EMBEDDING_MODEL"},
		{name: "key", mutate: func(c *EmbeddingConfig) { c.APIKey = "" }, want: "EMBEDDING_API_KEY"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validEmbeddingConfig()
			tt.mutate(&cfg)

			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() error = %v, want one naming %s", err, tt.want)
			}
		})
	}
}

func TestLoadTrimsTheEmbeddingBaseURLTrailingSlash(t *testing.T) {
	values := validValues()
	values["EMBEDDING_BASE_URL"] = "https://dashscope.aliyuncs.com/compatible-mode/v1/"

	cfg, err := Load(writeDotenv(t, values))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The client appends /embeddings, which would otherwise become //embeddings.
	if cfg.Embedding.BaseURL != "https://dashscope.aliyuncs.com/compatible-mode/v1" {
		t.Fatalf("Embedding.BaseURL = %q", cfg.Embedding.BaseURL)
	}
}

func TestLoadNeverEchoesTheEmbeddingAPIKey(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
	}{
		{name: "blank key", key: "EMBEDDING_API_KEY", value: " "},
		{name: "invalid base URL", key: "EMBEDDING_BASE_URL", value: "dashscope.aliyuncs.com"},
		{name: "invalid dimension", key: "EMBEDDING_DIMENSION", value: "wide"},
		{name: "invalid batch size", key: "EMBEDDING_BATCH_SIZE", value: "0"},
		{name: "invalid timeout", key: "EMBEDDING_TIMEOUT", value: "soon"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			values := validValues()
			values[tt.key] = tt.value

			_, err := Load(writeDotenv(t, values))
			if err == nil {
				t.Fatalf("Load() error = nil, want an error for %s", tt.key)
			}
			if strings.Contains(err.Error(), testEmbeddingAPIKey) {
				t.Fatal("configuration error exposed the embedding API key")
			}
		})
	}
}

func TestLoadKeepsTheEmbeddingAPIKeyVerbatim(t *testing.T) {
	values := validValues()
	// A quote is what keeps the inner spaces through the dotenv parser; the point
	// of the test is what this package does with the result. A secret is used as
	// written: trimming one would change it, and the provider would answer 401
	// for a key the operator can see is correct.
	values[testEmbeddingKey] = "'  spaced-embedding-key  '"

	cfg, err := Load(writeDotenv(t, values))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Embedding.APIKey != "  spaced-embedding-key  " {
		t.Fatalf("Embedding.APIKey = %q, want the value verbatim", cfg.Embedding.APIKey)
	}
}
