package config

import (
	"fmt"
	"time"
)

// KBConfig holds the knowledge base settings that have one value for the whole
// service. Per-base retrieval parameters are deliberately absent: the kb_*
// columns a per-base override would need do not exist, and a parameter with no
// reader is speculation rather than configuration.
type KBConfig struct {
	// ChunkSize and ChunkOverlap are measured in characters, because that is
	// what a citation points at (char_start/char_end index a document's text,
	// not a tokenizer's output). They are recorded on each document as it is
	// chunked, so a rebuild after a change is reproducible after the fact.
	ChunkSize    int
	ChunkOverlap int
	// RetrievalTopK is how many chunks reach the model. Candidates is how many
	// each branch of the hybrid search returns before rank fusion: it must be at
	// least TopK, because fusion can only rank what the branches retrieved —
	// candidates below the cut are the ones that let a chunk one branch missed
	// still surface.
	RetrievalTopK       int
	RetrievalCandidates int
	// RRFK is the constant in 1/(k + rank): it decides how much a first place
	// outranks a tenth, and lower values make the top ranks dominate. It is
	// configuration because it is a ranking preference, not a fact about the
	// data.
	RRFK int
	// MaxCharsPerDocument and MaxCharsPerTurn are the ATT-03 limits. The first
	// is applied while parsing (a document past it is stored truncated), the
	// second while assembling what a turn sends.
	MaxCharsPerDocument int
	MaxCharsPerTurn     int
	// VectorLease is how long a claimed document may sit in 'running' before the
	// next worker may claim it again. Too short and two workers embed the same
	// document; too long and a crashed worker's document waits that long.
	VectorLease time.Duration
	// IndexWorkers is how many documents are embedded at once. It bounds the
	// request rate against the embedding provider, not the CPU of this process.
	IndexWorkers int
}

// Validate checks the knowledge base settings.
func (c KBConfig) Validate() error {
	for _, field := range []struct {
		key   string
		value int
	}{
		{"KB_CHUNK_SIZE", c.ChunkSize},
		{"KB_RETRIEVAL_TOP_K", c.RetrievalTopK},
		{"KB_RETRIEVAL_CANDIDATES", c.RetrievalCandidates},
		{"KB_RRF_K", c.RRFK},
		{"KB_MAX_CHARS_PER_DOCUMENT", c.MaxCharsPerDocument},
		{"KB_MAX_CHARS_PER_TURN", c.MaxCharsPerTurn},
		{"KB_INDEX_WORKERS", c.IndexWorkers},
	} {
		if field.value <= 0 {
			return fmt.Errorf("%s must be a positive integer", field.key)
		}
	}
	if c.ChunkOverlap < 0 {
		return fmt.Errorf("KB_CHUNK_OVERLAP must be a non-negative integer")
	}
	// Overlap exists so a sentence split across a boundary is still readable by
	// one chunk. At or above the chunk size no text ever advances.
	if c.ChunkOverlap >= c.ChunkSize {
		return fmt.Errorf("KB_CHUNK_OVERLAP must be smaller than KB_CHUNK_SIZE")
	}
	if c.RetrievalCandidates < c.RetrievalTopK {
		return fmt.Errorf("KB_RETRIEVAL_CANDIDATES must not be smaller than KB_RETRIEVAL_TOP_K")
	}
	if c.VectorLease <= 0 {
		return fmt.Errorf("KB_VECTOR_LEASE must be a positive duration")
	}
	return nil
}

func loadKBConfig(values map[string]string) (KBConfig, error) {
	chunkSize, err := parsePositiveInt(values, "KB_CHUNK_SIZE")
	if err != nil {
		return KBConfig{}, err
	}
	chunkOverlap, err := parseNonNegativeInt(values, "KB_CHUNK_OVERLAP")
	if err != nil {
		return KBConfig{}, err
	}
	topK, err := parsePositiveInt(values, "KB_RETRIEVAL_TOP_K")
	if err != nil {
		return KBConfig{}, err
	}
	candidates, err := parsePositiveInt(values, "KB_RETRIEVAL_CANDIDATES")
	if err != nil {
		return KBConfig{}, err
	}
	rrfK, err := parsePositiveInt(values, "KB_RRF_K")
	if err != nil {
		return KBConfig{}, err
	}
	maxPerDocument, err := parsePositiveInt(values, "KB_MAX_CHARS_PER_DOCUMENT")
	if err != nil {
		return KBConfig{}, err
	}
	maxPerTurn, err := parsePositiveInt(values, "KB_MAX_CHARS_PER_TURN")
	if err != nil {
		return KBConfig{}, err
	}
	lease, err := parsePositiveDuration(values, "KB_VECTOR_LEASE")
	if err != nil {
		return KBConfig{}, err
	}
	workers, err := parsePositiveInt(values, "KB_INDEX_WORKERS")
	if err != nil {
		return KBConfig{}, err
	}
	return KBConfig{
		ChunkSize:           chunkSize,
		ChunkOverlap:        chunkOverlap,
		RetrievalTopK:       topK,
		RetrievalCandidates: candidates,
		RRFK:                rrfK,
		MaxCharsPerDocument: maxPerDocument,
		MaxCharsPerTurn:     maxPerTurn,
		VectorLease:         lease,
		IndexWorkers:        workers,
	}, nil
}
