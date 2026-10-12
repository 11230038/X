package agentloop

import (
	"strings"
	"testing"

	"backend/internal/llm"
)

// The messages below are sized against bytesPerToken: "user"/"hi" is 6 bytes, so
// 2 tokens, and the budgets are chosen to land between whole groups.

func msg(role llm.Role, content string) llm.Message {
	return llm.Message{Role: role, Content: content}
}

func toolCallMsg(name, arguments string) llm.Message {
	return llm.Message{
		Role:      llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{{ID: "call_1", Name: name, Arguments: arguments}},
	}
}

func toolResultMsg(content string) llm.Message {
	return llm.Message{Role: llm.RoleTool, ToolCallID: "call_1", Content: content}
}

func contents(messages []llm.Message) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		parts = append(parts, message.Content)
	}
	return strings.Join(parts, "|")
}

// groupIntact asserts the invariant that makes a trimmed conversation still
// sendable: an assistant message asking for tools keeps the answers to those
// calls, because a provider rejects a tool result whose call is missing.
func groupIntact(t *testing.T, messages []llm.Message) {
	t.Helper()
	for i, message := range messages {
		if message.Role != llm.RoleAssistant || len(message.ToolCalls) == 0 {
			continue
		}
		for j := range message.ToolCalls {
			answer := i + 1 + j
			if answer >= len(messages) || messages[answer].Role != llm.RoleTool {
				t.Fatalf("message %d asked for tools with no answer following it: %s", i, contents(messages))
			}
		}
	}
}

func TestTrimLeavesAConversationThatFits(t *testing.T) {
	messages := []llm.Message{
		msg(llm.RoleSystem, "sys"),
		msg(llm.RoleUser, "hi"),
	}

	trimmed := Trim(messages, 32000)

	if len(trimmed) != 2 {
		t.Fatalf("Trim() returned %d messages, want both: %s", len(trimmed), contents(trimmed))
	}
	if contents(trimmed) != "sys|hi" {
		t.Errorf("Trim() = %q", contents(trimmed))
	}
}

func TestTrimDoesNothingWithoutABudget(t *testing.T) {
	// A zero budget would otherwise trim everything: it is the absence of a
	// budget, not a budget of zero.
	messages := []llm.Message{
		msg(llm.RoleSystem, strings.Repeat("s", 600)),
		msg(llm.RoleUser, strings.Repeat("u", 600)),
	}

	for _, budget := range []int{0, -1} {
		trimmed := Trim(messages, budget)
		if len(trimmed) != 2 {
			t.Errorf("Trim(budget=%d) returned %d messages, want both", budget, len(trimmed))
		}
	}
}

func TestTrimDropsTheOldestFirst(t *testing.T) {
	messages := []llm.Message{
		msg(llm.RoleSystem, "sys"),
		msg(llm.RoleUser, "aaa"),
		msg(llm.RoleAssistant, "bbb"),
		msg(llm.RoleUser, "ccc"),
	}

	trimmed := Trim(messages, 7)

	if contents(trimmed) != "sys|ccc" {
		t.Errorf("Trim() = %q, want the prompt and the current question", contents(trimmed))
	}
}

func TestTrimKeepsTheSystemPrompt(t *testing.T) {
	// The prompt is what makes the model behave as this capability; dropping it
	// to fit would answer the question as a different assistant.
	messages := []llm.Message{
		msg(llm.RoleSystem, strings.Repeat("s", 300)),
		msg(llm.RoleUser, "hi"),
	}

	trimmed := Trim(messages, 10)

	if len(trimmed) != 2 || !strings.HasPrefix(trimmed[0].Content, "s") {
		t.Fatalf("Trim() = %q, want the prompt kept even over budget", contents(trimmed))
	}
}

func TestTrimKeepsTheTurnInProgress(t *testing.T) {
	// Everything from the last user message on is the question and the work done
	// answering it. Nothing there is droppable, so a budget that cannot fit it is
	// overrun rather than met by cutting the answer short.
	messages := []llm.Message{
		msg(llm.RoleUser, "old"),
		msg(llm.RoleAssistant, "old"),
		msg(llm.RoleUser, "new"),
		msg(llm.RoleAssistant, "reply"),
	}

	trimmed := Trim(messages, 5)

	if contents(trimmed) != "new|reply" {
		t.Errorf("Trim() = %q, want the current question and answer", contents(trimmed))
	}
}

func TestTrimDropsACallAndItsAnswersTogether(t *testing.T) {
	messages := []llm.Message{
		msg(llm.RoleUser, "old"),
		toolCallMsg("ask_user", `{"question":"a"}`),
		toolResultMsg("first"),
		toolCallMsg("ask_user", `{"question":"b"}`),
		toolResultMsg("second"),
		msg(llm.RoleUser, "q"),
	}

	trimmed := Trim(messages, 20)

	if contents(trimmed) != "|second|q" {
		t.Errorf("Trim() = %q, want the whole older call dropped", contents(trimmed))
	}
	groupIntact(t, trimmed)
}

func TestTrimNeverOrphansAToolResult(t *testing.T) {
	// The budget is set below what the turn in progress costs, so the only way to
	// meet it would be to split a call from its answer. It must not: the turn
	// stays over budget and still works.
	messages := []llm.Message{
		msg(llm.RoleSystem, "sys"),
		msg(llm.RoleUser, "old"),
		msg(llm.RoleUser, "q"),
		toolCallMsg("ask_user", `{}`),
		toolResultMsg("answer"),
	}

	trimmed := Trim(messages, 1)

	groupIntact(t, trimmed)
	// Everything left is protected, so the budget is overrun rather than met by
	// cutting a call away from the answer that belongs to it.
	if contents(trimmed) != "sys|q||answer" {
		t.Fatalf("Trim() = %q, want the prompt and the turn in progress", contents(trimmed))
	}
	if trimmed[2].Role != llm.RoleAssistant || trimmed[3].Role != llm.RoleTool {
		t.Errorf("the protected turn lost its tool pair: %s", contents(trimmed))
	}
}

func TestTrimTreatsAnAssistantMessageWithNoToolCallsAlone(t *testing.T) {
	// Plain text has no companion message, so it is droppable on its own.
	messages := []llm.Message{
		msg(llm.RoleUser, "old"),
		msg(llm.RoleAssistant, strings.Repeat("a", 60)),
		msg(llm.RoleUser, "q"),
	}

	trimmed := Trim(messages, 15)

	if contents(trimmed) != "q" {
		t.Errorf("Trim() = %q, want the old answer dropped on its own", contents(trimmed))
	}
}

func TestTrimLeavesTheInputAlone(t *testing.T) {
	messages := []llm.Message{
		msg(llm.RoleUser, "aaa"),
		msg(llm.RoleAssistant, "bbb"),
		msg(llm.RoleUser, "ccc"),
	}
	before := contents(messages)

	Trim(messages, 4)

	if contents(messages) != before {
		t.Errorf("Trim() modified its input: %q", contents(messages))
	}
	if len(messages) != 3 {
		t.Errorf("Trim() changed the input's length to %d", len(messages))
	}
}

func TestTrimHandlesAnEmptyConversation(t *testing.T) {
	if trimmed := Trim(nil, 10); len(trimmed) != 0 {
		t.Errorf("Trim(nil) returned %d messages", len(trimmed))
	}
}
