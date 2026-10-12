package ws

import (
	"testing"
	"time"

	"backend/internal/event"
	"backend/internal/turn"
)

func TestStartingATurnIsAcknowledgedBeforeItsEvents(t *testing.T) {
	fixture := newWSFixture(t)
	client := fixture.connect(t)

	client.send(map[string]any{"type": CommandStartTurn, "content": "闭包是什么？"})

	// A client that is sent events before it is told the turn's id cannot say
	// which turn they belong to.
	ack := client.recvOf(FrameCommandAck)
	if at(ack, "turn_id") != "turn_1" || at(ack, "session_id") != "sess_1" {
		t.Errorf("ack = %v, want the turn and session that were started", ack)
	}
	if at(ack, "status") != "accepted" {
		t.Errorf("status = %v, want the command accepted", ack["status"])
	}

	starts := fixture.turns.startRequests()
	if len(starts) != 1 || starts[0].Content != "闭包是什么？" {
		t.Fatalf("the runtime was asked to start %+v", starts)
	}

	stream := fixture.turns.streamFor(t)
	stream.publish(event.Event{
		TurnID: "turn_1", Seq: 1, Type: event.TypeContent, Source: "chat",
		Stage: "responding", Content: "闭包是……",
	})

	frame := client.recvOf(FrameEvent)
	if at(frame, "type") != string(event.TypeContent) || at(frame, "content") != "闭包是……" {
		t.Errorf("event = %v, want the event the turn produced", frame)
	}
	if at(frame, "turn_id") != "turn_1" || at(frame, "source") != "chat" {
		t.Errorf("event = %v, want it attributed to its turn and source", frame)
	}
	if frame["seq"] != float64(1) {
		t.Errorf("seq = %v, want the event's own sequence number", frame["seq"])
	}
	if at(frame, "protocol_version") != ProtocolVersion || at(frame, "timestamp") == "" {
		t.Errorf("event = %v, want an envelope a client can place", frame)
	}
}

func TestStartingATurnInASessionNamesThatSession(t *testing.T) {
	fixture := newWSFixture(t)
	client := fixture.connect(t)

	client.send(map[string]any{
		"type": CommandMessage, "session_id": "sess_9", "capability": "chat", "content": "继续",
	})
	client.recvOf(FrameCommandAck)

	starts := fixture.turns.startRequests()
	if len(starts) != 1 {
		t.Fatalf("the runtime was asked to start %+v", starts)
	}
	// The two names the product uses for one operation are one operation here.
	if starts[0].SessionID != "sess_9" || starts[0].Capability != "chat" {
		t.Errorf("start = %+v, want the session and capability the client named", starts[0])
	}
}

func TestARepeatedCommandIsAnsweredRatherThanPerformedAgain(t *testing.T) {
	fixture := newWSFixture(t)
	client := fixture.connect(t)

	command := map[string]any{"type": CommandCancel, "turn_id": "turn_1", "command_id": "cmd_7"}
	client.send(command)
	first := client.recvOf(FrameCommandAck)

	client.send(command)
	second := client.recvOf(FrameCommandAck)

	if at(first, "command_id") != "cmd_7" || at(second, "command_id") != "cmd_7" {
		t.Errorf("acks = %v and %v, want both to answer the command they repeat", first, second)
	}
	if first["status"] != second["status"] || at(first, "turn_id") != at(second, "turn_id") {
		t.Errorf("the second ack = %v, want the same answer as the first %v", second, first)
	}
	// A client repeats a command because it did not hear back. Hearing back
	// twice is fine; cancelling twice is not the same operation twice.
	if fixture.turns.cancelCount() != 1 {
		t.Errorf("the runtime was asked to cancel %d times, want once", fixture.turns.cancelCount())
	}
}

func TestAnUnsupportedCommandIsReportedWithoutEndingTheConnection(t *testing.T) {
	fixture := newWSFixture(t)
	client := fixture.connect(t)

	client.send(map[string]any{"type": CommandRegenerate, "session_id": "sess_1"})

	failed := client.recvOf(FrameError)
	if code := at(failed["error"].(map[string]any), "code"); code != CodeUnsupportedCommand {
		t.Errorf("error_code = %v, want %q", code, CodeUnsupportedCommand)
	}
	if failed["terminal"] == true {
		t.Error("a command this version does not know ended the connection")
	}

	// The client is still connected, which is the point: one unsupported
	// command is not a reason to lose the turns it is watching.
	client.send(map[string]any{"type": CommandPing})
	client.recvOf(FramePong)
}

