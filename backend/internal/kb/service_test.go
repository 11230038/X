package kb

// What is left of the knowledge base service's own tests, now that the storage
// halves have somewhere better to be. Two cases: the record a create hands back,
// and the input it refuses before storage ever sees it. Which base is the
// default, what the listing orders by, where the default goes when its base is
// deleted, and what two callers setting one at once leave behind are all
// questions about the rows — the fake can only restate the answers, so they are
// asked of a real seekdb in repository_db_test.go.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCreateKnowledgeBaseBindsTheProviderAndTheModel(t *testing.T) {
	h := newHarness(t)
	base := h.base(t, "数学分析")

	if base.Provider != ProviderNative {
		t.Errorf("provider = %q, want %q", base.Provider, ProviderNative)
	}
	if base.EmbeddingModel != "test-embed" {
		t.Errorf("embedding model = %q, want the configured one", base.EmbeddingModel)
	}
	// Whether this base is the default is decided in storage, and the record
	// that comes back is the stored one.
	if !base.IsDefault {
		t.Error("the first knowledge base is not the default one")
	}
	if base.ID == "" || !strings.HasPrefix(base.ID, kbIDPrefix) {
		t.Errorf("id = %q, want a generated %s id", base.ID, kbIDPrefix)
	}
}

func TestCreateKnowledgeBaseRefusesANamelessOne(t *testing.T) {
	h := newHarness(t)
	for _, name := range []string{"", "   ", "\t\n"} {
		if _, err := h.service.CreateKnowledgeBase(context.Background(), name, "", testCaller()); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("CreateKnowledgeBase(%q) error = %v, want ErrInvalidInput", name, err)
		}
	}
}
