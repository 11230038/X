package main

// The running process, as the two operations routes read it (§2.15).
//
// The adapter is here rather than in assemble.go for the reason turnRuntime is:
// it is the one place that says these three methods are the process's own work,
// and it keeps the assembly function about assembly. The three sources it joins
// — the turn runtime, the index worker, the usage record — are three different
// types that have nothing to do with each other, and a status page is the only
// caller that ever wants all three at once.

import (
	"context"

	"backend/internal/handler"
)

// The status route's interface is satisfied here rather than declared as a
// struct of three fields, so that a method added to one of the three sources
// cannot quietly become a second question this route answers.
var _ handler.Runtime = (*ai)(nil)

// ActiveTurns is how many turns are in flight right now.
func (a *ai) ActiveTurns() int { return a.turns.Running() }

// Indexing reports whether the index worker is running.
//
// The worker's lifetime is its channel — it is closed when the worker stops — so
// a closed channel is a stopped worker and no channel at all is a process that
// never started one. The select is non-blocking on purpose: this is a question
// about a worker meant to run for the life of the process, and waiting on it
// here would be waiting for the answer to arrive rather than asking for it.
func (a *ai) Indexing() bool {
	if a.indexing == nil {
		return false
	}
	select {
	case <-a.indexing:
		return false
	default:
		return true
	}
}

// UnfinishedCalls counts the model calls that have not come back (§2.7).
func (a *ai) UnfinishedCalls(ctx context.Context) (int64, error) {
	return a.usage.Unfinished(ctx)
}
