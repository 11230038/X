package mastery

import (
	"context"
	"encoding/json"
	"fmt"
)

// Evidence recording is its own file because it is its own act. A tree change
// answers "what does the path look like now"; an observation answers "what was
// seen", and the second is the input the gate reads. The two share the version
// guard, the event log and the transaction, and nothing else.

// RecordEvidence appends one observation to a path's history.
//
// It takes the path's version for the same reason a tree change does: the
// evidence is a fact about the path's progress, and the event left behind says
// which version it belongs to. What it does *not* do — yet — is recompute the
// mastery levels cached in the tree; that is the gate's job and it arrives with
// it, in the same transaction, so that "the evidence was recorded" and "the
// levels say so" can never come apart.
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
		eventID, err := newID(eventIDPrefix)
		if err != nil {
			return err
		}
		payload, err := json.Marshal(map[string]any{
			"evidence_id":     evidence.ID,
			"knowledge_point": evidence.KnowledgePoint,
			"result":          evidence.Result,
		})
		if err != nil {
			return fmt.Errorf("encode evidence %s: %w", evidence.ID, err)
		}
		event := Event{
			ID:        eventID,
			PathID:    path.ID,
			Type:      EventEvidenceRecorded,
			Version:   path.Version + 1,
			Payload:   payload,
			Actor:     evidence.Actor,
			CreatedAt: now,
		}
		if err := repository.AppendEvent(ctx, event); err != nil {
			return err
		}
		// The state is rewritten as itself, under the guard, so that the version the
		// evidence's event names is a version the path really reached. It is not a
		// no-op: without it the event would point at a version that the next tree
		// change would take instead, and the history would say two different changes
		// produced the same one.
		if _, err := repository.SaveTree(ctx, path.ID, path.Version, path.Tree, now); err != nil {
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
