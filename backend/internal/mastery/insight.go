package mastery

import (
	"context"
	"sort"
	"time"
)

// The insight: a path as "where am I and what next" (MP-05).
//
// It is a read and a reassembly and it has no table of its own (§2.12): every
// field points at rows that already exist — the goal in mastery_topic_meta, the
// tree in mastery_paths.state_json, the evidence in mastery_learning_evidences —
// and the one thing that is computed rather than read is the next step.
//
// Three rules from §2.12 are the shape of this file:
//
//   - no cache and no snapshot table. What is assembled here is a few indexed
//     reads at this data's scale, and a snapshot would raise the question of when
//     to refresh it in exchange for a speed nobody has measured.
//   - empty and broken are different answers. A path with no evidence is a path
//     with no evidence — its points come back unknown, not missing — while a
//     query that will not answer is an error, and the caller tells them apart.
//   - everything that is aggregated is aggregated over the tree. Evidence whose
//     coordinate is no longer in the tree is dropped here rather than reported as
//     a weak point, because a coordinate that does not resolve is not a finding
//     (§2.2, §8 第 12 条).

// PointInsight is one knowledge point as the insight reports it: its level, what
// the evidence says, and when to come back.
type PointInsight struct {
	ID       string
	Name     string
	ModuleID string
	Level    Level
	// Total, Correct, Incorrect and Partial are the evidence for this point,
	// counted. They are the aggregation MP-05 asks for, and they are here rather
	// than behind a second call so that "why is this point weak" is answerable
	// from one response.
	Total     int
	Correct   int
	Incorrect int
	Partial   int
	// LastObservedAt is when this point was last observed, and zero when it never
	// was. Zero is not a time in 1970 — it is "nobody has looked", which is what
	// LevelUnknown says in the other direction.
	LastObservedAt time.Time
	// DueAt is when the curve says to come back, and zero for a point with no
	// evidence: an unstarted point is not overdue, it is unstarted.
	DueAt time.Time
}

// Insight is one path's whole answer to "where am I".
type Insight struct {
	Path Path
	Meta TopicMeta
	// Points is every knowledge point in the tree, in the order the tree holds
	// them. It is complete rather than filtered: the levels are what the gate
	// decided for all of them, and a client that drew only what it was told about
	// could not tell an absent point from an unknown one.
	Points []PointInsight
	// Weak is the ids of the points the gate calls weak, most overdue first,
	// which is the order a learner should look at them in. It is ids and not a
	// second list of points because it is a *view* of Points: the same point
	// appearing twice with two names would eventually disagree with itself.
	Weak []string
	// Evidence is every observation this path has, oldest first. It is not
	// capped: one path's evidence is bounded by the size of one path (types.go),
	// and a number here would be this file inventing a limit the domain does not
	// have.
	Evidence []Evidence
	// Next is the next move, with its reason and its grounds.
	Next Step
}

// Insight assembles one path's state for a reader.
//
// The review state it reports is the knowledge-point curve of §2.4 — when to come
// back to *a knowledge point*. The question-level curve (`practice_review_state`)
// is about one question being asked again, and it joins this view once a question
// carries the coordinate it was asked against (§2.2); until then there is no way
// to aggregate it to a knowledge point, and reporting a question's due time as a
// knowledge point's would be a different number under the same name.
func (s *Service) Insight(ctx context.Context, pathID string) (Insight, error) {
	view, err := s.Load(ctx, pathID)
	if err != nil {
		return Insight{}, err
	}
	evidences, err := s.repository.Evidences(ctx, pathID)
	if err != nil {
		return Insight{}, err
	}
	next, err := NextStep(view.Path, evidences, s.clock())
	if err != nil {
		return Insight{}, err
	}

	byPoint := evidenceByPoint(evidences)
	points := make([]PointInsight, 0, len(byPoint))
	weak := make([]PointInsight, 0)
	for _, id := range view.Path.Tree.KnowledgePointIDs() {
		point, module, _ := view.Path.Tree.KnowledgePoint(id)
		observed := byPoint[id]
		row := PointInsight{
			ID:       id,
			Name:     point.Name,
			ModuleID: module.ID,
			Level:    GateDecide(observed, s.gate),
			Total:    len(observed),
			DueAt:    dueAt(observed),
		}
		for _, evidence := range observed {
			switch evidence.Result {
			case ResultCorrect:
				row.Correct++
			case ResultIncorrect:
				row.Incorrect++
			case ResultPartial:
				row.Partial++
			}
		}
		if len(observed) > 0 {
			row.LastObservedAt = observed[len(observed)-1].CreatedAt
		}
		points = append(points, row)
		if row.Level == LevelWeak {
			weak = append(weak, row)
		}
	}

	// The weak ones are ordered the way the step's choice is: the one that has
	// been waiting longest comes first, and tree order breaks the tie.
	sort.SliceStable(weak, func(i, j int) bool { return weak[i].DueAt.Before(weak[j].DueAt) })
	ids := make([]string, 0, len(weak))
	for _, row := range weak {
		ids = append(ids, row.ID)
	}

	return Insight{Path: view.Path, Meta: view.Meta, Points: points, Weak: ids, Evidence: evidences, Next: next}, nil
}

// dueAt is when a knowledge point's evidence says to come back, or the zero time
// for one with none: nothing to schedule rather than due now (ReviewInterval).
func dueAt(evidences []Evidence) time.Time {
	interval := ReviewInterval(evidences)
	if interval == 0 {
		return time.Time{}
	}
	return evidences[len(evidences)-1].CreatedAt.Add(interval)
}
