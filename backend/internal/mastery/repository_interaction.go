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
// "this is still open", and it is the only place the design needs one: the
// result column is NULL until something closes the row, which is what "at most
// one live interaction per path" is enforced against (§2.5).

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
// A path can only have one at a time — the service refuses to open a second —
// but the table has no constraint that says so, so this read names an order
// rather than assuming there is one row: the oldest open interaction is the one
// that is waiting, and a case that found two would get the same answer every
// time rather than whichever row the engine happened to reach first.
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
	return duplicateOr(r.db.WithContext(ctx).Create(&record).Error, "record interaction %s", interaction.ID)
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
