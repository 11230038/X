package mastery

import (
	"context"
	"fmt"
	"strings"
	"time"

	"backend/internal/authz"
)

// Counting what was learned in a window.
//
// A level is derived and not stamped, so "when did this knowledge point become
// mastered" is not a column anywhere: it is a question about the evidence, and
// the only honest way to answer it is to replay the gate. The replay is what
// makes this number mean something the tree's cache cannot: the cache says how
// many points are mastered *now*, and a learner who was already at ten in
// September and is still at ten in October learned nothing this week, however
// large the total is.

// MasteredBetween counts the knowledge points that became mastered inside
// [from, to).
//
// A point counts once, when the replay says its level at the end of the window
// is mastered and its level at the start was not. Three consequences are worth
// stating because they are the definition rather than side effects:
//
//   - a point mastered before the window does not count again, however many
//     right answers it got inside it;
//   - a point that reached mastered and then lost it inside the window does not
//     count either, because what the number reports is the state now;
//   - a point dropped from the tree is not counted, since the tree is what the
//     count is about and evidence for a coordinate that is gone is not learning
//     the learner still has.
//
// Evidence at or after `to` is ignored at both ends: the question is what the
// window did, and an observation made after it did not.
func (s *Service) MasteredBetween(ctx context.Context, from, to time.Time, caller authz.Caller) (int, error) {
	if !from.Before(to) {
		return 0, fmt.Errorf("%w: the window ends before it begins", ErrInvalidRequest)
	}
	rows, err := s.repository.Paths(ctx, "", caller)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, row := range rows {
		evidences, err := s.repository.Evidences(ctx, row.Path.ID, caller)
		if err != nil {
			return 0, err
		}
		byPoint := evidenceUntil(evidences, to)
		for _, id := range row.Path.Tree.KnowledgePointIDs() {
			if s.levelBetween(byPoint[id], from) == LevelMastered {
				continue
			}
			if GateDecide(byPoint[id], s.gate) == LevelMastered {
				count++
			}
		}
	}
	return count, nil
}

// PathProgress is one path's window: the modules it finished inside the window,
// over the modules it has.
//
// A module is finished when every knowledge point in it is mastered, and it is
// counted when it was not finished at the window's start and is finished at its
// end. A module with no knowledge points is in neither number: it can never be
// finished, and putting it in the denominator would make 100% unreachable
// through no fault of the learner's.
type PathProgress struct {
	ModulesDone  int
	ModulesTotal int
}

// PathProgressBetween counts the modules one path finished in a window.
//
// It replays the gate the same way MasteredBetween does — evidence before the
// window for the starting levels, evidence before its end for the current ones —
// and reads the tree for the modules, so the denominator is the path as it
// stands now. A module dropped from the tree is not counted, for the same reason
// a dropped knowledge point is not.
func (s *Service) PathProgressBetween(ctx context.Context, pathID string, from, to time.Time, caller authz.Caller) (PathProgress, error) {
	if strings.TrimSpace(pathID) == "" {
		return PathProgress{}, fmt.Errorf("%w: the path is blank", ErrInvalidRequest)
	}
	if !from.Before(to) {
		return PathProgress{}, fmt.Errorf("%w: the window ends before it begins", ErrInvalidRequest)
	}
	path, err := s.repository.Path(ctx, pathID, caller)
	if err != nil {
		return PathProgress{}, err
	}
	evidences, err := s.repository.Evidences(ctx, pathID, caller)
	if err != nil {
		return PathProgress{}, err
	}
	byPoint := evidenceUntil(evidences, to)
	progress := PathProgress{}
	for _, module := range path.Tree.Modules {
		if len(module.KnowledgePoints) == 0 {
			continue
		}
		progress.ModulesTotal++
		doneAtStart, doneAtEnd := true, true
		for _, point := range module.KnowledgePoints {
			history := byPoint[point.ID]
			if s.levelBetween(history, from) != LevelMastered {
				doneAtStart = false
			}
			if GateDecide(history, s.gate) != LevelMastered {
				doneAtEnd = false
			}
			// Once neither end can be "all mastered", the module is decided and
			// the rest of its points cannot change either answer.
			if !doneAtStart && !doneAtEnd {
				break
			}
		}
		if doneAtEnd && !doneAtStart {
			progress.ModulesDone++
		}
	}
	return progress, nil
}

// evidenceByPoint groups evidence by knowledge point, dropping anything at or
// after `to`.
//
// The repository returns evidence oldest first, so appending keeps each point's
// history in the order the gate reads a run in. Anything after `to` is not part
// of the question — the window is what happened up to its end.
func evidenceUntil(evidences []Evidence, to time.Time) map[string][]Evidence {
	byPoint := make(map[string][]Evidence, len(evidences))
	for _, evidence := range evidences {
		if !evidence.CreatedAt.Before(to) {
			continue
		}
		byPoint[evidence.KnowledgePoint] = append(byPoint[evidence.KnowledgePoint], evidence)
	}
	return byPoint
}

// levelBetween is a point's level at the moment the window opened: the gate over
// the evidence strictly before `from`, which is the prefix of a history that is
// already ordered.
func (s *Service) levelBetween(history []Evidence, from time.Time) Level {
	split := 0
	for split < len(history) && history[split].CreatedAt.Before(from) {
		split++
	}
	return GateDecide(history[:split], s.gate)
}
