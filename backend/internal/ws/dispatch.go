package ws

import (
	"context"
	"errors"
	"fmt"

	"backend/internal/capability"
	"backend/internal/session"
	"backend/internal/turn"
)

// handle reads one frame and does what it says.
//
// The command and the frame answering it are separate steps so that the answer
// is queued before anything the command set in motion. A client that starts a
// turn must learn its turn_id before it is sent the turn's first event, or it
// cannot tell which turn the event belongs to.
func (c *conn) handle(ctx context.Context, raw []byte) error {
	cmd, err := decode(raw)
	if err != nil {
		if errors.Is(err, errProtocolVersion) {
			// The whole connection is unusable, not just this frame: a client
			// speaking another version will speak it again on the next frame.
			c.sendLast(ctx, errorFrame{
				ProtocolVersion: ProtocolVersion,
				Frame:           FrameError,
				Terminal:        true,
				Error:           errorBody{Code: CodeUnsupportedProtocolVersion, Message: err.Error()},
			}, errProtocolVersion)
			return errClosed
		}
		return c.reportError(ctx, CodeMalformedCommand, err.Error())
	}

	if cmd.CommandID != "" {
		if ack, found := c.acks.recall(cmd.CommandID, cmd.Type, cmd.TurnID); found {
			return c.send(ack)
		}
	}

	response, after, err := c.perform(ctx, cmd)
	if err != nil {
		code, message := describe(err)
		if code == CodeInternal {
			// The client is told nothing it can act on, so this is the only
			// place the failure exists. An internal error a client reports as
			// "the command failed" and the server does not record at all is a
			// failure nobody can diagnose.
			c.logger.Error("ws: command failed",
				"command", cmd.Type, "turn_id", cmd.TurnID, "session_id", cmd.SessionID, "error", err)
		}
		return c.reportError(ctx, code, message)
	}
	if response != nil {
		if err := c.send(response); err != nil {
			return err
		}
		if ack, ok := response.(ackFrame); ok && cmd.CommandID != "" {
			c.acks.remember(cmd.CommandID, cmd.Type, cmd.TurnID, ack)
		}
	}
	if after != nil {
		return after()
	}
	return nil
}

// perform carries out a command, returning the frame that answers it and any
// work that must wait until that frame has been queued.
func (c *conn) perform(ctx context.Context, cmd command) (any, func() error, error) {
	switch cmd.Type {
	case CommandStartTurn, CommandMessage:
		return c.startTurn(ctx, cmd)
	case CommandSubscribe, CommandResume:
		return c.subscribe(ctx, cmd)
	case CommandUnsubscribe:
		return c.unsubscribe(cmd)
	case CommandCancel:
		return c.cancelTurn(ctx, cmd)
	case CommandSubmitReply:
		return c.submitReply(ctx, cmd)
	case CommandCheckActiveTurn:
		return c.checkActiveTurn(ctx, cmd)
	case CommandPing:
		return pongFrame{ProtocolVersion: ProtocolVersion, Frame: FramePong}, nil, nil
	case CommandRegenerate:
		return nil, nil, fmt.Errorf("%w: regenerate arrives with the version that can rewrite a conversation's tail", errUnsupportedCommand)
	default:
		return nil, nil, fmt.Errorf("%w: %s is not a command this version knows", errUnsupportedCommand, cmd.Type)
	}
}

// startTurn begins a turn and subscribes the client that asked for it: a client
// starting a turn is asking to watch it.
func (c *conn) startTurn(ctx context.Context, cmd command) (any, func() error, error) {
	started, err := c.turns.Start(ctx, turn.StartRequest{
		OwnerID:        c.caller.UserID,
		SessionID:      cmd.SessionID,
		Capability:     cmd.Capability,
		PersonaID:      cmd.Persona,
		Content:        cmd.Content,
		Attachments:    cmd.Attachments,
		KnowledgeBases: cmd.KnowledgeBases,
		Selection:      selectionOf(cmd.Selection),
	})
	if err != nil {
		return nil, nil, err
	}
	turnID := started.Turn.ID
	return ackFrame{
			ProtocolVersion: ProtocolVersion,
			Frame:           FrameCommandAck,
			CommandID:       cmd.CommandID,
			Command:         cmd.Type,
			Status:          "accepted",
			TurnID:          turnID,
			SessionID:       started.Session.ID,
		}, func() error {
			return c.watch(ctx, watchName(cmd, turnID), turnID, 0)
		}, nil
}

