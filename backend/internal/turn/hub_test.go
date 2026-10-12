package turn

import (
	"context"
	"errors"
	"testing"
	"time"

	"backend/internal/event"
)

// at is one stored event of the turn these tests watch.
func at(seq int64, kind event.Type) event.Event {
	return event.Event{TurnID: "turn_1", Seq: seq, Type: kind}
}

func newTestHub(t *testing.T) (*Hub, *stubRepository) {
	t.Helper()
	repository := newStubRepository()
	// A history long enough to be read in one go rather than one event at a
	// time, so a test that does not set the buffer is not testing the buffer.
	repository.lastSeq = 0
	hub, err := NewHub(repository)
	if err != nil {
		t.Fatalf("NewHub() error = %v", err)
	}
	return hub, repository
}

// next reads one event, failing if the stream ends or nothing arrives.
func next(t *testing.T, subscription *Subscription) event.Event {
	t.Helper()
	select {
	case record, ok := <-subscription.Events():
		if !ok {
			t.Fatalf("the stream ended while an event was expected: %v", subscription.Err())
		}
		return record
	case <-time.After(2 * time.Second):
		t.Fatal("no event arrived")
		return event.Event{}
	}
}

// drain reads until the stream ends.
func drain(t *testing.T, subscription *Subscription) []event.Event {
	t.Helper()
	var seen []event.Event
	for {
		select {
		case record, ok := <-subscription.Events():
			if !ok {
				return seen
			}
			seen = append(seen, record)
		case <-time.After(2 * time.Second):
			t.Fatalf("the stream did not end, having delivered %v", seqsOf(seen))
		}
	}
}

func seqsOf(records []event.Event) []int64 {
	seqs := make([]int64, 0, len(records))
	for _, record := range records {
		seqs = append(seqs, record.Seq)
	}
	return seqs
}

func sameSeqs(got []event.Event, want []int64) bool {
	seqs := seqsOf(got)
	if len(seqs) != len(want) {
		return false
	}
	for i := range want {
		if seqs[i] != want[i] {
			return false
		}
	}
	return true
}

func TestNewHubNeedsARepository(t *testing.T) {
	if _, err := NewHub(nil); err == nil {
		t.Fatal("NewHub(nil) error = nil, want a rejection")
	}
}

func TestAnEventReachesEverySubscriber(t *testing.T) {
	hub, _ := newTestHub(t)
	ctx := context.Background()

	first, err := hub.Subscribe(ctx, "turn_1", 0)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer first.Close()
	second, err := hub.Subscribe(ctx, "turn_1", 0)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer second.Close()

	hub.Publish("turn_1", at(1, event.TypeContent))

	// Two browsers on one conversation is the ordinary case, not a special one.
	for _, subscription := range []*Subscription{first, second} {
		if got := next(t, subscription).Seq; got != 1 {
			t.Errorf("Seq = %d, want 1", got)
		}
	}
}

func TestAnEventForAnotherTurnDoesNotReachASubscriber(t *testing.T) {
	hub, _ := newTestHub(t)
	subscription, err := hub.Subscribe(context.Background(), "turn_1", 0)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer subscription.Close()

	hub.Publish("turn_2", at(1, event.TypeContent))
	hub.Publish("turn_1", at(1, event.TypeSessionMeta))

	// The turn_2 event is numbered 1 as well, so anything leaking across turns
	// would also be a duplicate number.
	if got := seqsOf(drain(t, subscription)); len(got) != 1 || got[0] != 1 {
		t.Errorf("received %v, want only the event of turn_1", got)
	}
}

func TestASubscriberIsCaughtUpFromHistory(t *testing.T) {
	hub, repository := newTestHub(t)
	repository.seed(
		at(1, event.TypeContent),
		at(2, event.TypeContent),
		at(3, event.TypeDone),
		at(4, event.TypeSessionMeta),
	)

	subscription, err := hub.Subscribe(context.Background(), "turn_1", 0)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer subscription.Close()

	seen := drain(t, subscription)
	// Zero means "nothing yet", so subscribing from nothing has to produce the
	// whole turn in the order it happened.
	if !sameSeqs(seen, []int64{1, 2, 3, 4}) {
		t.Errorf("received %v, want 1, 2, 3, 4", seqsOf(seen))
	}
	if err := subscription.Err(); err != nil {
		t.Errorf("Err() = %v, want a stream that ended normally", err)
	}
}

func TestASubscriberStartsFromItsOwnPosition(t *testing.T) {
	hub, repository := newTestHub(t)
	repository.seed(
		at(1, event.TypeContent),
		at(2, event.TypeContent),
		at(3, event.TypeDone),
		at(4, event.TypeSessionMeta),
	)

	// This is what a client says after a reconnect: it has the first two, and
	// repeating them would put the same words on screen twice.
	subscription, err := hub.Subscribe(context.Background(), "turn_1", 2)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer subscription.Close()

	if seen := drain(t, subscription); !sameSeqs(seen, []int64{3, 4}) {
		t.Errorf("received %v, want 3, 4", seqsOf(seen))
	}
}

