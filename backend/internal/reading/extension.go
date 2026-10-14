package reading

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"backend/internal/llm"
)

// extensionTemperature is what the built-in extensions ask their model for.
// It is lower than any capability's: an extension answers in a declared shape,
// and the closer the model stays to the passage, the less often the answer has
// to be thrown away for not fitting.
const extensionTemperature = 0.2

// Extension is one reading aid: a name, the shape it answers in, whether it can
// run at all, and the run itself.
//
// Run is handed a Context and nothing else, and the Context is built by this
// package. That is the whole of the confinement: an extension cannot reach the
// user's own words, another material, or the conversation, because nothing that
// could carry them is passed in.
//
// A panic, a slow answer and a result that does not fit the declared schema are
// all the registry's to handle, not the extension's: an extension that has to
// defend itself against its own failures is an extension whose author has to
// think about the reader's resilience instead of the reading aid.
type Extension interface {
	// Name is what the extension is called, in the route that runs it and in the
	// catalogue. It is unique within a registry.
	Name() string
	// ResultSchema is the shape Run answers in.
	ResultSchema() ResultSchema
	// Available reports whether the extension can run, and when it cannot, why —
	// in a sentence a person can act on. A false with no reason is refused by the
	// registry, because "this is unavailable" without a why is a state nobody can
	// do anything about.
	Available() (bool, string)
	// Run produces the result. It is called only when Available said it could,
	// inside a recover and a timeout the registry owns.
	Run(ctx context.Context, reading Context) (json.RawMessage, error)
}

// Context is what an extension is given, and it is deliberately three fields.
//
// What is in it is a material, the coordinate the reader is looking at, and the
// text that coordinate resolves to. What is not in it is what an extension must
// not have: the user's own words, another material, the conversation this
// request came from. Adding a field here is giving every extension one more
// thing it can see, so nothing merely convenient is ever added.
type Context struct {
	MaterialID string
	Locator    Locator
	// Text is the passage the extension is about: the unit the locator names, or
	// the reader's selection when there is one. It is resolved by the caller,
	// which is what keeps the resolution — and the question of what a locator may
	// point at — out of every extension.
	Text string
}

// Outcome is what one extension did: a result, or a reason it produced none.
type Outcome struct {
	Name   string       `json:"name"`
	Schema ResultSchema `json:"schema"`
	// Result is the extension's answer, and it is absent when the extension was
	// skipped: an absent field rather than a null one, so that a client rendering
	// a result and a client rendering a reason each see only theirs.
	Result json.RawMessage `json:"result,omitempty"`
	// Skipped says nothing came back. It is a state and not an error: one
	// extension being unavailable, slow or broken never fails the request, and
	// never affects another extension.
	Skipped bool `json:"skipped"`
	// Reason says why, in a sentence. It is non-empty exactly when Skipped is
	// true.
	Reason string `json:"reason,omitempty"`
}

// Availability is one entry of the extension catalogue.
type Availability struct {
	Name      string       `json:"name"`
	Schema    ResultSchema `json:"schema"`
	Available bool         `json:"available"`
	// Reason is why not, and it is non-empty exactly when Available is false.
	Reason string `json:"reason,omitempty"`
}

// Registry is the set of extensions this build has.
//
// It is a value built at assembly time and not a package-level map, so which
// extensions exist is decided where every other dependency is decided, and a
// test can build one with the extensions it is about.
type Registry struct {
	extensions []Extension
	byName     map[string]Extension
	timeout    time.Duration
}

// NewRegistry builds a registry over the given extensions.
//
// An extension with no name, a name another one already has, or a schema outside
// the closed set is refused here rather than at the first request: a registry is
// built once, and every one of these is a mistake that would otherwise surface
// as a missing panel an hour later.
func NewRegistry(timeout time.Duration, extensions ...Extension) (*Registry, error) {
	if timeout <= 0 {
		return nil, fmt.Errorf("reading: the extension timeout must be positive")
	}
	registry := &Registry{
		extensions: make([]Extension, 0, len(extensions)),
		byName:     make(map[string]Extension, len(extensions)),
		timeout:    timeout,
	}
	for _, extension := range extensions {
		if extension == nil {
			return nil, fmt.Errorf("reading: a nil extension was registered")
		}
		name := strings.TrimSpace(extension.Name())
		if name == "" {
			return nil, fmt.Errorf("reading: an extension with no name was registered")
		}
		if _, exists := registry.byName[name]; exists {
			return nil, fmt.Errorf("reading: two extensions are named %q", name)
		}
		if !extension.ResultSchema().Valid() {
			return nil, fmt.Errorf("reading: extension %q declares an unknown result schema %q", name, extension.ResultSchema())
		}
		registry.byName[name] = extension
		registry.extensions = append(registry.extensions, extension)
	}
	return registry, nil
}

