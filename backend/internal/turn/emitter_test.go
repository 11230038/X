package turn

import (
	"context"
	"errors"
	"testing"
	"time"

	"backend/internal/event"
)

type emitterFixture struct {
	emitter     *Emitter
	repository  *stubRepository
	broadcaster *recordingBroadcaster
	now         time.Time
}

func newEmitterFixture(t *testing.T, lastSeq int64) *emitterFixture {
	t.Helper()
	repository := newStubRepository()
	repository.lastSeq = lastSeq
	broadcaster := newRecordingBroadcaster()
	emitter, err := NewEmitter(context.Background(), "turn_1", repository, broadcaster)
	if err != nil {
		t.Fatalf("NewEmitter() error = %v", err)
	}
	now := time.Date(2026, 10, 12, 9, 30, 0, 0, time.UTC)
	emitter.now = func() time.Time { return now }
	return &emitterFixture{emitter: emitter, repository: repository, broadcaster: broadcaster, now: now}
}

// say emits a piece of answer text the way a capability does.
func (f *emitterFixture) say(t *testing.T, source, stage, text string) {
	t.Helper()
	f.emit(t, event.Event{Type: event.TypeContent, Source: source, Stage: stage, Content: text})
}

func (f *emitterFixture) emit(t *testing.T, record event.Event) {
	t.Helper()
	if err := f.emitter.Emit(context.Background(), record); err != nil {
		t.Fatalf("Emit(%s) error = %v", record.Type, err)
	}
}

func (f *emitterFixture) close(t *testing.T) error {
	t.Helper()
	return f.emitter.Close(context.Background())
}

func TestNewEmitterNeedsItsParts(t *testing.T) {
	repository := newStubRepository()
	broadcaster := newRecordingBroadcaster()
	ctx := context.Background()

	cases := map[string]struct {
		ctx         context.Context
		repository  Repository
		broadcaster Broadcaster
	}{
		"no context":     {ctx: nil, repository: repository, broadcaster: broadcaster},
		"no repository":  {ctx: ctx, repository: nil, broadcaster: broadcaster},
		"no broadcaster": {ctx: ctx, repository: repository, broadcaster: nil},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewEmitter(test.ctx, "turn_1", test.repository, test.broadcaster); err == nil {
				t.Error("NewEmitter() error = nil, want a rejection")
			}
		})
	}
}

func TestNewEmitterReportsWhereTheStreamStopped(t *testing.T) {
	storageErr := errors.New("the database is unreachable")
	repository := newStubRepository()
	repository.lastErr = storageErr

	_, err := NewEmitter(context.Background(), "turn_1", repository, newRecordingBroadcaster())
	if !errors.Is(err, storageErr) {
		t.Errorf("NewEmitter() error = %v, want the storage failure", err)
	}
}

func TestTheFirstEventOfATurnIsNumberOne(t *testing.T) {
	fixture := newEmitterFixture(t, 0)

	fixture.emit(t, event.Event{Type: event.TypeStageStart, Source: "chat", Stage: "exploring"})

	stored := fixture.repository.stored()
	if len(stored) != 1 {
		t.Fatalf("storage holds %d events", len(stored))
	}
	// Zero means "not yet assigned", so the first event has to be 1 for an
	// unset sequence number to be recognisable at all.
	if stored[0].Seq != 1 {
		t.Errorf("Seq = %d, want 1", stored[0].Seq)
	}
	if stored[0].TurnID != "turn_1" {
		t.Errorf("TurnID = %q", stored[0].TurnID)
	}
}

func TestSeqContinuesFromWhatIsAlreadyStored(t *testing.T) {
	fixture := newEmitterFixture(t, 41)

	fixture.emit(t, event.Event{Type: event.TypeStageStart})

	if got := fixture.repository.stored()[0].Seq; got != 42 {
		t.Errorf("Seq = %d, want the stream to carry on at 42", got)
	}
}

