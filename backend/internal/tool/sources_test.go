package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"backend/internal/event"
)

// stubSources is a turn's material, as the tools see it: it answers out of what
// a test put in and refuses a label the turn does not carry, which is the whole
// of the scope rule it stands in for.
type stubSources struct {
	// labels are the attachments this turn carries.
	labels map[string]bool
	// reading is what every read returns.
	reading Reading
	// err is what every read fails with, for the refusals a turn raises itself.
	err error
	// asked is every read the turn accepted, in the order it arrived. A read of
	// a label the turn does not carry is not in it: a real turn refuses before
	// the library is reached, and a stub that recorded it would let a test
	// believe a refused read had been served.
	asked []string
}

var _ Sources = (*stubSources)(nil)

func (s *stubSources) ReadSource(_ context.Context, request ReadRequest) (Reading, error) {
	if !s.labels[request.Label] {
		return Reading{}, &Refusal{Note: fmt.Sprintf("本轮没有编号为「%s」的附件", request.Label)}
	}
	s.asked = append(s.asked, "read "+request.Label)
	return s.reading, s.err
}

func (s *stubSources) Retrieve(_ context.Context, query string) (Reading, error) {
	s.asked = append(s.asked, "retrieve "+query)
	return s.reading, s.err
}

// call builds the invocation a model would make.
func call(t *testing.T, arguments string) Call {
	t.Helper()
	if !json.Valid([]byte(arguments)) {
		t.Fatalf("the test's arguments are not JSON: %s", arguments)
	}
	return Call{ID: "call_1", Name: ReadSourceName, Arguments: json.RawMessage(arguments)}
}

func TestReadSourceRefusesAnAttachmentTheTurnIsNotCarrying(t *testing.T) {
	sources := &stubSources{labels: map[string]bool{"1": true}}
	tool := NewReadSourceTool(sources)

	result, err := tool.Invoke(context.Background(), call(t, `{"label": "7"}`))
	if err != nil {
		t.Fatalf("Invoke() error = %v, want a refusal the model can read", err)
	}
	// A failure rather than content: the model has to know the call did not
	// happen, or it will answer as though it had read something.
	if result.Success {
		t.Errorf("a read of something the turn does not carry was reported as successful: %+v", result)
	}
	if !strings.Contains(result.Content, "7") {
		t.Errorf("the refusal = %q, want it to name what was asked for", result.Content)
	}
	// And the runtime was never reached: the scope is enforced before anything
	// is looked up, not after.
	if len(sources.asked) != 0 {
		t.Errorf("the runtime was asked %v, want nothing", sources.asked)
	}
}

func TestReadSourceAnswersUnderTheLabelTheTurnNumbered(t *testing.T) {
	sources := &stubSources{
		labels: map[string]bool{"1": true},
		reading: Reading{Sources: []event.Source{{
			Index: 3, Title: "笔记.md", Snippet: "拉格朗日中值定理说的是……",
		}}},
	}
	result, err := NewReadSourceTool(sources).Invoke(context.Background(), call(t, `{"label": "1"}`))
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if !result.Success {
		t.Errorf("a read that ran was reported as a failure: %+v", result)
	}
	if got := sources.asked; len(got) != 1 || got[0] != "read 1" {
		t.Fatalf("the runtime was asked %v, want the attachment the turn numbered 1", got)
	}
	if !strings.Contains(result.Content, "[3] 笔记.md") || !strings.Contains(result.Content, "拉格朗日中值定理") {
		t.Errorf("content = %q, want the numbered source and its text", result.Content)
	}
}

func TestReadSourceTakesTheNumberAsEitherAStringOrANumber(t *testing.T) {
	// The numbering is the one place a model's own idea of a number shows, and
	// the two spellings are the same request: failing one of them would cost the
	// user a turn over a difference the model cannot see.
	for _, arguments := range []string{`{"label": "2"}`, `{"label": 2}`} {
		sources := &stubSources{labels: map[string]bool{"2": true}}
		if _, err := NewReadSourceTool(sources).Invoke(context.Background(), call(t, arguments)); err != nil {
			t.Errorf("Invoke(%s) error = %v", arguments, err)
		}
		if got := sources.asked; len(got) != 1 || got[0] != "read 2" {
			t.Errorf("Invoke(%s) asked the runtime %v", arguments, got)
		}
	}
}

func TestReadSourceWithoutALabelIsAMalformedCall(t *testing.T) {
	// A call that never said what to read is not a scope question, and it is
	// answered the way ask_user answers a missing question: an error, which the
	// loop reports as a failed call.
	for _, arguments := range []string{`{}`, `{"label": "  "}`, `{"label": null}`} {
		if _, err := NewReadSourceTool(&stubSources{}).Invoke(context.Background(), call(t, arguments)); err == nil {
			t.Errorf("Invoke(%s) error = nil, want a malformed-call error", arguments)
		}
	}
	if _, err := NewReadSourceTool(&stubSources{}).Invoke(
		context.Background(), call(t, `{"label": {"n": 1}}`)); err == nil {
		t.Error("Invoke(a label that is neither a number nor a string) error = nil")
	}
}

