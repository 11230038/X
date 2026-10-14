package mastery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"backend/internal/authz"
	"backend/internal/data"
	"backend/internal/data/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Repository is the storage the mastery domain needs.
//
// Every method is one statement, and the two that are not — the reads that
// return a tree, the reads that return a path's evidence — are one statement
// each because the whole of what they return lives in one row or in one table.
// The decisions live in the service: what an edit means, what level the evidence
// implies, what happens when a lease has expired. A repository that decided any
// of that would be a repository that could not be replaced by a fake for a test
// about the rules.
//
// The transaction is the service's, which is why the only way in is Within: the
// design's one hard requirement about this domain's storage is that state and
// history land in the same transaction (§2.1), and a repository that could be
// used outside a transaction would be a repository that could write a state with
// no event explaining it.
//
// Every method that reads or writes a path takes a caller and narrows itself to
// that account's rows. Ownership is a filter and not a check: a path belonging to
// somebody else is not refused, it is absent, so a request for it is answered
// exactly as a request for an id that was never issued (§2.4). Only
// mastery_paths carries an owner — migration 00014 gave the six tables that hang
// off a path no column of their own, because a child's owner is its parent's —
// so those reads narrow by a subquery over the paths that are owned, and those
// writes read the parent first (pathScope and ownPath, below).
type Repository interface {
	// Within runs work against one transaction. The repository it hands over is
	// bound to that transaction and is the only one the work may use: a write made
	// through the outer repository while a transaction is open would be committed
	// on its own and would survive the rollback that was supposed to take it back.
	Within(ctx context.Context, work func(Repository) error) error

	// Path reads one path, tree and version together. ErrNotFound when there is
	// no such path.
	Path(ctx context.Context, pathID string, caller authz.Caller) (Path, error)
	// Paths reads a listing: every path, most recently worked on first, or only
	// the paths one conversation has touched when sessionID is set. Each row
	// carries its tree, its metadata and when it was last worked on, because a
	// listing that read those per row would cost more the more there was to
	// list.
	Paths(ctx context.Context, sessionID string, caller authz.Caller) ([]PathRow, error)
	// Meta reads a path's topic metadata. ErrNotFound when the path has none,
	// which a path created before this version may not, and when the path belongs
	// to somebody else — the two are one answer.
	Meta(ctx context.Context, pathID string, caller authz.Caller) (TopicMeta, error)
	// Evidences reads a path's evidence, oldest first. Every one of them: the gate
	// counts runs, and a run at the start of a path's history counts as much as
	// one at the end.
	Evidences(ctx context.Context, pathID string, caller authz.Caller) ([]Evidence, error)
	// Events reads a path's history, oldest first.
	Events(ctx context.Context, pathID string, caller authz.Caller) ([]Event, error)

	// CreatePath inserts the path row at version 1, owner and all. ErrDuplicate
	// when the id is taken.
	CreatePath(ctx context.Context, path Path) error
	// SaveTree writes a tree over the version the caller read from, and reports
	// the version the write reached. ErrStaleState when the row has moved on.
	SaveTree(ctx context.Context, pathID string, expected int64, tree Tree, now time.Time, caller authz.Caller) (int64, error)
	// SaveMeta writes a path's topic metadata, replacing it if it is there.
	SaveMeta(ctx context.Context, meta TopicMeta, now time.Time, caller authz.Caller) error
	// TouchSession records that a conversation has touched a path, and moves the
	// time it was last seen.
	TouchSession(ctx context.Context, pathID string, actor Actor, now time.Time, caller authz.Caller) error

	// PathOfSession reads the path a conversation was last seen on. ErrNotFound
	// when it has never been on one, which is the ordinary state of a new
	// conversation rather than a failure.
	PathOfSession(ctx context.Context, sessionID string, caller authz.Caller) (string, error)
	// SessionsOn reads the conversations linked to a path, most recently seen
	// first.
	SessionsOn(ctx context.Context, pathID string, caller authz.Caller) ([]string, error)
	// SeenAt reads when a conversation last touched a path. ErrNotFound when the
	// two were never linked.
	SeenAt(ctx context.Context, pathID, sessionID string, caller authz.Caller) (time.Time, error)

	// ActiveInteraction reads the path's open interaction — the one question put
	// and not yet answered. ErrNotFound when there is none.
	ActiveInteraction(ctx context.Context, pathID string, caller authz.Caller) (Interaction, error)
	// CreateInteraction records one question that has been put. ErrDuplicate when
	// the id is taken.
	CreateInteraction(ctx context.Context, interaction Interaction, caller authz.Caller) error
	// CloseInteraction closes an open interaction, storing the answer and how it
	// ended, and reports whether this call is the one that closed it. A false is
	// not an error: two calls grading the same answer is one of them losing a
	// race it was allowed to lose.
	//
	// The outcome is bytes rather than a value because the two ways an
	// interaction ends — judged, and declined — carry different things, and the
	// column holds one JSON document either way.
	CloseInteraction(ctx context.Context, interactionID, answer string, outcome []byte, now time.Time, caller authz.Caller) (bool, error)

	// AppendEvidence writes one observation. ErrDuplicate when the id is taken.
	AppendEvidence(ctx context.Context, evidence Evidence, caller authz.Caller) error
	// AppendEvent writes one line of history. ErrDuplicate when the id is taken.
	AppendEvent(ctx context.Context, event Event, caller authz.Caller) error

	// Lease reads who is writing a path, expired leases included, and
	// ErrNotFound when nobody ever has.
	Lease(ctx context.Context, pathID string, caller authz.Caller) (Lease, error)
	// ClaimLease takes a path nobody holds. ErrDuplicate when another turn
	// claimed it in the same instant.
	ClaimLease(ctx context.Context, lease Lease, caller authz.Caller) error
	// TakeoverLease replaces an expired lease whose holder was fromTurnID, and
	// reports whether it was still there to replace. A false is not an error: it
	// is this call having lost a race it was allowed to lose.
	TakeoverLease(ctx context.Context, pathID, fromTurnID string, lease Lease, caller authz.Caller) (bool, error)
	// ReleaseLease drops a lease still held by turnID, and reports whether this
	// call is the one that removed it.
	ReleaseLease(ctx context.Context, pathID, turnID string, caller authz.Caller) (bool, error)
	// FencingToken reads a turn's fence, or 0 for one that has never had a fence.
	//
	// It takes no caller, and neither does SetFencingToken: the fence is stored on
	// a turn, and a turn belongs to a conversation rather than to an account, so
	// there is no owner column here to narrow to. What keeps another account's
	// lease out of reach is upstream of this pair — the lease was only taken after
	// the path was read under the caller's scope (Service.requirePath).
	FencingToken(ctx context.Context, turnID string) (int64, error)
	// SetFencingToken writes a turn's fence. ErrNotFound when there is no such
	// turn to carry it.
	SetFencingToken(ctx context.Context, turnID string, token int64) error
}