func TestTheEmitterStampsWhatTheProducerMayNot(t *testing.T) {
	fixture := newEmitterFixture(t, 0)

	// A producer filling these in would be guessing at its place in a stream it
	// cannot see the rest of.
	fixture.emit(t, event.Event{Type: event.TypeStageStart, Seq: 99, Timestamp: time.Now().Add(-time.Hour)})

	stored := fixture.repository.stored()[0]
	if stored.Seq != 1 {
		t.Errorf("Seq = %d, want the emitter's own number", stored.Seq)
	}
	if !stored.Timestamp.Equal(fixture.now) {
		t.Errorf("Timestamp = %v, want %v", stored.Timestamp, fixture.now)
	}
}

func TestARunOfAnswerTextIsWrittenAsOneEvent(t *testing.T) {
	fixture := newEmitterFixture(t, 0)

	for _, fragment := range []string{"闭", "包", "是"} {
		fixture.say(t, "chat", "responding", fragment)
	}
	// Nothing is written while the run is still growing: one row per token is
	// what this costs without merging.
	if stored := fixture.repository.stored(); len(stored) != 0 {
		t.Fatalf("storage holds %d events mid-run", len(stored))
	}

	if err := fixture.close(t); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	stored := fixture.repository.stored()
	if len(stored) != 1 {
		t.Fatalf("storage holds %d events, want the run merged", len(stored))
	}
	if stored[0].Content != "闭包是" {
		t.Errorf("Content = %q", stored[0].Content)
	}
	if stored[0].Seq != 1 {
		t.Errorf("Seq = %d, want the number the run started with", stored[0].Seq)
	}
	if stored[0].Source != "chat" || stored[0].Stage != "responding" {
		t.Errorf("Source/Stage = %q/%q", stored[0].Source, stored[0].Stage)
	}
}

func TestARunIsWrittenBeforeTheEventThatInterruptsIt(t *testing.T) {
	fixture := newEmitterFixture(t, 0)

	fixture.say(t, "chat", "responding", "让我查一下")
	fixture.emit(t, event.Event{Type: event.TypeToolCall, Source: "chat", Stage: "responding", Content: "ask_user"})
	fixture.emit(t, event.Event{Type: event.TypeDone})

	stored := fixture.repository.stored()
	if len(stored) != 3 {
		t.Fatalf("storage holds %d events: %+v", len(stored), stored)
	}
	// The buffered text happened first, so it has to be stored and numbered
	// first — a client replaying the stream would otherwise read the answer
	// after the tool call that interrupted it.
	want := []event.Type{event.TypeContent, event.TypeToolCall, event.TypeDone}
	for i, expected := range want {
		if stored[i].Type != expected {
			t.Errorf("event %d is %q, want %q", i, stored[i].Type, expected)
		}
		if stored[i].Seq != int64(i+1) {
			t.Errorf("event %d has Seq %d, want %d", i, stored[i].Seq, i+1)
		}
	}
}

func TestARunIsWrittenWhenItGrowsTooLarge(t *testing.T) {
	fixture := newEmitterFixture(t, 0)
	// Well past the clock's patience, so only the size can end this run.
	fixture.emitter.flushInterval = time.Hour

	blob := make([]byte, contentFlushBytes)
	for i := range blob {
		blob[i] = 'x'
	}
	fixture.say(t, "chat", "responding", string(blob))

	// The write that follows is not asserted on: what matters is that the run
	// did not sit in memory waiting for an event that never comes.
	fixture.repository.awaitAttempts(t, 1)
}

