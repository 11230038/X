package turn

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"backend/internal/event"
)

// ErrSlowConsumer reports a subscriber that stopped keeping up with a turn.
//
// The subscription ends rather than dropping events. A client that misses one
// cannot tell that it did, and the reason a turn has sequence numbers at all is
// that nobody should have to.
var ErrSlowConsumer = errors.New("turn: the subscriber fell behind")

// subscriptionBuffer is how far behind a subscriber may fall. It is large
// enough that a client writing frames at network speed never reaches it, so
// reaching it means the client has stopped reading.
const subscriptionBuffer = 256

// Hub is who is watching a turn, and the one place a turn's events reach them.
//
// It is the Broadcaster the emitter publishes to, which is why a subscriber
// only ever hears about an event that is already stored: the emitter publishes
// nothing else. That ordering is what lets a subscriber be told where it got to
// and be caught up from storage afterwards, however many times it reconnects.
type Hub struct {
	repository Repository
	// buffer is the per-subscriber backlog, held per hub so that a test can
	// make falling behind happen on purpose.
	buffer int

	mu       sync.Mutex
	watchers map[string]map[*Subscription]struct{}
}

// NewHub returns a hub reading a turn's history from storage.
func NewHub(repository Repository) (*Hub, error) {
	if repository == nil {
		return nil, fmt.Errorf("turn hub needs a repository")
	}
	return &Hub{
		repository: repository,
		buffer:     subscriptionBuffer,
		watchers:   make(map[string]map[*Subscription]struct{}),
	}, nil
}

var _ Broadcaster = (*Hub)(nil)

// Publish hands an event to everyone watching the turn.
//
// It copies the watcher list before delivering, so a subscriber that closes
// while another is being delivered to cannot deadlock against the map.
func (h *Hub) Publish(turnID string, record event.Event) {
	h.mu.Lock()
	watchers := make([]*Subscription, 0, len(h.watchers[turnID]))
	for subscription := range h.watchers[turnID] {
		watchers = append(watchers, subscription)
	}
	h.mu.Unlock()

	for _, subscription := range watchers {
		subscription.deliver(record)
	}
}

// Subscribe starts watching a turn from a sequence number.
//
// It returns before the history has been replayed, because the replay is
// delivered through the same channel as everything else: a caller that had to
// wait for it would have to read the channel while Subscribe was filling it.
// The history arrives first all the same — deliveries that land while it is
// being read are held back until it has been sent.
func (h *Hub) Subscribe(ctx context.Context, turnID string, afterSeq int64) (*Subscription, error) {
	if h == nil || h.repository == nil {
		return nil, fmt.Errorf("turn hub is not initialized")
	}
	subscription := &Subscription{
		hub:    h,
		turnID: turnID,
		events: make(chan event.Event, h.buffer),
		// Held from the start rather than set inside the goroutine: an event
		// published in between would otherwise reach the channel before the
		// history it comes after.
		replaying: true,
		lastSent:  afterSeq,
	}
	h.watch(subscription)
	go subscription.replay(ctx, afterSeq)
	return subscription, nil
}

func (h *Hub) watch(subscription *Subscription) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.watchers[subscription.turnID] == nil {
		h.watchers[subscription.turnID] = make(map[*Subscription]struct{})
	}
	h.watchers[subscription.turnID][subscription] = struct{}{}
}

func (h *Hub) forget(subscription *Subscription) {
	h.mu.Lock()
	defer h.mu.Unlock()
	watchers, ok := h.watchers[subscription.turnID]
	if !ok {
		return
	}
	delete(watchers, subscription)
	if len(watchers) == 0 {
		delete(h.watchers, subscription.turnID)
	}
}

// Subscription is one watcher's view of a turn: the events it has not seen yet,
// oldest first, ending when the turn's stream does.
type Subscription struct {
	hub    *Hub
	turnID string

	mu     sync.Mutex
	events chan event.Event
	// lastSent is the highest sequence number this subscriber has been given.
	// It is what makes a replay and the events arriving beside it agree without
	// either having to know about the other.
	lastSent int64
	// staged holds events published while the history was being read. They are
	// delivered once it has been, in order, and only the ones it did not
	// already carry.
	staged    []event.Event
	replaying bool
	err       error
	closed    bool
}

