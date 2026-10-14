package library

// Dedup and the two levels of deletion, at the service level.
//
// The repository double is in recycle_double_test.go and the fixtures the cases
// share with the listing are beside it. What is asserted here is the state
// machine §2.14 describes: one row per content, a bin that is not a second
// library, and a purge that is the only operation which cannot be undone.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"backend/internal/audit"
)

// TestUploadingTheSameBytesTwiceIsOneFile is FILE-01's first row (§2.14): the
// digest was computed from V1 and never once looked at, so two uploads of one
// file were two files. What makes this the fix is that the answer is the *same
// id* — a client that shows a library and a client that shows the upload's
// response are looking at one row, and the second bytes are gone from the disk.
func TestUploadingTheSameBytesTwiceIsOneFile(t *testing.T) {
	service, repository, _ := newRecycler(t)
	first := store(t, service, "notes.md", "the same bytes")
	second := store(t, service, "notes.md", "the same bytes")

	if second.ID != first.ID {
		t.Errorf("the second upload was answered with %s, want the file already here: %s",
			second.ID, first.ID)
	}
	if got := bytesOnDisk(t, service); got != 1 {
		t.Errorf("%d files are on disk, want one: the second upload must not write its own", got)
	}
	if len(repository.stored) != 1 {
		t.Errorf("%d rows are stored, want one", len(repository.stored))
	}
	// The name is the stored one, not the one just sent: there is one file here
	// and it has one name, and answering with the new spelling would report a
	// rename that did not happen.
	if second.Filename != first.Filename {
		t.Errorf("the second upload is named %q, want %q", second.Filename, first.Filename)
	}
}

// TestAFileInTheBinComesBackWhenItsBytesAreUploaded is the second half of §2.14's
// dedup rule, and it is the reason restore and dedup are the same act: the binned
// row still holds the only path this file can have, because
// `library_files_path_unique` covers every row and seekdb has no partial index.
// So a re-upload must bring that row back rather than write a second one — which
// is what it does, and what the audit says.
func TestAFileInTheBinComesBackWhenItsBytesAreUploaded(t *testing.T) {
	service, repository, auditor := newRecycler(t)
	uploaded := store(t, service, "notes.md", "the same bytes")
	if _, err := service.SoftDelete(context.Background(), uploaded.ID, testActor()); err != nil {
		t.Fatalf("SoftDelete() error = %v", err)
	}
	auditor.events = nil

	again := store(t, service, "notes.md", "the same bytes")

	if again.ID != uploaded.ID {
		t.Errorf("the re-upload was answered with %s, want the binned row %s", again.ID, uploaded.ID)
	}
	if got := bytesOnDisk(t, service); got != 1 {
		t.Errorf("%d files are on disk, want one", got)
	}
	if len(repository.stored) != 1 {
		t.Errorf("%d rows are stored, want one: a restore must not add a row", len(repository.stored))
	}
	record := repository.stored[uploaded.ID]
	if record.InRecycleBin() {
		t.Error("the file is still in the recycle bin after its bytes were uploaded again")
	}
	// The restore is recorded, and it says which way it was asked for — an upload
	// and a restore route are one change of state reached two ways (§2.14).
	if got := auditor.actions(); len(got) != 1 || got[0] != audit.ActionFileRestored {
		t.Errorf("recorded %v, want one %s", got, audit.ActionFileRestored)
	}
	if auditor.events[0].Detail.Reason != "restored_by_upload" {
		t.Errorf("the entry says %q, want the upload named as what caused it",
			auditor.events[0].Detail.Reason)
	}
}

// TestABinnedFileIsNotFound is the rule the whole bin is built on: while a file
// is in there it is not in the library, so every read that would have handed it
// over stops finding it — and *not found* is the same answer a file belonging to
// somebody else gets, so an id cannot be probed for (§2.3).
func TestABinnedFileIsNotFound(t *testing.T) {
	service, _, _ := newRecycler(t)
	uploaded := store(t, service, "notes.md", "the same bytes")
	if _, err := service.SoftDelete(context.Background(), uploaded.ID, testActor()); err != nil {
		t.Fatalf("SoftDelete() error = %v", err)
	}

	if _, err := service.Find(context.Background(), uploaded.ID, testCaller()); !errors.Is(err, ErrNotFound) {
		t.Errorf("Find(a binned file) error = %v, want %v", err, ErrNotFound)
	}
	if _, err := service.Open(context.Background(), uploaded.ID, testCaller()); !errors.Is(err, ErrNotFound) {
		t.Errorf("Open(a binned file) error = %v, want %v", err, ErrNotFound)
	}
	// And the other half of the rule: the operations that are about the bin
	// refuse a file that is not in it, so a client cannot delete twice or restore
	// something it still has.
	if _, err := service.Restore(context.Background(), uploaded.ID+"x", testActor()); !errors.Is(err, ErrNotFound) {
		t.Errorf("Restore(an unknown id) error = %v, want %v", err, ErrNotFound)
	}
	if err := service.Purge(context.Background(), uploaded.ID+"x", testActor()); !errors.Is(err, ErrNotFound) {
		t.Errorf("Purge(an unknown id) error = %v, want %v", err, ErrNotFound)
	}
}