func TestARunIsWrittenWhenTheClockRunsOut(t *testing.T) {
	fixture := newEmitterFixture(t, 0)
	fixture.emitter.flushInterval = time.Millisecond

	fixture.say(t, "chat", "responding", "慢")

	// Nothing but the clock can end this run — no interrupting event, and far
	// less text than the size threshold.
	fixture.repository.awaitAttempts(t, 1)
	if stored := fixture.repository.stored(); len(stored) != 1 || stored[0].Content != "慢" {
		t.Errorf("storage holds %+v", stored)
	}
	if err := fixture.close(t); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestAnswerTextFromDifferentProducersIsNotMerged(t *testing.T) {
	fixture := newEmitterFixture(t, 0)

	fixture.say(t, "chat", "responding", "第一段")
	fixture.say(t, "chat", "exploring", "第二段")
	fixture.say(t, "deep_solve", "responding", "第三段")
	if err := fixture.close(t); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Merging across producers would put one capability's words in another's
	// event, where the stage and source no longer describe them.
	stored := fixture.repository.stored()
	if len(stored) != 3 {
		t.Fatalf("storage holds %d events: %+v", len(stored), stored)
	}
	for i, want := range []string{"第一段", "第二段", "第三段"} {
		if stored[i].Content != want {
			t.Errorf("event %d is %q, want %q", i, stored[i].Content, want)
		}
	}
}

func TestAnswerTextCarryingMetadataIsNotMerged(t *testing.T) {
	fixture := newEmitterFixture(t, 0)

	fixture.say(t, "chat", "responding", "前")
	fixture.emit(t, event.Event{
		Type: event.TypeContent, Source: "chat", Stage: "responding", Content: "后",
		Metadata: map[string]any{"note": "kept"},
	})
	if err := fixture.close(t); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// A run accumulates text and nothing else, so metadata has to be its own
	// event or it would be dropped.
	stored := fixture.repository.stored()
	if len(stored) != 2 {
		t.Fatalf("storage holds %d events: %+v", len(stored), stored)
	}
	if stored[1].Metadata["note"] != "kept" {
		t.Errorf("metadata = %+v", stored[1].Metadata)
	}
}

func TestAnEventIsStoredBeforeItIsPublished(t *testing.T) {
	var trace []string
	fixture := newEmitterFixture(t, 0)
	fixture.repository.trace = &trace
	fixture.broadcaster.trace = &trace

	fixture.emit(t, event.Event{Type: event.TypeStageStart, Source: "chat"})

	// A subscriber told about an event storage does not have could never be
	// caught up: replaying the turn would skip something it already saw.
	want := []string{"store:stage_start", "publish:stage_start"}
	if len(trace) != len(want) {
		t.Fatalf("trace = %v, want %v", trace, want)
	}
	for i := range want {
		if trace[i] != want[i] {
			t.Fatalf("trace = %v, want %v", trace, want)
		}
	}
}

func TestAStorageFailureIsReportedAndNothingIsPublished(t *testing.T) {
	fixture := newEmitterFixture(t, 0)
	storageErr := errors.New("the database is unreachable")
	fixture.repository.appendErr = storageErr

	err := fixture.emitter.Emit(context.Background(), event.Event{Type: event.TypeStageStart})
	if !errors.Is(err, storageErr) {
		t.Errorf("Emit() error = %v, want the storage failure", err)
	}
	if told := fixture.broadcaster.told(); len(told) != 0 {
		t.Errorf("subscribers were told about %+v", told)
	}
}

func TestABackgroundFlushFailureIsReportedAtClose(t *testing.T) {
	fixture := newEmitterFixture(t, 0)
	fixture.emitter.flushInterval = time.Millisecond
	storageErr := errors.New("the database is unreachable")
	fixture.repository.appendErr = storageErr

	fixture.say(t, "chat", "responding", "慢")
	// The clock writes this run on its own goroutine, where a failure has no
	// caller to return to.
	fixture.repository.awaitAttempts(t, 1)

	// Close is the only place left that can say so; without it the turn would
	// finish having silently lost part of its answer.
	if err := fixture.close(t); !errors.Is(err, storageErr) {
		t.Errorf("Close() error = %v, want the storage failure", err)
	}
}

func TestCloseWritesWhatIsStillBuffered(t *testing.T) {
	fixture := newEmitterFixture(t, 0)
	fixture.emitter.flushInterval = time.Hour

	fixture.say(t, "chat", "responding", "最后一句")
	if err := fixture.close(t); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	stored := fixture.repository.stored()
	if len(stored) != 1 || stored[0].Content != "最后一句" {
		t.Errorf("storage holds %+v", stored)
	}
	if told := fixture.broadcaster.told(); len(told) != 1 {
		t.Errorf("subscribers were told %+v", told)
	}
}

func TestEmitAfterCloseIsRefused(t *testing.T) {
	fixture := newEmitterFixture(t, 0)
	if err := fixture.close(t); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// The turn is over; a producer still writing to it has not understood the
	// outcome it was given.
	if err := fixture.emitter.Emit(context.Background(), event.Event{Type: event.TypeDone}); err == nil {
		t.Error("Emit() after Close error = nil, want a rejection")
	}
}
