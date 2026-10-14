package memory

// Cleanup: forgetting a batch on purpose (`POST /memory/cleanup`).
//
// It is the one place this package removes a row, and the reason it is a batch
// rather than a `DELETE /memory/facts/:factID` is §2.14's: a deletion of one row
// named in a URL is an action nobody can describe afterwards, while MEM-02's three
// verbs are edit, retract and cleanup — and a retraction, which is the reversible
// half, leaves the row behind on purpose. So the deletion is the wide, blunt,
// explicit one, and it is given the two safeguards the blunt operation needs: a
// cutoff that has to be stated, and a preview that has to be run first.
//
// What a cleanup does *not* remove is L1. The observations a purged fact rested on
// stay in `memory_traces` — PRD §11.3 makes L1 append-only, and the same sentence
// is what §7.1 checks about a retraction: taking back what the system believes
// does not unsay what a surface said. The account going away takes them (the
// foreign key), and that is the deletion PRD §11.3's "the user must be able to
// delete Memory" is served by.
//
// Two runs, one question. The preview and the commit take the same query and walk
// it through the same function, which is what makes "the second run only touches
// what the report said it would" a property of the code rather than a promise in a
// comment: the report names the rows, and the purge is handed exactly those ids.
// The cutoff closes the one gap left: a fact admitted after the preview is stamped
// after the cutoff, so it cannot be inside a set the preview already looked at.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// CleanupQuery is what a cleanup takes: which of an account's facts are to go.
type CleanupQuery struct {
	OwnerID int64
	// Before is the cutoff: facts admitted strictly before it are the candidates.
	// It is required, and it must be a bound rather than a formality — a cleanup
	// with no cutoff and no filter would be "delete everything", which is the
	// account going away and not a batch.
	Before time.Time
	// Surface narrows the batch to one surface ("what the practice surface told you
	// about yourself, before May"). Empty means every surface.
	Surface Surface
	// Statuses narrows it the way a listing is narrowed; empty means every status.
	// It is the filter that separates the two ordinary cleanups: the facts already
	// out of force ({retracted, superseded}), which nobody is looking at any more,
	// and everything the account had before a date, which is what a user asking to
	// be forgotten is asking for.
	Statuses []Status
	// Actor is who asked.
	Actor Actor
	// DryRun reports what would go and writes nothing. The other routes' previews
	// are a courtesy (§2.13's import, Dedupe); this one is not, because the two
	// runs of a cleanup are not the same act twice — one is a question and the
	// other is the account losing rows.
	DryRun bool
}

// candidates is the listing both runs read, spelled once.
func (q CleanupQuery) candidates() FactQuery {
	return FactQuery{OwnerID: q.OwnerID, Statuses: q.Statuses, Surface: q.Surface, Before: q.Before}
}

// validate reports whether the request is one worth acting on. `now` is what makes
// "a cutoff in the future" a refusal: a bound past the present bounds nothing, and
// the caller who wrote it meant something else.
func (q CleanupQuery) validate(now time.Time) error {
	if q.OwnerID <= 0 {
		return fmt.Errorf("%w: the owner is %d", ErrInvalidRequest, q.OwnerID)
	}
	if strings.TrimSpace(string(q.Actor)) == "" {
		return fmt.Errorf("%w: the actor is blank", ErrInvalidRequest)
	}
	if q.Before.IsZero() {
		return fmt.Errorf("%w: a cleanup needs a cutoff", ErrInvalidRequest)
	}
	if q.Before.After(now) {
		return fmt.Errorf("%w: a cutoff of %s is in the future, which bounds nothing",
			ErrInvalidRequest, q.Before.UTC().Format(time.RFC3339))
	}
	if q.Surface != "" && !q.Surface.Valid() {
		return fmt.Errorf("%w: %q is not a surface", ErrInvalidRequest, q.Surface)
	}
	for _, status := range q.Statuses {
		if !status.Valid() {
			return fmt.Errorf("%w: %q is not a status", ErrInvalidRequest, status)
		}
	}
	return nil
}

// CleanupReport is what a cleanup found and what it did.
//
// The same type answers both runs, and the fields that separate them are Purged
// and DryRun: a preview purges nothing, so a preview that reported a count would
// be reporting something that did not happen.
type CleanupReport struct {
	// DryRun is the request this report answers, echoed so that a client which ran
	// both knows which answer it is holding.
	DryRun bool
	Before time.Time
	// Facts is the batch, in the listing's order — most recently changed first. On
	// a preview these are the rows that would go; on a commit they are the ones
	// that were named, and the ones the purge was handed.
	Facts []Fact
	// Purged is how many rows the purge removed, and it is zero on a preview. A
	// commit that says a different number from the length of Facts is a batch that
	// something else was deleting at the same time.
	Purged int
}

// ids is the batch as the purge takes it.
func (r CleanupReport) ids() []string {
	ids := make([]string, 0, len(r.Facts))
	for _, fact := range r.Facts {
		ids = append(ids, fact.ID)
	}
	return ids
}

// Cleanup removes a batch of an account's facts, after saying which ones.
//
// A cleanup that finds nothing writes no audit row, and neither does a preview.
// Both are deliberate and they are the same rule: `memory_audit` records actions,
// and neither "I looked and there was nothing" nor "I looked and told you what I
// saw" is an action on a memory. A commit that finds nothing is a commit that did
// nothing, and a line saying it ran would be a line about an action that did not
// happen — the failure mode that makes a trail unreadable.
//
// The row a commit does write is one row for the batch and not one per fact: the
// action is the cleanup, the cutoff and the surface are what it looked at, and the
// ids it removed are in the row's detail. A purge that wrote a line per row would
// make one action indistinguishable from a thousand.
func (s *Service) Cleanup(ctx context.Context, query CleanupQuery) (CleanupReport, error) {
	now := s.clock()
	if err := query.validate(now); err != nil {
		return CleanupReport{}, err
	}
	report := CleanupReport{DryRun: query.DryRun, Before: query.Before, Facts: []Fact{}}

	err := s.repository.Within(ctx, func(tx Repository) error {
		facts, err := tx.Facts(ctx, query.candidates())
		if err != nil {
			return err
		}
		report.Facts = facts
		if query.DryRun || len(facts) == 0 {
			return nil
		}

		ids := report.ids()
		purged, err := tx.PurgeFacts(ctx, query.OwnerID, ids)
		if err != nil {
			return err
		}
		report.Purged = purged
		return s.audit(ctx, tx, Audit{
			OwnerID:    query.OwnerID,
			Actor:      query.Actor,
			Action:     ActionFactPurged,
			TargetKind: TargetMemory,
			Result:     ResultDone,
			Detail:     cleanupDetail(query, ids, purged),
			CreatedAt:  now,
		})
	})
	if err != nil {
		return CleanupReport{}, err
	}
	return report, nil
}

// cleanupDetail is what an audit row about a cleanup carries: the question that was
// asked, the rows it named, and how many of them went.
//
// The ids are in the row because the facts themselves are not: after a purge there
// is nothing left to read, and a trail that said "3 facts were removed" without
// saying which would make the one irreversible action in this package the one with
// no record of what it took.
func cleanupDetail(query CleanupQuery, ids []string, purged int) []byte {
	detail, err := json.Marshal(map[string]any{
		"before":   query.Before.UTC().Format(time.RFC3339),
		"surface":  string(query.Surface),
		"statuses": statusesOf(query.Statuses),
		"facts":    ids,
		"purged":   purged,
	})
	if err != nil {
		return nil
	}
	return detail
}
