package turn

// Waiting for an answer: ask_user's pause, the turn's table of open waits, and what
// hands an answer to the goroutine that is parked on one.
//
// It is apart from runner.go because it is the one place a turn's progress depends
// on somebody outside it. Everything else in this package moves a turn forward from
// what the turn itself produced; here the turn stops, and two other things — a
// client's reply and the clock — decide when it goes on.

import (
	"context"
	"time"

	"backend/internal/event"
)

// askUser pauses the turn to ask the user something.
//
// It is what ask_user calls, through the context the turn put itself in. The
// wait is opened before the question is published, and holds one answer, so a
// user who answers the instant the question appears is not told the turn was
// not listening — which is exactly what a plain unbuffered send would say, in
// the moment between the question being published and this goroutine parking on
// it.
func (r *turnRun) askUser(ctx context.Context, question string) (string, error) {
	if err := r.pause(ctx); err != nil {
		return "", err
	}
	replies := make(chan string, 1)
	r.await(replies)

	if err := r.emit(ctx, event.Event{Type: event.TypeWaitForInput, Content: question}); err != nil {
		return "", err
	}

	select {
	case answer := <-replies:
		if err := r.resume(ctx); err != nil {
			return "", err
		}
		return answer, nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(r.runner.inputTimeout):
		return "", ErrInputTimeout
	}
}

// pause stores the turn as waiting for an answer and moves it on in memory with
// it. The two must not drift: a turn that moved in storage while this copy still
// said running would be refused every later write as a conflict, and it would
// wait for an answer it could never record the end of.
func (r *turnRun) pause(ctx context.Context) error {
	paused, err := r.turn.Transition(StatusWaitingInput, r.runner.now())
	if err != nil {
		return err
	}
	if err := r.runner.repository.Save(ctx, paused, r.turn.Status); err != nil {
		return err
	}
	r.turn = paused
	return nil
}

// resume picks the turn back up after the answer arrived.
func (r *turnRun) resume(ctx context.Context) error {
	resumed, err := r.turn.Transition(StatusRunning, r.runner.now())
	if err != nil {
		return err
	}
	if err := r.runner.repository.Save(ctx, resumed, r.turn.Status); err != nil {
		return err
	}
	r.turn = resumed
	return nil
}

// await opens this turn's wait for one answer, replacing whatever was there.
//
// A fresh channel per question is what keeps answers with their questions: once
// a question has been answered its channel is replaced, so an answer left in
// the old one is unreachable rather than waiting to satisfy the next question
// nobody has asked.
func (r *turnRun) await(replies chan string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reply = replies
}

// submit hands an answer to the turn if it is waiting for one.
//
// The send does not wait: an answer that arrives when the turn is not waiting
// is refused rather than held until it is (WS-09).
func (r *turnRun) submit(answer string) error {
	r.mu.Lock()
	replies := r.reply
	r.mu.Unlock()

	if replies == nil {
		return ErrNotWaiting
	}
	select {
	case replies <- answer:
		return nil
	default:
		return ErrNotWaiting
	}
}
