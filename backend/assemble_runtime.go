package main

// The joins between the assembled runtime and the layers that drive it.
//
// Each of these is a seam rather than a decision: the routing table speaks the
// product's language and the runtime speaks turns, the hub speaks subscriptions
// and the protocol layer speaks streams. Somebody has to say the two ends are
// the same thing, and this is where that is said. Keeping them out of assemble
// leaves that function about construction only.

import (
	"context"
	"fmt"

	"backend/internal/routing"
	"backend/internal/turn"
	"backend/internal/ws"
)

// questionRouting is the join between the rule table and the runtime.
//
// The two ends are kept apart on purpose — the table is a leaf that knows the
// language of the product's users and nothing about turns, and the runtime knows
// what runs a turn and nothing about how a sentence is read — so somebody has to
// say that these two are the same decision, and this is that place. It is the
// same seam as the source and question tools: what a turn calls is declared in
// the turn package, implemented elsewhere, and joined here.
func questionRouting() turn.RouterFunc {
	return func(content string) (turn.Routing, bool) {
		decision, matched := routing.Route(content)
		if !matched {
			return turn.Routing{}, false
		}
		return turn.Routing{
			Capability: decision.Capability,
			By:         decision.By,
			Reason:     decision.Reason,
			Confidence: decision.Confidence,
		}, true
	}
}

// turnRuntime adapts the turn runtime to what the protocol layer drives.
//
// The two differ in one method: a subscription is the hub's own type, and a
// connection needs only the events, the reason they stopped and the way to stop
// them. Narrowing it here is what keeps the protocol layer from depending on
// how a turn is watched, and what lets its tests drive a stream by hand.
type turnRuntime struct {
	*turn.Runner
}

var _ ws.Turns = turnRuntime{}

func (t turnRuntime) Subscribe(ctx context.Context, turnID string, afterSeq int64) (ws.Stream, error) {
	subscription, err := t.Runner.Subscribe(ctx, turnID, afterSeq)
	if err != nil {
		return nil, err
	}
	return subscription, nil
}

// close stops the socket, then the turns, then the index worker.
//
// The order matters: a turn that is ending publishes its ending, and a client
// that is still connected is told what it ended as. Closing the runtime first
// would leave whoever is watching a stream that stops without saying why.
//
// The worker goes last and its wait is bounded by ctx, which is the shutdown
// timeout: a document half embedded is one whose claim the lease will hand back
// on the next start, so giving up on it is a delay rather than a loss.
func (a *ai) close(ctx context.Context) error {
	if err := a.socket.Close(ctx); err != nil {
		return fmt.Errorf("close websocket connections: %w", err)
	}
	if err := a.turns.Close(ctx); err != nil {
		return fmt.Errorf("stop running turns: %w", err)
	}
	if a.indexing == nil {
		return nil
	}
	select {
	case <-a.indexing:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("stop the index worker: %w", ctx.Err())
	}
}
