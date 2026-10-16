package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"backend/internal/auth"
	"backend/internal/authz"
	"backend/internal/event"
	"backend/internal/middleware"
	"backend/internal/session"
	"backend/internal/turn"
	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

const testToken = "test-token"

// testCaller is the account the connection cases are opened for. What those
// cases are about is the wire, so there is one of these and none of them varies
// it.
var testCaller = authz.Caller{UserID: 1, Role: authz.RoleUser}

// testLogger logs nowhere. What these tests assert on is the wire, so a logger
// that writes to the test output would only add noise.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
}

// testTimeout bounds every wait in these tests, so that a missing frame fails
// the test rather than hanging it.
const testTimeout = 3 * time.Second

// stubVerifier accepts exactly one token.
type stubVerifier struct{}

var _ middleware.TokenVerifier = stubVerifier{}

func (stubVerifier) Authenticate(_ context.Context, token string) (auth.User, error) {
	if token != testToken {
		return auth.User{}, auth.ErrInvalidToken
	}
	return auth.User{UserID: 1, Username: "tester", Role: "user"}, nil
}

// testTicket is the ticket the fixture's socket accepts, and stubTickets the
// verifier that accepts exactly it. The two credentials resolve to the same
// account, because in the server they do: a ticket is not a second identity, it
// is the same one reached by a shorter-lived handle that fits in a URL.
const testTicket = "test-ticket"

type stubTickets struct{}

var _ middleware.TicketVerifier = stubTickets{}

func (stubTickets) Redeem(_ context.Context, ticket string) (auth.User, error) {
	if ticket != testTicket {
		return auth.User{}, auth.ErrInvalidTicket
	}
	return auth.User{UserID: 1, Username: "tester", Role: "user"}, nil
}

// stubStream is a turn's event stream, driven by the test rather than by a hub.
type stubStream struct {
	events chan event.Event
	// ended is what Err reports once the stream is over.
	ended error

	once sync.Once
	done chan struct{}
}

func newStubStream() *stubStream {
	return &stubStream{events: make(chan event.Event, 16), done: make(chan struct{})}
}

func (s *stubStream) Events() <-chan event.Event { return s.events }
func (s *stubStream) Err() error                 { return s.ended }
func (s *stubStream) Close()                     { s.once.Do(func() { close(s.done) }) }

func (s *stubStream) closed() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// publish puts an event on the stream, as the hub would.
func (s *stubStream) publish(record event.Event) { s.events <- record }

// end finishes the stream, as the hub would when the turn's stream is over.
func (s *stubStream) end(reason error) {
	s.ended = reason
	close(s.events)
}

// stubTurns is the runtime, recording what it was asked to do.
type stubTurns struct {
	mu sync.Mutex

	starts   []turn.StartRequest
	started  turn.Started
	startErr error

	cancels   []string
	cancelErr error

	replies    []turnReply
	replyErr   error
	active     turn.Turn
	activeErr  error
	lastSeq    int64
	lastSeqErr error

	subscribed   []subscriptionRequest
	subscribeErr error
	stream       *stubStream
}

type turnReply struct {
	turnID string
	answer string
}

type subscriptionRequest struct {
	turnID   string
	afterSeq int64
}

var _ Turns = (*stubTurns)(nil)

func newStubTurns() *stubTurns {
	return &stubTurns{started: turn.Started{
		Session: session.Session{ID: "sess_1"},
		Turn:    turn.Turn{ID: "turn_1", SessionID: "sess_1", Status: turn.StatusQueued},
	}}
}

func (s *stubTurns) Start(_ context.Context, request turn.StartRequest) (turn.Started, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.starts = append(s.starts, request)
	return s.started, s.startErr
}

func (s *stubTurns) Cancel(_ context.Context, turnID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancels = append(s.cancels, turnID)
	return s.cancelErr
}

func (s *stubTurns) SubmitReply(_ context.Context, turnID, answer string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replies = append(s.replies, turnReply{turnID: turnID, answer: answer})
	return s.replyErr
}

func (s *stubTurns) ActiveTurn(_ context.Context, _ string) (turn.Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active, s.activeErr
}

func (s *stubTurns) LastSeq(_ context.Context, _ string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastSeq, s.lastSeqErr
}

func (s *stubTurns) Subscribe(_ context.Context, turnID string, afterSeq int64) (Stream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subscribed = append(s.subscribed, subscriptionRequest{turnID: turnID, afterSeq: afterSeq})
	if s.subscribeErr != nil {
		return nil, s.subscribeErr
	}
	if s.stream == nil {
		s.stream = newStubStream()
	}
	return s.stream, nil
}

func (s *stubTurns) startCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.starts)
}

func (s *stubTurns) startRequests() []turn.StartRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]turn.StartRequest(nil), s.starts...)
}

func (s *stubTurns) replyRequests() []turnReply {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]turnReply(nil), s.replies...)
}

func (s *stubTurns) cancelCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.cancels)
}

func (s *stubTurns) subscriptions() []subscriptionRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]subscriptionRequest(nil), s.subscribed...)
}

// stream waits until the connection has subscribed, which happens just after
// the frame answering the command that caused it.
func (s *stubTurns) streamFor(t *testing.T) *stubStream {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for {
		s.mu.Lock()
		stream := s.stream
		s.mu.Unlock()
		if stream != nil {
			return stream
		}
		if time.Now().After(deadline) {
			t.Fatal("the connection never subscribed to a turn")
		}
		time.Sleep(time.Millisecond)
	}
}

