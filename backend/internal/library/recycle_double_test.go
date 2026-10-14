package library

// The half of the repository double that exists for the recycle bin and the
// listing, read with the behaviour it is standing in for.
//
// The other two methods — Create and Find — are declared in service_test.go
// beside the upload and the read. All of this is a double and not evidence: what
// the real predicates do to a real table — the ordering FindByDigest asks for,
// the substring search, the two columns the CHECK holds together, the chain
// file → document → material — is repository_recycle_db_test.go's business.

import (
	"context"
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

// auditRecorder stands in for the audit, and it is the reason several tests can
// ask what was recorded rather than only that something was.
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
