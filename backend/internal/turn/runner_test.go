package turn

import (
	"context"
	"errors"
	"testing"
	"time"

	"backend/internal/capability"
	"backend/internal/event"
	"backend/internal/llm"
	"backend/internal/tool"
)

// asking is a model that asks the user a question, then answers with what it
// was told.
func asking(question, answer string) []llm.Response {
	return []llm.Response{
		{ToolCalls: []llm.ToolCall{{
			ID: "call_1", Name: tool.AskUserName, Arguments: `{"question":"` + question + `"}`,
		}}},
		{Content: answer},
	}
}

func TestATurnStopsToAskAndCarriesOnWithTheAnswer(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.provider.responses = asking("部署到哪个环境？", "好，部署到预发")
	fixture.register(t, looping("chat", agentRequest()))

	started := fixture.start(t, StartRequest{Content: "帮我部署"})
	subscription, err := fixture.runner.Subscribe(context.Background(), started.Turn.ID, 0)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer subscription.Close()

	question := until(t, subscription, event.TypeWaitForInput)
	if question.Content != "部署到哪个环境？" {
		t.Errorf("wait_for_input = %q, want the model's question", question.Content)
	}
	// The turn is stored as waiting, not merely paused in memory: a client that
	// reconnects has to be told by check_active_turn that it may answer.
	if stored := fixture.repository.mustTurn(t, started.Turn.ID); stored.Status != StatusWaitingInput {
		t.Fatalf("Status = %s while the turn is waiting for an answer", stored.Status)
	}

	if err := fixture.runner.SubmitReply(context.Background(), started.Turn.ID, "预发"); err != nil {
		t.Fatalf("SubmitReply() error = %v", err)
	}

	seen := drain(t, subscription)
	done := only(t, seen, event.TypeDone)
	if got := done.Metadata[event.MetaStatus]; got != string(StatusCompleted) {
		t.Errorf("done status = %v, want %q", got, StatusCompleted)
	}

	// The answer reaches the model as the result of the call it made, which is
	// the only way it can act on it.
	sent := fixture.provider.asked()
	if len(sent) != 2 {
		t.Fatalf("the model was called %d times, want a second call after the answer", len(sent))
	}
	answer := sent[1].Messages[len(sent[1].Messages)-1]
	if answer.Role != llm.RoleTool || answer.Content != "预发" {
		t.Errorf("the last message is %+v, want the answer as the tool's result", answer)
	}
	if answer.ToolCallID != "call_1" {
		t.Errorf("ToolCallID = %q, want the call it answers", answer.ToolCallID)
	}

	stored := fixture.repository.awaitStatus(t, started.Turn.ID, StatusCompleted)
	if stored.StateVersion != 4 {
		// queued → running → waiting_input → running → completed.
		t.Errorf("StateVersion = %d, want the turn to have moved four times", stored.StateVersion)
	}
}

func TestAQuestionThatIsNeverAnsweredEndsTheTurn(t *testing.T) {
	fixture := newRunnerFixture(t, func(o *Options) { o.InputTimeout = 30 * time.Millisecond })
	fixture.provider.responses = asking("要继续吗？", "继续了")
	fixture.register(t, looping("chat", agentRequest()))

	started := fixture.start(t, StartRequest{Content: "开始"})
	seen := fixture.watch(t, started.Turn.ID)

	// A turn that waited and heard nothing is over, not pending forever: it
	// holds its session's turn slot while it waits.
	failed := only(t, seen, event.TypeError)
	if got := failed.Metadata[event.MetaErrorCode]; got != CodeInputTimeout {
		t.Errorf("error_code = %v, want %q", got, CodeInputTimeout)
	}
	if got := failed.Metadata[event.MetaRetryable]; got != true {
		t.Errorf("retryable = %v, want an unanswered question to be worth asking again", got)
	}
	stored := fixture.repository.awaitStatus(t, started.Turn.ID, StatusFailed)
	if stored.FailureCode != CodeInputTimeout {
		t.Errorf("FailureCode = %q, want %q", stored.FailureCode, CodeInputTimeout)
	}
}

