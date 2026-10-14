package library

// Dedup and the recycle bin, at the service level.
//
// The repository double is declared in service_test.go with the two methods the
// upload and the read were built on; the five this file is about are appended to
// it here, next to the tests that use them, so that the half of the double that
// exists for the recycle bin is read with the behaviour it is standing in for.
// It is a double and not evidence: what the real predicates do to a real table —
// the ordering FindByDigest asks for, the substring search, the two columns the
// CHECK holds together, the chain file → document → material — is
// repository_recycle_db_test.go's business.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"backend/internal/audit"
	"backend/internal/authz"
)

// auditRecorder stands in for the audit, and it is the reason several tests below
// can ask what was recorded rather than only that something was.
type auditRecorder struct {
	events []audit.Event
	err    error
}

func (r *auditRecorder) Record(_ context.Context, event audit.Event) error {
	if r.err != nil {
		return r.err
	}
	r.events = append(r.events, event)
	return nil
}

// actions is every action recorded, in order, which is what a test that asks
// "was this recorded and as what" wants.
func (r *auditRecorder) actions() []audit.Action {
	actions := make([]audit.Action, 0, len(r.events))
	for _, event := range r.events {
		actions = append(actions, event.Action)
	}
	return actions
}

// visible is the scope Find applies on both sides of the double, so that the two
// answers about "may this account see this row" cannot drift apart.
func (r *recordingRepository) visible(record FileRecord, caller authz.Caller) bool {
	return record.OwnerID == caller.UserID || (caller.IsAdmin() && record.OwnerID == 0)
}

// FindByDigest mirrors the dedup lookup and its ordering: the caller's rows
// holding these bytes, a live one before a binned one and the oldest before the
// rest. The order is the whole of the policy (§2.14), which is why it is
// reproduced here rather than left to a map's iteration order.
func (r *recordingRepository) FindByDigest(_ context.Context, digest string, caller authz.Caller) (FileRecord, error) {
	if r.findErr != nil {
		return FileRecord{}, r.findErr
	}
	var best FileRecord
	found := false
	for _, record := range r.stored {
		if record.SHA256 != digest || !r.visible(record, caller) {
			continue
		}
		if !found || before(record, best) {
			best, found = record, true
		}
	}
	if !found {
		return FileRecord{}, fmt.Errorf("%w: no file of yours holds that content", ErrNotFound)
	}
	return best, nil
}

// before is FindByDigest's ORDER BY, in one place: live before binned, then
// oldest, then by id so that two rows written in the same second still have an
// answer.
func before(candidate, held FileRecord) bool {
	if candidate.InRecycleBin() != held.InRecycleBin() {
		return !candidate.InRecycleBin()
	}
	if !candidate.CreatedAt.Equal(held.CreatedAt) {
		return candidate.CreatedAt.Before(held.CreatedAt)
	}
	return candidate.ID < held.ID
}

// Search mirrors the listing's filters. The filename match is a case-insensitive
// substring and not a pattern, which is the one part of this that is a decision
// rather than a copy: a user typing a name into a box means the name.
func (r *recordingRepository) Search(_ context.Context, query SearchQuery) ([]FileRecord, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	var matched []FileRecord
	for _, record := range r.stored {
		if record.InRecycleBin() != query.Deleted || !r.visible(record, query.Caller) {
			continue
		}
		if name := normalizedFilename(query.Filename); name != "" &&
			!strings.Contains(strings.ToLower(record.Filename), strings.ToLower(name)) {
			continue
		}
		if query.Category != "" {
			category, err := categoryOf(record.LibraryPath)
			if err != nil || category != query.Category {
				continue
			}
		}
		if !query.Since.IsZero() && record.CreatedAt.Before(query.Since) {
			continue
		}
		if !query.Until.IsZero() && !record.CreatedAt.Before(query.Until) {
			continue
		}
		matched = append(matched, record)
	}
	sort.Slice(matched, func(i, j int) bool {
		if !matched[i].CreatedAt.Equal(matched[j].CreatedAt) {
			return matched[i].CreatedAt.After(matched[j].CreatedAt)
		}
		return matched[i].ID < matched[j].ID
	})
	offset := max(query.Offset, 0)
	if offset >= len(matched) {
		return nil, nil
	}
	matched = matched[offset:]
	if limit := searchLimit(query.Limit); len(matched) > limit {
		matched = matched[:limit]
	}
	return matched, nil
}

