package reading

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// stubExtension is one reading aid built by hand, so that every way an extension
// can misbehave is something a test can ask for.
type stubExtension struct {
	name    string
	schema  ResultSchema
	ready   bool
	reason  string
	payload json.RawMessage
	err     error
	panics  bool
	delay   time.Duration
	// seen records what the extension was handed, which is how the confinement
	// rule is checked: an extension can only see what was passed in.
	seen *Context
}

func (e *stubExtension) Name() string               { return e.name }
func (e *stubExtension) ResultSchema() ResultSchema { return e.schema }
func (e *stubExtension) Available() (bool, string)  { return e.ready, e.reason }

func (e *stubExtension) Run(ctx context.Context, reading Context) (json.RawMessage, error) {
	if e.seen != nil {
		copied := reading
		e.seen = &copied
	}
	if e.panics {
		panic("this extension is broken")
	}
	if e.delay > 0 {
		select {
		case <-time.After(e.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return e.payload, e.err
}

// working is an extension that runs and answers.
func working(name string, schema ResultSchema, payload string) *stubExtension {
	return &stubExtension{name: name, schema: schema, ready: true, payload: json.RawMessage(payload)}
}

func newTestRegistry(t *testing.T, extensions ...Extension) *Registry {
	t.Helper()
	registry, err := NewRegistry(time.Second, extensions...)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	return registry
}

func TestABrokenExtensionIsSkippedAndTheOthersRun(t *testing.T) {
	registry := newTestRegistry(t,
		working("cards", ResultCards, `[{"title":"甲","body":"乙"}]`),
		&stubExtension{name: "broken", schema: ResultText, ready: true, panics: true},
		&stubExtension{name: "slow", schema: ResultText, ready: true, delay: time.Hour},
		&stubExtension{name: "wrong_shape", schema: ResultText, ready: true, payload: json.RawMessage(`{"not":"a string"}`)},
		&stubExtension{name: "failing", schema: ResultText, ready: true, err: errors.New("the model is unreachable")},
		&stubExtension{name: "unavailable", schema: ResultText, reason: "没有配置模型"},
	)

	outcomes := registry.RunAll(context.Background(), Context{MaterialID: "rm_a", Text: "原文"})
	if len(outcomes) != 6 {
		t.Fatalf("RunAll() returned %d outcomes, want one per extension", len(outcomes))
	}
	byName := make(map[string]Outcome, len(outcomes))
	for _, outcome := range outcomes {
		byName[outcome.Name] = outcome
	}

	// The one that works came back whole, and it is untouched by the four that
	// did not: one extension failing never costs another its result.
	good := byName["cards"]
	if good.Skipped || len(good.Result) == 0 || good.Reason != "" {
		t.Errorf("the working extension came back as %+v", good)
	}
	for name, want := range map[string]string{
		"broken":      "panicked",
		"slow":        "did not answer within",
		"wrong_shape": "must be a JSON string",
		"failing":     "unreachable",
		"unavailable": "没有配置模型",
	} {
		outcome := byName[name]
		if !outcome.Skipped {
			t.Errorf("%s produced a result instead of being skipped: %+v", name, outcome)
			continue
		}
		if !strings.Contains(outcome.Reason, want) {
			t.Errorf("%s was skipped because %q, want it to mention %q", name, outcome.Reason, want)
		}
		if len(outcome.Result) != 0 {
			t.Errorf("%s carries a result as well as a reason: %s", name, outcome.Result)
		}
	}
}

func TestAnExtensionThatTookTooLongIsLetGo(t *testing.T) {
	registry, err := NewRegistry(20*time.Millisecond,
		&stubExtension{name: "slow", schema: ResultText, ready: true, delay: time.Hour})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	started := time.Now()
	outcome, err := registry.Run(context.Background(), "slow", Context{MaterialID: "rm_a"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// The answer is waited for with a deadline rather than stopped: an extension
	// that ignores its context finishes into a channel nobody reads.
	if !outcome.Skipped || !strings.Contains(outcome.Reason, "did not answer within") {
		t.Errorf("a slow extension came back as %+v", outcome)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("the request waited %s for an extension that was already given up on", elapsed)
	}
}

func TestTheCallersOwnCancellationIsNotABrokenExtension(t *testing.T) {
	registry, err := NewRegistry(time.Hour,
		&stubExtension{name: "slow", schema: ResultText, ready: true, delay: time.Hour})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	outcome, err := registry.Run(ctx, "slow", Context{MaterialID: "rm_a"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// A turn the reader stopped is not an extension with a problem, and the
	// reason says so rather than blaming the extension's author. Which of the two
	// ends notices first is a race — the registry's own check and the extension
	// being handed a cancelled context — so the reason is only asked to be about
	// cancellation rather than one particular wording of it.
	if !outcome.Skipped || !strings.Contains(outcome.Reason, "cancel") {
		t.Errorf("a cancelled request came back as %+v", outcome)
	}
}

func TestTheCatalogueSaysWhichExtensionsCanRunAndWhyNot(t *testing.T) {
	registry := newTestRegistry(t,
		working("explain", ResultText, `"讲清楚了"`),
		&stubExtension{name: "audio", schema: ResultAudioRef, reason: "这台部署没有语音合成"},
		&stubExtension{name: "mute", schema: ResultText},
	)

	catalogue := registry.Catalogue()
	if len(catalogue) != 3 {
		t.Fatalf("Catalogue() = %+v", catalogue)
	}
	// Sorted by name: the order is the registry's to fix, so that a client
	// pairing a catalogue with a result list by position can.
	for index, want := range []string{"audio", "explain", "mute"} {
		if catalogue[index].Name != want {
			t.Fatalf("Catalogue() is ordered %+v", catalogue)
		}
	}
	if !catalogue[1].Available || catalogue[1].Reason != "" {
		t.Errorf("an available extension came back as %+v", catalogue[1])
	}
	if catalogue[0].Available || !strings.Contains(catalogue[0].Reason, "语音合成") {
		t.Errorf("an unavailable extension came back as %+v", catalogue[0])
	}
	// A false with no reason is a state nobody can act on, so the registry says
	// so on the extension's behalf rather than passing the silence through.
	if catalogue[2].Available || strings.TrimSpace(catalogue[2].Reason) == "" {
		t.Errorf("an unavailable extension that gave no reason came back as %+v", catalogue[2])
	}
}

func TestAnExtensionSeesOnlyThePassage(t *testing.T) {
	seen := &Context{}
	registry := newTestRegistry(t, &stubExtension{
		name: "watcher", schema: ResultText, ready: true, payload: json.RawMessage(`"好"`), seen: seen,
	})

	if _, err := registry.Run(context.Background(), "watcher", Context{
		MaterialID: "rm_a",
		Locator:    Locator{MaterialID: "rm_a", Ordinal: 3, CharStart: 10, CharEnd: 20},
		Text:       "闭包捕获的是变量",
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// The confinement is structural rather than a rule the extension follows:
	// the three fields below are the whole of what it was handed, so there is no
	// user message, no conversation and no second material for it to reach.
	if seen.MaterialID != "rm_a" || seen.Text != "闭包捕获的是变量" {
		t.Errorf("the extension was handed %+v", seen)
	}
	if seen.Locator.CharStart != 10 || seen.Locator.CharEnd != 20 || seen.Locator.Ordinal != 3 {
		t.Errorf("the extension was handed the locator %+v", seen.Locator)
	}
}

func TestAnUnknownExtensionIsAMissingResource(t *testing.T) {
	registry := newTestRegistry(t, working("explain", ResultText, `"讲清楚了"`))

	if _, err := registry.Run(context.Background(), "translate", Context{MaterialID: "rm_a"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Run(translate) error = %v, want a missing resource", err)
	}
}

func TestARegistryRefusesWhatItCannotServe(t *testing.T) {
	for _, test := range []struct {
		name       string
		extensions []Extension
		want       string
	}{
		{name: "an extension with no name", extensions: []Extension{&stubExtension{schema: ResultText}}, want: "no name"},
		{
			name: "two extensions with one name",
			extensions: []Extension{
				working("explain", ResultText, `"甲"`), working("explain", ResultText, `"乙"`),
			},
			want: "are named",
		},
		{
			// The set of shapes is closed because the reader has a renderer for
			// exactly these: a sixth shape reaches the interface as something it
			// cannot draw.
			name:       "a schema outside the closed set",
			extensions: []Extension{&stubExtension{name: "chart", schema: ResultSchema("chart"), ready: true}},
			want:       "unknown result schema",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewRegistry(time.Second, test.extensions...)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Errorf("NewRegistry() error = %v, want it to mention %q", err, test.want)
			}
		})
	}
	if _, err := NewRegistry(0, working("explain", ResultText, `"甲"`)); err == nil {
		t.Error("NewRegistry() accepted a timeout of zero, which is an extension that can never answer")
	}
}

func TestTheResultBoundsAreTheReaders(t *testing.T) {
	// Every bound is here rather than in each extension, so one test says what
	// the registry will and will not let through.
	long := strings.Repeat("字", maxResultTextChars+1)
	for _, test := range []struct {
		name    string
		schema  ResultSchema
		payload string
		refused bool
	}{
		{name: "a text result", schema: ResultText, payload: `"讲清楚了"`},
		{name: "a blank text result", schema: ResultText, payload: `"   "`, refused: true},
		{name: "a text result over the bound", schema: ResultText, payload: fmt.Sprintf("%q", long), refused: true},
		{name: "an empty cards result", schema: ResultCards, payload: `[]`},
		{name: "a card with no title", schema: ResultCards, payload: `[{"body":"没有标题"}]`, refused: true},
		{name: "an empty timeline", schema: ResultTimeline, payload: `[]`, refused: true},
		{name: "a quiz question with one choice", schema: ResultQuiz,
			payload: `[{"question":"哪一个？","choices":["甲"]}]`, refused: true},
		{name: "an audio reference with no url", schema: ResultAudioRef, payload: `{}`, refused: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateResult(test.schema, json.RawMessage(test.payload))
			if test.refused && err == nil {
				t.Errorf("validateResult(%s, %s) was accepted", test.schema, test.payload)
			}
			if !test.refused && err != nil {
				t.Errorf("validateResult(%s, %s) error = %v", test.schema, test.payload, err)
			}
		})
	}
}
