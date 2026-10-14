package reading

import (
	"context"
	"errors"
	"fmt"
	"time"

	"backend/internal/authz"
	"backend/internal/data/models"
	"gorm.io/gorm"
)

// The stored position: where one reader stopped in one material, keyed by the
// material and the conversation (§2.11).
//
// reading_positions carries no owner column (migration 00014 gave it none). A
// position's lifetime is the material's, so it is narrowed through the material
// in it: every read here says `material_id IN (the caller's materials)`, and a
// position whose material belongs to another account is absent rather than
// refused.

// Position reads where a reader stopped.
//
// The ownership filter is on the material rather than on the position, because a
// position has no owner of its own: it is a child of the material, and a child's
// owner is its parent's (migration 00014 gave it no column).
func (r *GORMRepository) Position(ctx context.Context, materialID, sessionID string, caller authz.Caller) (Position, error) {
	if err := r.ready(); err != nil {
		return Position{}, err
	}
	var record models.ReadingPosition
	err := r.db.WithContext(ctx).
		Where("material_id = ? AND session_id = ?", materialID, sessionID).
		Where("material_id IN (?)", ownedMaterials(ctx, r.db, caller)).
		First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Position{}, fmt.Errorf("%w: no position for material %s in conversation %s", ErrNotFound, materialID, sessionID)
	}
	if err != nil {
		return Position{}, fmt.Errorf("read the position of material %s: %w", materialID, err)
	}
	return positionOf(record)
}

// CreatePosition writes the first position for a material and a conversation.
//
// It reads the material first, under the caller's scope: a position's lifetime is
// the material's, and a material id from another account must not be a way to
// attach a position to a row this caller cannot read.
func (r *GORMRepository) CreatePosition(ctx context.Context, position Position, caller authz.Caller) error {
	if err := r.ready(); err != nil {
		return err
	}
	payload, err := encodeLocator(position.Locator)
	if err != nil {
		return err
	}
	record := models.ReadingPosition{
		MaterialID:  position.MaterialID,
		SessionID:   position.SessionID,
		LocatorJSON: payload,
		Revision:    position.Revision,
	}
	if !position.UpdatedAt.IsZero() {
		updated := position.UpdatedAt
		record.UpdatedAt = &updated
	}
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireMaterial(tx, position.MaterialID, caller); err != nil {
			return err
		}
		return tx.Create(&record).Error
	})
	return duplicateOr(err, "create the position of material %s in conversation %s", position.MaterialID, position.SessionID)
}

// SavePosition moves a position from the revision it was read at to the next one.
//
// `revision = revision + 1` and not a value, for the same reason the review state
// is advanced that way: the revision that lands is the one the row had plus one,
// which is what makes the guard a comparison rather than a write, and which stays
// correct when two writers reach the row in the same instant and only the first
// may count.
//
// The scope is on the material and not on the position row, so a stale id from
// another account matches nothing: what that caller sees is the same false they
// get for a material of their own with no such revision.
func (r *GORMRepository) SavePosition(
	ctx context.Context,
	materialID, sessionID string,
	expected int64,
	locator Locator,
	now time.Time,
	caller authz.Caller,
) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	payload, err := encodeLocator(locator)
	if err != nil {
		return false, err
	}
	result := r.db.WithContext(ctx).Table(models.ReadingPosition{}.TableName()).
		Where("material_id = ? AND session_id = ? AND revision = ?", materialID, sessionID, expected).
		Where("material_id IN (?)", ownedMaterials(ctx, r.db, caller)).
		Updates(map[string]any{
			"locator_json": payload,
			"revision":     gorm.Expr("revision + 1"),
			"update_time":  now,
		})
	if result.Error != nil {
		return false, fmt.Errorf("save the position of material %s: %w", materialID, result.Error)
	}
	return result.RowsAffected == 1, nil
}

// positionOf reads a stored row as a position, decoding its coordinate.
func positionOf(record models.ReadingPosition) (Position, error) {
	locator, err := decodeLocator(record.LocatorJSON,
		fmt.Sprintf("the position of material %s in conversation %s", record.MaterialID, record.SessionID))
	if err != nil {
		return Position{}, err
	}
	return Position{
		MaterialID: record.MaterialID,
		SessionID:  record.SessionID,
		Locator:    locator,
		Revision:   record.Revision,
		CreatedAt:  record.CreatedAt,
		UpdatedAt:  deref(record.UpdatedAt),
	}, nil
}
