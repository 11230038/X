package ws

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
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
	if envelope.Error.Code == "" {
		t.Errorf("the refusal carries no code: %s", body)
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