// TestSoftDeleteKeepsTheBytesAndRefusesASecondTime pins the difference between
// the two levels of deletion, which is the whole reason there are two: the bytes
// stay, and they are what a restore needs (§2.14).
func TestSoftDeleteKeepsTheBytesAndRefusesASecondTime(t *testing.T) {
	service, _, auditor := newRecycler(t)
	uploaded := store(t, service, "notes.md", "the same bytes")

	summary, err := service.SoftDelete(context.Background(), uploaded.ID, testActor())
	if err != nil {
		t.Fatalf("SoftDelete() error = %v", err)
	}
	if summary.DeletedAt == nil {
		t.Error("the answer says the file is in the bin and does not say since when")
	}
	if got := bytesOnDisk(t, service); got != 1 {
		t.Errorf("%d files are on disk, want one: a soft delete does not remove bytes", got)
	}
	if got := auditor.actions(); len(got) != 1 || got[0] != audit.ActionFileDeleted {
		t.Errorf("recorded %v, want one %s", got, audit.ActionFileDeleted)
	}
	// The entry names the file and the account, which is the whole of what "who
	// took this away" is answered from (§2.14).
	event := auditor.events[0]
	if event.Target.Kind != audit.TargetLibraryFile || event.Target.ID != uploaded.ID {
		t.Errorf("the entry points at %s/%s, want %s/%s",
			event.Target.Kind, event.Target.ID, audit.TargetLibraryFile, uploaded.ID)
	}
	if event.Actor.ID != testOwnerID || event.Actor.Name != testActor().Name {
		t.Errorf("the entry names the actor as %+v", event.Actor)
	}
	if event.Result != audit.ResultOK {
		t.Errorf("the entry's result is %q, want %q", event.Result, audit.ResultOK)
	}

	// Deleting it again is not found, because it is not in the library any more.
	if _, err := service.SoftDelete(context.Background(), uploaded.ID, testActor()); !errors.Is(err, ErrNotFound) {
		t.Errorf("a second SoftDelete() error = %v, want %v", err, ErrNotFound)
	}
}

// TestRestoreBringsAFileBack covers the fifth row of §2.14, and the refusal it
// pairs with: restoring a file nobody deleted is a state no client can have
// meant to ask for, so it is not found rather than a no-op.
func TestRestoreBringsAFileBack(t *testing.T) {
	service, _, auditor := newRecycler(t)
	uploaded := store(t, service, "notes.md", "the same bytes")
	if _, err := service.SoftDelete(context.Background(), uploaded.ID, testActor()); err != nil {
		t.Fatalf("SoftDelete() error = %v", err)
	}
	auditor.events = nil

	summary, err := service.Restore(context.Background(), uploaded.ID, testActor())
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if summary.DeletedAt != nil {
		t.Error("a restored file is reported as deleted-at something")
	}
	if got := auditor.actions(); len(got) != 1 || got[0] != audit.ActionFileRestored {
		t.Errorf("recorded %v, want one %s", got, audit.ActionFileRestored)
	}
	// It is in the library again, by the same id and from the same path.
	found, err := service.Find(context.Background(), uploaded.ID, testCaller())
	if err != nil {
		t.Fatalf("Find(a restored file) error = %v", err)
	}
	if found.ID != uploaded.ID || found.Filename != uploaded.Filename {
		t.Errorf("the restored file reads back as %+v", found)
	}

	if _, err := service.Restore(context.Background(), uploaded.ID, testActor()); !errors.Is(err, ErrNotFound) {
		t.Errorf("restoring a live file: error = %v, want %v", err, ErrNotFound)
	}
}

// TestRestoreRefusesAFileWhoseBytesAreGone is the case a soft delete never
// creates but a person can: the row is there and the file is not. Restoring it
// would put an entry in the library that can never be opened, and the user would
// have no way to tell that from a bug — so the restore says what is wrong.
func TestRestoreRefusesAFileWhoseBytesAreGone(t *testing.T) {
	service, repository, _ := newRecycler(t)
	thrown := atSecond(1)
	repository.stored = map[string]FileRecord{
		"lib_gone": {
			ID: "lib_gone", SHA256: fileDigest, Filename: "gone.md",
			LibraryPath: "md/lib_gone.md", OwnerID: testOwnerID,
			Deleted: true, DeletedAt: &thrown,
		},
	}
	if _, err := service.Restore(context.Background(), "lib_gone", testActor()); !errors.Is(err, ErrNotFound) {
		t.Errorf("Restore(a record with no file) error = %v, want %v", err, ErrNotFound)
	}
}

