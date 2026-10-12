package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// readTimeout bounds one wait for a frame. A turn that is waiting for a person
// to answer is legitimately quiet, so this is generous; it exists so that a
// probe left running overnight stops with a sentence rather than silently.
const readTimeout = 60 * time.Second

// defaultProtocolVersion is the version this probe speaks unless it is asked for
// another one. It is written out here rather than imported from the server so
// that a change to it is visible as a protocol change rather than as a rebuild
// that quietly keeps both sides in step.
const defaultProtocolVersion = "2.0"

// errWatched marks the probe having seen everything it set out to see. It is
// not a failure, and it is an error so that it can stop the read loop from
// wherever in the loop it was decided.
var errWatched = errors.New("the probe has watched what it came to watch")

// frame is what the probe reads.
//
// It decodes the fields the protocol defines and nothing else, so that what is
// printed is what arrived: a probe that only understood the current shapes
// could not show a frame that stopped matching them.
type frame struct {
	ProtocolVersion string `json:"protocol_version"`
	Frame           string `json:"frame"`

	// An event's own fields.
	TurnID    string          `json:"turn_id"`
	Seq       int64           `json:"seq"`
	Type      string          `json:"type"`
	Source    string          `json:"source"`
	Stage     string          `json:"stage"`
	Content   string          `json:"content"`
	Metadata  json.RawMessage `json:"metadata"`
	Timestamp string          `json:"timestamp"`

	// An answer to a command.
	CommandID string `json:"command_id"`
	Command   string `json:"command"`
	SessionID string `json:"session_id"`
	// Status is the outcome of a command, or the state of the turn that
	// check_active_turn reported on: the same field name, one level out.
	Status string `json:"status"`

	// An answer to check_active_turn.
	Active  bool  `json:"active"`
	LastSeq int64 `json:"last_seq"`

	// A failure. Terminal means the connection is over, not that the turn is.
	Terminal bool `json:"terminal"`
	Error    *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// command is what the probe sends. One struct for all of them, because the wire
// form is one flat object and the fields a command does not use are absent.
type command struct {
	ProtocolVersion string `json:"protocol_version"`
	Type            string `json:"type"`
	CommandID       string `json:"command_id,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
	TurnID          string `json:"turn_id,omitempty"`
	Capability      string `json:"capability,omitempty"`
	Content         string `json:"content,omitempty"`
	AfterSeq        *int64 `json:"after_seq,omitempty"`
}

// probe is one run's connection to the runtime.
type probe struct {
	options   options
	token     string
	sessionID string
	socket    *websocket.Conn

	// commands counts what this run has sent, so that each command is issued
	// under a name of its own and a repeat is recognisable as a repeat.
	commands int
	// turnID is the turn being watched, and lastSeq how far the client has been
	// told about — which is what it resumes from.
	turnID  string
	lastSeq int64
	// events counts what has been printed for this turn, which is what the
	// -cancel-after and -drop-after probes count against.
	events int
	// done records that the turn's end was seen and tailed that the metadata
	// after it was. Reading between the two is the point (WS-07).
	done   bool
	tailed bool
	// resumed records that a reconnect is in progress, so that the answer to
	// the question asked on reconnecting is the one that resumes the stream.
	resumed bool
	// dropped, cancelled and answered record that each one-shot probe has been
	// done, so that none of them repeats.
	dropped   bool
	cancelled bool
	answered  bool
}

// converse runs one conversation to its end.
func converse(ctx context.Context, options options, token, sessionID string) error {
	probe := &probe{options: options, token: token, sessionID: sessionID}
	if err := probe.connect(ctx); err != nil {
		return err
	}
	defer probe.close()

	probe.send(ctx, command{
		Type:       "start_turn",
		CommandID:  probe.nextCommandID("start"),
		SessionID:  sessionID,
		Capability: options.capability,
		Content:    options.prompt,
	})
	return probe.watch(ctx)
}

func (p *probe) connect(ctx context.Context) error {
	socket, _, err := websocket.Dial(ctx, p.socketURL(), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + p.token}},
	})
	if err != nil {
		return fmt.Errorf("connect to %s: %w", p.socketURL(), err)
	}
	p.socket = socket
	return nil
}

func (p *probe) close() {
	if p.socket == nil {
		return
	}
	// The turn keeps running either way; this is only politeness towards the
	// server's shutdown, which waits for connections to finish closing.
	_ = p.socket.Close(websocket.StatusNormalClosure, "the probe is done")
	p.socket = nil
}

func (p *probe) socketURL() string {
	parsed, err := url.Parse(p.options.base)
	if err != nil {
		return p.options.base
	}
	if parsed.Scheme == "https" {
		parsed.Scheme = "wss"
	} else {
		parsed.Scheme = "ws"
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/api/v1/ws"
	return parsed.String()
}

// watch reads until the turn is over, the connection ends, or the run has
// demonstrated what it was asked to.
func (p *probe) watch(ctx context.Context) error {
	for {
		readCtx, cancel := context.WithTimeout(ctx, readTimeout)
		_, raw, err := p.socket.Read(readCtx)
		cancel()
		if err != nil {
			return p.reportRead(ctx, err)
		}

		var received frame
		if err := json.Unmarshal(raw, &received); err != nil {
			return fmt.Errorf("the server sent something that is not a frame: %w (%s)", err, raw)
		}
		fmt.Println(describe(received))

		switch {
		case received.Frame == "event":
			if err := p.onEvent(ctx, received); err != nil {
				if errors.Is(err, errWatched) {
					return nil
				}
				return err
			}
		case received.Frame == "active_turn" && p.resumed:
			p.resubscribe(ctx, received)
		case received.Frame == "error" && received.Terminal:
			fmt.Println("the server ended the connection; a turn left running was not the reason")
			return nil
		}
	}
}

// onEvent reacts to what the turn produced.
func (p *probe) onEvent(ctx context.Context, received frame) error {
	p.turnID = received.TurnID
	p.lastSeq = received.Seq
	p.events++

	switch received.Type {
	case "done":
		// The turn is over but the stream is not: the session's own metadata
		// follows the turn's ending. Disconnecting here is the mistake this
		// probe exists to not make.
		p.done = true
		fmt.Println("  the turn ended; the socket stays open for the tail")
	case "session_meta":
		if p.done {
			p.tailed = true
			fmt.Println("  the tail arrived; the stream is complete")
			return errWatched
		}
	case "wait_for_input":
		return p.answer(ctx, received)
	}

	if p.options.cancelAfter > 0 && !p.cancelled && p.events >= p.options.cancelAfter {
		p.cancelled = true
		fmt.Printf("  cancelling after %d events\n", p.events)
		p.send(ctx, command{
			Type:      "cancel",
			CommandID: p.nextCommandID("cancel"),
			TurnID:    p.turnID,
		})
	}
	if p.options.dropAfter > 0 && !p.dropped && p.events >= p.options.dropAfter {
		p.dropped = true
		return p.resume(ctx)
	}
	return nil
}

// answer replies to a question the turn asked, and then repeats the reply under
// the same command id, which is what a client that did not hear the first ack
// does and what the server is expected to answer from memory.
func (p *probe) answer(ctx context.Context, received frame) error {
	if p.options.reply == "" {
		fmt.Println("  the turn is waiting for an answer; run again with -reply to give it one")
		return errWatched
	}
	if p.answered {
		return nil
	}
	p.answered = true

	reply := command{
		Type:      "submit_reply",
		CommandID: p.nextCommandID("reply"),
		TurnID:    received.TurnID,
		Content:   p.options.reply,
	}
	p.send(ctx, reply)
	fmt.Println("  sent the reply, and sent it again under the same command id")
	p.send(ctx, reply)
	return nil
}

// resume drops the connection and comes back for what it missed.
//
// The turn is not the probe's to stop: dropping the socket must leave it
// running, and the way to find out whether it did is to ask the server what the
// session is doing and then ask for the events after the last one seen.
func (p *probe) resume(ctx context.Context) error {
	fmt.Printf("  dropping the connection after %d events, having seen up to seq %d\n", p.events, p.lastSeq)
	if p.socket != nil {
		// CloseNow rather than Close: a clean close is a client saying goodbye,
		// and what is being demonstrated is a client that vanished.
		_ = p.socket.CloseNow()
		p.socket = nil
	}
	if err := p.connect(ctx); err != nil {
		return err
	}
	p.resumed = true
	fmt.Println("  reconnected; asking what the session is doing")

	p.send(ctx, command{
		Type:      "check_active_turn",
		CommandID: p.nextCommandID("active"),
		SessionID: p.sessionID,
	})
	return nil
}

// resubscribe asks for the events after the last one the probe saw. It runs on
// the answer to check_active_turn, which is the first thing the reconnected
// socket says.
func (p *probe) resubscribe(ctx context.Context, answered frame) {
	p.resumed = false
	after := p.lastSeq
	fmt.Printf("  resuming turn %s from seq %d\n", p.turnID, after)
	p.send(ctx, command{
		Type:      "resume",
		CommandID: p.nextCommandID("resume"),
		TurnID:    p.turnID,
		AfterSeq:  &after,
	})
}

// reportRead explains a connection that ended mid-watch.
func (p *probe) reportRead(ctx context.Context, err error) error {
	if websocket.CloseStatus(err) != -1 {
		fmt.Printf("the server closed the connection: %v\n", err)
		return nil
	}
	if cause := ctx.Err(); cause != nil {
		return fmt.Errorf("the probe ran out of time: %w", cause)
	}
	return fmt.Errorf("read from the connection: %w", err)
}

// send writes one command.
//
// The protocol version is stamped here rather than at each call site, so that
// every frame this probe sends speaks one version — including a wrong one, when
// that is what it was asked to check.
func (p *probe) send(ctx context.Context, out command) {
	if p.socket == nil {
		return
	}
	out.ProtocolVersion = p.version()
	raw, err := json.Marshal(out)
	if err != nil {
		fmt.Println("  could not encode the command:", err)
		return
	}
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := p.socket.Write(writeCtx, websocket.MessageText, raw); err != nil {
		fmt.Println("  could not send the command:", err)
	}
}

// version is the protocol the probe was asked to speak.
func (p *probe) version() string {
	if p.options.protocolVersion == "" {
		return defaultProtocolVersion
	}
	return p.options.protocolVersion
}

func (p *probe) nextCommandID(name string) string {
	p.commands++
	return fmt.Sprintf("%s-%d", name, p.commands)
}

// describe is one line per frame.
func describe(received frame) string {
	switch received.Frame {
	case "event":
		return fmt.Sprintf("event  seq=%-4d %-16s %s%s", received.Seq, received.Type, where(received), received.Content)
	case "command_ack":
		return fmt.Sprintf("ack    %-14s %-10s turn=%s command_id=%s",
			received.Command, received.Status, received.TurnID, received.CommandID)
	case "error":
		code, message := "", ""
		if received.Error != nil {
			code, message = received.Error.Code, received.Error.Message
		}
		return fmt.Sprintf("error  %s terminal=%v: %s", code, received.Terminal, message)
	case "active_turn":
		return fmt.Sprintf("active session=%s active=%v turn=%s status=%s last_seq=%d",
			received.SessionID, received.Active, received.TurnID, received.Status, received.LastSeq)
	default:
		return received.Frame
	}
}

// where is an event's origin, for a reader trying to follow which part of the
// runtime said what.
func where(received frame) string {
	parts := make([]string, 0, 2)
	for _, part := range []string{received.Source, received.Stage} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "[" + strings.Join(parts, "/") + "] "
}