// Catalogue lists the extensions and whether each can run, by name.
//
// It asks every extension rather than remembering an answer, because
// availability is about the world outside the extension — what the process was
// assembled with — and a remembered answer would go on saying yes after the
// thing it depends on had gone away.
func (r *Registry) Catalogue() []Availability {
	if r == nil {
		return []Availability{}
	}
	listed := make([]Availability, 0, len(r.extensions))
	for _, extension := range r.extensions {
		entry := Availability{Name: extension.Name(), Schema: extension.ResultSchema()}
		entry.Available, entry.Reason = extension.Available()
		if entry.Available {
			// An available extension has no reason to give, whatever it returned
			// there: a reason beside a yes reads as a caveat.
			entry.Reason = ""
		} else if strings.TrimSpace(entry.Reason) == "" {
			entry.Reason = "the extension did not say why it is unavailable"
		}
		listed = append(listed, entry)
	}
	sort.Slice(listed, func(i, j int) bool { return listed[i].Name < listed[j].Name })
	return listed
}

// refuser is a failure that says the request may not be made at all, as opposed
// to work that was asked for and did not finish.
//
// It is how a refusal gets past this file. Everything an extension does wrong is
// reported as a skipped outcome, and that is right for an extension that could
// not answer — one broken panel must not fail the page. It is wrong for an
// account that may not make the call: that is a fact about the request, and a
// client that sees it as a greyed-out panel with a sentence under it has been
// told the extension is what failed. The interface is unexported here and
// unexported where it is implemented, and discovered structurally, the same way
// the turn runtime finds a failure that knows whether it is retryable.
type refuser interface{ Refused() bool }

// isRefusal reports whether a failure is one of those.
func isRefusal(err error) bool {
	var refused refuser
	return errors.As(err, &refused) && refused.Refused()
}

// Run runs one extension by name.
//
// The error return is for an extension that does not exist, which is a missing
// resource, and for a refusal that stopped the request before any extension ran.
// Everything else — the extension being unavailable, panicking, running out of
// time, or answering in the wrong shape — comes back as a skipped Outcome,
// because those are states of the extension rather than failures of the request
// that asked for it.
func (r *Registry) Run(ctx context.Context, name string, reading Context) (Outcome, error) {
	extension, ok := r.lookup(name)
	if !ok {
		return Outcome{}, fmt.Errorf("%w: extension %s", ErrNotFound, name)
	}
	return r.runOne(ctx, extension, reading)
}

// RunAll runs every extension, each one independently of the others.
//
// The results come back in the catalogue's order, one per registered extension,
// so a caller can pair them up by position as well as by name. One extension
// failing is one skipped entry and never a shorter list.
//
// A refusal is the one failure dropped here, and it is dropped because this
// answer has no room for it: the list is a list, and it cannot be refused as a
// whole by one extension. What the caller sees is the extension skipped with the
// refusal as its reason, which names the item and the moment it comes back —
// the same two facts, in the only place this shape can carry them.
func (r *Registry) RunAll(ctx context.Context, reading Context) []Outcome {
	if r == nil {
		return []Outcome{}
	}
	outcomes := make([]Outcome, 0, len(r.extensions))
	for _, extension := range r.extensions {
		outcome, _ := r.runOne(ctx, extension, reading)
		outcomes = append(outcomes, outcome)
	}
	return outcomes
}

func (r *Registry) lookup(name string) (Extension, bool) {
	if r == nil {
		return nil, false
	}
	extension, ok := r.byName[strings.TrimSpace(name)]
	return extension, ok
}

// extensionRun is one extension's answer, as the goroutine that produced it
// hands it back.
type extensionRun struct {
	payload json.RawMessage
	err     error
}