// TestPurgeRemovesTheBytesAndThenTheRow covers the second level of deletion
// (§2.14): the file stops existing, which is the only operation here that cannot
// be undone.
func TestPurgeRemovesTheBytesAndThenTheRow(t *testing.T) {
	service, repository, auditor := newRecycler(t)
	uploaded := store(t, service, "notes.md", "the same bytes")
	if _, err := service.SoftDelete(context.Background(), uploaded.ID, testActor()); err != nil {
		t.Fatalf("SoftDelete() error = %v", err)
	}
	auditor.events = nil

	if err := service.Purge(context.Background(), uploaded.ID, testActor()); err != nil {
		t.Fatalf("Purge() error = %v", err)
	}
	if got := bytesOnDisk(t, service); got != 0 {
		t.Errorf("%d files are on disk, want none", got)
	}
	if len(repository.stored) != 0 {
		t.Errorf("%d rows are stored, want none", len(repository.stored))
	}
	if got := auditor.actions(); len(got) != 1 || got[0] != audit.ActionFilePurged {
		t.Errorf("recorded %v, want one %s", got, audit.ActionFilePurged)
	}
	if got := auditor.events[0].Result; got != audit.ResultOK {
		t.Errorf("the entry's result is %q, want %q", got, audit.ResultOK)
	}
	// A second purge finds nothing, which is also the shape a retry after a
	// partial failure takes: the row is gone, so this answers rather than
	// destroying something else.
	if err := service.Purge(context.Background(), uploaded.ID, testActor()); !errors.Is(err, ErrNotFound) {
		t.Errorf("a second Purge() error = %v, want %v", err, ErrNotFound)
	}
}

// TestPurgeRefusesAFileThatIsStillUsed is AC-15, and it is the one operation
// here whose refusal is the interesting answer: the file is kept, the caller is
// told who is holding it, and the refusal itself is recorded — because "somebody
// tried to destroy this and was told no" is a thing the audit has to be able to
// answer afterwards.
func TestPurgeRefusesAFileThatIsStillUsed(t *testing.T) {
	service, repository, auditor := newRecycler(t)
	uploaded := store(t, service, "notes.md", "the same bytes")
	if _, err := service.SoftDelete(context.Background(), uploaded.ID, testActor()); err != nil {
		t.Fatalf("SoftDelete() error = %v", err)
	}
	repository.references = map[string][]Reference{
		uploaded.ID: {
			{Kind: ReferenceDocument, ID: "doc_1", Name: "attachments"},
			{Kind: ReferenceReadingMaterial, ID: "mat_1", Name: "第一课"},
		},
	}
	auditor.events = nil

	err := service.Purge(context.Background(), uploaded.ID, testActor())
	var inUse *InUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("Purge() error = %v (%T), want an *InUseError", err, err)
	}
	if !errors.Is(err, ErrInUse) {
		t.Errorf("errors.Is(%v, ErrInUse) = false, want a caller to recognise the refusal", err)
	}
	// It names both kinds, and the material is the one that matters: it never
	// names the file itself, so its claim is invisible to anybody who only looks
	// at the file's own column.
	if len(inUse.References) != 2 {
		t.Fatalf("the refusal names %d things, want both", len(inUse.References))
	}
	if !strings.Contains(err.Error(), "mat_1") || !strings.Contains(err.Error(), "doc_1") {
		t.Errorf("Error() = %q, want it to name what is in the way", err)
	}

	// Nothing was destroyed and the refusal was recorded.
	if got := bytesOnDisk(t, service); got != 1 {
		t.Errorf("%d files are on disk, want the one that was refused", got)
	}
	if len(repository.stored) != 1 {
		t.Errorf("%d rows are stored, want the one that was refused", len(repository.stored))
	}
	if got := auditor.actions(); len(got) != 1 || got[0] != audit.ActionFilePurged {
		t.Fatalf("recorded %v, want one %s", got, audit.ActionFilePurged)
	}
	if got := auditor.events[0].Result; got != audit.ResultDenied {
		t.Errorf("the entry's result is %q, want %q", got, audit.ResultDenied)
	}
	if got := auditor.events[0].Detail.Reason; got != "file_in_use" {
		t.Errorf("the entry's reason is %q, want file_in_use", got)
	}
}

// TestPurgeRefusesALiveFile keeps the two levels apart at the destructive end: a
// file the user still has is not destroyed by a route that means "destroy the
// thing in the bin".
func TestPurgeRefusesALiveFile(t *testing.T) {
	service, _, _ := newRecycler(t)
	uploaded := store(t, service, "notes.md", "the same bytes")

	if err := service.Purge(context.Background(), uploaded.ID, testActor()); !errors.Is(err, ErrNotFound) {
		t.Errorf("purging a live file: error = %v, want %v", err, ErrNotFound)
	}
	if got := bytesOnDisk(t, service); got != 1 {
		t.Errorf("%d files are on disk, want the one that was refused", got)
	}
}
