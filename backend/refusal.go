package main

// The join between the middleware that refuses a request and the audit that
// records it.
//
// It is here, in the composition root, because it is the only place that may
// know both: `middleware` speaks of a `Refusal` and knows nothing about how one
// is stored, and `audit` is a leaf — it is the one package in this repository
// that imports nothing of ours, and it stays that way by not learning what a
// gin context or an authorizer is (§2.3). A method on the audit service that
// took a `middleware.Refusal` would make that package depend on the HTTP layer
// it is deliberately below.
//
// The conversion is the whole of this file, and it is written out rather than
// reflected over because every field of the entry has to be *chosen*: which
// action a refusal is filed under, what it points at, and what it is allowed to
// say about why. A generic copier would answer all three by accident.

import (
	"context"

	"backend/internal/audit"
	"backend/internal/middleware"
)

// refusalRecorder writes refusals into the audit.
type refusalRecorder struct {
	audit *audit.Service
}

// RecordRefusal turns one refusal into one entry.
//
// The entry is filed under `surface.denied` rather than under the action the
// route would have taken, and that is a statement about what is knowable at
// this point rather than a simplification: the check that refused ran before
// the handler, so all that is in hand is the area of the product that was
// reached for. Naming a domain action here would mean every route declaring the
// action it would have performed, and a route that declared the wrong one would
// fill the audit with refusals filed under things nobody tried.
//
// The result is `denied` and not an error. "Somebody asked and was told no" is
// the thing this table exists to be able to answer, and it is the one record in
// it that is about a person rather than about their material.
func (r refusalRecorder) RecordRefusal(ctx context.Context, refusal middleware.Refusal) error {
	if r.audit == nil {
		return audit.ErrNotConfigured
	}
	return r.audit.Record(ctx, audit.Event{
		Actor: audit.Actor{
			ID:   refusal.Caller.UserID,
			Name: refusal.Name,
			Role: refusal.Caller.Role,
		},
		Action: audit.ActionSurfaceDenied,
		Target: audit.Ref{Kind: audit.TargetSurface, ID: refusal.Surface},
		Result: audit.ResultDenied,
		// The two codes the 403 body carries are the two the entry carries, and
		// they are the same two on purpose: what the screen says and what the
		// record says have to be the same sentence, or the audit is a second
		// account of the same refusal that a reader has to reconcile by hand.
		Detail: audit.Detail{
			Reason: refusal.Decision.Reason,
			Layer:  string(refusal.Decision.Layer),
		},
		RequestID: refusal.RequestID,
	})
}

var _ middleware.Recorder = refusalRecorder{}
