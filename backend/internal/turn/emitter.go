package turn

import (
	"context"
	"fmt"
	"sync"
	"time"

	"backend/internal/event"
)

const (
	// contentFlushInterval is how long a run of answer text may sit unwritten
	// before it is sent on its own. Without it a slow model would hold every
	// fragment until the next event of a different kind, and the client would
	// see the answer arrive in one lump at the end.
	contentFlushInterval = 50 * time.Millisecond
	// contentFlushBytes is when a run of answer text is large enough to be
	// worth its own write regardless of the clock.
	contentFlushBytes = 1024
)

// Broadcaster is where a stored event goes so that whoever is watching the
// turn receives it. It is the one thing the emitter needs from the hub, which
// keeps the two testable apart.
type Broadcaster interface {
	Publish(turnID string, record event.Event)
}

// Emitter is the turn's single event outlet.
//
// It is the only writer of a turn's event stream, which is what makes Seq
// trustworthy: numbers are assigned here and nowhere else, one at a time, so
// they are gap-free and ordered by construction rather than by everyone
// agreeing to be careful.
//
// The order inside Emit is fixed: store the event, and only then publish it. A
// client that saw an event the database does not have would be unable to replay
// it, and the two views of the turn would never agree again.
type Emitter struct {
	turnID      string
	repository  Repository
	broadcaster Broadcaster
	// writeCtx bounds the background flush. It lives as long as the turn does,
	// because the flush writes a turn's own answer text rather than anything a
	// caller asked for.
	writeCtx context.Context
	now      func() time.Time

	mu sync.Mutex
	// seq is the number of the last event written, so the next one is seq+1 and
	// the first of a turn is 1.
	seq int64
	// pending is a run of answer text waiting to be written, holding the seq it
	// was assigned when the run started. The number is taken at that moment and
	// not at the write, so that a merged run keeps the place in the order that
	// its first fragment earned.
	pending *event.Event
	timer   *time.Timer
	// flushErr is a background flush that failed. Nothing is waiting on that
	// call to return, so Close carries the failure instead of it disappearing.
	flushErr error
	closed   bool

	flushInterval time.Duration
}

// NewEmitter starts the event stream of a turn that already has events.
//
// It reads where the previous stream stopped rather than assuming an empty one:
// the turn runtime is the only writer, but a turn that restarted would otherwise
// hand out numbers that are already taken.
func NewEmitter(ctx context.Context, turnID string, repository Repository, broadcaster Broadcaster) (*Emitter, error) {
	if repository == nil {
		return nil, fmt.Errorf("turn emitter needs a repository")
	}
	if broadcaster == nil {
		return nil, fmt.Errorf("turn emitter needs a broadcaster")
	}
	if ctx == nil {
		return nil, fmt.Errorf("turn emitter needs the turn's context")
	}
	lastSeq, err := repository.LastSeq(ctx, turnID)
	if err != nil {
		return nil, fmt.Errorf("find where the event stream stopped: %w", err)
	}
	return &Emitter{
		turnID:        turnID,
		repository:    repository,
		broadcaster:   broadcaster,
		writeCtx:      ctx,
		now:           time.Now,
		seq:           lastSeq,
		flushInterval: contentFlushInterval,
	}, nil
}

var _ event.Emitter = (*Emitter)(nil)

// Emit stores an event and then publishes it.
//
// Answer text is not written one fragment at a time: a run of content from the
// same source and stage is accumulated and written as one event, so that a
// long answer costs a handful of rows instead of one per token. Everything
// else is written immediately, and a run in progress is written first, so the
// stored order always follows the order things happened in.
func (e *Emitter) Emit(ctx context.Context, record event.Event) error {
	if e == nil || e.repository == nil {
		return fmt.Errorf("turn emitter is not initialized")
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return fmt.Errorf("turn emitter is closed")
	}
	if !merges(record) {
		if err := e.flushLocked(ctx); err != nil {
			return err
		}
		return e.write(ctx, e.assign(record))
	}
	if e.pending != nil && !sameRun(*e.pending, record) {
		if err := e.flushLocked(ctx); err != nil {
			return err
		}
	}
	if e.pending == nil {
		started := e.assign(record)
		e.pending = &started
		e.timer = time.AfterFunc(e.flushInterval, e.flushFromTimer)
		// The first fragment of a run can already be too large to hold: a
		// capability that produces its answer in one piece never gets a second
		// chance to trip the threshold.
		if len(started.Content) < contentFlushBytes {
			return nil
		}
		return e.flushLocked(ctx)
	}
	e.pending.Content += record.Content
	if len(e.pending.Content) < contentFlushBytes {
		return nil
	}
	return e.flushLocked(ctx)
}

// Close writes whatever is still buffered and stops the stream.
//
// It reports a flush that failed in the background, which no caller was
// waiting to hear about at the time.
func (e *Emitter) Close(ctx context.Context) error {
	if e == nil || e.repository == nil {
		return fmt.Errorf("turn emitter is not initialized")
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return e.flushErr
	}
	e.closed = true
	err := e.flushLocked(ctx)
	if err != nil {
		return err
	}
	return e.flushErr
}

// flushFromTimer is the clock-side flush: it runs on its own goroutine with no
// caller to return to, so a failure is kept for Close.
func (e *Emitter) flushFromTimer() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	_ = e.flushLocked(e.writeCtx)
}

// flushLocked writes the buffered run, if there is one. The caller holds the
// lock, which is what stops the timer and the streaming goroutine from both
// writing the same run, or from writing out of order.
func (e *Emitter) flushLocked(ctx context.Context) error {
	buffered := e.pending
	if buffered == nil {
		return nil
	}
	e.pending = nil
	e.stopTimerLocked()
	if err := e.write(ctx, *buffered); err != nil {
		e.flushErr = err
		return err
	}
	return nil
}

// write stores an event and then publishes it. The order is the whole point:
// storage is the record of what happened, and a subscriber may only be told
// about something that is already in it.
func (e *Emitter) write(ctx context.Context, record event.Event) error {
	if err := e.repository.AppendEvent(ctx, record); err != nil {
		return err
	}
	e.broadcaster.Publish(record.TurnID, record)
	return nil
}

// assign takes the next sequence number and stamps the event. Producers
// describe what happened; when it happened in the stream is not theirs to say.
func (e *Emitter) assign(record event.Event) event.Event {
	e.seq++
	record.TurnID = e.turnID
	record.Seq = e.seq
	record.Timestamp = e.now()
	return record
}

func (e *Emitter) stopTimerLocked() {
	if e.timer == nil {
		return
	}
	e.timer.Stop()
	e.timer = nil
}

// merges reports whether an event is answer text that can wait. Text carrying
// metadata is not merged: the metadata would have nowhere to go in a run that
// only accumulates text.
func merges(record event.Event) bool {
	return record.Type == event.TypeContent && len(record.Metadata) == 0
}

// sameRun reports whether two content events belong to one run of text. A
// different source or stage means a different producer, and merging across
// them would attribute one producer's words to another.
func sameRun(buffered, record event.Event) bool {
	return buffered.Source == record.Source && buffered.Stage == record.Stage
}