// GORMRepository reads and writes the mastery tables through GORM (seekdb over
// the MySQL protocol).
type GORMRepository struct {
	db *gorm.DB
}

// NewGORMRepository creates a storage adapter over an existing GORM handle.
func NewGORMRepository(db *gorm.DB) *GORMRepository {
	return &GORMRepository{db: db}
}

var _ Repository = (*GORMRepository)(nil)

// Within runs work in one transaction.
func (r *GORMRepository) Within(ctx context.Context, work func(Repository) error) error {
	if err := r.ready(); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return work(&GORMRepository{db: tx})
	})
}

// AppendEvidence writes one observation.
//
// The row has no owner of its own, so what keeps one account's evidence off
// another's path is the read of the parent: ownPath answers "is this a path the
// caller may write to" before the insert is reached, and a foreign path is
// ErrNotFound rather than a row written against somebody else's work.
func (r *GORMRepository) AppendEvidence(ctx context.Context, evidence Evidence, caller authz.Caller) error {
	if err := r.ready(); err != nil {
		return err
	}
	if err := r.ownPath(ctx, evidence.PathID, caller); err != nil {
		return err
	}
	extra, err := json.Marshal(evidenceExtra{
		KnowledgePointID: evidence.KnowledgePoint,
		Detail:           evidence.Detail,
	})
	if err != nil {
		return fmt.Errorf("encode evidence %s: %w", evidence.ID, err)
	}
	record := models.MasteryLearningEvidence{
		MLEID:          evidence.ID,
		SessionID:      evidence.Actor.SessionID,
		TurnID:         evidence.Actor.TurnID,
		PathID:         evidence.PathID,
		Extra:          datatypes.JSON(extra),
		Result:         nullable(string(evidence.Result)),
		CreatedAt:      evidence.CreatedAt,
		Source:         nullable(evidence.Source),
		Quality:        nullable(evidence.Quality),
		AssessmentType: nullable(evidence.AssessmentType),
	}
	return duplicateOr(r.db.WithContext(ctx).Create(&record).Error, "record evidence %s", evidence.ID)
}

