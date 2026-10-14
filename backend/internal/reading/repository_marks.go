package reading

import (
	"context"
	"errors"
	"fmt"

	"backend/internal/authz"
	"backend/internal/data/models"
	"gorm.io/gorm"
)

// The stored marks: a bookmark, a highlight or a note, owned by the material
// rather than by the conversation it was written in (migration 00012).
//
// reading_marks carries no owner column (migration 00014 gave it none). A mark's
// lifetime is the material's, so it is narrowed through the material in it: every
// read here says `material_id IN (the caller's materials)`, and a mark on another
// account's material is absent rather than refused.

// Marks lists the marks on one material.
//
// The order is the order they were written, and it is the only order available:
// a mark's place in the document lives in its locator, which is JSON, and a
// database cannot sort by a coordinate inside a document. A client that wants
// them in reading order sorts them itself, where the text it is displaying lives.
func (r *GORMRepository) Marks(ctx context.Context, materialID string, caller authz.Caller) ([]Mark, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	var records []models.ReadingMark
	err := r.db.WithContext(ctx).
		Where("material_id = ?", materialID).
		Where("material_id IN (?)", ownedMaterials(ctx, r.db, caller)).
		Order("creat_time ASC, mark_id ASC").
		Find(&records).Error
	if err != nil {
		return nil, fmt.Errorf("list the marks of material %s: %w", materialID, err)
	}
	marks := make([]Mark, 0, len(records))
	for _, record := range records {
		mark, err := markOf(record)
		if err != nil {
			return nil, err
		}
		marks = append(marks, mark)
	}
	return marks, nil
}

// Mark reads one mark by its id.
//
// A mark hangs off its material, so the id is narrowed by the material being the
// caller's: a mark on somebody else's material is reported as a mark that is not
// there, exactly as an id that was never issued is.
func (r *GORMRepository) Mark(ctx context.Context, markID string, caller authz.Caller) (Mark, error) {
	if err := r.ready(); err != nil {
		return Mark{}, err
	}
	var record models.ReadingMark
	err := r.db.WithContext(ctx).
		Where("mark_id = ?", markID).
		Where("material_id IN (?)", ownedMaterials(ctx, r.db, caller)).
		First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Mark{}, fmt.Errorf("%w: mark %s", ErrNotFound, markID)
	}
	if err != nil {
		return Mark{}, fmt.Errorf("read mark %s: %w", markID, err)
	}
	return markOf(record)
}

// CreateMark writes one mark.
//
// The material is read first, under the caller's scope, for the same reason a
// position's is: the mark's lifetime is the material's, and a stale material id
// from another account must not be a way to hang a mark off a row this caller
// cannot read.
func (r *GORMRepository) CreateMark(ctx context.Context, mark Mark, caller authz.Caller) error {
	if err := r.ready(); err != nil {
		return err
	}
	payload, err := encodeLocator(mark.Locator)
	if err != nil {
		return err
	}
	record := models.ReadingMark{
		MarkID:      mark.MarkID,
		MaterialID:  mark.MaterialID,
		SessionID:   nullable(mark.SessionID),
		Kind:        string(mark.Kind),
		LocatorJSON: payload,
		Body:        nullable(mark.Body),
		Revision:    mark.Revision,
	}
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireMaterial(tx, mark.MaterialID, caller); err != nil {
			return err
		}
		return tx.Create(&record).Error
	})
	return duplicateOr(err, "create mark %s", mark.MarkID)
}

// DeleteMark removes one mark.
//
// The delete is narrowed by the material being the caller's, so a mark id from
// another account matches nothing and is reported as a mark that was not there
// rather than removed.
func (r *GORMRepository) DeleteMark(ctx context.Context, markID string, caller authz.Caller) error {
	if err := r.ready(); err != nil {
		return err
	}
	result := r.db.WithContext(ctx).
		Where("mark_id = ?", markID).
		Where("material_id IN (?)", ownedMaterials(ctx, r.db, caller)).
		Delete(&models.ReadingMark{})
	if result.Error != nil {
		return fmt.Errorf("delete mark %s: %w", markID, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: mark %s", ErrNotFound, markID)
	}
	return nil
}

// markOf reads a stored row as a mark, decoding its coordinate.
func markOf(record models.ReadingMark) (Mark, error) {
	locator, err := decodeLocator(record.LocatorJSON, fmt.Sprintf("mark %s", record.MarkID))
	if err != nil {
		return Mark{}, err
	}
	return Mark{
		MarkID:     record.MarkID,
		MaterialID: record.MaterialID,
		SessionID:  text(record.SessionID),
		Kind:       MarkKind(record.Kind),
		Locator:    locator,
		Body:       text(record.Body),
		Revision:   record.Revision,
		CreatedAt:  record.CreatedAt,
		UpdatedAt:  deref(record.UpdatedAt),
	}, nil
}