// runOne runs one extension under this registry's timeout and recover.
//
// The recover is inside the goroutine and not around it, which is the whole
// reason the call runs in a goroutine at all: a panic in another goroutine
// cannot be recovered by the one that started it, and a Go function that ignores
// its context cannot be stopped once it has begun. So the answer is waited for
// with a deadline and then let go — a late extension is a goroutine that
// finishes into a channel nobody is reading, which is a buffered channel of one,
// and not a leak.
func (r *Registry) runOne(ctx context.Context, extension Extension, reading Context) (Outcome, error) {
	outcome := Outcome{Name: extension.Name(), Schema: extension.ResultSchema()}
	if available, reason := extension.Available(); !available {
		return skipped(outcome, reason), nil
	}

	runCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	done := make(chan extensionRun, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				done <- extensionRun{err: fmt.Errorf("the extension panicked: %v", recovered)}
			}
		}()
		payload, err := extension.Run(runCtx, reading)
		done <- extensionRun{payload: payload, err: err}
	}()

	select {
	case <-runCtx.Done():
		// The caller's own cancellation is a different thing from the extension
		// being slow, and the two are told apart here rather than collapsed: a
		// turn the user stopped is not a broken extension.
		if ctx.Err() != nil {
			return skipped(outcome, "the request was cancelled before the extension answered"), nil
		}
		return skipped(outcome, "the extension did not answer within "+r.timeout.String()), nil
	case run := <-done:
		if run.err != nil {
			// The one failure that is not the extension's, and the one reason
			// this returns an error at all: see refuser above.
			if isRefusal(run.err) {
				return outcome, run.err
			}
			return skipped(outcome, run.err.Error()), nil
		}
		if err := validateResult(outcome.Schema, run.payload); err != nil {
			return skipped(outcome, err.Error()), nil
		}
		outcome.Result = run.payload
		return outcome, nil
	}
}

// skipped marks an outcome as one that produced nothing, and gives it a reason.
//
// A skip with no reason is written as one that says so rather than left empty:
// the reader shows this sentence, and a blank line under a greyed-out button is
// the state §7.3 says must be drawable.
func skipped(outcome Outcome, reason string) Outcome {
	outcome.Skipped = true
	outcome.Result = nil
	if strings.TrimSpace(reason) == "" {
		reason = "the extension was skipped without saying why"
	}
	outcome.Reason = reason
	return outcome
}

// stripFences removes the code fence a model may have wrapped its answer in.
//
// It is a formatting habit rather than a mistake, and one this side can undo
// without knowing anything about what is inside: the opening line — the fence
// and whatever language the model named — goes, and so does the closing fence.
// What is left is the answer as the model meant it.
func stripFences(content string) string {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	if index := strings.IndexByte(trimmed, '\n'); index >= 0 {
		trimmed = trimmed[index+1:]
	}
	if index := strings.LastIndex(trimmed, "```"); index >= 0 {
		trimmed = trimmed[:index]
	}
	return strings.TrimSpace(trimmed)
}

// decodeModelJSON reads the JSON an extension's model produced.
//
// Models wrap JSON in a code fence or introduce it with a sentence often enough
// that refusing those answers would throw away work for a formatting habit. What
// is taken is the outermost array or object in the text and nothing else: the
// fence and the prose around it are dropped, and a reply with no JSON in it at
// all comes back as an error rather than as a guess at what was meant.
func decodeModelJSON(content string, target any) error {
	trimmed := stripFences(content)
	start := strings.IndexAny(trimmed, "[{")
	if start < 0 {
		return fmt.Errorf("the model's answer contained no JSON")
	}
	opener := trimmed[start]
	closer := byte('}')
	if opener == '[' {
		closer = ']'
	}
	end := strings.LastIndexByte(trimmed, closer)
	if end < start {
		return fmt.Errorf("the model's answer contained no complete JSON value")
	}
	if err := json.Unmarshal([]byte(trimmed[start:end+1]), target); err != nil {
		return fmt.Errorf("the model's answer was not readable as JSON: %w", err)
	}
	return nil
}

// modelAnswer runs one completion for an extension and returns its text.
//
// Streaming is the only mode the provider has, and an extension has nothing to
// do with the fragments as they arrive: what it needs is the whole answer before
// it can be parsed. So they are collected here, in the one place that needs
// them, rather than pushing a callback through every extension's signature.
func modelAnswer(ctx context.Context, provider llm.Provider, request llm.Request) (string, error) {
	var reply strings.Builder
	if _, err := provider.Stream(ctx, request, func(delta llm.Delta) error {
		reply.WriteString(delta.Content)
		return nil
	}); err != nil {
		return "", err
	}
	return reply.String(), nil
}

// modelConfigured is the availability check every built-in extension makes.
//
// There is one way for a model-backed extension to be unavailable in this build,
// and it is not "the network is down" — that is a failure of one run and the
// registry reports it as a skipped extension with the provider's own words. What
// is decided once, at assembly, is whether there is a model at all, and an
// answer that says so is one a person can act on.
func modelConfigured(provider llm.Provider, model string) (bool, string) {
	if provider == nil || strings.TrimSpace(model) == "" {
		return false, "no model is configured for reading extensions in this build"
	}
	return true, ""
}