func TestReadSourcePassesTheWindowAndTheQueryThrough(t *testing.T) {
	sources := &recordingSources{stubSources: stubSources{labels: map[string]bool{"1": true}}}
	tool := NewReadSourceTool(sources)
	if _, err := tool.Invoke(context.Background(),
		call(t, `{"label": "1", "offset": 400, "limit": 200}`)); err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if _, err := tool.Invoke(context.Background(),
		call(t, `{"label": "1", "query": " 中值定理 "}`)); err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}

	// Clamping a window and locating inside one document are the runtime's; what
	// the tool owes them is the values the model gave, unaltered.
	if len(sources.requests) != 2 {
		t.Fatalf("the runtime was given %d reads, want two", len(sources.requests))
	}
	if window := sources.requests[0]; window.Offset != 400 || window.Limit != 200 {
		t.Errorf("the window read was given %+v, want the range the model asked for", window)
	}
	if query := sources.requests[1]; query.Query != "中值定理" {
		t.Errorf("the located read was given %q, want the trimmed query", query.Query)
	}
}

// recordingSources keeps every request it was given, so a test can look at what
// the tool passed on and not only at what came back.
type recordingSources struct {
	stubSources
	requests []ReadRequest
}

func (s *recordingSources) ReadSource(ctx context.Context, request ReadRequest) (Reading, error) {
	s.requests = append(s.requests, request)
	return s.stubSources.ReadSource(ctx, request)
}

func TestAReadWithNothingToShowSaysSoRatherThanLookingEmpty(t *testing.T) {
	sources := &stubSources{
		labels: map[string]bool{"1": true},
		reading: Reading{Unavailable: []string{
			"附件「1」（扫描件.png）没有读到：当前模型不支持读取图片，这个附件没有被读入。",
		}},
	}
	result, err := NewReadSourceTool(sources).Invoke(context.Background(), call(t, `{"label": "1"}`))
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if !result.Success {
		t.Errorf("a read that ran was reported as a failure: %+v", result)
	}
	// "The material does not mention this" and "the material could not be read"
	// are different sentences, and only one of them is true here (ATT-04).
	if !strings.Contains(result.Content, "没有找到") {
		t.Errorf("content = %q, want it to say the read found nothing", result.Content)
	}
	if !strings.Contains(result.Content, "不支持读取图片") {
		t.Errorf("content = %q, want the reason the material could not be read", result.Content)
	}
}

func TestRetrieveSearchesWhatTheTurnCarries(t *testing.T) {
	sources := &stubSources{reading: Reading{Sources: []event.Source{{
		Index: 1, Title: "分析讲义.md", Snippet: "一致连续蕴涵连续。",
	}}}}
	result, err := NewRetrieveTool(sources).Invoke(context.Background(), call(t, `{"query": " 一致连续 "}`))
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if !result.Success || !strings.Contains(result.Content, "[1] 分析讲义.md") {
		t.Errorf("result = %+v, want the numbered source", result)
	}
	// No scope travels with the call: which collections are searched is decided
	// by the turn (KB-07).
	if got := sources.asked; len(got) != 1 || got[0] != "retrieve 一致连续" {
		t.Fatalf("the runtime was asked %v", got)
	}
}

func TestRetrieveWithoutAQueryIsAMalformedCall(t *testing.T) {
	for _, arguments := range []string{`{}`, `{"query": "   "}`} {
		if _, err := NewRetrieveTool(&stubSources{}).Invoke(context.Background(), call(t, arguments)); !errors.Is(err, ErrMissingQuery) {
			t.Errorf("Invoke(%s) error = %v, want ErrMissingQuery", arguments, err)
		}
	}
}

func TestAFailureThatIsNotARefusalStaysAFailure(t *testing.T) {
	// The two must not be confused: a refusal is a sentence for the model, and
	// anything else is this service's own trouble, which the loop reports and
	// the log carries. Handing a database error to the model as content would
	// make it read as an answer.
	broken := &stubSources{labels: map[string]bool{"1": true}, err: errors.New("the database stopped answering")}
	result, err := NewReadSourceTool(broken).Invoke(context.Background(), call(t, `{"label": "1"}`))
	if err == nil {
		t.Fatalf("Invoke() = %+v, want the failure to stay one", result)
	}
	if result.Content != "" {
		t.Errorf("a failure was given content: %q", result.Content)
	}
}