func TestAMalformedFrameIsReported(t *testing.T) {
	fixture := newWSFixture(t)
	client := fixture.connect(t)

	client.sendRaw("this is not a frame")

	failed := client.recvOf(FrameError)
	if code := at(failed["error"].(map[string]any), "code"); code != CodeMalformedCommand {
		t.Errorf("error_code = %v, want %q", code, CodeMalformedCommand)
	}
	client.send(map[string]any{"type": CommandPing})
	client.recvOf(FramePong)
}

func TestAFrameInAnotherProtocolVersionEndsTheConnection(t *testing.T) {
	tests := []struct {
		name string
		send map[string]any
	}{
		{name: "a version this build does not speak", send: map[string]any{"protocol_version": "3.0"}},
		{name: "no version at all", send: map[string]any{"protocol_version": ""}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWSFixture(t)
			client := fixture.connect(t)

			test.send["type"] = CommandPing
			client.send(test.send)

			failed := client.recvOf(FrameError)
			if code := at(failed["error"].(map[string]any), "code"); code != CodeUnsupportedProtocolVersion {
				t.Errorf("error_code = %v, want %q", code, CodeUnsupportedProtocolVersion)
			}
			// Reading the rest of the connection from a version this build
			// cannot read would be guessing what the frames mean.
			if failed["terminal"] != true {
				t.Error("a client speaking another version was left connected")
			}
			client.waitClosed()
		})
	}
}

func TestASubmitReplyToATurnThatIsNotWaitingIsRefused(t *testing.T) {
	fixture := newWSFixture(t)
	fixture.turns.replyErr = turn.ErrNotWaiting
	client := fixture.connect(t)

	client.send(map[string]any{"type": CommandSubmitReply, "turn_id": "turn_1", "content": "预发"})

	failed := client.recvOf(FrameError)
	if code := at(failed["error"].(map[string]any), "code"); code != CodeNotWaiting {
		t.Errorf("error_code = %v, want %q", code, CodeNotWaiting)
	}
	client.send(map[string]any{"type": CommandPing})
	client.recvOf(FramePong)
}

func TestSubmittingAReplyCarriesItToTheTurn(t *testing.T) {
	fixture := newWSFixture(t)
	client := fixture.connect(t)

	client.send(map[string]any{
		"type": CommandSubmitReply, "turn_id": "turn_1", "content": "预发", "command_id": "cmd_1",
	})
	client.recvOf(FrameCommandAck)

	replies := fixture.turns.replyRequests()
	if len(replies) != 1 || replies[0].answer != "预发" {
		t.Fatalf("the runtime was given %+v", replies)
	}
	if replies[0].turnID != "turn_1" {
		t.Errorf("the answer went to %q, want the turn it named", replies[0].turnID)
	}
}

func TestCheckingASessionWithNothingRunningIsAnAnswer(t *testing.T) {
	fixture := newWSFixture(t)
	fixture.turns.activeErr = turn.ErrNotFound
	client := fixture.connect(t)

	client.send(map[string]any{"type": CommandCheckActiveTurn, "session_id": "sess_1"})

	frame := client.recvOf(FrameActiveTurn)
	if frame["active"] != false {
		t.Errorf("active = %v, want an answer rather than a failure", frame["active"])
	}
	if at(frame, "session_id") != "sess_1" {
		t.Errorf("session_id = %v, want the session that was asked about", frame["session_id"])
	}
}

func TestCheckingAnActiveTurnSaysWhereItGotTo(t *testing.T) {
	fixture := newWSFixture(t)
	fixture.turns.active = turn.Turn{ID: "turn_4", SessionID: "sess_1", Status: turn.StatusWaitingInput}
	fixture.turns.lastSeq = 7
	client := fixture.connect(t)

	client.send(map[string]any{"type": CommandCheckActiveTurn, "session_id": "sess_1"})

	frame := client.recvOf(FrameActiveTurn)
	if frame["active"] != true || at(frame, "turn_id") != "turn_4" {
		t.Fatalf("frame = %v, want the turn that is running", frame)
	}
	if at(frame, "status") != string(turn.StatusWaitingInput) {
		t.Errorf("status = %v, want the turn's own status", frame["status"])
	}
	// Without how far the turn got, a client cannot tell what it missed.
	if frame["last_seq"] != float64(7) {
		t.Errorf("last_seq = %v, want 7", frame["last_seq"])
	}
}

func TestSubscribingBySessionFollowsItsTurn(t *testing.T) {
	fixture := newWSFixture(t)
	fixture.turns.active = turn.Turn{ID: "turn_4", SessionID: "sess_1", Status: turn.StatusRunning}
	client := fixture.connect(t)

	client.send(map[string]any{"type": CommandSubscribe, "session_id": "sess_1"})

	ack := client.recvOf(FrameCommandAck)
	if at(ack, "turn_id") != "turn_4" {
		t.Errorf("turn_id = %v, want the session's running turn", ack["turn_id"])
	}
	subscribed := fixture.turns.subscriptions()
	if len(subscribed) != 1 || subscribed[0].turnID != "turn_4" || subscribed[0].afterSeq != 0 {
		t.Fatalf("the runtime was subscribed to %+v", subscribed)
	}
}