func TestASubscriberUpToDateWithAFinishedTurnIsClosed(t *testing.T) {
	hub, repository := newTestHub(t)
	repository.seed(at(1, event.TypeDone), at(2, event.TypeSessionMeta))

	// Nothing to replay, and nothing left to wait for: without the second
	// question this subscriber would sit on a stream that is already complete.
	subscription, err := hub.Subscribe(context.Background(), "turn_1", 2)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer subscription.Close()

	if seen := drain(t, subscription); len(seen) != 0 {
		t.Errorf("received %v, want nothing", seqsOf(seen))
	}
	if err := subscription.Err(); err != nil {
		t.Errorf("Err() = %v, want a stream that ended normally", err)
	}
}

func TestTheStreamEndsAtSessionMeta(t *testing.T) {
	hub, _ := newTestHub(t)
	subscription, err := hub.Subscribe(context.Background(), "turn_1", 0)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer subscription.Close()

	hub.Publish("turn_1", at(1, event.TypeContent))
	hub.Publish("turn_1", at(2, event.TypeDone))
	hub.Publish("turn_1", at(3, event.TypeSessionMeta))

	seen := drain(t, subscription)
	if !sameSeqs(seen, []int64{1, 2, 3}) {
		t.Errorf("received %v, want 1, 2, 3", seqsOf(seen))
	}
	if err := subscription.Err(); err != nil {
		t.Errorf("Err() = %v, want a stream that ended normally", err)
	}
}

func TestAnEventPublishedDuringTheReplayIsNeitherLostNorRepeated(t *testing.T) {
	hub, repository := newTestHub(t)
	repository.seed(at(1, event.TypeContent), at(2, event.TypeContent))

	// The history is held open, so everything published from here on lands
	// beside a replay that is already under way.
	repository.eventsHold = make(chan struct{})
	subscription, err := hub.Subscribe(context.Background(), "turn_1", 0)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer subscription.Close()

	hub.Publish("turn_1", at(3, event.TypeContent))
	hub.Publish("turn_1", at(4, event.TypeDone))
	hub.Publish("turn_1", at(5, event.TypeSessionMeta))
	close(repository.eventsHold)

	// 3 is in neither the history nor the live stream alone — it was published
	// after the read began — and being in both would be the other failure.
	if seen := drain(t, subscription); !sameSeqs(seen, []int64{1, 2, 3, 4, 5}) {
		t.Errorf("received %v, want 1, 2, 3, 4, 5", seqsOf(seen))
	}
}

func TestASubscriberThatFallsBehindIsCutOff(t *testing.T) {
	hub, repository := newTestHub(t)
	hub.buffer = 1
	repository.seed(at(1, event.TypeContent), at(2, event.TypeContent), at(3, event.TypeContent))

	subscription, err := hub.Subscribe(context.Background(), "turn_1", 0)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer subscription.Close()

	seen := drain(t, subscription)
	// Dropping the rest silently would leave the client with a gap it cannot
	// detect; ending the stream tells it to ask again from where it got to.
	if !errors.Is(subscription.Err(), ErrSlowConsumer) {
		t.Errorf("Err() = %v, want ErrSlowConsumer", subscription.Err())
	}
	// What it did receive is a prefix, and it is short of the whole turn: how
	// much fitted depends on how fast the reader was, but that it stopped early
	// does not.
	if len(seen) >= 3 {
		t.Errorf("received %v, want the stream cut short", seqsOf(seen))
	}
	for i, record := range seen {
		if record.Seq != int64(i+1) {
			t.Errorf("event %d is seq %d, want %d", i, record.Seq, i+1)
		}
	}
}

func TestASubscriberThatFallsBehindDuringTheReplayIsCutOff(t *testing.T) {
	hub, repository := newTestHub(t)
	hub.buffer = 1

	repository.eventsHold = make(chan struct{})
	subscription, err := hub.Subscribe(context.Background(), "turn_1", 0)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer subscription.Close()

	hub.Publish("turn_1", at(1, event.TypeContent))
	hub.Publish("turn_1", at(2, event.TypeContent))
	close(repository.eventsHold)

	if !errors.Is(subscription.Err(), ErrSlowConsumer) {
		t.Errorf("Err() = %v, want ErrSlowConsumer", subscription.Err())
	}
}

func TestClosingASubscriptionStopsTheStream(t *testing.T) {
	hub, _ := newTestHub(t)
	subscription, err := hub.Subscribe(context.Background(), "turn_1", 0)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	hub.Publish("turn_1", at(1, event.TypeContent))
	if got := next(t, subscription).Seq; got != 1 {
		t.Fatalf("Seq = %d, want 1", got)
	}
	subscription.Close()

	// A client that unsubscribed chose to stop hearing about this turn, which
	// is not the turn's failure and must not be reported as one.
	hub.Publish("turn_1", at(2, event.TypeContent))
	if _, ok := <-subscription.Events(); ok {
		t.Error("an event arrived after the subscription was closed")
	}
	if err := subscription.Err(); err != nil {
		t.Errorf("Err() = %v, want a close the caller asked for", err)
	}
	// Closing twice is what a connection teardown does after an explicit
	// unsubscribe, and it must not be an error.
	subscription.Close()
}

func TestASubscriberCanAlwaysReadWithoutBlockingThePublisher(t *testing.T) {
	hub, _ := newTestHub(t)
	subscription, err := hub.Subscribe(context.Background(), "turn_1", 0)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer subscription.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for seq := int64(1); seq <= 100; seq++ {
			hub.Publish("turn_1", at(seq, event.TypeContent))
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publishing blocked on a subscriber")
	}
	if subscription.Err() != nil {
		t.Errorf("Err() = %v, want a stream that is still running", subscription.Err())
	}
}