// SetDeleted writes the two columns together, which is what the table's CHECK
// requires of any writer.
func (r *recordingRepository) SetDeleted(_ context.Context, id string, deleted bool, now time.Time, caller authz.Caller) error {
	record, ok := r.stored[id]
	if !ok || !r.visible(record, caller) {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	record.Deleted = deleted
	if deleted {
		record.DeletedAt = &now
	} else {
		record.DeletedAt = nil
	}
	r.stored[id] = record
	return nil
}

func (r *recordingRepository) Delete(_ context.Context, id string, caller authz.Caller) error {
	record, ok := r.stored[id]
	if !ok || !r.visible(record, caller) {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	delete(r.stored, id)
	return nil
}

// References is deliberately not scoped to an account, and neither is this: a
// material belonging to somebody else is exactly the fact a purge must not miss.
func (r *recordingRepository) References(_ context.Context, id string) ([]Reference, error) {
	return r.references[id], nil
}

// newRecycler is a library whose auditor a test can read back.
func newRecycler(t *testing.T) (*Service, *recordingRepository, *auditRecorder) {
	t.Helper()
	repository := &recordingRepository{}
	auditor := &auditRecorder{}
	service, err := NewService(Options{
		Root: t.TempDir(), MaxFileBytes: 1024 * 1024, Auditor: auditor,
	}, repository)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service, repository, auditor
}

// store uploads one file and returns what the upload answered.
func store(t *testing.T, service *Service, filename, content string) UploadResult {
	t.Helper()
	result, err := service.Upload(context.Background(), UploadInput{
		Actor: testActor(), Filename: filename, Content: strings.NewReader(content),
	})
	if err != nil {
		t.Fatalf("Upload(%s) error = %v", filename, err)
	}
	return result
}

func bytesOnDisk(t *testing.T, service *Service) int {
	t.Helper()
	count := 0
	for _, category := range categories {
		entries, err := os.ReadDir(filepath.Join(service.root, category))
		if err != nil {
			t.Fatalf("read %s: %v", category, err)
		}
		count += len(entries)
	}
	return count
}

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

// TestSearchListsOneHalfOfTheLibraryAtATime: a listing is live or binned and
// never both, because a client showing both would have to filter every row to
// know which actions it may offer beside it.
func TestSearchListsOneHalfOfTheLibraryAtATime(t *testing.T) {
	service, _, _ := newRecycler(t)
	kept := store(t, service, "线性代数.md", "kept")
	thrown := store(t, service, "线性代数习题.md", "thrown")
	if _, err := service.SoftDelete(context.Background(), thrown.ID, testActor()); err != nil {
		t.Fatalf("SoftDelete() error = %v", err)
	}

	live, err := service.Search(context.Background(), SearchQuery{Caller: testCaller()})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(live) != 1 || live[0].ID != kept.ID {
		t.Errorf("the live listing is %+v, want only the file that was kept", live)
	}
	binned, err := service.Search(context.Background(), SearchQuery{Caller: testCaller(), Deleted: true})
	if err != nil {
		t.Fatalf("Search(the bin) error = %v", err)
	}
	if len(binned) != 1 || binned[0].ID != thrown.ID {
		t.Fatalf("the recycle bin is %+v, want only the file that was thrown away", binned)
	}
	// The bin says when, which is the one thing a binned row has that a live one
	// does not — and it is the column the CHECK keeps beside the flag.
	if binned[0].DeletedAt == nil {
		t.Error("the binned file is listed without the moment it was thrown away")
	}
}

// TestSearchNarrowsByAllFourFilters covers what the listing is for. The filename
// match is case-insensitive and literal, which is why "代数" finds both files
// whose names contain it and "线性*" finds neither: a user typing a name into a
// box means the name, not a pattern.
func TestSearchNarrowsByAllFourFilters(t *testing.T) {
	service, _, _ := newRecycler(t)
	first := store(t, service, "Linear-Algebra.md", "a")
	store(t, service, "notes.pdf", "b")

	tests := []struct {
		name  string
		query SearchQuery
		// want is the count, and it is the count rather than a list because two of
		// these cases are "both files", and which one comes first is the ordering
		// test's business rather than this one's.
		want int
	}{
		{"a name in another case", SearchQuery{Filename: "linear-algebra"}, 1},
		{"a name in a case nobody typed", SearchQuery{Filename: "LINEAR-ALGEBRA"}, 1},
		{"a name that is a wildcard", SearchQuery{Filename: "Linear*"}, 0},
		{"a name nothing is called", SearchQuery{Filename: "微积分"}, 0},
		{"a category", SearchQuery{Category: "md"}, 1},
		{"a category nothing was stored under", SearchQuery{Category: "mp3"}, 0},
		{"everything from the beginning of time", SearchQuery{Since: atSecond(0)}, 2},
		{"nothing before the beginning of time", SearchQuery{Until: atSecond(0)}, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.query.Caller = testCaller()
			found, err := service.Search(context.Background(), test.query)
			if err != nil {
				t.Fatalf("Search() error = %v", err)
			}
			if len(found) != test.want {
				t.Fatalf("Search() found %d files, want %d", len(found), test.want)
			}
			if test.want == 1 && found[0].ID != first.ID {
				t.Errorf("Search() found %s, want %s", found[0].ID, first.ID)
			}
		})
	}
}

// TestSearchIsNarrowedToOneAccount is AUTH-06 arriving at the listing: two
// accounts that each upload a file with the same name must not see each other's,
// and the answer is a shorter list rather than a refusal.
func TestSearchIsNarrowedToOneAccount(t *testing.T) {
	service, repository, _ := newRecycler(t)
	mine := store(t, service, "习题.md", "mine")
	repository.stored["lib_theirs"] = FileRecord{
		ID: "lib_theirs", SHA256: strings.Repeat("c", 64), Filename: "习题.md",
		MIMEType: "text/markdown", LibraryPath: "md/lib_theirs.md",
		OwnerID: testOwnerID + 1, CreatedAt: atSecond(9), UpdatedAt: atSecond(9),
	}

	found, err := service.Search(context.Background(), SearchQuery{Caller: testCaller(), Filename: "习题"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(found) != 1 || found[0].ID != mine.ID {
		t.Errorf("the listing is %+v, want only this account's file", found)
	}
	// And the same query as the other account sees only theirs — the filter is
	// not "hide what is not mine", it is "list what is".
	found, err = service.Search(context.Background(), SearchQuery{Caller: otherCaller(), Filename: "习题"})
	if err != nil {
		t.Fatalf("Search() as the other account: %v", err)
	}
	if len(found) != 1 || found[0].ID != "lib_theirs" {
		t.Errorf("the other account's listing is %+v, want their own file", found)
	}
}

// TestTheListingPagesAndClamps: a limit above the maximum is a client asking for
// more than it should get rather than an error it could correct, so it is
// clamped; an offset past the end is an empty page and not a failure.
func TestTheListingPagesAndClamps(t *testing.T) {
	service, repository, _ := newRecycler(t)
	for index := 0; index < 5; index++ {
		id := fmt.Sprintf("lib_%032d", index)
		repository.stored[id] = FileRecord{
			ID: id, SHA256: fmt.Sprintf("%064d", index), Filename: "notes.md",
			MIMEType: "text/markdown", LibraryPath: "md/" + id + ".md", OwnerID: testOwnerID,
			CreatedAt: atSecond(index), UpdatedAt: atSecond(index),
		}
	}

	page, err := service.Search(context.Background(), SearchQuery{Caller: testCaller(), Limit: 2, Offset: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(page) != 2 {
		t.Fatalf("the page holds %d files, want 2", len(page))
	}
	// Newest first, so the second page-from-one starts at the fourth file.
	if page[0].ID != "lib_00000000000000000000000000000003" {
		t.Errorf("the page starts at %s, want the fourth file", page[0].ID)
	}

	beyond, err := service.Search(context.Background(), SearchQuery{Caller: testCaller(), Offset: 99})
	if err != nil {
		t.Fatalf("Search(past the end) error = %v", err)
	}
	if len(beyond) != 0 {
		t.Errorf("a page past the end holds %d files, want none", len(beyond))
	}
}
