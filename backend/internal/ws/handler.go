package ws

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"

	"backend/internal/authz"
	"backend/internal/httpx"
	"backend/internal/middleware"
	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

// queueSize is how many frames may be waiting for one client.
//
// It is a backlog, not a buffer: a client reading at network speed never fills
// it, so filling it means the client has stopped, and a client that has stopped
// is disconnected rather than served a conversation with a hole in it.
const queueSize = 64

// Handler serves the WebSocket endpoint.
//
// It authenticates before upgrading, so an unauthenticated request is answered
// as an ordinary HTTP request and never becomes a socket. That ordering is the
// point: after an upgrade there is no status code left to refuse with.
type Handler struct {
	turns  Turns
	logger *slog.Logger

	mu     sync.Mutex
	live   map[*conn]struct{}
	closed bool
	// serving counts the connections being handled, so that shutdown can wait
	// for them rather than pulling the socket out from under a frame mid-write.
	serving sync.WaitGroup
}

// NewHandler constructs the WebSocket handler over the turn runtime.
func NewHandler(turns Turns, logger *slog.Logger) (*Handler, error) {
	if turns == nil {
		return nil, errors.New("ws: a handler needs the turn runtime")
	}
	if logger == nil {
		return nil, errors.New("ws: a handler needs a logger")
	}
	return &Handler{turns: turns, logger: logger, live: make(map[*conn]struct{})}, nil
}

// Serve upgrades an authenticated request and runs the connection until it ends.
//
// It is registered under the authentication middleware, which is what makes the
// account available here; the check below is the defence for a route that was
// mounted without it.
func (h *Handler) Serve(c *gin.Context) {
	if h == nil || h.turns == nil {
		httpx.WriteError(c, http.StatusInternalServerError, "websocket_unavailable", "the websocket endpoint is not configured")
		return
	}
	user, authenticated := middleware.CurrentUser(c)
	if !authenticated || user.UserID <= 0 {
		httpx.WriteError(c, http.StatusUnauthorized, "authorization_required", "bearer token is required")
		return
	}

	socket, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{
		// The browser origin policy the library applies by default stays on:
		// this version has no browser client, and the way to add one is a
		// deliberate decision about origins rather than a permissive default.
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		// Not an upgrade request, or one the library refused before reading it
		// as a socket. There is still an HTTP response to write.
		h.logger.Warn("ws: upgrade refused", "error", err)
		httpx.WriteError(c, http.StatusBadRequest, "upgrade_failed", "this endpoint expects a websocket upgrade")
		return
	}

	// The account is read here and carried on the connection, because this is the
	// last place the request still has it: past the upgrade there is no gin
	// context to ask, and a frame naming a conversation cannot say whose it is —
	// a socket that could would be a socket that could claim somebody else's.
	connection := newConn(socket, h.turns, authz.Caller{UserID: user.UserID, Role: user.Role}, h.logger, queueSize)
	if !h.adopt(connection) {
		_ = socket.Close(websocket.StatusGoingAway, "the server is shutting down")
		return
	}
	defer h.release(connection)

	// The connection's work is bounded by the request context, which gin
	// cancels when the handler returns — and by the server's shutdown, which
	// closes it from Close below.
	connection.serve(c.Request.Context())
}

// adopt registers a connection, refusing it if the server is shutting down.
func (h *Handler) adopt(connection *conn) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	h.live[connection] = struct{}{}
	h.serving.Add(1)
	return true
}

func (h *Handler) release(connection *conn) {
	h.mu.Lock()
	delete(h.live, connection)
	h.mu.Unlock()
	h.serving.Done()
}

// Close ends every live connection and waits for them to finish.
//
// An HTTP server does not own a hijacked connection, so shutting it down does
// not close one. Without this, a graceful shutdown would return while clients
// still held sockets the process is about to stop serving.
func (h *Handler) Close(ctx context.Context) error {
	h.mu.Lock()
	h.closed = true
	connections := make([]*conn, 0, len(h.live))
	for connection := range h.live {
		connections = append(connections, connection)
	}
	h.mu.Unlock()

	for _, connection := range connections {
		connection.endConnection(ctx, errClosed)
	}

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		h.serving.Wait()
	}()
	select {
	case <-stopped:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
