package memory

// What can be done to a memory after it exists (MEM-02).
//
// MEM-02's three verbs are edit, retract and retract-again — 编辑 / 撤销 / 取消撤销
// — and this file is the first two of them plus the user's own way of writing a
// fact down. Each is an action somebody asked for, so each leaves one line in
// `memory_audit` (audit.go says which actions do *not*, and the biggest omission
// there is the gate, which writes no line because the fact it wrote names its own
// evidence).
//
// **Nothing here deletes anything, and that is the point of the word 撤销.** A
// retracted fact is still a row, still readable by id, still carrying the
// observations it was admitted on — what changes is that it stops being in force,
// so it stops being listed as believed and stops contributing to the synthesis.
// The one deletion this package has is cleanup.go's purge, which is a batch with a
// preview, and even it leaves L1 alone: what a surface said stays said (PRD §11.3
// makes L1 append-only).
//
// Edit and retract both read the fact and then write it back over the version they
// read, inside one transaction. That is what makes two of them racing end with one
// winner and one ErrStaleState rather than with the second write silently
// overwriting the first — the same guard Observe's fact writes use, for the same
// reason.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// userConfidence is what a fact the user wrote down is written with.
//
// The gate never looks at it — the fourth path of §2.7 skips all three conditions
// — but the number is stored on the fact and read back, and a fact the user
// asserted themselves reading 60 would be a fact that looks half-believed. This
// scale's top is the only honest value for "the account holder said so".
const userConfidence = 100

// FactRequest names one fact and who is acting on it. It is the shape retract and
// restore take, because they are one action read in two directions.
type FactRequest struct {
	OwnerID int64
	FactID  string
	// Actor is who asked (audit.go). Blank is refused rather than defaulted: an
	// audit row saying who did something is the point of the audit row.
	Actor Actor
}

func (r FactRequest) validate() error {
	if r.OwnerID <= 0 {
		return fmt.Errorf("%w: the owner is %d", ErrInvalidRequest, r.OwnerID)
	}
	if strings.TrimSpace(r.FactID) == "" {
		return fmt.Errorf("%w: which fact is not said", ErrInvalidRequest)
	}
	if strings.TrimSpace(string(r.Actor)) == "" {
		return fmt.Errorf("%w: the actor is blank", ErrInvalidRequest)
	}
	return nil
}

// EditRequest is a change to what a fact says.
type EditRequest struct {
	OwnerID int64
	FactID  string
	// Statement and Scope are what to change, and a nil leaves that field as it
	// is. It is why there is one route and not two: "fix the wording", "move it to
	// another scope" and both at once are the same edit with different fields
	// filled in.
	Statement *string
	Scope     *string
	Actor     Actor
}

func (r EditRequest) validate() error {
	if err := (FactRequest{OwnerID: r.OwnerID, FactID: r.FactID, Actor: r.Actor}).validate(); err != nil {
		return err
	}
	if r.Statement == nil && r.Scope == nil {
		return fmt.Errorf("%w: the edit changes nothing", ErrInvalidRequest)
	}
	if r.Statement != nil && strings.TrimSpace(*r.Statement) == "" {
		return fmt.Errorf("%w: the statement is blank", ErrInvalidRequest)
	}
	return nil
}

// Edit changes what a fact says, and recomputes the fingerprint with it.
//
// Recomputing is the whole of what makes an edit an edit rather than a rewrite:
// the fingerprint is what the gate and the dedupe read by, so a fact whose
// statement no longer matches its fingerprint is a fact that two rows can point
// at and neither can find. Writing the new statement with the old fingerprint
// would be exactly that, which is why the two are set in one place here.
//
// A fact out of force can still be edited. A user correcting the wording of
// something they took back, before restoring it, is not a strange thing to ask,
// and refusing it would be a rule with no harm behind it.
//
// The answer is the fact as it now stands with its evidence, so that a client
// which echoed the edit back does not have to read again to show the row.
func (s *Service) Edit(ctx context.Context, request EditRequest) (FactWithEvidence, error) {
	if err := request.validate(); err != nil {
		return FactWithEvidence{}, err
	}
	var edited FactWithEvidence
	err := s.repository.Within(ctx, func(tx Repository) error {
		held, err := tx.Fact(ctx, request.OwnerID, request.FactID)
		if err != nil {
			return err
		}
		before := held
		if request.Statement != nil {
			held.Statement = *request.Statement
			// The statement is stored as it was given and the fingerprint is taken
			// of its normalized form, which is the same division the gate uses: what
			// the user typed is what they see, and what the system compares is folded.
			held.Fingerprint = Fingerprint(held.Statement)
		}
		if request.Scope != nil {
			held.Scope = *request.Scope
		}

		now := s.clock()
		reached, err := tx.SaveFact(ctx, held, before.Version, now)
		if err != nil {
			return err
		}
		held.Version = reached
		held.UpdatedAt = now

		if err := s.audit(ctx, tx, Audit{
			OwnerID:    request.OwnerID,
			Actor:      request.Actor,
			Action:     ActionFactEdited,
			TargetKind: TargetFact,
			TargetID:   held.ID,
			Result:     ResultDone,
			Detail:     editDetail(before, held),
			CreatedAt:  now,
		}); err != nil {
			return err
		}

		traces, err := tx.TracesOf(ctx, held.ID)
		if err != nil {
			return err
		}
		edited = FactWithEvidence{Fact: held, Traces: traces}
		return nil
	})
	if err != nil {
		return FactWithEvidence{}, err
	}
	return edited, nil
}