func TestCancellingWhileWaitingEndsTheTurnAsCancelled(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.provider.responses = asking("要继续吗？", "继续了")
	fixture.register(t, looping("chat", agentRequest()))

	started := fixture.start(t, StartRequest{Content: "开始"})
	subscription, err := fixture.runner.Subscribe(context.Background(), started.Turn.ID, 0)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer subscription.Close()
	until(t, subscription, event.TypeWaitForInput)

	if err := fixture.runner.Cancel(context.Background(), started.Turn.ID); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}

	seen := drain(t, subscription)
	done := only(t, seen, event.TypeDone)
	if got := done.Metadata[event.MetaStatus]; got != string(StatusCancelled) {
		t.Errorf("done status = %v, want %q", got, StatusCancelled)
	}
	// A client's own cancel is not a failure of the turn, and reporting it as
	// one would put an error on screen for something the user asked for.
	if kinds := kindsOf(seen); contains(kinds, event.TypeError) {
		t.Errorf("stream = %v, want no error event for a cancelled turn", kinds)
	}
	stored := fixture.repository.awaitStatus(t, started.Turn.ID, StatusCancelled)
	if stored.Error != "" || stored.FailureCode != "" {
		t.Errorf("the cancelled turn records %q/%q, want neither", stored.Error, stored.FailureCode)
	}
}

func TestATurnOnlyEndsOnce(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.provider.responses = asking("要继续吗？", "继续了")
	fixture.register(t, looping("chat", agentRequest()))

	started := fixture.start(t, StartRequest{Content: "开始"})
	subscription, err := fixture.runner.Subscribe(context.Background(), started.Turn.ID, 0)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer subscription.Close()
	until(t, subscription, event.TypeWaitForInput)

	// A cancel arriving as the turn is being answered is the case where two
	// endings could be written. Only one may be.
	if err := fixture.runner.SubmitReply(context.Background(), started.Turn.ID, "继续"); err != nil {
		t.Fatalf("SubmitReply() error = %v", err)
	}
	if err := fixture.runner.Cancel(context.Background(), started.Turn.ID); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}

	seen := drain(t, subscription)
	if endings := countOf(seen, event.TypeDone); endings != 1 {
		t.Errorf("the stream has %d done events, want exactly one", endings)
	}
	if stored := fixture.repository.mustTurn(t, started.Turn.ID); !stored.Status.Terminal() {
		t.Errorf("Status = %s, want a terminal state", stored.Status)
	}
}

func TestARepeatedAnswerIsNotActedOnTwice(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.provider.responses = asking("要继续吗？", "继续了")
	fixture.register(t, looping("chat", agentRequest()))

	started := fixture.start(t, StartRequest{Content: "开始"})
	subscription, err := fixture.runner.Subscribe(context.Background(), started.Turn.ID, 0)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer subscription.Close()
	until(t, subscription, event.TypeWaitForInput)

	if err := fixture.runner.SubmitReply(context.Background(), started.Turn.ID, "继续"); err != nil {
		t.Fatalf("SubmitReply() error = %v", err)
	}
	// A repeat is what a client sends when it did not see the first one land.
	// Whether it is refused or simply never used, it must not reach the model a
	// second time: a question answered twice is a conversation that ran on a
	// reply the user only gave once (WS-09).
	if err := fixture.runner.SubmitReply(context.Background(), started.Turn.ID, "继续"); err != nil && !errors.Is(err, ErrNotWaiting) {
		t.Errorf("SubmitReply() error = %v, want the repeat refused or dropped", err)
	}
	drain(t, subscription)

	asked := fixture.provider.asked()
	answers := 0
	for _, message := range asked[len(asked)-1].Messages {
		if message.Role == llm.RoleTool {
			answers++
		}
	}
	if answers != 1 {
		t.Errorf("the model was given %d answers, want the one that was submitted", answers)
	}
}

