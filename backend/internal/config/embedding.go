package config

import (
	"fmt"
	"strings"
	"time"
)

// EmbeddingConfig holds the vector provider that knowledge base retrieval
// needs. It is a section of its own rather than part of LLMConfig because it is
// a different service: the chat model is DeepSeek, which serves no embedding
// endpoint at all, so the two can never share a base URL or a key.
type EmbeddingConfig struct {
	// BaseURL is a prefix, not an endpoint: the client appends /embeddings to
	// it, the same way the chat client appends /chat/completions.
	BaseURL string
	Model   string
	// APIKey sits at the same level as LLM_API_KEYS and gets the same treatment:
	// never echoed, never logged, never inside an error message. It stays a single
	// key rather than a pool: §17.3 is about the provider that serves every
	// conversation, and the embedding provider is called once per document rather
	// than once per person.
	APIKey string
	// Dimension is the width of document_chunks.embedding. It is not a free
	// choice. The schema pins the width in VECTOR(N), vectors from two models
	// cannot be compared with each other, and both facts mean the same thing: a
	// change here is a migration plus a rebuild of every base, not an edit to
	// this file. Startup refuses to run while the two disagree.
	Dimension int
	Timeout   time.Duration
	// BatchSize is how many chunk texts ride in one request. It bounds the
	// request body rather than the work: a document with more chunks than this
	// is embedded in several requests.
	BatchSize int
}

// Validate checks the vector provider settings. The key is checked for presence
// and never appears in a failure message.
func (c EmbeddingConfig) Validate() error {
	if err := validateBaseURL(c.BaseURL); err != nil {
		return fmt.Errorf("invalid EMBEDDING_BASE_URL: %w", err)
	}
	// An ordered list rather than a map, so that a file missing both names the
	// first one every time instead of either one at random.
	for _, field := range []struct{ key, value string }{
		{"EMBEDDING_MODEL", c.Model},
		{"EMBEDDING_API_KEY", c.APIKey},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s must not be empty", field.key)
		}
	}
	if c.Dimension <= 0 {
		return fmt.Errorf("EMBEDDING_DIMENSION must be a positive integer")
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("EMBEDDING_TIMEOUT must be a positive duration")
	}
	if c.BatchSize <= 0 {
		return fmt.Errorf("EMBEDDING_BATCH_SIZE must be a positive integer")
	}
	return nil
}

func loadEmbeddingConfig(values map[string]string) (EmbeddingConfig, error) {
	dimension, err := parsePositiveInt(values, "EMBEDDING_DIMENSION")
	if err != nil {
		return EmbeddingConfig{}, err
	}
	timeout, err := parsePositiveDuration(values, "EMBEDDING_TIMEOUT")
	if err != nil {
		return EmbeddingConfig{}, err
	}
	batchSize, err := parsePositiveInt(values, "EMBEDDING_BATCH_SIZE")
	if err != nil {
		return EmbeddingConfig{}, err
	}
	return EmbeddingConfig{
		// A trailing slash would double up when the client appends its path.
		BaseURL:   strings.TrimSuffix(strings.TrimSpace(values["EMBEDDING_BASE_URL"]), "/"),
		Model:     strings.TrimSpace(values["EMBEDDING_MODEL"]),
		APIKey:    values["EMBEDDING_API_KEY"],
		Dimension: dimension,
		Timeout:   timeout,
		BatchSize: batchSize,
	}, nil
}
