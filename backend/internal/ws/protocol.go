// Package ws is the WebSocket protocol layer: it turns what a client sends
// into calls on the turn runtime, and what the runtime produces into frames.
//
// It owns no state of its own. A turn keeps running whether or not anyone is
// connected, so a connection is a view of turns rather than the thing that runs
// them — which is what makes reconnecting a matter of asking for what was
// missed rather than of restarting anything.
package ws

import (
	"encoding/json"
	"fmt"

	"backend/internal/event"
)

// ProtocolVersion is the only version this build speaks. A client that asks for
// another one is refused before anything is executed, because the alternative
// is guessing which parts of a newer protocol this build happens to share.
const ProtocolVersion = "2.0"

// The kinds of frame the server sends. A client reads these to decide what it
// is looking at; `event` is the only one that carries a turn's own traffic.
const (
	FrameEvent      = "event"
	FrameCommandAck = "command_ack"
	FrameError      = "error"
	FramePong       = "pong"
	FrameActiveTurn = "active_turn"
)

// The commands a client may send.
const (
	CommandStartTurn = "start_turn"
	// CommandMessage is the PRD's other name for starting a turn in a session
	// that already exists. It is the same operation, so it is the same code
	// path: two spellings of one command, not two commands.
	CommandMessage = "message"
	// CommandSubscribe and CommandResume are likewise one operation. A client
	// that was never disconnected subscribes from zero; one that was resumes
	// from where it got to.
	CommandSubscribe       = "subscribe"
	CommandResume          = "resume"
	CommandUnsubscribe     = "unsubscribe"
	CommandCancel          = "cancel"
	CommandSubmitReply     = "submit_reply"
	CommandCheckActiveTurn = "check_active_turn"
	CommandRegenerate      = "regenerate"
	CommandPing            = "ping"
)

// The error codes this layer reports. They are the client's side of a failure:
// what the client can do about it, rather than what went wrong inside.
const (
	CodeUnsupportedProtocolVersion = "unsupported_protocol_version"
	CodeMalformedCommand           = "malformed_command"
	CodeUnsupportedCommand         = "unsupported_command"
	CodeSessionNotFound            = "session_not_found"
	CodeTurnNotFound               = "turn_not_found"
	CodeNoActiveTurn               = "no_active_turn"
	CodeTurnConflict               = "active_turn_conflict"
	CodeNotWaiting                 = "turn_not_waiting"
	CodeEmptyContent               = "empty_content"
	CodeEmptyTitle                 = "empty_title"
	CodeNotFound                   = "not_found"
	CodeSlowConsumer               = "slow_consumer"
	CodeInternal                   = "internal_error"
)

// eventFrame is one event of a turn, on the wire.
//
// The event's fields are flattened into the envelope rather than nested, so
// that the frame and the turn_events row describe the same thing field for
// field: a reader comparing the two should not have to hop between levels.
type eventFrame struct {
	ProtocolVersion string         `json:"protocol_version"`
	Frame           string         `json:"frame"`
	TurnID          string         `json:"turn_id"`
	Seq             int64          `json:"seq"`
	Type            string         `json:"type"`
	Source          string         `json:"source,omitempty"`
	Stage           string         `json:"stage,omitempty"`
	Content         string         `json:"content,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	Timestamp       string         `json:"timestamp"`
}

// ackFrame answers a command that changed something.
//
// A mutation the client may repeat carries a command_id, and the ack for it is
// returned again rather than performed again. The client cannot tell a repeated
// ack from a fresh one, which is the point: it only has to know its command
// arrived.
type ackFrame struct {
	ProtocolVersion string `json:"protocol_version"`
	Frame           string `json:"frame"`
	CommandID       string `json:"command_id,omitempty"`
	Command         string `json:"command"`
	Status          string `json:"status"`
	TurnID          string `json:"turn_id,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
}

