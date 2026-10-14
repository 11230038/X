package reading

import (
	"context"
	"fmt"
	"time"

	"backend/internal/data/models"
)

// The storage behind the bilingual pairing: one row per confirmation, read from
// either end and written once (§2.10).
//
// The candidates are not here because they are not stored. A recommendation is
// computed from the catalogue on every request, and the only thing this file
// knows about is the row a reader confirmed.

// pairingColumns is the whole row, named one column at a time so that a column
// added to the table shows up as a compile-time mismatch here rather than as a
// silently absent field.
const pairingColumns = "pairing_id, primary_material_id, counterpart_material_id, " +
	"primary_language, counterpart_language, creat_time"

// pairingOfSQL reads the confirmation one material is in.
//
// Either end matches, because a pairing is a relationship and has no direction: a
// caller holding one material should not have to know which side of the pair it
// committed it as. The order is spelled out rather than left to the engine, and
// it settles two rows the same way on every read — the service refuses a second
// pairing for one material, and a row written by hand outside it should still be
// read the same twice.
const pairingOfSQL = "SELECT " + pairingColumns + " FROM reading_pairings " +
	"WHERE primary_material_id = ? OR counterpart_material_id = ? " +
	"ORDER BY creat_time ASC, pairing_id ASC LIMIT 1"

// pairingBetweenSQL reads the confirmation of one pair, in either direction.
const pairingBetweenSQL = "SELECT " + pairingColumns + " FROM reading_pairings " +
	"WHERE (primary_material_id = ? AND counterpart_material_id = ?) " +
	"OR (primary_material_id = ? AND counterpart_material_id = ?) " +
	"ORDER BY creat_time ASC, pairing_id ASC LIMIT 1"

// pairingRow is one pairing as it is scanned. Every column is NOT NULL, so there
// is nothing to null-check: an empty id is the absence of a row and nothing else.
type pairingRow struct {
	PairingID             string    `gorm:"column:pairing_id"`
	PrimaryMaterialID     string    `gorm:"column:primary_material_id"`
	CounterpartMaterialID string    `gorm:"column:counterpart_material_id"`
	PrimaryLanguage       string    `gorm:"column:primary_language"`
	CounterpartLanguage   string    `gorm:"column:counterpart_language"`
	CreatedAt             time.Time `gorm:"column:creat_time"`
}

func (row pairingRow) pairing() Pairing {
	return Pairing{
		PairingID:             row.PairingID,
		PrimaryMaterialID:     row.PrimaryMaterialID,
		CounterpartMaterialID: row.CounterpartMaterialID,
		PrimaryLanguage:       row.PrimaryLanguage,
		CounterpartLanguage:   row.CounterpartLanguage,
		CreatedAt:             row.CreatedAt,
	}
}

// PairingOf reads the pairing one material is in.
func (r *GORMRepository) PairingOf(ctx context.Context, materialID string) (Pairing, error) {
	if err := r.ready(); err != nil {
		return Pairing{}, err
	}
	var row pairingRow
	err := r.db.WithContext(ctx).Raw(pairingOfSQL, materialID, materialID).Scan(&row).Error
	if err != nil {
		return Pairing{}, fmt.Errorf("read the pairing of material %s: %w", materialID, err)
	}
	if row.PairingID == "" {
		return Pairing{}, fmt.Errorf("%w: material %s is in no pairing", ErrNotFound, materialID)
	}
	return row.pairing(), nil
}

// PairingBetween reads the pairing of two materials, whichever way round it was
// confirmed.
func (r *GORMRepository) PairingBetween(ctx context.Context, first, second string) (Pairing, error) {
	if err := r.ready(); err != nil {
		return Pairing{}, err
	}
	var row pairingRow
	err := r.db.WithContext(ctx).Raw(pairingBetweenSQL, first, second, second, first).Scan(&row).Error
	if err != nil {
		return Pairing{}, fmt.Errorf("read the pairing of materials %s and %s: %w", first, second, err)
	}
	if row.PairingID == "" {
		return Pairing{}, fmt.Errorf("%w: materials %s and %s are not paired", ErrNotFound, first, second)
	}
	return row.pairing(), nil
}

// CreatePairing writes one confirmation.
//
// A collision is either the primary key or the unique index on the pair, and both
// mean the same thing to the caller: this confirmation is already there. The two
// are not told apart because the answer to both is to read the row that won.
func (r *GORMRepository) CreatePairing(ctx context.Context, pairing Pairing) error {
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
	err := r.db.WithContext(ctx).Create(&record).Error
	return duplicateOr(err, "create the pairing of materials %s and %s",
		pairing.PrimaryMaterialID, pairing.CounterpartMaterialID)
}

// DeletePairing removes one confirmation, ErrNotFound when there was none.
//
// Nothing else is deleted with it, because nothing else was made: a pairing
// produces no derived text to clean up (§2.10).
func (r *GORMRepository) DeletePairing(ctx context.Context, pairingID string) error {
	if err := r.ready(); err != nil {
		return err
	}
	result := r.db.WithContext(ctx).
		Where("pairing_id = ?", pairingID).
		Delete(&models.ReadingPairing{})
	if result.Error != nil {
		return fmt.Errorf("delete pairing %s: %w", pairingID, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: pairing %s", ErrNotFound, pairingID)
	}
	return nil
}
