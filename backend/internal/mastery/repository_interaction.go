package mastery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"backend/internal/authz"
	"backend/internal/data/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// mastery_interactions, which is one question-answer transaction per row.
//
// The table has no column for the knowledge point and none for the question id,
// so the coordinate travels in question_json exactly as an evidence's coordinate
// travels in its extra column (§2.2). What it *does* have is a way of saying
// "this is still open": the result column is NULL until something closes the
// row, and migration 00019 turned that fact into the constraint "at most one
// live interaction per path" by putting a unique key over a generated column
// that carries the path while the row is open and NULL once it is closed
// (§2.7).

// interactionExtra is the coordinate an interaction stores: which question was
// asked, and about what.
//
// The question itself is deliberately not in here. A question is addressed by
// its content and lives in the bank (§2.2, QN-01), so a copy of it in this
// column would be a second answer to "what was asked" — and the two would drift
// the first time anything reworded a question.
type interactionExtra struct {
	KnowledgePointID string `json:"knowledge_point_id"`
	QuestionID       string `json:"question_id"`
}

// interactionOutcome lives in interactions.go, beside the two writes that build
// it: what a closed interaction says is the domain's business, and this file
// only sees the bytes.

// ActiveInteraction reads the path's open interaction.
//
// There is at most one, and migration 00019's unique key is what says so now
// rather than the service's read. The order is kept anyway: it costs a clause and
// it is the difference between an answer this code chose and whichever row the
// engine reached first, which is the difference that matters the day the
// constraint stops being the only thing holding the rule.
func (r *GORMRepository) ActiveInteraction(ctx context.Context, pathID string, caller authz.Caller) (Interaction, error) {
	if err := r.ready(); err != nil {
		return Interaction{}, err
	}
	var row struct {
		InteractionID string    `gorm:"column:interaction_id"`
		PathID        string    `gorm:"column:path_id"`
		TurnID        string    `gorm:"column:turn_id"`
		SessionID     string    `gorm:"column:session_id"`
		Question      []byte    `gorm:"column:question_json"`
		CreatedAt     time.Time `gorm:"column:creat_time"`
	}
	err := r.db.WithContext(ctx).Table("mastery_interactions").
		Select("interaction_id, path_id, turn_id, session_id, question_json, creat_time").
		Where("path_id = ? AND result_json IS NULL", pathID).
		Where("path_id IN (?)", pathScope(r.db.WithContext(ctx), caller)).
		Order("creat_time ASC, interaction_id ASC").
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Interaction{}, fmt.Errorf("%w: path %s has no open interaction", ErrNotFound, pathID)
	}
	if err != nil {
		return Interaction{}, fmt.Errorf("read the open interaction of path %s: %w", pathID, err)
	}
	extra, err := decodeInteractionExtra(row.InteractionID, row.Question)
	if err != nil {
		return Interaction{}, err
	}
	return Interaction{
		ID:               row.InteractionID,
		PathID:           row.PathID,
		KnowledgePointID: extra.KnowledgePointID,
		QuestionID:       extra.QuestionID,
		Actor:            Actor{SessionID: row.SessionID, TurnID: row.TurnID},
		CreatedAt:        row.CreatedAt,
	}, nil
}

