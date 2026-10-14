package mastery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Evidence recording is its own file because it is its own act. A tree change
// answers "what does the path look like now"; an observation answers "what was
// seen", and the second is the input the gate reads. The two share the version
// guard, the event log and the transaction, and nothing else.

// RecordEvidence appends one observation to a path's history and moves the
// levels the gate derives from it.
//
// The order is state first, event second — the reverse of a tree change — and
// the reversal is the whole of the lease's expiry rule (§2.3). A turn whose
// lease expired while it was still running loses the version guard; if losing it
// rolled the transaction back, it would also destroy the observation, and the
// observation is still true: the learner did answer that question, whatever the
// next writer did to the tree. So the guard loses, the state stays where the
// other writer put it, and the evidence and its event land anyway.
//
// Which version the event names follows from that. When the state moved, the
// event names the version it reached; when it did not, the event names the
// version the observation was made against. Both are true statements about
// different things, and a made-up version would be a statement about neither.
func (s *Service) RecordEvidence(ctx context.Context, evidence Evidence) (Evidence, error) {
	if err := evidence.Validate(); err != nil {
		return Evidence{}, err
	}
	now := s.clock()
	if evidence.CreatedAt.IsZero() {
		evidence.CreatedAt = now
	}

	var recorded Evidence
	err := s.repository.Within(ctx, func(repository Repository) error {
		path, err := repository.Path(ctx, evidence.PathID)
		if err != nil {
			return err
		}
		// The knowledge point has to be in the tree, and refusing one that is not is
		// the one place this package can check a coordinate at all: knowledge point
		// ids are not a table and no foreign key can reach them (§2.2). An
		// observation about a knowledge point that does not exist is a caller that
		// has the wrong path or the wrong id, and either way it is not something to
		// store.
		if _, _, ok := path.Tree.KnowledgePoint(evidence.KnowledgePoint); !ok {
			return fmt.Errorf("%w: knowledge point %s is not in path %s",
				ErrNotFound, evidence.KnowledgePoint, path.ID)
		}

		if err := repository.AppendEvidence(ctx, evidence); err != nil {
			return err
		}
		evidences, err := repository.Evidences(ctx, path.ID)
		if err != nil {
			return err
		}
		tree := GateTree(path.Tree, evidences, s.gate)

		// The guard first, because which version the event may name depends on
		// whether it held.
		version := path.Version
		updated := false
		moved, err := repository.SaveTree(ctx, path.ID, path.Version, tree, now)
		switch {
		case err == nil:
			version, updated = moved, true
		case errors.Is(err, ErrStaleState):
			// The path moved under this observation. The evidence stands; the state
			// does not get written, and the levels wait for the writer that did move
			// it to recompute them — which it will, because every writer of the state
			// runs the gate over the whole evidence.
		default:
			return err
		}

		eventID, err := newID(eventIDPrefix)
		if err != nil {
			return err
		}
		payload, err := json.Marshal(map[string]any{
			"evidence_id":     evidence.ID,
			"knowledge_point": evidence.KnowledgePoint,
			"result":          evidence.Result,
			"state_updated":   updated,
		})
		if err != nil {
			return fmt.Errorf("encode evidence %s: %w", evidence.ID, err)
		}
		event := Event{
			ID:        eventID,
			PathID:    path.ID,
			Type:      EventEvidenceRecorded,
			Version:   version,
			Payload:   payload,
			Actor:     evidence.Actor,
			CreatedAt: now,
		}
		if err := repository.AppendEvent(ctx, event); err != nil {
			return err
		}
		if err := repository.TouchSession(ctx, path.ID, evidence.Actor, now); err != nil {
			return err
		}
		recorded = evidence
		return nil
	})
	if err != nil {
		return Evidence{}, err
	}
	return recorded, nil
}

// Evidences reads a path's evidence, oldest first, so that the gate and the
// insight endpoint do not each have to decide what order means.
func (s *Service) Evidences(ctx context.Context, pathID string) ([]Evidence, error) {
	return s.repository.Evidences(ctx, pathID)
}
