package reading

import (
	"context"
	"fmt"

	"backend/internal/authz"
	"backend/internal/data/models"
	"gorm.io/gorm"
)

// The storage behind the bilingual pairing: one row per confirmation, read from
// either end and written once (§2.10).
//
// The candidates are not here because they are not stored. A recommendation is
// computed from the catalogue on every request, and the only thing this file
// knows about is the row a reader confirmed.
//
// A pairing carries no owner column (migration 00014 gave it none), and it is
// narrowed through *both* of its materials rather than one: a pairing names two
// materials, each of which has an owner, and a row whose far end is another
// account's would be a way to learn that a material exists. Requiring both ends
// to be the caller's is the derived-table rule applied to a table with two
// parents, and it is why every statement here filters on two subqueries.

// PairingOf reads the pairing one material is in.
//
// Either end matches, because a pairing is a relationship and has no direction: a
// caller holding one material should not have to know which side of the pair it
// committed it as. The order is spelled out rather than left to the engine, and
// it settles two rows the same way on every read — the service refuses a second
// pairing for one material, and a row written by hand outside it should still be
// read the same twice.
func (r *GORMRepository) PairingOf(ctx context.Context, materialID string, caller authz.Caller) (Pairing, error) {
	if err := r.ready(); err != nil {
		return Pairing{}, err
	}
	owned := ownedMaterials(ctx, r.db, caller)
	var model models.ReadingPairing
	err := r.db.WithContext(ctx).
		Model(&models.ReadingPairing{}).
		Where("(primary_material_id = ? OR counterpart_material_id = ?)", materialID, materialID).
		Where("primary_material_id IN (?) AND counterpart_material_id IN (?)", owned, owned).
		Order("creat_time ASC, pairing_id ASC").
		Limit(1).
		Find(&model).Error
	if err != nil {
		return Pairing{}, fmt.Errorf("read the pairing of material %s: %w", materialID, err)
	}
	if model.PairingID == "" {
		return Pairing{}, fmt.Errorf("%w: material %s is in no pairing", ErrNotFound, materialID)
	}
	return pairingOf(model), nil
}

// PairingBetween reads the pairing of two materials, whichever way round it was
// confirmed.
func (r *GORMRepository) PairingBetween(ctx context.Context, first, second string, caller authz.Caller) (Pairing, error) {
	if err := r.ready(); err != nil {
		return Pairing{}, err
	}
	owned := ownedMaterials(ctx, r.db, caller)
	var model models.ReadingPairing
	err := r.db.WithContext(ctx).
		Model(&models.ReadingPairing{}).
		Where("(primary_material_id = ? AND counterpart_material_id = ?) OR "+
			"(primary_material_id = ? AND counterpart_material_id = ?)", first, second, second, first).
		Where("primary_material_id IN (?) AND counterpart_material_id IN (?)", owned, owned).
		Order("creat_time ASC, pairing_id ASC").
		Limit(1).
		Find(&model).Error
	if err != nil {
		return Pairing{}, fmt.Errorf("read the pairing of materials %s and %s: %w", first, second, err)
	}
	if model.PairingID == "" {
		return Pairing{}, fmt.Errorf("%w: materials %s and %s are not paired", ErrNotFound, first, second)
	}
	return pairingOf(model), nil
}

// CreatePairing writes one confirmation.
//
// Both materials are read first, under the caller's scope, so that a confirmation
// cannot name a material this caller cannot read: a pairing puts two rows in one
// statement, and the row's existence would otherwise be a way to point at another
// account's material.
//
// A collision is either the primary key or the unique index on the pair, and both
// mean the same thing to the caller: this confirmation is already there. The two
// are not told apart because the answer to both is to read the row that won.
func (r *GORMRepository) CreatePairing(ctx context.Context, pairing Pairing, caller authz.Caller) error {
	if err := r.ready(); err != nil {
		return err
	}
	record := models.ReadingPairing{
		PairingID:             pairing.PairingID,
		PrimaryMaterialID:     pairing.PrimaryMaterialID,
		CounterpartMaterialID: pairing.CounterpartMaterialID,
		PrimaryLanguage:       pairing.PrimaryLanguage,
		CounterpartLanguage:   pairing.CounterpartLanguage,
	}
	if !pairing.CreatedAt.IsZero() {
		record.CreatedAt = pairing.CreatedAt
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireMaterial(tx, pairing.PrimaryMaterialID, caller); err != nil {
			return err
		}
		if err := requireMaterial(tx, pairing.CounterpartMaterialID, caller); err != nil {
			return err
		}
		return tx.Create(&record).Error
	})
	return duplicateOr(err, "create the pairing of materials %s and %s",
		pairing.PrimaryMaterialID, pairing.CounterpartMaterialID)
}

// DeletePairing removes one confirmation, ErrNotFound when there was none.
//
// Nothing else is deleted with it, because nothing else was made: a pairing
// produces no derived text to clean up (§2.10). The delete is narrowed on both
// materials, so a pairing id from another account matches nothing and reports the
// state it already has.
func (r *GORMRepository) DeletePairing(ctx context.Context, pairingID string, caller authz.Caller) error {
	if err := r.ready(); err != nil {
		return err
	}
	owned := ownedMaterials(ctx, r.db, caller)
	result := r.db.WithContext(ctx).
		Where("pairing_id = ?", pairingID).
		Where("primary_material_id IN (?) AND counterpart_material_id IN (?)", owned, owned).
		Delete(&models.ReadingPairing{})
	if result.Error != nil {
		return fmt.Errorf("delete pairing %s: %w", pairingID, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: pairing %s", ErrNotFound, pairingID)
	}
	return nil
}

func pairingOf(model models.ReadingPairing) Pairing {
	return Pairing{
		PairingID:             model.PairingID,
		PrimaryMaterialID:     model.PrimaryMaterialID,
		CounterpartMaterialID: model.CounterpartMaterialID,
		PrimaryLanguage:       model.PrimaryLanguage,
		CounterpartLanguage:   model.CounterpartLanguage,
		CreatedAt:             model.CreatedAt,
	}
}
