package ws

import (
	"fmt"
	"testing"

	"backend/internal/event"
	"backend/internal/turn"
)

// TestAReaderSelectionTravelsWithTheTurnThatAsksAboutIt pins the wire form of
// §2.14's optional selection: the command that starts an immersive reading turn
// carries the passage the reader had chosen, spelled the way a citation's
// coordinate is spelled.
func TestAReaderSelectionTravelsWithTheTurnThatAsksAboutIt(t *testing.T) {
	fixture := newWSFixture(t)
	client := fixture.connect(t)

	client.send(map[string]any{
		"type": CommandStartTurn, "capability": "immersive_reading", "content": "这里为什么是 O(n log n)？",
		"selection": map[string]any{
			"material_id": "rm_a",
			"locator": map[string]any{
				"kind": "window", "ordinal": 7, "char_start": 120, "char_end": 138,
			},
			"text": "快速排序每次把枢轴放到它最终的位置上",
		},
	})
	client.recvOf(FrameCommandAck)

	starts := fixture.turns.startRequests()
	if len(starts) != 1 {
		t.Fatalf("the runtime was asked to start %+v", starts)
	}
	selected := starts[0].Selection
	if selected == nil {
		t.Fatal("the turn reached the runtime with no selection")
	}
	if selected.MaterialID != "rm_a" {
		t.Errorf("material = %q, want the one the reader was reading", selected.MaterialID)
	}
	// The coordinate arrives whole, minus what the client did not send: the page
	// is optional on the wire because not every engine can place its text on one.
	want := event.Locator{Kind: "window", Ordinal: 7, CharStart: 120, CharEnd: 138}
	if selected.Locator != want {
		t.Errorf("locator = %+v, want %+v", selected.Locator, want)
	}
	if selected.Text != "快速排序每次把枢轴放到它最终的位置上" {
		t.Errorf("text = %q, want the reader's own copy of the passage", selected.Text)
	}
}

// TestATurnWithoutASelectionCarriesNone is the other half: an absent selection
// stays absent. A turn with none and a turn whose selection names nothing are
// different things, and only the second is the client's mistake.
func TestATurnWithoutASelectionCarriesNone(t *testing.T) {
	fixture := newWSFixture(t)
	client := fixture.connect(t)

	client.send(map[string]any{"type": CommandStartTurn, "capability": "chat", "content": "在吗"})
	client.recvOf(FrameCommandAck)

	starts := fixture.turns.startRequests()
	if len(starts) != 1 || starts[0].Selection != nil {
		t.Errorf("the runtime was asked to start %+v, want no selection", starts)
	}
}

// TestASelectionThatCannotBeASpanIsReportedAsAMalformedCommand pins what the
// client is told: the command's own payload cannot be acted on, and the
// sentence saying which part of the coordinate is wrong is the runtime's.
func TestASelectionThatCannotBeASpanIsReportedAsAMalformedCommand(t *testing.T) {
	fixture := newWSFixture(t)
	client := fixture.connect(t)
	fixture.turns.startErr = fmt.Errorf("%w: the selection ends before it starts", turn.ErrInvalidSelection)

	client.send(map[string]any{
		"type": CommandStartTurn, "capability": "immersive_reading", "content": "这段什么意思？",
		"selection": map[string]any{"material_id": "rm_a", "locator": map[string]any{"char_start": 20, "char_end": 10}},
	})

	failed := client.recvOf(FrameError)
	body := failed["error"].(map[string]any)
	if code := at(body, "code"); code != CodeMalformedCommand {
		t.Errorf("error_code = %v, want %q", code, CodeMalformedCommand)
	}
	if message := at(body, "message"); message == "" {
		t.Error("the client was given no reason a coordinate was refused")
	}
	// The connection stays open: a client that sent one bad coordinate can send a
	// good one, and it is watching turns it has not finished with.
	client.send(map[string]any{"type": CommandPing})
	client.recvOf(FramePong)
}
