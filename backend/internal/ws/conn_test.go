package ws

import (
	"context"
	"errors"
	"testing"
)

func TestAFullQueueIsAClientThatStoppedReading(t *testing.T) {
	// A connection with nowhere to put a frame, so that one frame is already
	// more than the client is taking.
	connection := newConn(nil, newStubTurns(), testLogger(), 0)

	err := connection.send(pongFrame{ProtocolVersion: ProtocolVersion, Frame: FramePong})
	if !errors.Is(err, errSlowConsumer) {
		t.Fatalf("send() error = %v, want the client to be reported as not reading", err)
	}

	// The reason is kept, because it is what the connection ends as: a client
	// that fell behind is disconnected, not quietly served a hole.
	if !errors.Is(connection.reason(), errSlowConsumer) {
		t.Errorf("reason() = %v, want the connection to have ended for falling behind", connection.reason())
	}

	// And a second frame is refused rather than queued: the connection is over,
	// and pretending otherwise would let a turn keep pushing events into it.
	if err := connection.send(pongFrame{ProtocolVersion: ProtocolVersion, Frame: FramePong}); err == nil {
		t.Error("send() accepted a frame after the queue had already overflowed")
	}
}

func TestTheFirstReasonIsTheOneKept(t *testing.T) {
	connection := newConn(nil, newStubTurns(), testLogger(), queueSize)
	connection.record(context.Background(), errSlowConsumer)
	connection.record(context.Background(), errClosed)

	// The first reason explains the rest: a client that fell behind and was
	// then closed by the shutdown did not end because of the shutdown.
	if !errors.Is(connection.reason(), errSlowConsumer) {
		t.Errorf("reason() = %v, want the first reason", connection.reason())
	}
}