// editDetail is what an audit row about an edit carries: what the fact said
// before and what it says now.
//
// Both sides whole, rather than a list of changed fields, because the row is read
// years later by somebody asking what happened, and "the scope went from learning
// to health" is a sentence that needs the old scope to be in the row to be true.
func editDetail(before, after Fact) []byte {
	detail, err := json.Marshal(map[string]any{
		"before": factRecord(before),
		"after":  factRecord(after),
	})
	if err != nil {
		// A map of strings and ints cannot fail to marshal. If it ever does, the row
		// is still worth writing: the edit happened.
		return nil
	}
	return detail
}

// factRecord is one fact as an audit row records it.
func factRecord(fact Fact) map[string]any {
	return map[string]any{
		"statement":   fact.Statement,
		"scope":       fact.Scope,
		"fingerprint": fact.Fingerprint,
		"version":     fact.Version,
	}
}

// Retract takes a fact back.
//
// Only a fact in force can be taken back, and only into one state. A fact that is
// already retracted has nothing to retract, and a merged fact cannot be taken back
// because it was never taken anywhere: its claim still stands, in the fact it was
// folded into, and undoing a merge would mean deciding which of two rows the
// evidence belongs to a second time (Dedupe's answer is that it belongs to both).
// Both of those are refusals rather than no-ops, because an audit row records an
// action, and "took back a fact that was already back" is not one.
func (s *Service) Retract(ctx context.Context, request FactRequest) (Fact, error) {
	return s.move(ctx, request, StatusRetracted, ActionFactRetracted)
}

// Restore undoes a retraction.
func (s *Service) Restore(ctx context.Context, request FactRequest) (Fact, error) {
	return s.move(ctx, request, StatusActive, ActionFactRestored)
}

// move is retract and restore: one operation read in two directions.
//
// What the two share is everything except which state they start from and which
// they end in, and the state they start from is not "whatever the fact is" but the
// other one — which is what makes each of them refuse the fact that is already
// where it was going.
func (s *Service) move(ctx context.Context, request FactRequest, to Status, action Action) (Fact, error) {
	if err := request.validate(); err != nil {
		return Fact{}, err
	}
	from := StatusActive
	if to == StatusActive {
		from = StatusRetracted
	}

	var moved Fact
	err := s.repository.Within(ctx, func(tx Repository) error {
		held, err := tx.Fact(ctx, request.OwnerID, request.FactID)
		if err != nil {
			return err
		}
		if held.Status != from {
			return fmt.Errorf("%w: fact %s is %s, and only a %s fact can be %s",
				ErrInvalidRequest, held.ID, held.Status, from, to)
		}

		now := s.clock()
		held.Status = to
		if to == StatusRetracted {
			held.RetractTime = now
		} else {
			// A restored fact has not been retracted, and the column says so by being
			// NULL rather than by holding the time it was once taken back at. The
			// times it was taken back are in the audit trail, which is where a history
			// of a state belongs.
			held.RetractTime = time.Time{}
		}
		reached, err := tx.SaveFact(ctx, held, held.Version, now)
		if err != nil {
			return err
		}
		held.Version = reached
		held.UpdatedAt = now

		if err := s.audit(ctx, tx, Audit{
			OwnerID:    request.OwnerID,
			Actor:      request.Actor,
			Action:     action,
			TargetKind: TargetFact,
			TargetID:   held.ID,
			Result:     ResultDone,
			Detail:     statusDetail(from, to),
			CreatedAt:  now,
		}); err != nil {
			return err
		}
		moved = held
		return nil
	})
	if err != nil {
		return Fact{}, err
	}
	return moved, nil
}