// Evidences reads a path's evidence, oldest first.
//
// The order is by creation time and then by id, and the second half is not
// decoration: creat_time is a DATETIME, which holds whole seconds, so evidence
// recorded inside the same second — two knowledge points assessed by one turn,
// which is the ordinary case — would otherwise come back in whatever order the
// engine chose. The gate counts runs, and a run is a sequence.
func (r *GORMRepository) Evidences(ctx context.Context, pathID string, caller authz.Caller) ([]Evidence, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	var rows []struct {
		ID             string    `gorm:"column:mle_id"`
		Extra          []byte    `gorm:"column:extra"`
		Result         *string   `gorm:"column:result"`
		CreatedAt      time.Time `gorm:"column:creat_time"`
		Source         *string   `gorm:"column:source"`
		Quality        *string   `gorm:"column:quality"`
		AssessmentType *string   `gorm:"column:assessment_type"`
	}
	err := r.db.WithContext(ctx).Table("mastery_learning_evidences").
		Select("mle_id, extra, result, creat_time, source, quality, assessment_type").
		Where("path_id = ?", pathID).
		Where("path_id IN (?)", pathScope(r.db.WithContext(ctx), caller)).
		Order("creat_time ASC, mle_id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read the evidence of path %s: %w", pathID, err)
	}

	evidences := make([]Evidence, 0, len(rows))
	for _, row := range rows {
		var extra evidenceExtra
		if len(row.Extra) > 0 {
			if err := json.Unmarshal(row.Extra, &extra); err != nil {
				return nil, fmt.Errorf("evidence %s: read its extra column: %w", row.ID, err)
			}
		}
		evidences = append(evidences, Evidence{
			ID:             row.ID,
			PathID:         pathID,
			KnowledgePoint: extra.KnowledgePointID,
			Result:         Result(text(row.Result)),
			Source:         text(row.Source),
			AssessmentType: text(row.AssessmentType),
			Quality:        text(row.Quality),
			Detail:         extra.Detail,
			CreatedAt:      row.CreatedAt,
		})
	}
	return evidences, nil
}

// AppendEvent writes one line of a path's history.
func (r *GORMRepository) AppendEvent(ctx context.Context, event Event, caller authz.Caller) error {
	if err := r.ready(); err != nil {
		return err
	}
	if err := r.ownPath(ctx, event.PathID, caller); err != nil {
		return err
	}
	payload := event.Payload
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	version := int32(event.Version)
	eventType := string(event.Type)
	record := models.MasteryEvent{
		MasteryEventsID: event.ID,
		PathID:          event.PathID,
		TurnID:          event.Actor.TurnID,
		SessionID:       event.Actor.SessionID,
		Version:         &version,
		Payload:         datatypes.JSON(payload),
		EventType:       &eventType,
		CreatedAt:       event.CreatedAt,
	}
	return duplicateOr(r.db.WithContext(ctx).Create(&record).Error, "record event %s", event.ID)
}

