package library

import (
	"sort"
	"strings"
	"testing"
	"time"

	"backend/internal/parse"
)

// The file library decides what a file is; the parser registry decides who reads
// it. They meet on one string — the category — and neither can see the other's
// list: a category renamed on one side would turn files that used to be readable
// into files nothing reads, and nothing would fail. This is the test that fails.
//
// It is deliberately a list of expectations rather than a comparison of two
// exported lists: when a category is added, the author has to say here whether
// any engine reads it, and a category nobody thought about is the failure mode
// being guarded against.
func TestFileCategoriesMatchTheParserRegistry(t *testing.T) {
	// Docling is given an address so that the registry is built with every
	// engine this build has; Select is asked, and it never calls the engine.
	registry, err := parse.NewRegistry(parse.Options{
		DefaultEngine:  parse.EngineText,
		DoclingBaseURL: "http://docling.invalid:5001",
		Timeout:        time.Minute,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	// Which engine reads each category, and "" for the ones no engine in this
	// build reads. Those files can still be stored and downloaded; they are
	// just not text this service can use.
	engines := map[string]string{
		"md":     parse.EngineText,
		"pdf":    parse.EngineDocling,
		"doc":    parse.EngineDocling,
		"images": "",
		"ppt":    "",
		"mp3":    "",
	}

	present := make(map[string]bool)
	for _, rule := range extensionRules {
		present[rule.Category] = true
	}

	for category := range present {
		want, known := engines[category]
		if !known {
			t.Fatalf("the file library classifies files as %q and this test does not say what reads them; "+
				"add it, and to the parser registry if an engine should", category)
		}

		engine, err := registry.Select(category)
		if want == "" {
			if code := parse.CodeOf(err); code != parse.CodeUnsupportedFormat {
				t.Errorf("Select(%q) code = %q (%v), want %q: this test says nothing reads it",
					category, code, err, parse.CodeUnsupportedFormat)
			}
			continue
		}
		if err != nil {
			t.Errorf("Select(%q) error = %v, want %q", category, err, want)
			continue
		}
		if engine.Name() != want {
			t.Errorf("Select(%q) chose %q, want %q", category, engine.Name(), want)
		}
	}

	// And the other way round: an expectation for a category the library cannot
	// produce is a stale line in this test, which is how a rename here goes
	// unnoticed.
	names := make([]string, 0, len(present))
	for category := range present {
		names = append(names, category)
	}
	sort.Strings(names)
	for category := range engines {
		if !present[category] {
			t.Errorf("this test expects a %q category; the file library has %s",
				category, strings.Join(names, ", "))
		}
	}
}