func TestAnAnswerToATurnThatIsNotWaitingIsRefused(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.register(t, answering("chat", "我在"))

	started := fixture.start(t, StartRequest{Content: "在吗"})
	fixture.repository.awaitStatus(t, started.Turn.ID, StatusCompleted)

	if err := fixture.runner.SubmitReply(context.Background(), started.Turn.ID, "在"); !errors.Is(err, ErrNotWaiting) {
		t.Errorf("SubmitReply() error = %v, want ErrNotWaiting", err)
	}
}

func TestAnEmptyAnswerIsRefused(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.provider.responses = asking("要继续吗？", "继续了")
	fixture.register(t, looping("chat", agentRequest()))

	started := fixture.start(t, StartRequest{Content: "开始"})
	subscription, err := fixture.runner.Subscribe(context.Background(), started.Turn.ID, 0)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer subscription.Close()
	until(t, subscription, event.TypeWaitForInput)

	if err := fixture.runner.SubmitReply(context.Background(), started.Turn.ID, "  "); !errors.Is(err, ErrEmptyReply) {
		t.Fatalf("SubmitReply() error = %v, want ErrEmptyReply", err)
	}
	// The turn is still waiting: refusing the answer must not consume the wait.
	if stored := fixture.repository.mustTurn(t, started.Turn.ID); stored.Status != StatusWaitingInput {
		t.Errorf("Status = %s, want the turn to still be waiting", stored.Status)
	}
	fixture.runner.Cancel(context.Background(), started.Turn.ID)
	drain(t, subscription)
}

func TestAModelThatStopsAnsweringEndsTheTurn(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.provider.err = errors.New("deepseek returned 503")
	fixture.register(t, looping("chat", agentRequest()))

	started := fixture.start(t, StartRequest{Content: "在吗"})
	seen := fixture.watch(t, started.Turn.ID)

	failed := only(t, seen, event.TypeError)
	if got := failed.Metadata[event.MetaErrorCode]; got != CodeInternalError {
		t.Errorf("error_code = %v, want %q", got, CodeInternalError)
	}
	stored := fixture.repository.awaitStatus(t, started.Turn.ID, StatusFailed)
	// The operator gets the provider's own words; the client does not.
	if stored.Error == "" {
		t.Error("the failed turn recorded no reason for an operator to read")
	}
	if failed.Content == "" || failed.Content == stored.Error {
		t.Errorf("the client was given %q, want a message about the failure rather than the provider's", failed.Content)
	}
	if got := failed.Metadata[event.MetaPartialResponse]; got != false {
		t.Errorf("partial_response = %v, want false for a turn that said nothing", got)
	}
}

func TestATurnThatSaidSomethingBeforeFailingSaysSo(t *testing.T) {
	fixture := newRunnerFixture(t)
	// The model speaks, asks for a tool, and then stops answering: whatever it
	// said first has already reached the client.
	fixture.provider.responses = []llm.Response{{
		Content:   "前半句",
		ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "read_file", Arguments: `{}`}},
	}}
	fixture.provider.failAfter = 1
	fixture.register(t, looping("chat", agentRequest()))

	started := fixture.start(t, StartRequest{Content: "在吗"})
	seen := fixture.watch(t, started.Turn.ID)

	failed := only(t, seen, event.TypeError)
	if got := failed.Metadata[event.MetaPartialResponse]; got != true {
		t.Errorf("partial_response = %v, want true for a turn that had spoken", got)
	}
	if countOf(seen, event.TypeContent) == 0 {
		t.Error("the stream carries no text, so nothing partial was reported")
	}
	// What was streamed is not stored: the conversation keeps the question, and
	// the failure is what the client is left with. Only a capability's result
	// becomes an assistant message.
	stored := fixture.repository.awaitStatus(t, started.Turn.ID, StatusFailed)
	if stored.AssistantMessageID != "" {
		t.Error("a failed turn stored no answer but points at one")
	}
}