func TestSubscribingToASessionWithNoTurnIsReported(t *testing.T) {
	fixture := newWSFixture(t)
	fixture.turns.activeErr = turn.ErrNotFound
	client := fixture.connect(t)

	client.send(map[string]any{"type": CommandSubscribe, "session_id": "sess_1"})

	failed := client.recvOf(FrameError)
	if code := at(failed["error"].(map[string]any), "code"); code != CodeNoActiveTurn {
		t.Errorf("error_code = %v, want %q", code, CodeNoActiveTurn)
	}
}

func TestResumingPicksUpFromTheWatermark(t *testing.T) {
	fixture := newWSFixture(t)
	client := fixture.connect(t)

	client.send(map[string]any{"type": CommandResume, "turn_id": "turn_1", "after_seq": 12})
	ack := client.recvOf(FrameCommandAck)
	if at(ack, "turn_id") != "turn_1" {
		t.Fatalf("ack = %v, want the turn that was resumed", ack)
	}

	subscribed := fixture.turns.subscriptions()
	if len(subscribed) != 1 || subscribed[0].afterSeq != 12 {
		t.Fatalf("the runtime was subscribed to %+v", subscribed)
	}

	// Only what the client has not seen: the watermark is the client's, and it
	// is passed through rather than decided here.
	stream := fixture.turns.streamFor(t)
	stream.publish(event.Event{TurnID: "turn_1", Seq: 13, Type: event.TypeResult})
	frame := client.recvOf(FrameEvent)
	if frame["seq"] != float64(13) {
		t.Errorf("seq = %v, want the event after the watermark", frame["seq"])
	}
}

func TestUnsubscribingStopsTheStream(t *testing.T) {
	fixture := newWSFixture(t)
	client := fixture.connect(t)

	client.send(map[string]any{"type": CommandSubscribe, "turn_id": "turn_1"})
	client.recvOf(FrameCommandAck)
	stream := fixture.turns.streamFor(t)

	client.send(map[string]any{"type": CommandUnsubscribe, "turn_id": "turn_1"})
	client.recvOf(FrameCommandAck)

	deadline := testDeadline(t)
	for !stream.closed() {
		if deadline.passed() {
			t.Fatal("the subscription was not closed after unsubscribing")
		}
	}

	// The connection is still there, and still answers: unsubscribing from one
	// turn is not leaving.
	client.send(map[string]any{"type": CommandPing})
	client.recvOf(FramePong)
}

func TestAStreamThatEndsIsNotAClientBeingCutOff(t *testing.T) {
	fixture := newWSFixture(t)
	client := fixture.connect(t)

	client.send(map[string]any{"type": CommandSubscribe, "turn_id": "turn_1"})
	client.recvOf(FrameCommandAck)
	stream := fixture.turns.streamFor(t)

	stream.publish(event.Event{TurnID: "turn_1", Seq: 1, Type: event.TypeSessionMeta})
	client.recvOf(FrameEvent)
	stream.end(nil)

	// The turn is over, which the client was told by the last event. The
	// connection stays for whatever the client does next (WS-07).
	client.send(map[string]any{"type": CommandPing})
	client.recvOf(FramePong)
}

func TestAStreamThatFellBehindEndsTheConnection(t *testing.T) {
	fixture := newWSFixture(t)
	client := fixture.connect(t)

	client.send(map[string]any{"type": CommandSubscribe, "turn_id": "turn_1"})
	client.recvOf(FrameCommandAck)
	stream := fixture.turns.streamFor(t)
	stream.end(turn.ErrSlowConsumer)

	failed := client.recvOf(FrameError)
	if code := at(failed["error"].(map[string]any), "code"); code != CodeSlowConsumer {
		t.Errorf("error_code = %v, want %q", code, CodeSlowConsumer)
	}
	// The events after the last one the client got exist and it has not been
	// given them, so the connection cannot continue as though it had.
	if failed["terminal"] != true {
		t.Error("a connection that fell behind the turn was left open")
	}
	client.waitClosed()
}

// testDeadline is a small helper for polling with a bound.
type deadline struct {
	until time.Time
}

func testDeadline(t *testing.T) deadline {
	t.Helper()
	return deadline{until: time.Now().Add(testTimeout)}
}

func (d deadline) passed() bool {
	if time.Now().After(d.until) {
		return true
	}
	time.Sleep(time.Millisecond)
	return false
}
