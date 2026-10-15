package mastery

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"backend/internal/authz"
)

// A question is put, an answer comes back, and what came of it is written down.
//
// This is the one place in the domain where the learner is expected to do
// something, and it is why `mastery_interactions` exists at all: a knowledge
// point's level is derived from evidence, and evidence about a knowledge point
// is only worth counting if something established what was actually asked. The
// three tools that touch this table are the three moments of one transaction —
// put the question, record the verdict, or decline to.
//
// **At most one interaction is open on a path at a time.** This table used to
// keep that rule in the service alone, in one transaction, because the reference
// implementation states it as a partial unique index and this engine has none —
// and the service's read cannot hold it: the read matches no row, and under READ
// COMMITTED a read that matches nothing locks nothing. Migration 00019 moved the
// rule into the schema in 00009's shape, a generated column plus a unique key, so
// two racing asks cannot both land. What is left here is the fast path: the read
// below refuses the ordinary second call without an insert attempt, and the key
// refuses the one that raced it (§2.7).

// Interaction is one question put to a learner, while it is open.
//
// It carries no result and no answer. Those exist only once the interaction is
// closed, and what reads them is a different question — the history of a path —
// which is a read this version does not do; the two writes below are all the
// domain needs of the row it keeps.
type Interaction struct {
	ID     string
	PathID string
	// KnowledgePointID is the coordinate the question was filed against, as the
	// model named it. It is what a grade records its evidence about, and it is
	// the reason the coordinate is stored at all: without it a verdict would be
	// an observation about nothing in particular.
	KnowledgePointID string
	// QuestionID is the bank's content address of the question that was asked.
	QuestionID string
	Actor      Actor
	CreatedAt  time.Time
}

// Validate reports whether the interaction is fit to store.
func (i Interaction) Validate() error {
	if strings.TrimSpace(i.ID) == "" {
		return fmt.Errorf("%w: the interaction id is blank", ErrInvalidRequest)
	}
	if strings.TrimSpace(i.PathID) == "" {
		return fmt.Errorf("%w: the path is blank", ErrInvalidRequest)
	}
	if strings.TrimSpace(i.KnowledgePointID) == "" {
		return fmt.Errorf("%w: the interaction names no knowledge point", ErrInvalidRequest)
	}
	if strings.TrimSpace(i.QuestionID) == "" {
		return fmt.Errorf("%w: the interaction names no question", ErrInvalidRequest)
	}
	return i.Actor.Validate()
}