// selectionOf carries a reader's selection from the wire into the runtime, and
// it is the one place the two spellings of it meet.
//
// An absent selection stays absent rather than becoming an empty one: a turn
// with no selection and a turn whose selection names nothing are different
// things, and only the second is the client's mistake.
func selectionOf(selected *selection) *turn.Selection {
	if selected == nil {
		return nil
	}
	return &turn.Selection{
		MaterialID: selected.MaterialID,
		Locator:    selected.Locator,
		Text:       selected.Text,
	}
}

// subscribe starts watching a turn, or whichever turn a session is running.
func (c *conn) subscribe(ctx context.Context, cmd command) (any, func() error, error) {
	turnID, name, err := c.target(ctx, cmd)
	if err != nil {
		return nil, nil, err
	}
	return ackFrame{
			ProtocolVersion: ProtocolVersion,
			Frame:           FrameCommandAck,
			CommandID:       cmd.CommandID,
			Command:         cmd.Type,
			Status:          "accepted",
			TurnID:          turnID,
			SessionID:       cmd.SessionID,
		}, func() error {
			return c.watch(ctx, name, turnID, cmd.watermark())
		}, nil
}

// unsubscribe stops watching what the client names. Naming something that is not
// being watched is not a failure: the client wanted it not watched, and it is
// not.
func (c *conn) unsubscribe(cmd command) (any, func() error, error) {
	name, err := nameFor(cmd)
	if err != nil {
		return nil, nil, err
	}
	c.unwatch(name)
	return ackFrame{
		ProtocolVersion: ProtocolVersion,
		Frame:           FrameCommandAck,
		CommandID:       cmd.CommandID,
		Command:         cmd.Type,
		Status:          "accepted",
		TurnID:          cmd.TurnID,
		SessionID:       cmd.SessionID,
	}, nil, nil
}

func (c *conn) cancelTurn(ctx context.Context, cmd command) (any, func() error, error) {
	if cmd.TurnID == "" {
		return nil, nil, fmt.Errorf("%w: cancel needs a turn_id", errMalformedCommand)
	}
	if err := c.turns.Cancel(ctx, cmd.TurnID); err != nil {
		return nil, nil, err
	}
	return ackFrame{
		ProtocolVersion: ProtocolVersion,
		Frame:           FrameCommandAck,
		CommandID:       cmd.CommandID,
		Command:         cmd.Type,
		Status:          "accepted",
		TurnID:          cmd.TurnID,
	}, nil, nil
}

func (c *conn) submitReply(ctx context.Context, cmd command) (any, func() error, error) {
	if cmd.TurnID == "" {
		return nil, nil, fmt.Errorf("%w: submit_reply needs a turn_id", errMalformedCommand)
	}
	if err := c.turns.SubmitReply(ctx, cmd.TurnID, cmd.Content); err != nil {
		return nil, nil, err
	}
	return ackFrame{
		ProtocolVersion: ProtocolVersion,
		Frame:           FrameCommandAck,
		CommandID:       cmd.CommandID,
		Command:         cmd.Type,
		Status:          "accepted",
		TurnID:          cmd.TurnID,
	}, nil, nil
}

// checkActiveTurn answers where a session got to. Having nothing running is an
// answer rather than a failure: this is what a client asks when it reconnects
// and does not yet know.
func (c *conn) checkActiveTurn(ctx context.Context, cmd command) (any, func() error, error) {
	if cmd.SessionID == "" {
		return nil, nil, fmt.Errorf("%w: check_active_turn needs a session_id", errMalformedCommand)
	}
	active, err := c.turns.ActiveTurn(ctx, cmd.SessionID)
	if err != nil {
		if errors.Is(err, turn.ErrNotFound) {
			return activeTurnFrame{
				ProtocolVersion: ProtocolVersion,
				Frame:           FrameActiveTurn,
				SessionID:       cmd.SessionID,
			}, nil, nil
		}
		return nil, nil, err
	}
	last, err := c.turns.LastSeq(ctx, active.ID)
	if err != nil {
		return nil, nil, err
	}
	return activeTurnFrame{
		ProtocolVersion: ProtocolVersion,
		Frame:           FrameActiveTurn,
		SessionID:       cmd.SessionID,
		Active:          true,
		TurnID:          active.ID,
		Status:          string(active.Status),
		LastSeq:         last,
	}, nil, nil
}

