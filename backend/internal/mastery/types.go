// Package mastery owns the mastery path: the tree a learner is working through,
// the evidence that says how far along they are, and the record of how the tree
// got to be what it is.
//
// The seven tables were created in migration 00004 and have carried no rows
// since; V4 is their first writer. What this package decides is which of them
// says what, and the split is the design's (§2.1):
//
//   - mastery_paths.state_json is what the tree looks like now, with a version
//     that every write compares against;
//   - mastery_events is how it came to look that way — ordered, versioned,
//     append-only, written in the same transaction as the state it describes;
//   - mastery_learning_evidences is what was observed about one knowledge point;
//   - mastery_interactions is one question-answer transaction, whole;
//   - mastery_topic_meta is what the path is for;
//   - mastery_path_sessions is which conversations have touched it;
//   - mastery_path_leases is who is writing it (lease.go).
//
// The line that matters most is between the first two and the third: the state
// holds the tree and a level per knowledge point that is *derived* from the
// evidence, and the evidence itself lives in exactly one table. A copy of the
// evidence inside state_json would give the gate and the insight endpoint two
// half-readings of the same thing, and the two would drift.
package mastery

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// The sentinels this package's callers switch on.
var (
	// ErrNotFound is a path, a knowledge point or an interaction that is not
	// there.
	ErrNotFound = errors.New("mastery: not found")
	// ErrInvalidRequest is a request the domain will not act on.
	ErrInvalidRequest = errors.New("mastery: the request is not valid")
	// ErrDuplicate is a write that lost a race on a key: the same event id, the
	// same interaction id, the same session on a path.
	ErrDuplicate = errors.New("mastery: already recorded")
	// ErrStaleState is a write that lost the version guard. The state it was
	// computed from is no longer the state on the row, and the caller has to read
	// again rather than write.
	ErrStaleState = errors.New("mastery: the path moved on")
)

// Result is the outcome of one piece of evidence, and it is a closed set.
//
// The gate reads this column and not `quality`, and that is a decision with a
// reason: `quality` is an optional score string, so a gate built on it would
// have to decide what an absent score means and what `"0.8"` means, and every
// case about the gate would be a case about parsing. Three values that the code
// refuses to widen make the gate's cases exhaustive.
//
// The set is a constant in this file and not a `.env` key, for the same reason
// migration 00011's column is a BOOLEAN and not a string: it is the shape of the
// domain, not a deployment's preference.
type Result string

const (
	// ResultCorrect is the answer being right, or the learner demonstrating the
	// knowledge point by some other means.
	ResultCorrect Result = "correct"
	// ResultIncorrect is the answer being wrong. It is not the same as "not
	// tested" — see LevelUnknown.
	ResultIncorrect Result = "incorrect"
	// ResultPartial is credit given for part of an answer. It is stored and it is
	// reported, and it is deliberately neither of the other two when the gate
	// counts a run.
	ResultPartial Result = "partial"
)

// Valid reports whether the result is one of the three.
func (r Result) Valid() bool {
	switch r {
	case ResultCorrect, ResultIncorrect, ResultPartial:
		return true
	default:
		return false
	}
}

// Level is how well a knowledge point is known, as the surface shows it.
//
// There are four, and the third and fourth exist because the two ways of not
// knowing a thing are not one state (PRD §19.3's distinction, applied here):
// a knowledge point nobody has tested is not a weak point, and a report that
// merges them tells a learner they are bad at something they have never tried.
//
// The value stored in the tree is a cache. It is recomputed from the evidence
// every time the state is written (gate.go), and the evidence is the truth — the
// copy exists so that a read of the tree does not have to decode a second table
// to draw itself.
type Level string

const (
	// LevelUnknown is fewer than the gate's minimum evidence. It is not
	// "weak" and must not be rendered as one (§2.4).
	LevelUnknown Level = "unknown"
	// LevelWeak is enough evidence, and the recent run says the knowledge point
	// is not landing.
	LevelWeak Level = "weak"
	// LevelLearning is progress that is neither failing nor finished: some right
	// answers, no run long enough to call it mastered.
	LevelLearning Level = "learning"
	// LevelMastered is a run of correct answers at or above the gate's
	// threshold.
	LevelMastered Level = "mastered"
)

// Valid reports whether the level is one this package defines.
func (l Level) Valid() bool {
	switch l {
	case LevelUnknown, LevelWeak, LevelLearning, LevelMastered:
		return true
	default:
		return false
	}
}

// EventType names one change to a path. The vocabulary is dotted and modelled on
// the reference implementation's, which is the only place these names come from
// — they are not a database enum and the column that holds them is a plain
// VARCHAR, so an unrecognised value is a row written by a newer build rather
// than an error.
type EventType string

const (
	// EventPathCreated is a path being made, from a goal or from a material.
	EventPathCreated EventType = "path.created"
	// EventPathMapped is the tree being changed: a module or a knowledge point
	// added, moved, reworded or dropped.
	EventPathMapped EventType = "path.mapped"
	// EventEvidenceRecorded is one observation about one knowledge point.
	EventEvidenceRecorded EventType = "evidence.recorded"
	// EventInteractionOpened is a question being put to the learner.
	EventInteractionOpened EventType = "interaction.opened"
	// EventInteractionAnswered is that question being answered and judged.
	EventInteractionAnswered EventType = "interaction.answered"
	// EventInteractionSkipped is the learner declining it.
	EventInteractionSkipped EventType = "interaction.skipped"
	// EventInteractionExpired is the question closing unanswered after its ttl.
	// It is not a verdict: no evidence is written, and the log says only that
	// the path stopped waiting.
	EventInteractionExpired EventType = "interaction.expired"
	// EventPathSwitched is a turn moving to a different path.
	EventPathSwitched EventType = "path.switched"
)

