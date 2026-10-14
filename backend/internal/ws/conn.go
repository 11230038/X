package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"backend/internal/authz"
	"backend/internal/event"
	"backend/internal/turn"
	"github.com/coder/websocket"
)

// writeTimeout bounds one frame write. A client that has stopped reading
// eventually blocks the write, and this is what turns that into a closed
// connection instead of a goroutine parked forever.
const writeTimeout = 10 * time.Second

// readLimit bounds one inbound frame. A command carries a message and a few
// identifiers; anything larger is not a command, and reading it would be
// letting a client choose how much memory to spend.
const readLimit = 1 << 20

// queueGrace bounds how long the teardown waits to hand a queued frame to a
// client that has stopped reading.
const queueGrace = 250 * time.Millisecond

// errSlowConsumer ends the connection.
//
// A client that cannot keep up is disconnected rather than skipped past. The
// alternative is a client rendering a conversation with a hole in it, and it
// cannot tell that it has one.
var errSlowConsumer = errors.New("ws: the client is not reading its frames")

// errClosed reports work that arrived after the connection ended.
var errClosed = errors.New("ws: the connection is closed")

// Turns is the runtime the protocol layer drives.
//
// A connection does not run turns: it starts them, watches them and asks about
// them. That is what lets a client disconnect without ending anything.
type Turns interface {
	Start(ctx context.Context, request turn.StartRequest) (turn.Started, error)
	Cancel(ctx context.Context, turnID string) error
	SubmitReply(ctx context.Context, turnID, answer string) error
	ActiveTurn(ctx context.Context, sessionID string) (turn.Turn, error)
	LastSeq(ctx context.Context, turnID string) (int64, error)
	Subscribe(ctx context.Context, turnID string, afterSeq int64) (Stream, error)
}

// Stream is one turn's events as they arrive.
//
// The connection needs three things from a subscription — the events, the
// reason it stopped, and the ability to stop it — and nothing else. Naming just
// those keeps this layer from having an opinion about where the events come
// from, and lets a test drive one by hand.
type Stream interface {
	Events() <-chan event.Event
	Err() error
	Close()
}

var _ Stream = (*turn.Subscription)(nil)

// outbound is a frame waiting to be written.
//
// written is closed once raw has reached the socket, so that a sender whose
// frame must not be dropped can wait for it. Closing the socket before that
// point would discard the frame, and the frame a sender bothers to wait for is
// the one explaining why the connection is ending.
type outbound struct {
	raw     []byte
	written chan struct{}
}

// watch is one subscription held by a connection.
//
// Its context carries its own stop and nothing else, so that replacing a
// subscription ends exactly that one and not the ones beside it.
type watch struct {
	ctx  context.Context
	stop context.CancelFunc
}

// conn is one client's connection.
//
// Every frame it sends goes through one writer goroutine and one bounded queue.
// A queue that fills is a client that is not reading, which is a fact about the
// client and not about the turn, so it ends the connection and leaves the turn
// running.
type conn struct {
	socket *websocket.Conn
	turns  Turns
	// caller is the account that authenticated this connection. It is read once,
	// at the handshake, and every turn started on this socket runs as that
	// account: a signed-in connection cannot start a turn for somebody else,
	// because no frame has a field that could say who.
	caller authz.Caller
	logger *slog.Logger
	// buffer is how many frames may be waiting for this client. It is a field
	// rather than a constant so a test can make the queue fill up.
	buffer int

	out  chan outbound
	acks *acks

	mu     sync.Mutex
	closed bool
	ended  error
	subs   map[string]*watch
	// watching counts the forwarders still running, so that teardown can wait
	// for them rather than leaving a goroutine writing to a closed socket.
	watching sync.WaitGroup
}

func newConn(socket *websocket.Conn, turns Turns, caller authz.Caller, logger *slog.Logger, buffer int) *conn {
	return &conn{
		socket: socket,
		turns:  turns,
		caller: caller,
		logger: logger,
		buffer: buffer,
		out:    make(chan outbound, buffer),
		acks:   newAcks(),
		subs:   make(map[string]*watch),
	}
}

// serve runs the connection until it ends: the reader loop, which is what
// notices a client going away, and the writer goroutine beside it.
func (c *conn) serve(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		c.write(ctx)
	}()

	c.read(ctx)

	// The reader is the last thing still running, so when it returns the
	// connection is over whatever the reason recorded so far says.
	c.endConnection(ctx, c.reason())
	cancel()
	<-writerDone
	c.stopWatching()
}

