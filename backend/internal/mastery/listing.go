package mastery

import (
	"context"
	"fmt"
	"strings"
	"time"

	"backend/internal/authz"
)

// Reading and naming paths, which is what the surface does with them outside a
// turn (MP-01).
//
// Three of the four acts here are reads or near-reads: listing what exists,
// reading one, and releasing a claim somebody left behind. The fourth — renaming
// what a path is for — writes one table and no history, and that is the one
// judgement worth stating: mastery_events is the tree's history (§2.1), and "the
// learner reworded the goal" is not a fact about the tree.

// Summary is one path as a listing shows it: what it is for, how much is in it,
// how much of that is known, and when it was last worked on.
//
// The counts are read off the tree rather than recomputed from the evidence, and
// that is the point of the level cache: a listing that ran the gate over every
// path's every evidence would be a listing that got slower the longer somebody
// studied.
type Summary struct {
	PathID      string
	Goal        string
	Description string
	Emoji       string
	Status      string
	Version     int64
	// Modules and KnowledgePoints are the size of the tree; Mastered is how many
	// of those points the gate calls mastered. The other three levels are the
	// insight's business, not a list row's.
	Modules         int
	KnowledgePoints int
	Mastered        int
	LastSeenAt      time.Time
	UpdatedAt       time.Time
}

// ListRequest is how a listing is narrowed. An empty SessionID lists every path,
// which is what a surface asks for when it has no conversation in hand.
type ListRequest struct {
	// Caller narrows the listing to one account, and it rides on the request
	// rather than being a parameter for the reason the session listing's does
	// (session.ListOptions.Caller): the struct already exists to carry what the
	// listing is narrowed by, and the caller is one of those things. An empty
	// caller is not a listing of everything — before V5 this route answered every
	// path in the database, which was right only because the database held one
	// person's work.
	Caller authz.Caller
	// SessionID, when set, lists only the paths this conversation has touched.
	SessionID string
}

// List reads the paths, most recently seen first.
func (s *Service) List(ctx context.Context, request ListRequest) ([]Summary, error) {
	rows, err := s.repository.Paths(ctx, strings.TrimSpace(request.SessionID), request.Caller)
	if err != nil {
		return nil, err
	}
	listed := make([]Summary, 0, len(rows))
	for _, row := range rows {
		summary := Summary{
			PathID:      row.Path.ID,
			Goal:        row.Meta.Goal,
			Description: row.Meta.Description,
			Emoji:       row.Meta.Emoji,
			Status:      row.Meta.Status,
			Version:     row.Path.Version,
			Modules:     len(row.Path.Tree.Modules),
			LastSeenAt:  row.LastSeenAt,
			UpdatedAt:   row.Path.UpdatedAt,
		}
		// An emoji nobody chose reads as the default here rather than as a blank
		// in a list, which is the same substitution Create makes when it writes
		// one (§2.1).
		if strings.TrimSpace(summary.Emoji) == "" {
			summary.Emoji = defaultEmoji
		}
		for _, module := range row.Path.Tree.Modules {
			for _, point := range module.KnowledgePoints {
				summary.KnowledgePoints++
				if point.Mastery == LevelMastered {
					summary.Mastered++
				}
			}
		}
		listed = append(listed, summary)
	}
	return listed, nil
}

// MetaRequest is a change to what a path is for.
//
// Every field is a pointer, because the request has to be able to tell "leave the
// goal alone" from "call the goal an empty string" and only one of those is
// something anybody wants (the same reason tool.KnowledgePointEdit is written
// this way). A request that names nothing is refused rather than accepted as a
// no-op: it is a client that meant to change something and got the field name
// wrong.
type MetaRequest struct {
	PathID      string
	Goal        *string
	Description *string
	Emoji       *string
	Status      *string
}

// UpdateMeta rewrites a path's topic metadata.
//
// It writes no event and takes no version, and that is a decision rather than an
// omission: the tree's history is what mastery_events holds, and this changes
// none of it. A goal that was reworded is still the same goal, and a version
// bump here would make every turn holding a read state look stale for a reason
// that has nothing to do with the tree.
func (s *Service) UpdateMeta(ctx context.Context, request MetaRequest, caller authz.Caller) (TopicMeta, error) {
	if strings.TrimSpace(request.PathID) == "" {
		return TopicMeta{}, fmt.Errorf("%w: the path is blank", ErrInvalidRequest)
	}
	if request.Goal == nil && request.Description == nil && request.Emoji == nil && request.Status == nil {
		return TopicMeta{}, fmt.Errorf("%w: the request names nothing to change", ErrInvalidRequest)
	}
	if err := s.requirePath(ctx, request.PathID, caller); err != nil {
		return TopicMeta{}, err
	}

	meta, err := s.repository.Meta(ctx, request.PathID, caller)
	if err != nil {
		if !isNotFound(err) {
			return TopicMeta{}, err
		}
		// A path from before this version has a tree and no metadata row, and
		// renaming it is how it gets one. Refusing would make the first edit of an
		// old path a failure.
		meta = TopicMeta{PathID: request.PathID, Status: StatusActive}
	}
	// A status this version does not write is refused rather than stored. Nothing
	// in MP-01–05 reads the column, so accepting "archived" would be this package
	// inventing a vocabulary and then not honouring it (§2.1, types.go).
	if request.Status != nil {
		if status := strings.TrimSpace(*request.Status); status != StatusActive {
			return TopicMeta{}, fmt.Errorf("%w: %q is not a status this version writes",
				ErrInvalidRequest, *request.Status)
		}
	}
	if request.Goal != nil {
		meta.Goal = strings.TrimSpace(*request.Goal)
	}
	if request.Description != nil {
		meta.Description = strings.TrimSpace(*request.Description)
	}
	if request.Emoji != nil {
		meta.Emoji = strings.TrimSpace(*request.Emoji)
	}
	if request.Status != nil {
		meta.Status = strings.TrimSpace(*request.Status)
	}
	if strings.TrimSpace(meta.Emoji) == "" {
		meta.Emoji = defaultEmoji
	}
	if err := s.repository.SaveMeta(ctx, meta, s.clock(), caller); err != nil {
		return TopicMeta{}, err
	}
	return meta, nil
}

// ReleaseAny gives up a path's claim whoever holds it, and reports the lease that
// was released.
//
// It is the endpoint's act and not a turn's, which is why it exists beside
// Release rather than inside it: a turn leaves the path it is on (Release names
// the holder, so a turn whose lease was taken over cannot drop the new holder's),
// while this is a person saying "nobody is writing this any more" about a claim a
// crashed turn left behind. The two are told apart by who names what.
//
// It releases nothing and reports false when nobody holds the path, which is the
// truth rather than a failure: a second release, or a release after the lease
// expired and was dropped, asked for a state that is already the state.
func (s *Service) ReleaseAny(ctx context.Context, pathID string, caller authz.Caller) (Lease, bool, error) {
	if strings.TrimSpace(pathID) == "" {
		return Lease{}, false, fmt.Errorf("%w: the path is blank", ErrInvalidRequest)
	}
	lease, err := s.repository.Lease(ctx, pathID, caller)
	if err != nil {
		if isNotFound(err) {
			return Lease{}, false, nil
		}
		return Lease{}, false, err
	}
	released, err := s.Release(ctx, pathID, Actor{SessionID: lease.SessionID, TurnID: lease.TurnID}, caller)
	if err != nil {
		return Lease{}, false, err
	}
	return lease, released, nil
}