// Events is the stream of events this subscriber has not seen. It closes when
// the subscription ends, which may be because the turn is over or because the
// subscriber fell behind; Err says which.
func (s *Subscription) Events() <-chan event.Event { return s.events }

// Err reports why the subscription ended: nil if the turn's stream finished
// normally, and otherwise the reason it was cut short.
func (s *Subscription) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Close stops the subscription. It is safe to call more than once.
func (s *Subscription) Close() {
	s.mu.Lock()
	s.endLocked(nil)
	s.mu.Unlock()
	s.hub.forget(s)
}

// deliver hands an event to this subscriber, unless it has already been sent.
func (s *Subscription) deliver(record event.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || record.Seq <= s.lastSent {
		return
	}
	if s.replaying {
		if len(s.staged) >= s.hub.buffer {
			s.endLocked(ErrSlowConsumer)
			return
		}
		s.staged = append(s.staged, record)
		return
	}
	s.pushLocked(record)
}

// replay sends the history a subscriber asked for, then everything that arrived
// while it was being read.
func (s *Subscription) replay(ctx context.Context, afterSeq int64) {
	stored, err := s.hub.repository.Events(ctx, s.turnID, afterSeq)
	if err != nil {
		s.finishReplay(ctx, err)
		return
	}
	for _, record := range stored {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		if record.Seq > s.lastSent {
			s.pushLocked(record)
		}
		s.mu.Unlock()
	}
	s.finishReplay(ctx, nil)
}

func (s *Subscription) finishReplay(ctx context.Context, err error) {
	s.mu.Lock()
	s.replaying = false
	staged := s.staged
	s.staged = nil
	if err != nil {
		s.endLocked(err)
		s.mu.Unlock()
		return
	}
	for _, record := range staged {
		if s.closed {
			s.mu.Unlock()
			return
		}
		// A staged event the history already carried is the same event seen
		// twice, which is exactly what lastSent exists to settle.
		if record.Seq > s.lastSent {
			s.pushLocked(record)
		}
	}
	ended := s.closed
	s.mu.Unlock()
	if ended {
		return
	}
	s.endIfTheTurnDid(ctx)
}

// endIfTheTurnDid closes a subscription whose turn has nothing left to produce.
//
// A subscriber that arrives after a turn ended sees the whole history and would
// otherwise wait on a stream that is already complete. The history just read
// cannot answer this on its own: a subscriber whose sequence number is already
// at the end is handed nothing at all.
func (s *Subscription) endIfTheTurnDid(ctx context.Context) {
	last, err := s.hub.repository.LastEvent(ctx, s.turnID)
	if err != nil {
		// No events yet, or the store could not be read: either way this is not
		// a stream that has ended, so the subscriber stays for what is to come.
		return
	}
	if !endsStream(last) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.endLocked(nil)
}

func (s *Subscription) pushLocked(record event.Event) {
	select {
	case s.events <- record:
		s.lastSent = record.Seq
		if endsStream(record) {
			s.endLocked(nil)
		}
	default:
		s.endLocked(ErrSlowConsumer)
	}
}

// endLocked finishes the subscription. The first reason is the one kept: a
// subscriber cut off for falling behind did not end normally, whatever the turn
// went on to do.
func (s *Subscription) endLocked(reason error) {
	if s.closed {
		return
	}
	s.closed = true
	if s.err == nil {
		s.err = reason
	}
	close(s.events)
}

// endsStream reports whether an event is the last one its turn will produce.
//
// Every turn's stream ends with session_meta: the runtime writes it after the
// terminal event, so that a client learns the turn's outcome and then, once
// nothing can follow, that the stream is over (WS-07).
func endsStream(record event.Event) bool {
	return record.Type == event.TypeSessionMeta
}
