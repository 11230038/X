package parse

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// stubParser reads one category and returns whatever the test told it to.
type stubParser struct {
	name       string
	categories []string
	result     Result
	err        error
	calls      int
}

func (p *stubParser) Name() string { return p.name }

func (p *stubParser) Supports(category string) bool {
	return containsCategory(p.categories, category)
}

func (p *stubParser) Parse(context.Context, Request) (Result, error) {
	p.calls++
	return p.result, p.err
}

func newTestRegistry(t *testing.T, doclingURL string) *Registry {
	t.Helper()
	registry, err := NewRegistry(Options{
		DefaultEngine:  EngineText,
		DoclingBaseURL: doclingURL,
		Timeout:        5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	return registry
}

func TestSelectChoosesTheEngineForTheCategory(t *testing.T) {
	registry := newTestRegistry(t, "http://docling:5001")

	cases := []struct {
		category string
		want     string
	}{
		{category: CategoryMarkdown, want: EngineText},
		{category: CategoryPDF, want: EngineDocling},
		{category: CategoryDocument, want: EngineDocling},
	}

	for _, testCase := range cases {
		t.Run(testCase.category, func(t *testing.T) {
			engine, err := registry.Select(testCase.category)
			if err != nil {
				t.Fatalf("Select() error = %v", err)
			}
			if engine.Name() != testCase.want {
				t.Fatalf("engine = %q, want %q", engine.Name(), testCase.want)
			}
		})
	}
}

// The two codes mean different things to whoever reads them: one is a setting
// they can change, the other is a format this service does not handle.
func TestSelectTellsAnUnconfiguredEngineFromAnUnsupportedFormat(t *testing.T) {
	t.Run("no engine configured for the category", func(t *testing.T) {
		registry := newTestRegistry(t, "")

		_, err := registry.Select(CategoryPDF)
		if CodeOf(err) != CodeParserUnavailable {
			t.Fatalf("code = %q, want %q (error: %v)", CodeOf(err), CodeParserUnavailable, err)
		}
		// The message has to name the key that fixes it: a user who is told
		// "unavailable" and nothing else has nowhere to go.
		if !strings.Contains(err.Error(), "PARSER_DOCLING_BASE_URL") {
			t.Fatalf("error = %v, want it to name PARSER_DOCLING_BASE_URL", err)
		}
	})

	t.Run("no engine in this build reads the category", func(t *testing.T) {
		registry := newTestRegistry(t, "http://docling:5001")

		for _, category := range []string{"images", "ppt", "mp3"} {
			_, err := registry.Select(category)
			if CodeOf(err) != CodeUnsupportedFormat {
				t.Fatalf("%s: code = %q, want %q (error: %v)", category, CodeOf(err), CodeUnsupportedFormat, err)
			}
		}
	})

	t.Run("a category nobody named", func(t *testing.T) {
		registry := newTestRegistry(t, "http://docling:5001")

		if _, err := registry.Select("  "); CodeOf(err) != CodeUnsupportedFormat {
			t.Fatalf("code = %q, want %q", CodeOf(err), CodeUnsupportedFormat)
		}
	})
}

// When two engines can read the same category, the configured default is the
// one that reads it — otherwise the setting would mean nothing.
func TestSelectPrefersTheDefaultEngine(t *testing.T) {
	first := &stubParser{name: "alpha", categories: []string{CategoryMarkdown}}
	second := &stubParser{name: "beta", categories: []string{CategoryMarkdown}}
	registry := &Registry{
		defaultEngine: "beta",
		engines:       map[string]Parser{"alpha": first, "beta": second},
	}

	engine, err := registry.Select(CategoryMarkdown)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if engine.Name() != "beta" {
		t.Fatalf("engine = %q, want the configured default beta", engine.Name())
	}
}

func TestSelectFallsBackToAnotherEngine(t *testing.T) {
	markdown := &stubParser{name: "alpha", categories: []string{CategoryMarkdown}}
	pdf := &stubParser{name: "beta", categories: []string{CategoryPDF}}
	registry := &Registry{
		defaultEngine: "alpha",
		engines:       map[string]Parser{"alpha": markdown, "beta": pdf},
	}

	engine, err := registry.Select(CategoryPDF)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if engine.Name() != "beta" {
		t.Fatalf("engine = %q, want beta", engine.Name())
	}
}

func TestNewRegistryRefusesADefaultEngineItDoesNotHave(t *testing.T) {
	cases := []struct {
		name string
		opts Options
	}{
		{name: "unknown name", opts: Options{DefaultEngine: "unstructured", Timeout: time.Second}},
		{name: "docling without an address", opts: Options{DefaultEngine: EngineDocling, Timeout: time.Second}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := NewRegistry(testCase.opts)
			if err == nil {
				t.Fatal("NewRegistry() error = nil, want an error")
			}
			if !strings.Contains(err.Error(), "PARSER_DEFAULT_ENGINE") {
				t.Fatalf("error = %v, want it to name PARSER_DEFAULT_ENGINE", err)
			}
		})
	}
}

func TestNewRegistryRejectsANonPositiveTimeout(t *testing.T) {
	_, err := NewRegistry(Options{DefaultEngine: EngineText})
	if err == nil {
		t.Fatal("NewRegistry() error = nil, want an error")
	}
}

func TestEngineNamesListsEveryEngineThisBuildHas(t *testing.T) {
	names := EngineNames()
	want := []string{EngineDocling, EngineText}

	if len(names) != len(want) {
		t.Fatalf("EngineNames() = %v, want %v", names, want)
	}
	for index, name := range want {
		if names[index] != name {
			t.Fatalf("EngineNames() = %v, want %v", names, want)
		}
	}
}

func TestRegistryParseRecordsTheEngineThatRead(t *testing.T) {
	registry := newTestRegistry(t, "")

	result, err := registry.Parse(context.Background(), Request{
		Filename: "notes.md", Category: CategoryMarkdown,
		Content: strings.NewReader("hello"), MaxChars: 100,
	})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if result.Text != "hello" {
		t.Errorf("Text = %q, want %q", result.Text, "hello")
	}
	if result.Engine != EngineText {
		t.Errorf("Engine = %q, want %q", result.Engine, EngineText)
	}
}

// A document over the limit is still a document. The text that fits comes back
// with the error, so the caller can store it and say what happened — the
// alternative is losing material the user uploaded or cutting it silently.
func TestRegistryParseReturnsTheTextItTruncated(t *testing.T) {
	registry := newTestRegistry(t, "")

	result, err := registry.Parse(context.Background(), Request{
		Filename: "long.md", Category: CategoryMarkdown,
		Content: strings.NewReader(strings.Repeat("x", 50)), MaxChars: 10,
	})
	if CodeOf(err) != CodeParseTruncated {
		t.Fatalf("code = %q, want %q (error: %v)", CodeOf(err), CodeParseTruncated, err)
	}
	if result.Text != strings.Repeat("x", 10) {
		t.Fatalf("Text = %q, want the first 10 characters", result.Text)
	}
	if result.Engine != EngineText {
		t.Fatalf("Engine = %q, want the engine that read it", result.Engine)
	}
	// The message has to say where it stopped, or a reader cannot tell a
	// truncated document from a complete one that happens to end there.
	if !strings.Contains(err.Error(), "character 10") {
		t.Fatalf("error = %v, want it to name the cut position", err)
	}
}

func TestRegistryParseKeepsADocumentThatOnlyJustFits(t *testing.T) {
	registry := newTestRegistry(t, "")

	result, err := registry.Parse(context.Background(), Request{
		Filename: "exact.md", Category: CategoryMarkdown,
		Content: strings.NewReader(strings.Repeat("x", 10)), MaxChars: 10,
	})
	if err != nil {
		t.Fatalf("Parse() error = %v, want the limit itself to be allowed", err)
	}
	if len(result.Text) != 10 {
		t.Fatalf("Text has %d characters, want 10", len(result.Text))
	}
}

// limitChars is where "characters" means code points rather than bytes: a cut
// inside a multi-byte character would store text that is not valid UTF-8.
func TestLimitCharsCutsAtCharacterBoundaries(t *testing.T) {
	// Each of these characters is three bytes.
	const text = "闭包是函数"

	cases := []struct {
		maxChars  int
		want      string
		truncated bool
	}{
		{maxChars: 1, want: "闭", truncated: true},
		{maxChars: 2, want: "闭包", truncated: true},
		{maxChars: 5, want: text, truncated: false},
		{maxChars: 99, want: text, truncated: false},
	}

	for _, testCase := range cases {
		got, truncated := limitChars(text, testCase.maxChars)
		if got != testCase.want || truncated != testCase.truncated {
			t.Errorf("limitChars(%q, %d) = %q, %v; want %q, %v",
				text, testCase.maxChars, got, truncated, testCase.want, testCase.truncated)
		}
	}
}

func TestRegistryParseRefusesMalformedRequests(t *testing.T) {
	registry := newTestRegistry(t, "")

	cases := []struct {
		name    string
		request Request
	}{
		{name: "no content", request: Request{Category: CategoryMarkdown, MaxChars: 10}},
		{name: "no limit", request: Request{
			Category: CategoryMarkdown, Content: strings.NewReader("x"), MaxChars: 0}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := registry.Parse(context.Background(), testCase.request)
			if err == nil {
				t.Fatal("Parse() error = nil, want an error")
			}
			// These are callers' mistakes, not document outcomes: they carry no
			// code because no user action could fix them.
			if CodeOf(err) != "" {
				t.Fatalf("code = %q, want none for a caller's mistake", CodeOf(err))
			}
		})
	}
}

