package config

import (
	"strings"
	"testing"
	"time"
)

func validKBConfig() KBConfig {
	return KBConfig{
		ChunkSize:           512,
		ChunkOverlap:        50,
		RetrievalTopK:       5,
		RetrievalCandidates: 20,
		RRFK:                60,
		MaxCharsPerDocument: 200000,
		MaxCharsPerTurn:     150000,
		VectorLease:         5 * time.Minute,
		IndexWorkers:        2,
	}
}

func TestKBConfigValidateAcceptsTheShippedShape(t *testing.T) {
	if err := validKBConfig().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestKBConfigValidateRejectsInvalidSettings(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*KBConfig)
		wantKey string
	}{
		{name: "zero chunk size", mutate: func(c *KBConfig) { c.ChunkSize = 0 }, wantKey: "KB_CHUNK_SIZE"},
		{name: "negative chunk size", mutate: func(c *KBConfig) { c.ChunkSize = -1 }, wantKey: "KB_CHUNK_SIZE"},
		// Overlap is not required to be positive, only to leave the window
		// moving: at or above the chunk size the next chunk starts where the
		// last one did and the loop never ends.
		{name: "negative overlap", mutate: func(c *KBConfig) { c.ChunkOverlap = -1 }, wantKey: "KB_CHUNK_OVERLAP"},
		{name: "overlap equal to the chunk size", mutate: func(c *KBConfig) { c.ChunkOverlap = 512 }, wantKey: "KB_CHUNK_OVERLAP"},
		{name: "overlap above the chunk size", mutate: func(c *KBConfig) { c.ChunkOverlap = 600 }, wantKey: "KB_CHUNK_OVERLAP"},
		{name: "zero top k", mutate: func(c *KBConfig) { c.RetrievalTopK = 0 }, wantKey: "KB_RETRIEVAL_TOP_K"},
		// Candidates below the cut would silently lower the result count: rank
		// fusion only ranks what the two branches retrieved.
		{name: "candidates below top k", mutate: func(c *KBConfig) { c.RetrievalCandidates = 4 }, wantKey: "KB_RETRIEVAL_CANDIDATES"},
		{name: "zero rrf k", mutate: func(c *KBConfig) { c.RRFK = 0 }, wantKey: "KB_RRF_K"},
		{name: "zero document budget", mutate: func(c *KBConfig) { c.MaxCharsPerDocument = 0 }, wantKey: "KB_MAX_CHARS_PER_DOCUMENT"},
		{name: "zero turn budget", mutate: func(c *KBConfig) { c.MaxCharsPerTurn = 0 }, wantKey: "KB_MAX_CHARS_PER_TURN"},
		{name: "zero lease", mutate: func(c *KBConfig) { c.VectorLease = 0 }, wantKey: "KB_VECTOR_LEASE"},
		{name: "negative lease", mutate: func(c *KBConfig) { c.VectorLease = -time.Minute }, wantKey: "KB_VECTOR_LEASE"},
		{name: "zero workers", mutate: func(c *KBConfig) { c.IndexWorkers = 0 }, wantKey: "KB_INDEX_WORKERS"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validKBConfig()
			tt.mutate(&cfg)

			err := cfg.Validate()
			if err == nil {
				t.Fatal("Validate() error = nil, want an error")
			}
			if !strings.Contains(err.Error(), tt.wantKey) {
				t.Fatalf("Validate() error = %v, want one naming %s", err, tt.wantKey)
			}
		})
	}
}

func TestKBConfigValidateAcceptsZeroOverlap(t *testing.T) {
	cfg := validKBConfig()
	cfg.ChunkOverlap = 0

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want zero overlap accepted", err)
	}
}

func TestKBConfigValidateAcceptsCandidatesEqualToTopK(t *testing.T) {
	cfg := validKBConfig()
	cfg.RetrievalCandidates = cfg.RetrievalTopK

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want equal values accepted", err)
	}
}

func TestLoadRejectsInvalidKBValues(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		value   string
		wantKey string
	}{
		{name: "chunk size", key: "KB_CHUNK_SIZE", value: "0"},
		{name: "overlap", key: "KB_CHUNK_OVERLAP", value: "-1"},
		{name: "overlap not below the chunk size", key: "KB_CHUNK_OVERLAP", value: "512"},
		{name: "top k", key: "KB_RETRIEVAL_TOP_K", value: "0"},
		{name: "candidates", key: "KB_RETRIEVAL_CANDIDATES", value: "0"},
		{name: "rrf k", key: "KB_RRF_K", value: "0"},
		{name: "document budget", key: "KB_MAX_CHARS_PER_DOCUMENT", value: "0"},
		{name: "turn budget", key: "KB_MAX_CHARS_PER_TURN", value: "0"},
		{name: "lease", key: "KB_VECTOR_LEASE", value: "soon"},
		{name: "workers", key: "KB_INDEX_WORKERS", value: "0"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			values := validValues()
			values[tt.key] = tt.value

			if _, err := Load(writeDotenv(t, values)); err == nil {
				t.Fatalf("Load() error = nil, want an error for %s", tt.key)
			}
		})
	}
}

// The two limits are read from the same file and are easy to transpose; this
// pins each to its own key.
func TestLoadReadsEachRetrievalBoundFromItsOwnKey(t *testing.T) {
	values := validValues()
	values["KB_RETRIEVAL_TOP_K"] = "3"
	values["KB_RETRIEVAL_CANDIDATES"] = "40"

	cfg, err := Load(writeDotenv(t, values))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.KB.RetrievalTopK != 3 {
		t.Errorf("KB_RETRIEVAL_TOP_K was read as %d", cfg.KB.RetrievalTopK)
	}
	if cfg.KB.RetrievalCandidates != 40 {
		t.Errorf("KB_RETRIEVAL_CANDIDATES was read as %d", cfg.KB.RetrievalCandidates)
	}
}
