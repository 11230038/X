package library

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"backend/internal/audit"
	"backend/internal/authz"
)

// The recycle bin: the four operations a file's life has beyond being stored
// (§2.14, FILE-01, AC-15).
//
// The state is not a state machine. A file is either in the library or in the
// bin, the two columns that say which are written together, and the rule that
// makes the bin mean something is the one every read above this file follows: a
// binned file is *not found*. So "delete" is not a flag a listing filters on, it
// is the file leaving the library — and the bin is the place it is while it is
// out, which is why only the three operations that are about the bin look there.
//
// Every one of them writes its audit entry before it changes anything. That
// ordering is deliberate and it is the smaller of two wrongs: a record of a
// change that then failed is an error the caller is told about and a line in the
// log, while a change with no record is a file somebody took away and nobody can
// say who — which is the one question this table exists to answer (§2.14). The
// two writes are not in one transaction, and they cannot be: the audit is
// append-only and lives in a table of its own, and nothing in this repository
// has ever wrapped a domain change and its audit together.

// live reads the file a request names, refusing one that is in the recycle bin.
//
// The blank-id and empty-string handling is here rather than in each of the four
// callers so that "the id in the path is not a file id" has one answer. The
// refusal for a binned file is ErrNotFound and not a distinct "it is in the bin":
// telling those apart would tell a caller that the id names something, which is
// what a probe for ids is trying to learn (§2.3, AUTH-06).
func (s *Service) live(ctx context.Context, id string, caller authz.Caller) (FileRecord, error) {
	record, err := s.lookup(ctx, id, caller)
	if err != nil {
		return FileRecord{}, err
	}
	if record.InRecycleBin() {
		return FileRecord{}, fmt.Errorf("%w: %s is in the recycle bin", ErrNotFound, id)
	}
	return record, nil
}

// binned reads the file a request names, refusing one that is live.
//
// It is the other half of the same rule, and it is what keeps the bin's three
// operations from being performed on a file nobody took out of the library:
// restoring a file that was never deleted, or destroying one its owner still has,
// is not a thing a client can have meant to ask for.
func (s *Service) binned(ctx context.Context, id string, caller authz.Caller) (FileRecord, error) {
	record, err := s.lookup(ctx, id, caller)
	if err != nil {
		return FileRecord{}, err
	}
	if !record.InRecycleBin() {
		return FileRecord{}, fmt.Errorf("%w: %s is not in the recycle bin", ErrNotFound, id)
	}
	return record, nil
}

// lookup reads one record by id, whatever state it is in.
//
// The state is the caller's business above this line, and that is the point of
// having this at all: the repository answers about the row, and which of the two
// states is acceptable is a rule, so it is written where the rules are.
func (s *Service) lookup(ctx context.Context, id string, caller authz.Caller) (FileRecord, error) {
	if s == nil || s.repository == nil {
		return FileRecord{}, fmt.Errorf("file library is not initialized")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return FileRecord{}, ErrInvalidFile
	}
	return s.repository.Find(ctx, id, caller)
}

