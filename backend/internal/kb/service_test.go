package kb

import (
	"context"
	"errors"
	"strings"
	"sync"
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
		if _, err := h.service.CreateKnowledgeBase(context.Background(), name, ""); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("CreateKnowledgeBase(%q) error = %v, want ErrInvalidInput", name, err)
		}
	}
}

func TestOnlyTheFirstBaseIsTheDefaultOne(t *testing.T) {
	h := newHarness(t)
	first := h.base(t, "first")
	second := h.base(t, "second")

	if !first.IsDefault || second.IsDefault {
		t.Fatalf("defaults: first = %v, second = %v, want only the first", first.IsDefault, second.IsDefault)
	}
	bases, err := h.service.KnowledgeBases(context.Background())
	if err != nil {
		t.Fatalf("KnowledgeBases() error = %v", err)
	}
	if len(bases) != 2 || !bases[0].IsDefault {
		t.Fatalf("the listing does not put the default base first: %+v", bases)
	}
}

// Two callers setting the default at once must not leave two defaults behind.
// The lock that arranges that is in storage; what is checked here is that the
// service leaves the whole move to one call, so that there is nothing for a
// caller to interleave.
func TestSetDefaultLeavesExactlyOneUnderConcurrency(t *testing.T) {
	h := newHarness(t)
	ids := make([]string, 0, 4)
	for _, name := range []string{"a", "b", "c", "d"} {
		ids = append(ids, h.base(t, name).ID)
	}

	var wait sync.WaitGroup
	for _, kbID := range ids {
		wait.Add(1)
		go func(kbID string) {
			defer wait.Done()
			for attempt := 0; attempt < 10; attempt++ {
				if err := h.service.SetDefaultKnowledgeBase(context.Background(), kbID); err != nil {
					t.Errorf("SetDefaultKnowledgeBase(%s) error = %v", kbID, err)
					return
				}
			}
		}(kbID)
	}
	wait.Wait()

	defaults := 0
	for _, base := range h.repo.bases {
		if base.IsDefault {
			defaults++
		}
	}
	if defaults != 1 {
		t.Fatalf("%d knowledge bases are default, want exactly 1", defaults)
	}
}

func TestDeletingTheDefaultBaseLeavesAnotherOneInItsPlace(t *testing.T) {
	h := newHarness(t)
	first := h.base(t, "first")
	second := h.base(t, "second")

	if err := h.service.DeleteKnowledgeBase(context.Background(), first.ID); err != nil {
		t.Fatalf("DeleteKnowledgeBase() error = %v", err)
	}
	bases, err := h.service.KnowledgeBases(context.Background())
	if err != nil {
		t.Fatalf("KnowledgeBases() error = %v", err)
	}
	if len(bases) != 1 || bases[0].ID != second.ID || !bases[0].IsDefault {
		t.Fatalf("after deleting the default base, the listing is %+v", bases)
	}
}