// wsFixture is the whole layer: the handler behind the authentication
// middleware, served over a real socket.
type wsFixture struct {
	turns   *stubTurns
	handler *Handler
	http    *httptest.Server
}

func newWSFixture(t *testing.T) *wsFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	logger := testLogger()
	turns := newStubTurns()
	handler, err := NewHandler(turns, logger)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	router := gin.New()
	// The real gate, not the bearer one: the ticket branch is the reason a
	// browser can connect at all, so a fixture that mounted RequireAuth would be
	// exercising a route the front end cannot use.
	router.GET("/api/v1/ws",
		middleware.RequireSocketAuth(stubVerifier{}, stubTickets{}, logger), handler.Serve)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	// The handler's own shutdown is what closes connections the HTTP server
	// does not own, so the fixture has no reason to close them here.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		if err := handler.Close(ctx); err != nil {
			t.Errorf("Handler.Close() error = %v", err)
		}
	})
	return &wsFixture{turns: turns, handler: handler, http: server}
}

func (f *wsFixture) url() string {
	return "ws" + strings.TrimPrefix(f.http.URL, "http") + "/api/v1/ws"
}

// dialTicket is what a browser does: it puts the credential in the URL, because
// `new WebSocket(url, protocols)` gives it nowhere else to put one.
func (f *wsFixture) dialTicket(t *testing.T, ticket string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	return websocket.Dial(ctx, f.url()+"?ticket="+ticket, &websocket.DialOptions{})
}

// connectWithTicket opens a connection the way the front end will, and returns
// the same client the bearer cases use — the wire is identical past the gate,
// which is the property being relied on rather than a convenience.
func (f *wsFixture) connectWithTicket(t *testing.T) *client {
	t.Helper()
	socket, response, err := f.dialTicket(t, testTicket)
	if err != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("Dial() error = %v (status %d)", err, status)
	}
	t.Cleanup(func() { _ = socket.CloseNow() })
	return &client{t: t, socket: socket}
}

// connect opens an authenticated connection, closed when the test ends.
func (f *wsFixture) connect(t *testing.T) *client {
	t.Helper()
	socket, response, err := f.dial(t, testToken)
	if err != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("Dial() error = %v (status %d)", err, status)
	}
	t.Cleanup(func() { _ = socket.CloseNow() })
	return &client{t: t, socket: socket}
}

func (f *wsFixture) dial(t *testing.T, token string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	options := &websocket.DialOptions{}
	if token != "" {
		options.HTTPHeader = http.Header{"Authorization": {"Bearer " + token}}
	}
	return websocket.Dial(ctx, f.url(), options)
}

// client is a test client that speaks the protocol as a client would.
type client struct {
	t      *testing.T
	socket *websocket.Conn
}

// send writes one command frame.
func (c *client) send(command map[string]any) {
	c.t.Helper()
	if _, exists := command["protocol_version"]; !exists {
		command["protocol_version"] = ProtocolVersion
	}
	raw, err := json.Marshal(command)
	if err != nil {
		c.t.Fatalf("encoding the command: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	if err := c.socket.Write(ctx, websocket.MessageText, raw); err != nil {
		c.t.Fatalf("Write() error = %v", err)
	}
}

// sendRaw writes bytes that are not necessarily a command.
func (c *client) sendRaw(raw string) {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	if err := c.socket.Write(ctx, websocket.MessageText, []byte(raw)); err != nil {
		c.t.Fatalf("Write() error = %v", err)
	}
}

// recv reads the next frame.
func (c *client) recv() map[string]any {
	c.t.Helper()
	frame, err := c.next()
	if err != nil {
		c.t.Fatalf("reading a frame: %v", err)
	}
	return frame
}

// recvOf reads the next frame and fails unless it is of the given kind.
func (c *client) recvOf(kind string) map[string]any {
	c.t.Helper()
	frame := c.recv()
	if frame["frame"] != kind {
		c.t.Fatalf("frame = %v, want a %s frame", frame, kind)
	}
	return frame
}

// waitClosed fails unless the connection ends within the timeout.
func (c *client) waitClosed() {
	c.t.Helper()
	if frame, err := c.next(); err == nil {
		c.t.Fatalf("the connection is still open and sent %v", frame)
	}
}

// awaitEnd reads from the connection in the background until it ends, as a real
// client's read loop would, and reports why it ended.
//
// It exists because the server's closing handshake waits for the peer to answer
// it: a test client that stopped reading while it waited for the server to
// close would be the reason the server could not.
func (c *client) awaitEnd(t *testing.T) <-chan error {
	t.Helper()
	ended := make(chan error, 1)
	go func() {
		for {
			_, err := c.next()
			if err == nil {
				continue
			}
			ended <- err
			return
		}
	}()
	return ended
}

func (c *client) next() (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	_, raw, err := c.socket.Read(ctx)
	if err != nil {
		return nil, err
	}
	var frame map[string]any
	if err := json.Unmarshal(raw, &frame); err != nil {
		return nil, err
	}
	return frame, nil
}

// at returns a field of a frame as a string.
func at(frame map[string]any, field string) string {
	value, _ := frame[field].(string)
	return value
}