// target resolves what a subscribe or resume is about: a turn the client named,
// or the turn the session it named is running.
func (c *conn) target(ctx context.Context, cmd command) (turnID, name string, err error) {
	name, err = nameFor(cmd)
	if err != nil {
		return "", "", err
	}
	if cmd.TurnID != "" {
		return cmd.TurnID, name, nil
	}
	active, err := c.turns.ActiveTurn(ctx, cmd.SessionID)
	if err != nil {
		if errors.Is(err, turn.ErrNotFound) {
			return "", "", fmt.Errorf("%w: session %s has no turn to subscribe to", errNoActiveTurn, cmd.SessionID)
		}
		return "", "", err
	}
	return active.ID, name, nil
}

// nameFor is what a client subscribed as, and therefore what it must name to
// unsubscribe: the turn if it named one, and the session otherwise.
func nameFor(cmd command) (string, error) {
	switch {
	case cmd.TurnID != "":
		return turnName(cmd.TurnID), nil
	case cmd.SessionID != "":
		return sessionName(cmd.SessionID), nil
	default:
		return "", fmt.Errorf("%w: %s needs a turn_id or a session_id", errMalformedCommand, cmd.Type)
	}
}

func watchName(cmd command, turnID string) string {
	if cmd.TurnID == "" && cmd.SessionID != "" {
		return sessionName(cmd.SessionID)
	}
	return turnName(turnID)
}

func turnName(turnID string) string { return "turn:" + turnID }
func sessionName(id string) string  { return "session:" + id }

// reportError tells the client a command failed without ending the connection:
// the command was wrong, not the client's connection to us.
func (c *conn) reportError(ctx context.Context, code, message string) error {
	return c.send(errorFrame{
		ProtocolVersion: ProtocolVersion,
		Frame:           FrameError,
		Error:           errorBody{Code: code, Message: message},
	})
}

// The sentinels this layer raises for problems that are its own rather than the
// runtime's: a command that cannot be carried out, and one that names nothing
// to act on.
var (
	errMalformedCommand   = errors.New("ws: malformed command")
	errUnsupportedCommand = errors.New("ws: unsupported command")
	errNoActiveTurn       = errors.New("ws: no active turn")
)

// describe turns a failure into what the client is told.
//
// A failure the client can act on is reported as such; everything else is
// reported as an internal error, because a client cannot do anything with the
// wording and the wording may be about us.
func describe(err error) (code, message string) {
	switch {
	case errors.Is(err, errMalformedCommand):
		return CodeMalformedCommand, err.Error()
	case errors.Is(err, errUnsupportedCommand):
		return CodeUnsupportedCommand, err.Error()
	case errors.Is(err, errNoActiveTurn):
		return CodeNoActiveTurn, err.Error()
	case errors.Is(err, session.ErrNotFound):
		return CodeSessionNotFound, "no such session"
	case errors.Is(err, turn.ErrNotFound):
		return CodeTurnNotFound, "no such turn"
	case errors.Is(err, turn.ErrActiveTurnExists):
		return CodeTurnConflict, "this session is already running a turn"
	case errors.Is(err, turn.ErrNotWaiting):
		return CodeNotWaiting, "this turn is not waiting for an answer"
	case errors.Is(err, turn.ErrEmptyMessage):
		return CodeEmptyContent, "the message is empty"
	case errors.Is(err, turn.ErrInvalidSelection):
		// The command's own payload cannot be acted on, which is what this code
		// says; the sentence is the runtime's, because only it can say which part
		// of the coordinate is not a span.
		return CodeMalformedCommand, err.Error()
	case errors.Is(err, turn.ErrEmptyReply):
		return CodeEmptyContent, "the reply is empty"
	case errors.Is(err, session.ErrInvalidTitle):
		return CodeEmptyTitle, "the title is empty"
	case errors.Is(err, turn.ErrPersonaNotFound):
		return CodePersonaNotFound, "no such persona"
	case errors.Is(err, capability.ErrUnknownCapability):
		return CodeNotFound, "no such capability"
	default:
		return CodeInternal, "the command failed"
	}
}