// read is the inbound half: one frame at a time, until the client stops or the
// server ends the conversation.
func (c *conn) read(ctx context.Context) {
	c.socket.SetReadLimit(readLimit)
	for {
		kind, raw, err := c.socket.Read(ctx)
		if err != nil {
			return
		}
		if kind != websocket.MessageText {
			continue
		}
		if err := c.handle(ctx, raw); err != nil {
			// A command that failed is the command's outcome, not the
			// connection's: the client is told and may send another. Only the
			// reasons that end a connection stop this loop.
			if errors.Is(err, errSlowConsumer) || errors.Is(err, errClosed) {
				return
			}
			c.logger.Warn("ws: command failed", "error", err)
		}
	}
}

// write is the outbound half: it owns the socket's write side so that no two
// goroutines write a frame at once, and so that a client that stopped reading
// is discovered by a write timing out rather than by a goroutine blocking.
func (c *conn) write(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-c.out:
			if len(frame.raw) > 0 {
				writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
				err := c.socket.Write(writeCtx, websocket.MessageText, frame.raw)
				cancel()
				if frame.written != nil {
					close(frame.written)
				}
				if err != nil {
					c.record(ctx, err)
					c.closeSocket(err)
					return
				}
			}
		}
	}
}

// send queues a frame.
//
// It never blocks: a full queue means the client is not reading, and waiting for
// it would make one stalled client hold up a turn's whole fan-out.
func (c *conn) send(frame any) error {
	raw, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("ws: encode %T: %w", frame, err)
	}
	return c.enqueue(outbound{raw: raw})
}

// sendLast sends a frame and then ends the connection, so that a client which
// is being disconnected is told why before it is.
//
// It waits for the frame to be written, because the thing that ends the
// connection is what would otherwise discard it. A client that is not reading
// its frames is exactly the client this is called for, so the wait is bounded
// and giving up on it ends the connection anyway.
func (c *conn) sendLast(ctx context.Context, frame any, reason error) {
	raw, err := json.Marshal(frame)
	if err != nil {
		raw = nil
	}
	c.record(ctx, reason)

	written := make(chan struct{})
	select {
	case c.out <- outbound{raw: raw, written: written}:
	case <-ctx.Done():
		c.closeSocket(reason)
		return
	case <-time.After(queueGrace):
		// Nobody is draining the queue, which is the situation being reported.
		c.closeSocket(reason)
		return
	}

	select {
	case <-written:
	case <-ctx.Done():
	case <-time.After(writeTimeout):
	}
	c.closeSocket(reason)
}

func (c *conn) enqueue(frame outbound) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errClosed
	}
	select {
	case c.out <- frame:
		return nil
	default:
		c.ended = errSlowConsumer
		return errSlowConsumer
	}
}

// record keeps why the connection is over. The first reason is the one kept, as
// it is the one that explains the rest.
func (c *conn) record(_ context.Context, reason error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ended == nil {
		c.ended = reason
	}
}

func (c *conn) reason() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ended
}

// endConnection closes the socket once, with the reason it ended for.
func (c *conn) endConnection(ctx context.Context, reason error) {
	c.record(ctx, reason)
	c.closeSocket(reason)
}

// closeSocket shuts the socket, telling the client why when the reason is its
// own doing. It is safe to call from anywhere and more than once: the first
// close wins, and closing is also what interrupts a read that is waiting on a
// client that has gone quiet.
func (c *conn) closeSocket(reason error) {
	c.mu.Lock()
	already := c.closed
	c.closed = true
	ended := c.ended
	c.mu.Unlock()
	if already {
		return
	}

	status := websocket.StatusNormalClosure
	if errors.Is(ended, errSlowConsumer) {
		status = websocket.StatusPolicyViolation
	}
	_ = c.socket.Close(status, closeReason(ended))
}

func closeReason(err error) string {
	switch {
	case err == nil || errors.Is(err, errClosed):
		return ""
	case errors.Is(err, errSlowConsumer):
		return "the client is not reading its frames"
	default:
		return "the connection ended"
	}
}

