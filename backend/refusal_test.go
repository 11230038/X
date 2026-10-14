package main

// The one conversion in refusal.go, and the reason it is worth a test of its
// own: a refusal is produced by the middleware and stored by the audit, and if
// the two disagree about which action it is filed under or which layer said no,
// nothing fails. The 403 the person sees is identical either way — the record
// of it is what silently stops being true.

import (
	"context"
	"errors"
	"testing"
	"time"

	"backend/internal/audit"
	"backend/internal/authz"
	"backend/internal/middleware"
)

// recordingStore is the audit's store as this file needs it: it keeps what it
// was handed and can be told to fail.
type recordingStore struct {
	appended []audit.Event
	err      error
}

func (s *recordingStore) Append(_ context.Context, event audit.Event) (int64, error) {
	if s.err != nil {
		return 0, s.err
	}
	s.appended = append(s.appended, event)
	return int64(len(s.appended)), nil
}

func (s *recordingStore) Page(context.Context, audit.Filter) ([]audit.Event, error) {
	return nil, nil
}

func (s *recordingStore) Prune(context.Context, time.Time) (int64, error) { return 0, nil }

func newRecorder(t *testing.T, store audit.Store) refusalRecorder {
	t.Helper()
	service, err := audit.NewService(store, audit.Options{MaxPageSize: 100, RetentionDays: 180})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return refusalRecorder{audit: service}
}

// aRefusal is one denied request, as the middleware hands it over.
func aRefusal() middleware.Refusal {
	return middleware.Refusal{
		Caller: authz.Caller{UserID: 7, Role: authz.RoleUser},
		Name:   "wei",
		Decision: authz.Decision{
			Item:    authz.Item{Kind: authz.KindSurface, ID: authz.SurfaceReading},
			Allowed: false,
			Layer:   authz.LayerPreset,
			Reason:  authz.ReasonBaselineClosed,
		},
		Surface:   authz.SurfaceReading,
		RequestID: "req_0123456789",
	}
}

func TestARefusalIsFiledUnderTheSurfaceItReachedFor(t *testing.T) {
	store := &recordingStore{}
	if err := newRecorder(t, store).RecordRefusal(context.Background(), aRefusal()); err != nil {
		t.Fatalf("RecordRefusal() error = %v", err)
	}
	if len(store.appended) != 1 {
		t.Fatalf("the audit was written %d times, want once", len(store.appended))
	}
	got := store.appended[0]

	if got.Action != audit.ActionSurfaceDenied {
		t.Errorf("Action = %q, want %q", got.Action, audit.ActionSurfaceDenied)
	}
	if got.Result != audit.ResultDenied {
		t.Errorf("Result = %q, want %q: a refusal is a result and not an error",
			got.Result, audit.ResultDenied)
	}
	if got.Target.Kind != audit.TargetSurface || got.Target.ID != authz.SurfaceReading {
		t.Errorf("Target = %+v, want the surface that was reached for", got.Target)
	}
}

// The actor is a snapshot, and the two things an audit answers from it are
// "which account" and "under which name". A refusal that stored the id and
// dropped the name would answer the first and not the second, months later,
// about an account that has since been renamed.
func TestARefusalCarriesTheAccountAsItWas(t *testing.T) {
	store := &recordingStore{}
	if err := newRecorder(t, store).RecordRefusal(context.Background(), aRefusal()); err != nil {
		t.Fatalf("RecordRefusal() error = %v", err)
	}
	got := store.appended[0].Actor

	if got.ID != 7 || got.Name != "wei" || got.Role != authz.RoleUser {
		t.Errorf("Actor = %+v, want the account snapshot from the request", got)
	}
}

// The two codes the 403 body carries are the two the entry carries. They are
// the same two on purpose: what the screen says and what the record says have
// to be one sentence, or the audit is a second account of the same refusal that
// a reader has to reconcile by hand.
func TestARefusalRecordsWhyAndFromWhichLayer(t *testing.T) {
	store := &recordingStore{}
	if err := newRecorder(t, store).RecordRefusal(context.Background(), aRefusal()); err != nil {
		t.Fatalf("RecordRefusal() error = %v", err)
	}
	got := store.appended[0].Detail

	if got.Reason != authz.ReasonBaselineClosed {
		t.Errorf("Reason = %q, want %q", got.Reason, authz.ReasonBaselineClosed)
	}
	if got.Layer != string(authz.LayerPreset) {
		t.Errorf("Layer = %q, want %q", got.Layer, authz.LayerPreset)
	}
}

// The request id is what makes an entry and a log line the same request rather
// than two records that happen to agree about an account.
func TestARefusalCarriesTheRequestItRefused(t *testing.T) {
	store := &recordingStore{}
	if err := newRecorder(t, store).RecordRefusal(context.Background(), aRefusal()); err != nil {
		t.Fatalf("RecordRefusal() error = %v", err)
	}

	if got := store.appended[0].RequestID; got != "req_0123456789" {
		t.Errorf("RequestID = %q, want the id the access log carries", got)
	}
}

// A refusal is about the person who was refused and about nothing they were
// carrying: no subject, and nothing in the detail but the two codes.
func TestARefusalSaysNothingAboutAnybodyElse(t *testing.T) {
	store := &recordingStore{}
	if err := newRecorder(t, store).RecordRefusal(context.Background(), aRefusal()); err != nil {
		t.Fatalf("RecordRefusal() error = %v", err)
	}
	got := store.appended[0]

	if got.SubjectID != 0 {
		t.Errorf("SubjectID = %d, want none", got.SubjectID)
	}
	if len(got.Detail.Counts) != 0 || len(got.Detail.Items) != 0 {
		t.Errorf("Detail = %+v, want only the reason and the layer", got.Detail)
	}
}

// The audit's own shape check holds on this path too, and this is the case that
// proves it: a reason that is a sentence is refused here rather than written,
// which is what §2.6 asks for and what no filter could be trusted to do.
func TestARefusalWithASentenceInItIsRefused(t *testing.T) {
	store := &recordingStore{}
	refusal := aRefusal()
	refusal.Decision.Reason = "the learner preset does not reach reading"

	err := newRecorder(t, store).RecordRefusal(context.Background(), refusal)

	if !errors.Is(err, audit.ErrNotACode) {
		t.Fatalf("RecordRefusal() error = %v, want %v", err, audit.ErrNotACode)
	}
	if len(store.appended) != 0 {
		t.Error("the audit was written to with a detail the audit had refused")
	}
}

// A store that failed is reported, and that is the whole of what this does: the
// caller is the middleware, whose answer does not depend on it — a refusal that
// went through because the audit was unreachable would make the audit's
// availability decide who may read whose material.
func TestARefusalReportsAStoreThatFailed(t *testing.T) {
	store := &recordingStore{err: errors.New("the disk is on fire")}
	err := newRecorder(t, store).RecordRefusal(context.Background(), aRefusal())

	if err == nil || !errors.Is(err, store.err) {
		t.Fatalf("RecordRefusal() error = %v, want the store's own error", err)
	}
}

// A recorder built before the audit was is a wiring mistake, and the one thing
// it must not do is answer "recorded".
func TestARefusalWithoutAnAuditIsAWiringMistake(t *testing.T) {
	err := refusalRecorder{}.RecordRefusal(context.Background(), aRefusal())

	if !errors.Is(err, audit.ErrNotConfigured) {
		t.Fatalf("RecordRefusal() error = %v, want %v", err, audit.ErrNotConfigured)
	}
}