func TestRunningOutOfRoundsIsReportedAsSuch(t *testing.T) {
	fixture := newRunnerFixture(t)
	// A model that keeps asking for a tool it cannot have, until the limit
	// stops it: the failure is the loop's, and a client can act on that.
	fixture.provider.stuck = llm.ToolCall{ID: "call_1", Name: "read_file", Arguments: `{}`}
	request := agentRequest()
	request.MaxRounds = 2
	fixture.register(t, looping("chat", request))

	started := fixture.start(t, StartRequest{Content: "读一下这个文件"})
	seen := fixture.watch(t, started.Turn.ID)

	failed := only(t, seen, event.TypeError)
	if got := failed.Metadata[event.MetaErrorCode]; got != CodeRoundsExhausted {
		t.Errorf("error_code = %v, want %q", got, CodeRoundsExhausted)
	}
	stored := fixture.repository.awaitStatus(t, started.Turn.ID, StatusFailed)
	if stored.Retryable {
		t.Error("Retryable is true for a turn that ran out of rounds")
	}
}

func TestTheLoopIsGivenTheConversationSoFar(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.provider.responses = []llm.Response{{Content: "我在"}}
	fixture.register(t, looping("chat", agentRequest()))

	first := fixture.start(t, StartRequest{Content: "在吗"})
	fixture.repository.awaitStatus(t, first.Turn.ID, StatusCompleted)

	fixture.provider.responses = []llm.Response{{Content: "还在"}}
	second := fixture.start(t, StartRequest{SessionID: first.Session.ID, Content: "还在吗"})
	fixture.repository.awaitStatus(t, second.Turn.ID, StatusCompleted)

	asked := fixture.provider.asked()
	last := asked[len(asked)-1]
	// A turn that could not see the conversation would answer every question as
	// though it were the first.
	if len(last.Messages) != 3 {
		t.Fatalf("the model was given %d messages: %+v", len(last.Messages), last.Messages)
	}
	want := []struct {
		role    llm.Role
		content string
	}{
		{llm.RoleUser, "在吗"},
		{llm.RoleAssistant, "我在"},
		{llm.RoleUser, "还在吗"},
	}
	for i, expected := range want {
		if last.Messages[i].Role != expected.role || last.Messages[i].Content != expected.content {
			t.Errorf("message %d = %+v, want %s %q", i, last.Messages[i], expected.role, expected.content)
		}
	}
}

func TestACapabilityThatFailsEndsTheTurnWithoutAModel(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.register(t, stubCapability{
		manifest: capability.Manifest{Name: "chat", Description: "breaks", Stages: []string{"responding"}},
		run:      func(context.Context, capability.RunContext) error { return errors.New("the capability gave up") },
	})

	started := fixture.start(t, StartRequest{Content: "在吗"})
	seen := fixture.watch(t, started.Turn.ID)

	if countOf(seen, event.TypeDone) != 1 {
		t.Error("a capability that failed still has to end its turn once")
	}
	stored := fixture.repository.awaitStatus(t, started.Turn.ID, StatusFailed)
	if stored.Error != "the capability gave up" {
		t.Errorf("Error = %q, want what the capability reported", stored.Error)
	}
}

func TestATurnIsNotRunTwice(t *testing.T) {
	fixture := newRunnerFixture(t)
	ran := 0
	fixture.register(t, stubCapability{
		manifest: capability.Manifest{Name: "chat", Description: "counts", Stages: []string{"responding"}},
		run: func(ctx context.Context, rc capability.RunContext) error {
			ran++
			return rc.Emitter().Emit(ctx, event.Event{Type: event.TypeResult, Content: "一次"})
		},
	})

	started := fixture.start(t, StartRequest{Content: "在吗"})
	fixture.repository.awaitStatus(t, started.Turn.ID, StatusCompleted)
	fixture.watch(t, started.Turn.ID)

	if ran != 1 {
		t.Errorf("the capability ran %d times, want once per turn", ran)
	}
}