// watch starts watching a turn, named by whatever the client asked with so that
// it can unsubscribe by the same name.
func (c *conn) watch(ctx context.Context, name, turnID string, afterSeq int64) error {
	subscription, err := c.turns.Subscribe(ctx, turnID, afterSeq)
	if err != nil {
		return err
	}

	watched := c.install(ctx, name)
	if watched == nil {
		subscription.Close()
		return errClosed
	}
	c.watching.Add(1)
	go func() {
		defer c.watching.Done()
		defer subscription.Close()
		defer c.forget(name, watched)
		c.forward(watched.ctx, subscription, turnID)
	}()
	return nil
}

// install takes the slot for a name, stopping whatever held it. A client that
// subscribes again to the same turn is correcting where it got to, and two
// subscriptions would send it two copies of everything that follows.
func (c *conn) install(ctx context.Context, name string) *watch {
	watchCtx, stop := context.WithCancel(ctx)
	watched := &watch{ctx: watchCtx, stop: stop}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		stop()
		return nil
	}
	if previous, exists := c.subs[name]; exists {
		previous.stop()
	}
	c.subs[name] = watched
	return watched
}

// forward feeds a subscription's events to the client until there are none
// left.
func (c *conn) forward(ctx context.Context, subscription Stream, turnID string) {
	for {
		select {
		case <-ctx.Done():
			return
		case record, ok := <-subscription.Events():
			if !ok {
				c.onStreamEnd(ctx, subscription.Err(), turnID)
				return
			}
			if err := c.send(toEventFrame(record)); err != nil {
				c.record(ctx, err)
				c.closeSocket(err)
				return
			}
		}
	}
}

// onStreamEnd decides what the end of a stream means for the connection.
//
// A stream that ended normally is a turn being over, which the client already
// saw in the last frame it was sent. A stream that was cut short means events
// after the last delivered one exist and this client has not been given them,
// so the connection ends and the client is told to come back for them.
func (c *conn) onStreamEnd(ctx context.Context, err error, turnID string) {
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}
	if errors.Is(err, turn.ErrSlowConsumer) {
		c.sendLast(ctx, errorFrame{
			ProtocolVersion: ProtocolVersion,
			Frame:           FrameError,
			Terminal:        true,
			Error: errorBody{
				Code:    CodeSlowConsumer,
				Message: "this connection fell behind the turn; reconnect and resume from the last seq it received",
			},
		}, errSlowConsumer)
		return
	}
	c.logger.Error("ws: the event stream ended early", "turn_id", turnID, "error", err)
	c.sendLast(ctx, errorFrame{
		ProtocolVersion: ProtocolVersion,
		Frame:           FrameError,
		Terminal:        true,
		Error:           errorBody{Code: CodeInternal, Message: "the turn's stream ended unexpectedly"},
	}, err)
}

// forget releases a name, but only if the watch being finished is the one still
// installed: a replacement has already taken the slot.
func (c *conn) forget(name string, watched *watch) {
	c.mu.Lock()
	current, exists := c.subs[name]
	if exists && current == watched {
		delete(c.subs, name)
	}
	c.mu.Unlock()
	watched.stop()
}

// unwatch stops watching what the client named, whether or not anything is
// being watched under that name.
func (c *conn) unwatch(name string) {
	c.mu.Lock()
	watched, exists := c.subs[name]
	if exists {
		delete(c.subs, name)
	}
	c.mu.Unlock()
	if exists {
		watched.stop()
	}
}

// stopWatching ends every subscription and waits for the forwarders to notice.
func (c *conn) stopWatching() {
	c.mu.Lock()
	watchers := make([]*watch, 0, len(c.subs))
	for _, watched := range c.subs {
		watchers = append(watchers, watched)
	}
	c.subs = make(map[string]*watch)
	c.mu.Unlock()

	for _, watched := range watchers {
		watched.stop()
	}
	c.watching.Wait()
}

// toEventFrame is the wire form of an event.
//
// It carries the event's own fields and nothing else: a client comparing a
// frame with a replay of the same seq should find them identical.
func toEventFrame(record event.Event) eventFrame {
	return eventFrame{
		ProtocolVersion: ProtocolVersion,
		Frame:           FrameEvent,
		TurnID:          record.TurnID,
		Seq:             record.Seq,
		Type:            string(record.Type),
		Source:          record.Source,
		Stage:           record.Stage,
		Content:         record.Content,
		Metadata:        record.Metadata,
		Timestamp:       record.Timestamp.UTC().Format(time.RFC3339Nano),
	}
}
