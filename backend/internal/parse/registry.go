package parse

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Options configure a Registry.
type Options struct {
	// DefaultEngine is the engine a document is read with when it can read that
	// kind of document at all. It is not a preference between equals: the two
	// engines in this build read different categories, so this decides who
	// reads a markdown file when both could.
	DefaultEngine string
	// DoclingBaseURL is a prefix, not an endpoint: the adapter appends
	// /v1/convert/file. An empty value means the engine is not configured,
	// which is a supported deployment and not an error.
	DoclingBaseURL string
	// Timeout bounds one call to an external engine.
	Timeout time.Duration
}

// engineKind is one engine this build has, whether or not configuration turned
// it on.
type engineKind struct {
	name       string
	categories []string
	// hint names the key that turns this engine on. It goes into the message a
	// user sees when their file needs an engine nobody configured.
	hint    string
	enabled func(Options) bool
	build   func(Options) Parser
}

// catalogue is every engine this build has.
//
// The engines that configuration turned off are listed here too, and that is the
// whole point: it is what lets Select tell "we could read PDFs, but no PDF
// engine is configured" from "nothing here reads audio". The first is a setting
// someone can change and the second is a format this service does not handle,
// and telling a user the wrong one sends them looking for a fix that does not
// exist.
var catalogue = []engineKind{
	{
		name:       EngineText,
		categories: []string{CategoryMarkdown},
		enabled:    func(Options) bool { return true },
		build:      func(Options) Parser { return textParser{} },
	},
	{
		name:       EngineDocling,
		categories: []string{CategoryPDF, CategoryDocument},
		hint:       "PARSER_DOCLING_BASE_URL",
		enabled:    func(opts Options) bool { return strings.TrimSpace(opts.DoclingBaseURL) != "" },
		build:      func(opts Options) Parser { return newDoclingParser(opts.DoclingBaseURL, opts.Timeout) },
	},
}

// Registry chooses the engine that reads a document and applies the per-document
// limit to what it returns.
type Registry struct {
	defaultEngine string
	engines       map[string]Parser
}

// NewRegistry builds the registry this configuration describes.
//
// A default engine that this build does not have configured is refused here
// rather than at the first upload: the engine would otherwise be chosen never
// and the file would fail as unavailable, which reads as a missing service when
// the real fault is a setting.
func NewRegistry(opts Options) (*Registry, error) {
	if opts.Timeout <= 0 {
		return nil, fmt.Errorf("parse: timeout must be a positive duration")
	}
	engines := make(map[string]Parser, len(catalogue))
	for _, kind := range catalogue {
		if kind.enabled(opts) {
			engines[kind.name] = kind.build(opts)
		}
	}
	defaultEngine := strings.TrimSpace(opts.DefaultEngine)
	if _, ok := engines[defaultEngine]; !ok {
		return nil, fmt.Errorf(
			"parse: PARSER_DEFAULT_ENGINE is %q, which this build does not have configured (configured: %s)",
			defaultEngine, strings.Join(namesOf(engines), ", "))
	}
	return &Registry{defaultEngine: defaultEngine, engines: engines}, nil
}

// EngineNames lists every engine this build has, configured or not, in a stable
// order. It is what a startup check compares PARSER_DEFAULT_ENGINE against, and
// what the message above is built from.
func EngineNames() []string {
	names := make([]string, 0, len(catalogue))
	for _, kind := range catalogue {
		names = append(names, kind.name)
	}
	sort.Strings(names)
	return names
}

// Select returns the engine that reads a document of this category.
func (r *Registry) Select(category string) (Parser, error) {
	category = strings.TrimSpace(category)
	if category == "" {
		return nil, &Error{
			Code:    CodeUnsupportedFormat,
			Message: "the file has no category, so no engine can be chosen for it",
		}
	}
	// The default engine goes first, then the rest by name: with two engines
	// that read different categories the order rarely matters, and when it does
	// the answer is the same one every time.
	if engine, ok := r.engines[r.defaultEngine]; ok && engine.Supports(category) {
		return engine, nil
	}
	for _, name := range namesOf(r.engines) {
		if r.engines[name].Supports(category) {
			return r.engines[name], nil
		}
	}
	for _, kind := range catalogue {
		if containsCategory(kind.categories, category) {
			return nil, &Error{
				Code: CodeParserUnavailable,
				Message: fmt.Sprintf(
					"no engine that reads %s files is configured; set %s to a running parser service and restart",
					category, kind.hint),
			}
		}
	}
	return nil, &Error{
		Code:    CodeUnsupportedFormat,
		Message: fmt.Sprintf("this service does not read %s files", category),
	}
}

// Parse chooses an engine for the request's category and reads the document.
func (r *Registry) Parse(ctx context.Context, req Request) (Result, error) {
	if req.Content == nil {
		return Result{}, fmt.Errorf("parse: no content to read")
	}
	if req.MaxChars <= 0 {
		return Result{}, fmt.Errorf("parse: MaxChars must be a positive integer")
	}
	engine, err := r.Select(req.Category)
	if err != nil {
		return Result{}, err
	}
	result, err := engine.Parse(ctx, req)
	if err != nil {
		return Result{}, err
	}
	// The engine's own name, not what the engine called itself: the registry is
	// what chose the engine, so the answer is recorded where the choice was made.
	result.Engine = engine.Name()

	kept, truncated := limitChars(result.Text, req.MaxChars)
	result.Text = kept
	if !truncated {
		return result, nil
	}
	return result, &Error{
		Code:   CodeParseTruncated,
		Engine: engine.Name(),
		Message: fmt.Sprintf(
			"the document is longer than the %d character limit and was cut at character %d",
			req.MaxChars, req.MaxChars),
	}
}

// limitChars cuts text to at most maxChars characters and reports whether it had
// to.
//
// The cut is made at a character boundary, never inside one: an offset that
// landed in the middle of a multi-byte character would make the stored text
// invalid UTF-8, and every offset after it would be off by what was cut.
func limitChars(text string, maxChars int) (string, bool) {
	if maxChars <= 0 {
		return "", text != ""
	}
	count := 0
	for index := range text {
		if count == maxChars {
			return text[:index], true
		}
		count++
	}
	return text, false
}

// byteCeiling is how many bytes maxChars+1 characters can occupy, which is how
// much an engine may read before it can be sure the document is too long. The
// reading stops one character past the limit because the caller — not the
// engine — decides what "too long" means, and it can only decide that if it sees
// that there was more.
func byteCeiling(maxChars int) int64 {
	const perChar = utf8.UTFMax
	limit := int64(maxChars)
	// The multiplication is saturating: a limit that large is "no limit" for
	// any file this service accepts, and overflowing it into a negative number
	// would read nothing at all.
	if limit > (math.MaxInt64-perChar)/perChar {
		return math.MaxInt64
	}
	return limit*perChar + perChar
}

func namesOf(engines map[string]Parser) []string {
	names := make([]string, 0, len(engines))
	for name := range engines {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func containsCategory(categories []string, category string) bool {
	for _, candidate := range categories {
		if candidate == category {
			return true
		}
	}
	return false
}