func TestRegistryParseReportsAnUnavailableEngine(t *testing.T) {
	registry := newTestRegistry(t, "")

	_, err := registry.Parse(context.Background(), Request{
		Filename: "paper.pdf", Category: CategoryPDF,
		Content: strings.NewReader("%PDF-1.4"), MaxChars: 100,
	})
	if CodeOf(err) != CodeParserUnavailable {
		t.Fatalf("code = %q, want %q (error: %v)", CodeOf(err), CodeParserUnavailable, err)
	}
}

func TestRegistryParseDoesNotSwallowAnEngineFailure(t *testing.T) {
	engine := &stubParser{
		name: "alpha", categories: []string{CategoryMarkdown},
		err: &Error{Code: CodeParseFailed, Message: "the file is broken"},
	}
	registry := &Registry{defaultEngine: "alpha", engines: map[string]Parser{"alpha": engine}}

	_, err := registry.Parse(context.Background(), Request{
		Category: CategoryMarkdown, Content: strings.NewReader("x"), MaxChars: 10,
	})
	if CodeOf(err) != CodeParseFailed {
		t.Fatalf("code = %q, want %q", CodeOf(err), CodeParseFailed)
	}
	if engine.calls != 1 {
		t.Fatalf("engine called %d times, want 1", engine.calls)
	}
}

func TestCodeOfIgnoresOtherErrors(t *testing.T) {
	if got := CodeOf(errors.New("something else")); got != "" {
		t.Fatalf("CodeOf() = %q, want an empty code", got)
	}
	if got := CodeOf(nil); got != "" {
		t.Fatalf("CodeOf(nil) = %q, want an empty code", got)
	}
}

func TestErrorUnwrapsTheCause(t *testing.T) {
	cause := errors.New("connection refused")
	err := &Error{Code: CodeParserUnavailable, Message: "no engine", Cause: cause}

	if !errors.Is(err, cause) {
		t.Fatal("errors.Is() could not find the cause through the parse error")
	}
	if !strings.Contains(err.Error(), string(CodeParserUnavailable)) {
		t.Fatalf("Error() = %q, want it to name the code", err.Error())
	}
	if !strings.Contains(err.Error(), "no engine") {
		t.Fatalf("Error() = %q, want it to carry the user-facing message", err.Error())
	}
}
