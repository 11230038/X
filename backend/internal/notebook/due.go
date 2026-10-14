package notebook

import (
	"context"
	"strings"
	"time"

	"backend/internal/authz"
)

// DueReader is the review domain as the question bank reads it (§1.2).
//
// It is declared here, implemented by practice, and joined in the assembly, and
// the reason is a cycle that would otherwise be real: practice imports the bank
// — judging an answer needs the question it was asked about — so the bank
// importing practice back would be two ordinary imports pointing at each other.
// This is the same seam as the turn's ports and the knowledge base's file
// source: what a reader needs is declared where the reader is.
//
// It is an interface over *two* domains rather than a second read of the review
// tables because the review state is practice's to interpret: which column is
// the due time and what an absent one means are facts about the curve, and a
// bank that read `practice_review_state` itself would be a second implementation
// of them.
type DueReader interface {
	// DueNext reports when each of these questions is next due, keyed by the
	// bank's own row id — the id the review state stores, so that a listing of
	// twenty questions is one read and not twenty.
	//
	// A question the reader has nothing to say about is absent from the map
	// rather than present with a zero time. "Never reviewed" is not "due at the
	// epoch": one is a question nobody has met, the other is one that is
	// hopelessly overdue, and a map that spelled them the same way would make
	// the difference unreadable at every caller.
	//
	// The caller travels for the reason every read in this version takes one:
	// the review state has no owner column of its own — it is reached through
	// the question it is filed under — so the reader narrows by the caller's
	// questions, and a row key belonging to somebody else yields no date rather
	// than somebody else's date.
	DueNext(ctx context.Context, entryIDs []string, caller authz.Caller) (map[string]time.Time, error)
}

// WithDueReader attaches the review domain, and returns the service so that the
// assembly can write it in one expression.
//
// A bank without one is a working bank: it is a deployment where nothing is
// scheduled, and every question simply carries no due time. That is why this is
// an attachment and not a constructor argument — the bank's own tests and the
// tool paths build it with nothing but a repository, and a required argument
// would make "there is no review domain here" a case every caller had to name.
func (s *Service) WithDueReader(reader DueReader) *Service {
	s.due = reader
	return s
}

// withDue attaches each entry's next due time.
//
// It is one call for the whole page and not one per entry: a listing that asked
// the review domain twenty times would be a listing whose cost grew with the
// page size, and the same fifty rows would be fifty round trips.
func (s *Service) withDue(ctx context.Context, entries []Entry, caller authz.Caller) ([]Entry, error) {
	if s.due == nil || len(entries) == 0 {
		return entries, nil
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.TrimSpace(entry.ID) != "" {
			ids = append(ids, entry.ID)
		}
	}
	due, err := s.due.DueNext(ctx, ids, caller)
	if err != nil {
		return nil, err
	}
	if len(due) == 0 {
		return entries, nil
	}
	for index := range entries {
		if at, ok := due[entries[index].ID]; ok {
			// A copy, so that the pointer in the entry does not alias a value
			// the map still owns.
			when := at
			entries[index].Answer.DueAt = &when
		}
	}
	return entries, nil
}

// withOneDue is withDue for the single-entry reads.
//
// It goes through the same call rather than having its own, because "when is
// this due" must not be answered two ways for a list and for a page: a question
// showing a date in the list and no date when opened is the kind of drift that
// takes a bug report to find.
func (s *Service) withOneDue(ctx context.Context, entry Entry, caller authz.Caller) (Entry, error) {
	entries, err := s.withDue(ctx, []Entry{entry}, caller)
	if err != nil {
		return Entry{}, err
	}
	return entries[0], nil
}