// Actor is who a write belongs to: the conversation and the turn it happened in.
//
// It is not optional and not inferred. Four of the seven tables require both
// columns, and the reason is that a path is edited over time by many turns —
// without them the event log would say that the tree changed but not who changed
// it, which is most of what the log is for.
type Actor struct {
	SessionID string
	TurnID    string
}

// Validate reports whether the actor names both halves.
func (a Actor) Validate() error {
	if strings.TrimSpace(a.SessionID) == "" {
		return fmt.Errorf("%w: the session is blank", ErrInvalidRequest)
	}
	if strings.TrimSpace(a.TurnID) == "" {
		return fmt.Errorf("%w: the turn is blank", ErrInvalidRequest)
	}
	return nil
}

// Path is one mastery path as the domain reads it: the tree, the version that
// guards it, and whose it is.
type Path struct {
	ID string
	// OwnerSessionID is the conversation the path was created in. It is the path's
	// owner in the schema's sense (a foreign key), not an access-control boundary:
	// other sessions reach the path through mastery_path_sessions.
	OwnerSessionID string
	// OwnerID is the account the path belongs to, or zero for a row written before
	// V5 (migration 00014 left every earlier row NULL). Zero is a state rather than
	// a missing value: such a path belongs to nobody, is visible to the
	// administrator and to nobody else, and is the state a claim would hand out.
	//
	// It is not OwnerSessionID. That column names the conversation the path was
	// started in, and a path outlives its session — the owner is the account that
	// came back for it, which is what the scoping on every read here narrows to.
	OwnerID int64
	Tree    Tree
	// Version is the value the next write has to match. Zero means the row has
	// never been written, which is what a row left behind by an older deployment
	// looks like.
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TopicMeta is what a path is for: the goal the learner stated and how the
// surface should draw it.
//
// It is a table of its own and not a corner of state_json because it is written
// by a different act: the tree changes when the learner works, and this changes
// when they say what they are working towards.
type TopicMeta struct {
	PathID      string
	Goal        string
	Description string
	Emoji       string
	Status      string
}

// StatusActive is the only status this version writes. The column exists in the
// schema and the reference implementation has more values; nothing in MP-01–05
// reads it, so inventing a vocabulary for it would be inventing a requirement.
const StatusActive = "active"

// Evidence is one observation about one knowledge point.
//
// It hangs off the path and names the knowledge point, and the second of those
// has nowhere of its own to live: migration 00004 gave this table an `extra`
// JSON column and no knowledge-point column, so the coordinate travels in the
// JSON. That is a real cost — the gate cannot index on it and reads a path's
// evidence to answer a question about one knowledge point — and it is bounded by
// the size of one path, which is tens to hundreds of rows.
type Evidence struct {
	ID             string
	PathID         string
	KnowledgePoint string
	Result         Result
	// Source says where the observation came from — a graded answer, an
	// assessment — and AssessmentType how it was made. Both are optional in the
	// table and both are reported by the insight endpoint when they are there.
	Source         string
	AssessmentType string
	// Quality is the optional score the reference keeps beside the result. It is
	// stored and never gated on.
	Quality string
	// Detail is the evidence's own JSON, kept verbatim.
	Detail    []byte
	Actor     Actor
	CreatedAt time.Time
}

// The two sources this package writes into that column. They are here rather
// than in the tools because they are part of what the row means: an observation
// that came from a question being answered and one that came from the model
// saying what it saw are told apart by this string, and the insight endpoint
// reports it. A third source — another capability observing something — would be
// a constant beside these.
const (
	// SourceGradedAnswer is evidence from an answer that was judged.
	SourceGradedAnswer = "graded_answer"
	// SourceAssessment is evidence from an assessment the model made itself.
	SourceAssessment = "assessment"
)

// Validate reports whether the evidence is fit to store.
func (e Evidence) Validate() error {
	if strings.TrimSpace(e.ID) == "" {
		return fmt.Errorf("%w: the evidence id is blank", ErrInvalidRequest)
	}
	if strings.TrimSpace(e.PathID) == "" {
		return fmt.Errorf("%w: the path is blank", ErrInvalidRequest)
	}
	if strings.TrimSpace(e.KnowledgePoint) == "" {
		return fmt.Errorf("%w: the knowledge point is blank", ErrInvalidRequest)
	}
	if !e.Result.Valid() {
		return fmt.Errorf("%w: %q is not a result", ErrInvalidRequest, e.Result)
	}
	return e.Actor.Validate()
}

// Event is one line of the path's history.
//
// Payload is the event's own JSON and EventType is what kind of change it
// records; Version is the version the path reached *because* of this event, so a
// reader can say exactly which state a change produced without joining anything.
type Event struct {
	ID        string
	PathID    string
	Type      EventType
	Version   int64
	Payload   []byte
	Actor     Actor
	CreatedAt time.Time
}

// Validate reports whether the event is fit to store.
func (e Event) Validate() error {
	if strings.TrimSpace(e.ID) == "" {
		return fmt.Errorf("%w: the event id is blank", ErrInvalidRequest)
	}
	if strings.TrimSpace(e.PathID) == "" {
		return fmt.Errorf("%w: the path is blank", ErrInvalidRequest)
	}
	if strings.TrimSpace(string(e.Type)) == "" {
		return fmt.Errorf("%w: the event type is blank", ErrInvalidRequest)
	}
	if e.Version < 1 {
		return fmt.Errorf("%w: the event version is %d", ErrInvalidRequest, e.Version)
	}
	return e.Actor.Validate()
}