// CreateInteraction records one question that has been put.
//
// This insert is where "at most one open interaction per path" is held. Migration
// 00019 puts a unique key over a generated column that carries the path while the
// row is open and NULL once it is closed, so of two racing inserts the first
// commits and the second is told the key already exists — 00009's design, on this
// table, and it is here because the service's read cannot hold the rule: the read
// matches no row, and under READ COMMITTED a read that matches nothing locks
// nothing (§2.7).
//
// A collision is therefore not a defect to report as one. It is the refusal Ask
// would have given if it had seen the row, so it is mapped back to the same
// error — same type, same fields — which is what lets a model that lost the race
// carry on with the question that is waiting instead of being handed a 500.
func (r *GORMRepository) CreateInteraction(ctx context.Context, interaction Interaction, caller authz.Caller) error {
	if err := r.ready(); err != nil {
		return err
	}
	// The row is derived from the path and has no owner of its own: the path is
	// read first, so a question cannot be opened on somebody else's path.
	if err := r.ownPath(ctx, interaction.PathID, caller); err != nil {
		return err
	}
	extra, err := json.Marshal(interactionExtra{
		KnowledgePointID: interaction.KnowledgePointID,
		QuestionID:       interaction.QuestionID,
	})
	if err != nil {
		return fmt.Errorf("encode interaction %s: %w", interaction.ID, err)
	}
	record := models.MasteryInteraction{
		InteractionID: interaction.ID,
		PathID:        interaction.PathID,
		TurnID:        interaction.Actor.TurnID,
		SessionID:     interaction.Actor.SessionID,
		Question:      datatypes.JSON(extra),
		CreatedAt:     interaction.CreatedAt,
	}
	err = r.db.WithContext(ctx).Create(&record).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrDuplicatedKey) {
		return fmt.Errorf("record interaction %s: %w", interaction.ID, err)
	}

	// Which key was hit is not in the error, so the answer is read back: the
	// unique key is what a racing ask collides with, but a repeated interaction id
	// collides with the primary key and is a caller's fault rather than a race.
	// The read is this package's own, so the refusal it produces is the one Ask
	// produces in the case it can see for itself.
	//
	// It sees the winner's row because this engine runs at READ COMMITTED: the row
	// that caused the collision is committed by definition — an uncommitted one
	// would have blocked the insert instead of failing it — and a statement under
	// that isolation reads the latest committed data. Under REPEATABLE READ this
	// would answer ErrDuplicate instead, which is the worse of the two errors but
	// still not a wrong row, and the concurrency case is what would notice.
	open, readErr := r.ActiveInteraction(ctx, interaction.PathID, caller)
	if readErr != nil {
		return fmt.Errorf("%w: record interaction %s", ErrDuplicate, interaction.ID)
	}
	return &OpenInteractionError{Interaction: open}
}

// CloseInteraction closes an open interaction.
//
// The `result_json IS NULL` in the WHERE is the whole of the concurrency rule
// here: it is a compare-and-swap on the one thing that distinguishes an open
// interaction from a closed one, so of two calls that both decided to close the
// same row, exactly one writes and the other is told it was not the one. That is
// the same shape TakeoverLease has, and it is worth the same: what the loser
// gets back is a false rather than an error, because the row it wanted is in the
// state it wanted it in.
func (r *GORMRepository) CloseInteraction(
	ctx context.Context,
	interactionID, answer string,
	outcome []byte,
	now time.Time,
	caller authz.Caller,
) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	if len(outcome) == 0 {
		outcome = []byte("{}")
	}
	// The row is derived from a path, so the scope is a subquery over the owned
	// paths rather than a check on a column this table does not have: an
	// interaction on somebody else's path matches nothing, and a false — the same
	// answer a lost race gets — is what the caller sees. The service read the
	// interaction under the caller's scope before it got here, so this is the
	// second line rather than the first.
	written := r.db.WithContext(ctx).Table("mastery_interactions").
		Where("interaction_id = ? AND result_json IS NULL", interactionID).
		Where("path_id IN (?)", pathScope(r.db.WithContext(ctx), caller)).
		Updates(map[string]any{
			"user_answer": nullable(answer),
			"result_json": datatypes.JSON(outcome),
			"update_time": now,
		})
	if written.Error != nil {
		return false, fmt.Errorf("close interaction %s: %w", interactionID, written.Error)
	}
	return written.RowsAffected > 0, nil
}

// decodeInteractionExtra reads the coordinate out of a question_json column.
//
// An empty column is a row written by something other than this package, and it
// is refused rather than treated as an interaction with no question: a grade
// that does not know which knowledge point it is about would record evidence
// against nothing.
func decodeInteractionExtra(interactionID string, raw []byte) (interactionExtra, error) {
	if len(raw) == 0 {
		return interactionExtra{}, fmt.Errorf(
			"%w: interaction %s records no question", ErrInvalidRequest, interactionID)
	}
	var extra interactionExtra
	if err := json.Unmarshal(raw, &extra); err != nil {
		return interactionExtra{}, fmt.Errorf("interaction %s: read its question column: %w", interactionID, err)
	}
	return extra, nil
}