// interactionOutcome is what a closed interaction says happened.
//
// One column, two shapes, told apart by which field is set — because the two
// endings are not two kinds of result. A judgement answers the question; a skip
// is the learner declining to answer it, and the design is explicit that a skip
// is not a wrong answer and must not lower anything (§2.5). Keeping them in one
// document is what makes "closed" one fact instead of two.
type interactionOutcome struct {
	Result  string `json:"result,omitempty"`
	Detail  string `json:"detail,omitempty"`
	Skipped bool   `json:"skipped,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// ErrOpenInteraction is a path that already has a question waiting.
//
// It is a sentinel as well as a carrier, like ErrLeaseHeld, and for the same
// reason: the caller that only needs to know "there is one already" switches on
// the sentinel, and the caller that has to say which question it is reads the
// interaction out of the error. That second caller is the tool: a model told
// "there is an open question (min_…)" can pick it back up, while a model told
// "conflict" can only start over.
var ErrOpenInteraction = fmt.Errorf("mastery: this path already has an open question")

// OpenInteractionError is that refusal, with the interaction that is waiting.
type OpenInteractionError struct {
	Interaction Interaction
}

// Error names the question and its knowledge point, which are the two things the
// caller needs in order to carry on with it rather than work around it.
func (e *OpenInteractionError) Error() string {
	return fmt.Sprintf("mastery: path %s already has the question %s open on knowledge point %s",
		e.Interaction.PathID, e.Interaction.ID, e.Interaction.KnowledgePointID)
}

// Is makes errors.Is(err, ErrOpenInteraction) true for every one of these.
func (e *OpenInteractionError) Is(target error) bool {
	return target == ErrOpenInteraction
}

// AskRequest is one question put to the learner on a path this turn is on.
type AskRequest struct {
	PathID string
	Actor  Actor
	// KnowledgePointID is the coordinate the question is filed against. It has
	// to be in the tree, and that is the only check that can be made: knowledge
	// point ids are not a table (§2.2).
	KnowledgePointID string
	// QuestionID is the bank's id for the question, which the caller has just
	// written there. This call does not write the question; it records that this
	// one was asked.
	QuestionID string
}

// Ask records that a question has been put, and reports the interaction that is
// now waiting for an answer.
//
// The reading of "nothing is open" and the writing of "this one is" happen in
// one transaction. The read is what refuses the ordinary second call, and it is
// not what makes the rule true: a second call that reads the same empty answer
// is stopped by the unique key migration 00019 builds, and the insert below maps
// that collision back to the same refusal this read gives — see the note at the
// top of this file.
func (s *Service) Ask(ctx context.Context, ask AskRequest, caller authz.Caller) (Interaction, error) {
	if err := ask.Actor.Validate(); err != nil {
		return Interaction{}, err
	}
	if strings.TrimSpace(ask.KnowledgePointID) == "" {
		return Interaction{}, fmt.Errorf("%w: no knowledge point was named", ErrInvalidRequest)
	}
	if strings.TrimSpace(ask.QuestionID) == "" {
		return Interaction{}, fmt.Errorf("%w: no question was named", ErrInvalidRequest)
	}
	now := s.clock()

	var asked Interaction
	err := s.repository.Within(ctx, func(repository Repository) error {
		// Scoped first: a question put on another account's path is ErrNotFound
		// before any interaction or event is written.
		path, err := repository.Path(ctx, ask.PathID, caller)
		if err != nil {
			return err
		}
		if _, _, ok := path.Tree.KnowledgePoint(ask.KnowledgePointID); !ok {
			return fmt.Errorf("%w: knowledge point %s is not in path %s",
				ErrNotFound, ask.KnowledgePointID, path.ID)
		}
		open, err := repository.ActiveInteraction(ctx, path.ID, caller)
		switch {
		case err == nil:
			return &OpenInteractionError{Interaction: open}
		case !isNotFound(err):
			return err
		}

		id, err := newID(interactionIDPrefix)
		if err != nil {
			return err
		}
		interaction := Interaction{
			ID:               id,
			PathID:           path.ID,
			KnowledgePointID: ask.KnowledgePointID,
			QuestionID:       ask.QuestionID,
			Actor:            ask.Actor,
			CreatedAt:        now,
		}
		if err := interaction.Validate(); err != nil {
			return err
		}
		if err := repository.CreateInteraction(ctx, interaction, caller); err != nil {
			return err
		}
		payload, err := json.Marshal(map[string]any{
			"interaction_id":  id,
			"knowledge_point": ask.KnowledgePointID,
			"question_id":     ask.QuestionID,
		})
		if err != nil {
			return fmt.Errorf("encode interaction %s: %w", id, err)
		}
		eventID, err := newID(eventIDPrefix)
		if err != nil {
			return err
		}
		event := Event{
			ID:        eventID,
			PathID:    path.ID,
			Type:      EventInteractionOpened,
			Version:   path.Version,
			Payload:   payload,
			Actor:     ask.Actor,
			CreatedAt: now,
		}
		if err := repository.AppendEvent(ctx, event, caller); err != nil {
			return err
		}
		if err := repository.TouchSession(ctx, path.ID, ask.Actor, now, caller); err != nil {
			return err
		}
		asked = interaction
		return nil
	})
	if err != nil {
		return Interaction{}, err
	}
	return asked, nil
}

// GradeRequest is one verdict on one answer.
type GradeRequest struct {
	PathID string
	Actor  Actor
	// QuestionID is the question the answer was given to, and the open
	// interaction is looked up by it: a verdict on a question this path did not
	// ask is a verdict with nothing to attach to, and it is refused rather than
	// recorded against whatever is open.
	QuestionID string
	// UserAnswer is what the learner wrote, verbatim. It is stored as it came,
	// and the reason is the same one the review path gives: the record of what
	// somebody answered is not a field that gets tidied.
	UserAnswer string
	// Result is the verdict, and it comes from the caller. Deciding it is not
	// this package's job — an objective question is a comparison against the
	// bank's stored answer and an open one is a model's reading, and both of
	// those belong to the code that owns them (practice). What this package owns
	// is what a verdict does to the path.
	Result Result
	// Detail is the judge's own account of the verdict, kept verbatim.
	Detail []byte
	// AssessmentType says how the verdict was reached — by comparison, or by a
	// model's reading. It is optional in the table and reported when it is there.
	AssessmentType string
}

// Grade closes the open question with the answer and records what the verdict
// says about the knowledge point.
//
// The two are one transaction on purpose. Closing the question and recording the
// evidence are the two halves of one event, and a transaction that spanned only
// one of them could leave a question answered with nothing said about the
// learner — which is the case this whole table exists to prevent (the design's
// "state and history in one transaction", §2.1).
//
// The evidence is recorded *after* the close, so a call that loses the race to
// close the question also records nothing: the winner's verdict is the one that
// counts, and a second verdict on one answer is not a second observation.
func (s *Service) Grade(ctx context.Context, grade GradeRequest, caller authz.Caller) (Interaction, error) {
	if err := grade.Actor.Validate(); err != nil {
		return Interaction{}, err
	}
	if !grade.Result.Valid() {
		return Interaction{}, fmt.Errorf("%w: %q is not a result", ErrInvalidRequest, grade.Result)
	}
	now := s.clock()

	var graded Interaction
	err := s.repository.Within(ctx, func(repository Repository) error {
		// The path is read for its version and for the fact that it is there: the
		// answered event is a statement about the state the answer arrived at, and
		// the evidence write below may move that state afterwards. The read is
		// scoped, so a verdict aimed at another account's path never gets past it.
		path, err := repository.Path(ctx, grade.PathID, caller)
		if err != nil {
			return err
		}
		open, err := repository.ActiveInteraction(ctx, grade.PathID, caller)
		if err != nil {
			return err
		}
		if open.QuestionID != grade.QuestionID {
			return fmt.Errorf("%w: path %s is waiting on question %s, not %s",
				ErrNotFound, grade.PathID, open.QuestionID, grade.QuestionID)
		}

		outcome, err := json.Marshal(interactionOutcome{
			Result: string(grade.Result),
			Detail: string(grade.Detail),
		})
		if err != nil {
			return fmt.Errorf("encode the verdict on %s: %w", open.ID, err)
		}
		closed, err := repository.CloseInteraction(ctx, open.ID, grade.UserAnswer, outcome, now, caller)
		if err != nil {
			return err
		}
		if !closed {
			return fmt.Errorf("%w: interaction %s was closed by another call",
				ErrStaleState, open.ID)
		}

		eventID, err := newID(eventIDPrefix)
		if err != nil {
			return err
		}
		payload, err := json.Marshal(map[string]any{
			"interaction_id":  open.ID,
			"knowledge_point": open.KnowledgePointID,
			"question_id":     open.QuestionID,
			"result":          grade.Result,
		})
		if err != nil {
			return fmt.Errorf("encode the answer to %s: %w", open.ID, err)
		}
		event := Event{
			ID:        eventID,
			PathID:    grade.PathID,
			Type:      EventInteractionAnswered,
			Version:   path.Version,
			Payload:   payload,
			Actor:     grade.Actor,
			CreatedAt: now,
		}
		if err := repository.AppendEvent(ctx, event, caller); err != nil {
			return err
		}

		evidenceID, err := newID(evidenceIDPrefix)
		if err != nil {
			return err
		}
		evidence := Evidence{
			ID:             evidenceID,
			PathID:         grade.PathID,
			KnowledgePoint: open.KnowledgePointID,
			Result:         grade.Result,
			Source:         SourceGradedAnswer,
			AssessmentType: grade.AssessmentType,
			Detail:         grade.Detail,
			Actor:          grade.Actor,
			CreatedAt:      now,
		}
		if err := s.recordEvidence(ctx, repository, evidence, now, caller); err != nil {
			return err
		}
		graded = open
		return nil
	})
	if err != nil {
		return Interaction{}, err
	}
	return graded, nil
}

// SkipRequest is a learner moving past one knowledge point.
type SkipRequest struct {
	PathID           string
	Actor            Actor
	KnowledgePointID string
	// Reason is why, and it is the only part of a skip worth keeping. An empty
	// one is allowed: "this one is not for now" is often all there is to say.
	Reason string
}

// Skip records that the learner moved past a knowledge point.
//
// It writes no evidence, and that is the whole of the rule (§2.5). A skip
// recorded as an observation would move the gate — one `incorrect` and the
// knowledge point is weak — and the learner who said "先放着" would come back to
// find themselves marked bad at it. So the fact is written where it belongs: an
// interaction ended this way, and an event saying so.
//
// An open question on the same knowledge point is closed as skipped rather than
// left open. It is the case this tool exists for — the model asked, the learner
// said not now — and leaving the row open would block the next question with a
// refusal about a question nobody is going to answer.
func (s *Service) Skip(ctx context.Context, skip SkipRequest, caller authz.Caller) error {
	if err := skip.Actor.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(skip.KnowledgePointID) == "" {
		return fmt.Errorf("%w: no knowledge point was named", ErrInvalidRequest)
	}
	now := s.clock()

	return s.repository.Within(ctx, func(repository Repository) error {
		// Scoped first, like the other two interaction writes: a skip aimed at
		// another account's path is ErrNotFound before anything is written.
		path, err := repository.Path(ctx, skip.PathID, caller)
		if err != nil {
			return err
		}
		// The coordinate is checked here for the same reason the other writers
		// check it: an id that is not in the tree is a caller holding the wrong
		// path or the wrong id, and a history line about it would be a line about
		// nothing.
		if _, _, ok := path.Tree.KnowledgePoint(skip.KnowledgePointID); !ok {
			return fmt.Errorf("%w: knowledge point %s is not in path %s",
				ErrNotFound, skip.KnowledgePointID, path.ID)
		}

		open, err := repository.ActiveInteraction(ctx, path.ID, caller)
		closed := false
		switch {
		case err == nil && open.KnowledgePointID == skip.KnowledgePointID:
			outcome, err := json.Marshal(interactionOutcome{Skipped: true, Reason: skip.Reason})
			if err != nil {
				return fmt.Errorf("encode the skip of %s: %w", open.ID, err)
			}
			closed, err = repository.CloseInteraction(ctx, open.ID, "", outcome, now, caller)
			if err != nil {
				return err
			}
		case err != nil && !isNotFound(err):
			return err
		}

		payload, err := json.Marshal(map[string]any{
			"knowledge_point": skip.KnowledgePointID,
			"reason":          skip.Reason,
			"interaction_id":  closedInteractionID(open, closed),
		})
		if err != nil {
			return fmt.Errorf("encode the skip of %s: %w", skip.KnowledgePointID, err)
		}
		eventID, err := newID(eventIDPrefix)
		if err != nil {
			return err
		}
		event := Event{
			ID:        eventID,
			PathID:    path.ID,
			Type:      EventInteractionSkipped,
			Version:   path.Version,
			Payload:   payload,
			Actor:     skip.Actor,
			CreatedAt: now,
		}
		if err := repository.AppendEvent(ctx, event, caller); err != nil {
			return err
		}
		return repository.TouchSession(ctx, path.ID, skip.Actor, now, caller)
	})
}

// closedInteractionID names the interaction a skip closed, or nothing when it
// closed none. It is in the payload because "this ended the question that was
// open" and "this moved past a knowledge point" are different histories, and a
// reader of the log should not have to guess which one happened.
func closedInteractionID(open Interaction, closed bool) string {
	if !closed {
		return ""
	}
	return open.ID
}