// Search lists one account's files: the live ones, or the recycle bin.
//
// Which of the two is the query's, not two methods, because the two listings are
// one question with one answer — "what do I have" — and a client that could ask
// for both at once would have to sort out which rows it may offer restore on.
func (s *Service) Search(ctx context.Context, query SearchQuery) ([]FileSummary, error) {
	if s == nil || s.repository == nil {
		return nil, fmt.Errorf("file library is not initialized")
	}
	// A listing with nobody behind it would be a listing of the whole table. The
	// repository narrows by the caller, and this is the check that there is one.
	if !query.Caller.Valid() {
		return nil, ErrNoActor
	}
	records, err := s.repository.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	summaries := make([]FileSummary, 0, len(records))
	for _, record := range records {
		summary, err := s.summaryOf(record)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

// SoftDelete moves one file into the recycle bin.
//
// The bytes do not move and the path is not given up. That is the whole of the
// difference between this and Purge, and it is what makes a restore a change of
// two columns rather than a second upload: the file is still where it was, and
// the row still holds the path, which is why a re-upload of the same bytes can
// bring it back rather than collide with it (§2.14).
func (s *Service) SoftDelete(ctx context.Context, id string, actor Actor) (FileSummary, error) {
	record, err := s.live(ctx, id, actor.Caller)
	if err != nil {
		return FileSummary{}, err
	}
	if err := s.record(ctx, actor, audit.ActionFileDeleted, record.ID, audit.ResultOK, audit.Detail{}); err != nil {
		return FileSummary{}, err
	}
	now := time.Now().UTC()
	if err := s.repository.SetDeleted(ctx, record.ID, true, now, actor.Caller); err != nil {
		return FileSummary{}, err
	}
	record.Deleted = true
	record.DeletedAt = &now
	// The summary is built from the row as it now is rather than from the row as
	// it was read, so that what the client is shown is what it would be shown by
	// the listing it will look at next.
	return s.summaryOf(record)
}

// Restore brings one file back out of the recycle bin.
//
// §2.14 describes this as writing the two columns back *and rebuilding the
// path*, and the prose that follows explains why there is nothing to rebuild:
// the binned row never gave its path up, because `library_files_path_unique`
// covers every row and seekdb has no partial index, so a binned row still holds
// the only path this file has. Restoring is therefore exactly the two columns —
// and a build that tried to write a *new* path would be a build that had to
// rewrite where the bytes are on disk, which no part of a soft delete touched.
func (s *Service) Restore(ctx context.Context, id string, actor Actor) (FileSummary, error) {
	record, err := s.binned(ctx, id, actor.Caller)
	if err != nil {
		return FileSummary{}, err
	}
	// The bytes are checked for before the row comes back. A soft delete does not
	// remove them, so a file that has none is a file somebody deleted under the
	// service — and restoring the row would put an entry in the library that can
	// never be opened, which the user would have no way to tell from a bug.
	full, err := safeJoin(s.root, record.LibraryPath)
	if err != nil {
		return FileSummary{}, fmt.Errorf("resolve stored path for %s: %w", record.ID, err)
	}
	if _, err := os.Stat(full); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return FileSummary{}, fmt.Errorf("%w: %s has a record but no file", ErrNotFound, record.ID)
		}
		return FileSummary{}, fmt.Errorf("read the bytes of %s: %w", record.ID, err)
	}
	if err := s.record(ctx, actor, audit.ActionFileRestored, record.ID, audit.ResultOK, audit.Detail{}); err != nil {
		return FileSummary{}, err
	}
	now := time.Now().UTC()
	if err := s.repository.SetDeleted(ctx, record.ID, false, now, actor.Caller); err != nil {
		return FileSummary{}, err
	}
	record.Deleted = false
	record.DeletedAt = nil
	return s.summaryOf(record)
}

// Purge removes one file for good: its bytes, and then the row (AC-15).
//
// It asks who is still using the file first and refuses while anybody is. The
// two kinds of user are a document parsed from it and a reading material built
// on one of those documents, and the second is the one that is easy to miss: a
// material never names a file, so its claim is visible only through the chain
// file → document → material. Deleting the row would cascade the documents away
// and leave the materials pointing at rows that are gone — which is a reader
// opening a book that no longer exists, which is why the check is here and not
// left to the foreign key (§2.14).
//
// The refusal is *InUseError and it names what is in the way, because the client
// answering it offers to show the user which documents or materials to deal with
// first, and "some of them" is not something a user can act on.
//
// A "document created from this file" that arrives between the check and the
// delete would be cascaded away unseen. That window is left open deliberately:
// closing it means holding a lock across two tables on the upload path, and the
// consequence of losing that race is one document, not a wrong answer to anybody
// — the alternative is a lock on the busiest write in the module.
func (s *Service) Purge(ctx context.Context, id string, actor Actor) error {
	record, err := s.binned(ctx, id, actor.Caller)
	if err != nil {
		return err
	}
	references, err := s.repository.References(ctx, record.ID)
	if err != nil {
		return err
	}
	if len(references) > 0 {
		refusal := &InUseError{References: references}
		// The refusal is recorded like any other answer, and a refusal whose
		// record could not be written is not returned: §2.6's audit answers "what
		// has this account tried and been told no", and a denial that leaves no
		// trace is the one entry that question cannot be answered from.
		if err := s.record(ctx, actor, audit.ActionFilePurged, record.ID, audit.ResultDenied, audit.Detail{
			Reason: "file_in_use",
			Counts: map[string]int{"references": len(references)},
		}); err != nil {
			return err
		}
		return refusal
	}

	if err := s.record(ctx, actor, audit.ActionFilePurged, record.ID, audit.ResultOK, audit.Detail{}); err != nil {
		return err
	}
	// The bytes go first, and the order matters. If the row is removed and the
	// removal of the bytes then fails, the file is off the library's index and
	// nothing will ever look for it again — orphaned on disk with no record that
	// it is there. This way round, a failure leaves the row in the bin and a
	// second purge finishes the job: the bytes are already gone, which os.Remove
	// is told to forgive, and only the delete is retried.
	full, err := safeJoin(s.root, record.LibraryPath)
	if err != nil {
		return fmt.Errorf("resolve stored path for %s: %w", record.ID, err)
	}
	if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove the bytes of %s: %w", record.ID, err)
	}
	if err := s.repository.Delete(ctx, record.ID, actor.Caller); err != nil {
		return fmt.Errorf("remove the metadata of %s after its bytes: %w", record.ID, err)
	}
	return nil
}
