package library

// The three rules that are about the record rather than about the file: the
// record is written before the change it describes, a library with nothing to
// record through is refused when it is built, and an operation with nobody
// behind it never reaches storage.
//
// They are separated from recycle_test.go because they are the ones a change to
// the plumbing would break, and because the first of them is the only case in
// which the two can be told apart at all.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestTheRecordIsWrittenBeforeTheChange pins the ordering §2.14 chose, and it is
// the one place the two can be told apart: an audit that cannot be written means
// the operation does not happen. A change with no record is a file somebody took
// away and nobody can say who, which is the one question this table exists to
// answer; a record for a change that then failed is an error the caller is told
// about.
func TestTheRecordIsWrittenBeforeTheChange(t *testing.T) {
	repository := &recordingRepository{}
	auditor := &auditRecorder{err: errors.New("audit_events is unreachable")}
	service, err := NewService(Options{
		Root: t.TempDir(), MaxFileBytes: 1024, Auditor: auditor,
	}, repository)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	uploaded := store(t, service, "notes.md", "the same bytes")

	if _, err := service.SoftDelete(context.Background(), uploaded.ID, testActor()); err == nil {
		t.Fatal("SoftDelete() succeeded while the audit could not be written")
	}
	if repository.stored[uploaded.ID].InRecycleBin() {
		t.Error("the file was deleted and the deletion was not recorded")
	}

	// The same at the destructive end, which is the case that matters most: a
	// purge nobody can attribute is worse than a purge that did not happen. The
	// file is put in the bin first by way of an audit that answers, because a
	// purge is only ever asked for on a binned file.
	auditor.err = nil
	if _, err := service.SoftDelete(context.Background(), uploaded.ID, testActor()); err != nil {
		t.Fatalf("SoftDelete() error = %v, want it to work once the audit does", err)
	}
	auditor.err = errors.New("audit_events is unreachable")
	if err := service.Purge(context.Background(), uploaded.ID, testActor()); err == nil {
		t.Error("Purge() destroyed a file while the audit could not be written")
	}
	if len(repository.stored) != 1 {
		t.Error("the file was destroyed and the destruction was not recorded")
	}
	if got := bytesOnDisk(t, service); got != 1 {
		t.Errorf("%d files are on disk, want the one whose purge was refused", got)
	}
}

// TestALibraryWithoutAnAuditorIsRefusedAtConstruction is where the requirement
// lives: a library that cannot record what was taken from it is a wiring fault,
// and a wiring fault belongs at startup where it refuses a deployment rather than
// at the moment a user deletes something.
func TestALibraryWithoutAnAuditorIsRefusedAtConstruction(t *testing.T) {
	if _, err := NewService(Options{Root: t.TempDir(), MaxFileBytes: 1024}, &recordingRepository{}); err == nil {
		t.Fatal("NewService() built a library with nothing to record through")
	}
}

// TestAnUploadWithNobodyBehindItIsRefused: the actor is what the file belongs to
// and what a restore is recorded under, and a zero one is a wiring fault rather
// than a request.
func TestAnUploadWithNobodyBehindItIsRefused(t *testing.T) {
	service, _, _ := newRecycler(t)
	_, err := service.Upload(context.Background(), UploadInput{
		Filename: "notes.md", Content: strings.NewReader("the same bytes"),
	})
	if !errors.Is(err, ErrNoActor) {
		t.Errorf("Upload(no actor) error = %v, want %v", err, ErrNoActor)
	}
	if _, err := service.Search(context.Background(), SearchQuery{}); !errors.Is(err, ErrNoActor) {
		t.Errorf("Search(no caller) error = %v, want %v", err, ErrNoActor)
	}
}