// statusDetail is what an audit row about a state change carries.
func statusDetail(from, to Status) []byte {
	detail, err := json.Marshal(map[string]any{"from": string(from), "to": string(to)})
	if err != nil {
		return nil
	}
	return detail
}

// StateRequest is the user writing a fact down themselves: §2.7's fourth path,
// `POST /memory/facts`.
//
// There is no surface and no kind on it, and their absence is the safety of this
// route rather than an omission. The surface and the kind are what the gate keys
// the fourth path on (Observation.writtenByUser), so a request that could choose
// them would be a request that could choose to skip the gate: the caller's kind
// travels with what a model saw, and a client free to send kind = user_stated
// could walk a model's guess straight into L2. Here the two are constants, which
// leaves the statement, the scope and who is asking.
type StateRequest struct {
	OwnerID int64
	// Statement is what the user wrote. Confidence is not asked for: the user's own
	// word is not a model reporting on itself.
	Statement string
	// Scope is where the fact belongs in the synthesis, or "" for none.
	Scope string
	Actor Actor
}

func (r StateRequest) validate() error {
	if r.OwnerID <= 0 {
		return fmt.Errorf("%w: the owner is %d", ErrInvalidRequest, r.OwnerID)
	}
	if strings.TrimSpace(r.Statement) == "" {
		return fmt.Errorf("%w: the statement is blank", ErrInvalidRequest)
	}
	if strings.TrimSpace(string(r.Actor)) == "" {
		return fmt.Errorf("%w: the actor is blank", ErrInvalidRequest)
	}
	return nil
}

// State writes a fact the user wrote themselves, and leaves a line saying they
// did.
//
// The observation goes through the same road as any other (Service.admit) — it is
// written to L1 first, and then it is either joined to a fact that already says
// the same thing or admitted on its own. That is deliberate: the fourth path skips
// the *conditions*, not the mechanism, so the user's statement is an observation
// like the ones the surfaces make, with a surface, a kind and an evidence chain
// like theirs. The difference is that the two facts worth knowing are both true
// here — `confirmed_by = user`, and no other surface can corroborate what only the
// user could have said.
//
// Stating something the account already believes does not make a second fact: the
// observation joins the one in force, and the audit trail then holds two lines
// saying the user wrote it, which is what happened twice.
func (s *Service) State(ctx context.Context, request StateRequest) (FactWithEvidence, error) {
	if err := request.validate(); err != nil {
		return FactWithEvidence{}, err
	}
	var written FactWithEvidence
	err := s.repository.Within(ctx, func(tx Repository) error {
		observed, err := s.admit(ctx, tx, Observation{
			OwnerID:    request.OwnerID,
			Surface:    SurfaceUser,
			Kind:       KindUserStated,
			Statement:  request.Statement,
			Confidence: userConfidence,
			Scope:      request.Scope,
		})
		if err != nil {
			return err
		}
		if !observed.Admitted {
			// Unreachable: writtenByUser holds by construction above, and the gate
			// admits on it before looking at anything else. Said out loud because the
			// alternative — carrying on with an empty fact id — would write an audit
			// row about a fact that does not exist.
			return fmt.Errorf("memory: the user's own statement did not become a fact (%s)", observed.TraceID)
		}

		now := s.clock()
		if err := s.audit(ctx, tx, Audit{
			OwnerID:    request.OwnerID,
			Actor:      request.Actor,
			Action:     ActionFactStated,
			TargetKind: TargetFact,
			TargetID:   observed.FactID,
			Result:     ResultDone,
			Detail:     statedDetail(observed),
			CreatedAt:  now,
		}); err != nil {
			return err
		}

		fact, err := tx.Fact(ctx, request.OwnerID, observed.FactID)
		if err != nil {
			return err
		}
		traces, err := tx.TracesOf(ctx, fact.ID)
		if err != nil {
			return err
		}
		written = FactWithEvidence{Fact: fact, Traces: traces}
		return nil
	})
	if err != nil {
		return FactWithEvidence{}, err
	}
	return written, nil
}

// statedDetail is what an audit row about the user writing a fact carries: the
// observation this statement made, which is what says whether it was the one that
// admitted the fact or one more voice joining one already in force.
func statedDetail(observed Observed) []byte {
	detail, err := json.Marshal(map[string]any{"trace": observed.TraceID})
	if err != nil {
		return nil
	}
	return detail
}
