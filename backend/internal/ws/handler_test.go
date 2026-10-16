package ws

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAnUnauthenticatedRequestIsRefusedBeforeTheUpgrade(t *testing.T) {
	fixture := newWSFixture(t)

	socket, response, err := fixture.dial(t, "")
	if err == nil {
		_ = socket.CloseNow()
		t.Fatal("an unauthenticated request was upgraded to a socket")
	}
	if response == nil {
		t.Fatalf("Dial() failed with %v and no HTTP response to report it in", err)
	}
	defer func() { _ = response.Body.Close() }()

	// The status is the whole point of authenticating first: after a 101 there
	// is no code left to refuse with, so a client would have to infer the
	// failure from an immediate close.
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
	if response.StatusCode == http.StatusSwitchingProtocols {
		t.Fatal("the response was an upgrade")
	}

	// And the refusal is still the API's ordinary envelope, so a client parses
	// it the same way it parses every other refusal.
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading the refusal: %v", err)
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("the refusal is not the API envelope: %v (%s)", err, body)
	}
	// The code is named rather than merely required to be non-empty, because the
	// socket's gate is a different middleware from the one every other route
	// uses: a request that arrived with no credential must be answered the same
	// way it was before the ticket branch existed, and "some code" would not
	// notice if it started saying something else.
	if envelope.Error.Code != "authorization_required" {
		t.Errorf("code = %q, want authorization_required; body=%s", envelope.Error.Code, body)
	}
}

func TestAWrongTokenIsRefusedTheSameWay(t *testing.T) {
	fixture := newWSFixture(t)

	socket, response, err := fixture.dial(t, "not-the-token")
	if err == nil {
		_ = socket.CloseNow()
		t.Fatal("a request with the wrong token was upgraded to a socket")
	}
	if response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Dial() gave %v and %+v, want a 401", err, response)
	}
}

// TestATicketConnects is the path the front end actually takes, exercised
// through the same gate the server mounts. A browser has nowhere to put an
// Authorization header on a handshake, so if this did not work the interface
// would have no way to reach a turn at all.
func TestATicketConnects(t *testing.T) {
	fixture := newWSFixture(t)
	client := fixture.connectWithTicket(t)

	// Not merely connected: the account the ticket resolved to is the one the
	// connection acts as. That is asserted where it is observable — the turn the
	// connection asks for carries it — because a ticket that verified but
	// published nothing would connect just as well and run every turn as nobody.
	client.send(map[string]any{"type": CommandStartTurn, "content": "你好"})
	client.recvOf(FrameCommandAck)

	starts := fixture.turns.startRequests()
	if len(starts) != 1 {
		t.Fatalf("the runtime was asked to start %+v", starts)
	}
	if starts[0].OwnerID != testCaller.UserID {
		t.Errorf("the turn runs as account %d, want %d", starts[0].OwnerID, testCaller.UserID)
	}
}

// TestAWrongTicketIsRefusedTheSameWay pins that the ticket branch refuses at the
// same place the bearer branch does — before the upgrade. A ticket cannot be
// sent in a header, so it is the one credential whose refusal has to be read out
// of an HTTP response rather than out of a failed write.
func TestAWrongTicketIsRefusedTheSameWay(t *testing.T) {
	fixture := newWSFixture(t)

	socket, response, err := fixture.dialTicket(t, "not-the-ticket")
	if err == nil {
		_ = socket.CloseNow()
		t.Fatal("a request with the wrong ticket was upgraded to a socket")
	}
	if response == nil {
		t.Fatalf("Dial() failed with %v and no HTTP response to report it in", err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading the refusal: %v", err)
	}
	// Named, and not the same name the bearer branch gives: what a client has to
	// do about the two differs — fetch a new ticket, or sign in again — so a
	// refusal that read the same either way would be a client told to do the
	// wrong thing.
	if !strings.Contains(string(body), `"code":"invalid_ticket"`) {
		t.Errorf("body = %s, want invalid_ticket", body)
	}
}

func TestShuttingDownEndsTheConnections(t *testing.T) {
	fixture := newWSFixture(t)
	client := fixture.connect(t)

	// The socket answers first, so that the close below is a shutdown and not
	// a connection that was never established.
	client.send(map[string]any{"type": CommandPing})
	client.recvOf(FramePong)

	// A client is always reading, and has to be: the closing handshake waits
	// for the peer to answer it, so a client that is only sending would leave
	// the server waiting.
	ended := client.awaitEnd(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	if err := fixture.handler.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	select {
	case err := <-ended:
		if err == nil {
			t.Error("the connection ended without an error to report")
		}
	case <-time.After(testTimeout):
		t.Fatal("the connection was still open after the server shut down")
	}
}

func TestAHandlerNeedsSomethingToDriveAndSomewhereToLog(t *testing.T) {
	if _, err := NewHandler(nil, testLogger()); err == nil {
		t.Error("NewHandler accepted no runtime")
	}
	if _, err := NewHandler(newStubTurns(), nil); err == nil {
		t.Error("NewHandler accepted no logger")
	}
}
