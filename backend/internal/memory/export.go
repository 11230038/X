package memory

// Export: the account being handed everything this package holds about it
// (`GET /memory/export`, PRD §11.3's "the user must be able to export Memory").
//
// Three parts and the order they are in is the graph. The facts come first with
// their evidence attached, because PRD §11.3's requirement is that the L2 → L1
// link survives the export: a fact handed over without the observations it was
// admitted on is an assertion, and the one thing this package must never look
// like is a system that asserts things about a person without showing its work
// (MEM-01).
//
// The observations come next, all of them — including the ones that support no
// fact. They are not a leftover: an observation that stayed in L1 is MEM-03's
// second state, the system having heard something it does not believe, and an
// export that dropped them would drop exactly the rows a user most wants to see
// when asking what has been written about them. A trace that *is* evidence appears
// twice, once under its fact and once here, and that is not a duplication to be
// tidied away: the two are two statements about the same row — "this is why the
// system believes that" and "this is everything it was ever told" — and a reader
// looking for one of them should not have to search the other's shape.
//
// The audit trail comes last. It is what makes the export an answer to "what has
// been done to my memory" rather than only "what does it say now", and it is the
// part that cannot be re-derived from the rest: a retracted fact looks like a fact
// in the export, and only the trail says who took it back and when.
//
// The export's own line is written after the trail was read, so an export does not
// contain the record of itself. It could be arranged either way, and this way is
// the one that keeps the trail a record of what was done *before* the moment the
// export describes: an export listing its own line would make two exports of an
// unchanged memory disagree by one, and the disagreement would be the export.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ExportRequest is an account asking for its memory.
type ExportRequest struct {
	OwnerID int64
	// Actor is who asked (audit.go). An export is an audited action — it is a read
	// that hands the account's whole memory to whoever is holding the request, and
	// the trail is the only place that says so.
	Actor Actor
}

func (r ExportRequest) validate() error {
	if r.OwnerID <= 0 {
		return fmt.Errorf("%w: the owner is %d", ErrInvalidRequest, r.OwnerID)
	}
	if strings.TrimSpace(string(r.Actor)) == "" {
		return fmt.Errorf("%w: the actor is blank", ErrInvalidRequest)
	}
	return nil
}

// Export is one account's whole memory as it is handed over.
//
// Every fact is here whatever state it is in — retracted and superseded included.
// An export is not a view: the views are the listings and the synthesis, and they
// answer "what does the system believe now". This answers "what do you have about
// me", and a fact somebody took back is a thing the system has about them.
type Export struct {
	OwnerID int64
	// ExportedAt is the moment the export describes.
	ExportedAt time.Time
	// Facts is L2 with its evidence chain, most recently changed first.
	Facts []FactWithEvidence
	// Observations is L1 in full, newest first.
	Observations []Trace
	// Actions is the audit trail, newest first, up to this export.
	Actions []Audit
}

// Export reads one account's memory and records that it was read.
//
// The whole thing runs in one transaction, and the audit row is written through
// the same one as the reads. That is not for atomicity in the sense of a write
// being taken back — nothing here writes — but because the three parts have to
// describe one moment: a fact read before a retraction and the trail read after it
// would hand over a document that contradicts itself, and the contradiction would
// be invisible.
func (s *Service) Export(ctx context.Context, request ExportRequest) (Export, error) {
	if err := request.validate(); err != nil {
		return Export{}, err
	}
	exported := Export{
		OwnerID:      request.OwnerID,
		Facts:        []FactWithEvidence{},
		Observations: []Trace{},
		Actions:      []Audit{},
	}

	err := s.repository.Within(ctx, func(tx Repository) error {
		facts, err := tx.Facts(ctx, FactQuery{OwnerID: request.OwnerID})
		if err != nil {
			return err
		}
		for _, fact := range facts {
			traces, err := tx.TracesOf(ctx, fact.ID)
			if err != nil {
				return err
			}
			exported.Facts = append(exported.Facts, FactWithEvidence{Fact: fact, Traces: traces})
		}

		observations, err := tx.Traces(ctx, TraceQuery{OwnerID: request.OwnerID})
		if err != nil {
			return err
		}
		exported.Observations = observations

		actions, err := tx.Audits(ctx, AuditQuery{OwnerID: request.OwnerID})
		if err != nil {
			return err
		}
		exported.Actions = actions

		exported.ExportedAt = s.clock()
		return s.audit(ctx, tx, Audit{
			OwnerID:    request.OwnerID,
			Actor:      request.Actor,
			Action:     ActionMemoryExported,
			TargetKind: TargetMemory,
			Result:     ResultDone,
			Detail:     exportDetail(exported),
			CreatedAt:  exported.ExportedAt,
		})
	})
	if err != nil {
		return Export{}, err
	}
	return exported, nil
}

// exportDetail is what an audit row about an export carries: how much was handed
// over.
//
// Counts rather than ids, which is the opposite of what cleanup's row does, and
// the difference is what each action takes away. A purge destroys rows, so the
// trail is the only place their ids survive. An export takes nothing: everything
// it named is still there to be read, so the row only has to say how much was
// carried out.
func exportDetail(exported Export) []byte {
	detail, err := json.Marshal(map[string]any{
		"facts":        len(exported.Facts),
		"observations": len(exported.Observations),
		"actions":      len(exported.Actions),
	})
	if err != nil {
		return nil
	}
	return detail
}