// Events reads a path's history, oldest first, ordered the same way Evidences is
// and for the same reason.
func (r *GORMRepository) Events(ctx context.Context, pathID string, caller authz.Caller) ([]Event, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	var rows []struct {
		ID        string    `gorm:"column:mastery_events_id"`
		Version   *int32    `gorm:"column:version"`
		Payload   []byte    `gorm:"column:payload_json"`
		EventType *string   `gorm:"column:event_type"`
		SessionID string    `gorm:"column:session_id"`
		TurnID    string    `gorm:"column:turn_id"`
		CreatedAt time.Time `gorm:"column:creat_time"`
	}
	err := r.db.WithContext(ctx).Table("mastery_events").
		Select("mastery_events_id, version, payload_json, event_type, session_id, turn_id, creat_time").
		Where("path_id = ?", pathID).
		Where("path_id IN (?)", pathScope(r.db.WithContext(ctx), caller)).
		Order("creat_time ASC, mastery_events_id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read the history of path %s: %w", pathID, err)
	}

	events := make([]Event, 0, len(rows))
	for _, row := range rows {
		event := Event{
			ID:        row.ID,
			PathID:    pathID,
			Type:      EventType(text(row.EventType)),
			Payload:   row.Payload,
			Actor:     Actor{SessionID: row.SessionID, TurnID: row.TurnID},
			CreatedAt: row.CreatedAt,
		}
		if row.Version != nil {
			event.Version = int64(*row.Version)
		}
		events = append(events, event)
	}
	return events, nil
}

func (r *GORMRepository) ready() error {
	if r == nil || r.db == nil {
		return fmt.Errorf("mastery repository is not initialized")
	}
	return nil
}

// pathScope is the set of path ids the caller may see, as a subquery.
//
// The six tables that hang off a path — evidence, events, interactions, leases,
// the session link, the topic metadata — carry no owner_id (migration 00014 gave
// the column to mastery_paths and to nothing that derives from it), because a
// child's owner is its parent's. This is that one rule, in one place: each of
// those reads says `path_id IN (?)` and hands over what this returns, rather than
// repeating the ownership predicate or, worse, leaving it out. It is
// data.OwnerScope and not a hand-written `owner_id = ?`, so the administrator's
// view — owned rows plus the NULL-owner rows of before V5 — is defined once, in
// the package that owns the rule.
func pathScope(db *gorm.DB, caller authz.Caller) *gorm.DB {
	return data.OwnerScope(
		db.Model(&models.MasteryPath{}).Select("mastery_path_id"),
		caller.UserID, caller.IsAdmin(), "owner_id",
	)
}

// ownPath reports whether pathID is a path the caller may write to.
//
// Every write to a table that hangs off a path asks this before it writes, and
// the reason it is a read of the parent rather than a predicate on the write is
// that the child table has no owner column to predicate on. Without it, a stale
// path id from another account — or from one an account used to hold — would let
// a turn write a lease or an event against somebody else's work, which is the
// failure §2.4 rules out but which no column and no constraint here could catch.
// ErrNotFound and not a permission error: the path is not the caller's, so as far
// as the caller is concerned it is not there.
func (r *GORMRepository) ownPath(ctx context.Context, pathID string, caller authz.Caller) error {
	var stored struct {
		PathID string `gorm:"column:mastery_path_id"`
	}
	query := data.OwnerScope(r.db.WithContext(ctx).Table("mastery_paths"),
		caller.UserID, caller.IsAdmin(), "owner_id")
	err := query.Select("mastery_path_id").Where("mastery_path_id = ?", pathID).Take(&stored).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("%w: path %s", ErrNotFound, pathID)
	}
	if err != nil {
		return fmt.Errorf("look for path %s: %w", pathID, err)
	}
	return nil
}

// ownerOf reads the nullable owner column as the zero this domain uses for "no
// account". A NULL and a zero are the same absence said two ways — the column is
// NULL for a row written before V5 (migration 00014) and the struct field is zero
// because Go has no nullable int — so this is where the two meet.
func ownerOf(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

// duplicateOr turns a collision on a primary key into ErrDuplicate, and says
// which statement collided — which matters because the two callers mean
// different things by it.
func duplicateOr(err error, what string, args ...any) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return fmt.Errorf("%w: %s", ErrDuplicate, fmt.Sprintf(what, args...))
	}
	return fmt.Errorf("%s: %w", fmt.Sprintf(what, args...), err)
}

func nullable(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func text(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// deref reads a nullable time as the zero time, which is how a column a LEFT
// JOIN found nothing for comes back: "there is no such row", said in the one
// value of this type that means nothing rather than 1970.
func deref(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}
