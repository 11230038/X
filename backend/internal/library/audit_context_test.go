package library

// The request id, and how it gets into an audit entry.
//
// The service writes its own entries, so the id cannot be handed to it as an
// argument — it arrives on the context, marked by the handler that read it off
// the request (handler.actorAndID). That is two halves, and they are tested
// together because a break in either one is invisible in the other: a marking
// that cannot be read back leaves every entry blank, and an entry that reads
// nothing out of a marked context leaves them blank too.
//
// It has a file of its own rather than a place in recycle_test.go because it is
// the one thing here that is about the audit rather than about the recycle bin,
// and that file already has the recycle bin to itself.

import (
	"context"
	"testing"
)

func TestTheRequestIdTravelsOnTheContext(t *testing.T) {
	if got := requestIDOf(context.Background()); got != "" {
		t.Errorf("an unmarked context reads %q, want nothing", got)
	}
	marked := WithRequestID(context.Background(), "req_7")
	if got := requestIDOf(marked); got != "req_7" {
		t.Errorf("requestIDOf() = %q, want req_7", got)
	}

	// And marking a context does not reach back into the one it came from. A
	// value belongs to the context it was put on, which is what keeps one
	// request's id out of another request's entries when two run at once.
	if got := requestIDOf(context.Background()); got != "" {
		t.Errorf("the context the mark was derived from reads %q, want nothing", got)
	}

	// A second mark wins. That is "this work is for this request" rather than
	// "this work is for these requests", and the nearer mark is the truer one.
	if got := requestIDOf(WithRequestID(marked, "req_8")); got != "req_8" {
		t.Errorf("requestIDOf() = %q, want the nearer mark", got)
	}
}

// TestTheEntryCarriesTheRequestIdOfTheWork is the half that could go wrong
// silently: everything about a soft delete would still work, and the entry
// would still be written — naming a request that never existed, which is worse
// than naming none, because it reads as an answer.
func TestTheEntryCarriesTheRequestIdOfTheWork(t *testing.T) {
	service, _, auditor := newRecycler(t)
	uploaded := store(t, service, "notes.md", "# 笔记\n")
	written := len(auditor.events)

	ctx := WithRequestID(context.Background(), "req_42")
	if _, err := service.SoftDelete(ctx, uploaded.ID, testActor()); err != nil {
		t.Fatalf("SoftDelete() error = %v", err)
	}

	events := auditor.events[written:]
	if len(events) != 1 {
		t.Fatalf("one soft delete wrote %d entries, want one", len(events))
	}
	if events[0].RequestID != "req_42" {
		t.Errorf("the entry carries request id %q, want req_42", events[0].RequestID)
	}
	// The entry is the whole of the answer here, so it is worth saying that this
	// one is the soft delete's and not some other entry's that happened to be
	// written beside it.
	if events[0].Action != "library.file_deleted" {
		t.Errorf("the entry is for %q, want the soft delete", events[0].Action)
	}
}