// errorFrame reports a failure of the connection or of a command.
//
// Terminal means the connection is over: the client must fix what the error
// says and reconnect, because the server will not read another command from it.
type errorFrame struct {
	ProtocolVersion string    `json:"protocol_version"`
	Frame           string    `json:"frame"`
	Terminal        bool      `json:"terminal"`
	Error           errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// activeTurnFrame answers check_active_turn.
//
// It is not an error to have no active turn: a client asks this when it
// reconnects and does not yet know, and "nothing is running" is the answer it
// needs rather than a failure to handle.
type activeTurnFrame struct {
	ProtocolVersion string `json:"protocol_version"`
	Frame           string `json:"frame"`
	SessionID       string `json:"session_id"`
	Active          bool   `json:"active"`
	TurnID          string `json:"turn_id,omitempty"`
	Status          string `json:"status,omitempty"`
	LastSeq         int64  `json:"last_seq"`
}

type pongFrame struct {
	ProtocolVersion string `json:"protocol_version"`
	Frame           string `json:"frame"`
}

// command is what a client sends. One struct rather than one per command,
// because the wire form is a flat object and the fields not belonging to a
// command are simply absent from it.
type command struct {
	ProtocolVersion string `json:"protocol_version"`
	// Type names the command, which is what the reference protocol calls the
	// discriminator. The server's `frame` field is the same idea one level out:
	// which envelope, and then which thing inside it.
	Type string `json:"type"`
	// CommandID is set on the commands a client may repeat. Two commands with
	// the same id are one command (WS-09).
	CommandID  string `json:"command_id"`
	SessionID  string `json:"session_id"`
	TurnID     string `json:"turn_id"`
	Capability string `json:"capability"`
	Content    string `json:"content"`
	// Attachments are the stored files this turn carries, and KnowledgeBases the
	// collections it may search. Both are optional, and both stay inside the
	// start_turn command: material belongs to one turn, so it has nowhere else
	// to live. Neither field changes the protocol version — a client that never
	// sends them sends exactly the frames it sent before, which is also why they
	// are read from the wire rather than made required.
	Attachments    []string `json:"attachments"`
	KnowledgeBases []string `json:"knowledge_bases"`
	// Selection is the passage the reader had chosen in a reading material, for
	// the turn that asks about one (§2.8). It is absent for every other turn, and
	// it is one turn's and not a conversation's: what a reader was looking at is
	// not stored, so a client sends it with each turn it is about.
	Selection *selection `json:"selection,omitempty"`
	// AfterSeq is where a subscription starts reading. Absent means zero, which
	// is the beginning of the turn — the zero value is already the useful
	// default, so a client that omits it is asking for everything.
	AfterSeq *int64 `json:"after_seq"`
}

// selection is the passage a reader had chosen, as a client sends it: which
// material they were reading, where in it they had got to, and the text they
// believe is there.
//
// The text is what lets the two ends be compared when they disagree, and it is
// not what the turn reads by: a client can be a stale tab, so what the model is
// given is what the coordinate resolves to. The locator is the same coordinate
// a citation carries, spelled the same way, because it is the same thing — where
// in a document a passage is (§2.8).
type selection struct {
	MaterialID string        `json:"material_id"`
	Locator    event.Locator `json:"locator"`
	Text       string        `json:"text"`
}

// errProtocolVersion reports a frame this build cannot read.
var errProtocolVersion = fmt.Errorf("protocol_version must be %q", ProtocolVersion)

// decode reads a client frame.
//
// An unknown field is tolerated and an unknown command is not: the first is
// usually a newer client sending something harmless, and the second is a client
// that will not get what it asked for, which it has to be told.
func decode(raw []byte) (command, error) {
	var decoded command
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return command{}, fmt.Errorf("the frame is not a JSON object: %w", err)
	}
	if decoded.ProtocolVersion != ProtocolVersion {
		return command{}, errProtocolVersion
	}
	if decoded.Type == "" {
		return command{}, fmt.Errorf("the frame names no command")
	}
	return decoded, nil
}

// watermark is where a subscription should start. A missing after_seq means the
// beginning, which is the same as zero.
func (c command) watermark() int64 {
	if c.AfterSeq == nil {
		return 0
	}
	return *c.AfterSeq
}
