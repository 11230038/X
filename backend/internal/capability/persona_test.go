package capability

import (
	"strings"
	"testing"

	"backend/internal/llm"
)

// TestAPersonaIsASystemMessageBehindAFixedFrame pins the two things the frame
// says: the voice is this turn's, and it does not change what the turn may do.
// The second half is not decoration — a model told its persona outranks the
// rules would treat someone's prose as a permission.
func TestAPersonaIsASystemMessageBehindAFixedFrame(t *testing.T) {
	message := PersonaMessage("  请用诗意的口吻回答。\n")

	if message.Role != llm.RoleSystem {
		t.Fatalf("Role = %q, want a system message", message.Role)
	}
	if message.Content != personaPrefix+"请用诗意的口吻回答。" {
		t.Errorf("the message is %q, want the frame followed by the trimmed instruction", message.Content)
	}
	for _, phrase := range []string{"这一轮", "不改变", "权限"} {
		if !strings.Contains(personaPrefix, phrase) {
			t.Errorf("the frame no longer says %q:\n%s", phrase, personaPrefix)
		}
	}
}

// TestNoPersonaIsNoMessage: an empty persona is the absence of one, not a blank
// system turn. Sending a zero message would make the two indistinguishable to
// the model and would put an empty instruction in front of every turn.
func TestNoPersonaIsNoMessage(t *testing.T) {
	for _, instruction := range []string{"", "   ", "\n\t"} {
		got := PersonaMessage(instruction)
		if got.Role != "" || got.Content != "" || len(got.ToolCalls) != 0 {
			t.Errorf("PersonaMessage(%q) = %+v, want no message", instruction, got)
		}
	}
	history := []llm.Message{{Role: llm.RoleUser, Content: "你好"}}
	if messages := Messages("system", history, PersonaMessage("")); len(messages) != 2 {
		t.Errorf("Messages() added %d messages, want only the instruction and the history: %+v", len(messages), messages)
	}
}
