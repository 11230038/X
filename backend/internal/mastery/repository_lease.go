package mastery

import (
	"context"
	"errors"
	"fmt"

	"backend/internal/data/models"
	"gorm.io/gorm"
)

// The lease's storage, and the fence's.
//
// It is a file of its own rather than a section of repository.go because it is
// the one part of this domain's storage that is about *timing* rather than about
// content: every other table here holds what a path is, and these two hold who
// is allowed to say so at this moment. The interface it extends is declared with
// the rest of them.

// Lease reads the lease on a path, in whatever state it is in. ErrNotFound when
// nobody has ever held it.
//
// An expired lease is returned rather than hidden. Expiry is a comparison
// against a clock, and this layer does not have one — a repository that decided
// a lease was expired would make every case about leases a case about the day it
// ran on.
func (r *GORMRepository) Lease(ctx context.Context, pathID string) (Lease, error) {
	if err := r.ready(); err != nil {
		return Lease{}, err
	}
	var record models.MasteryPathLease
	err := r.db.WithContext(ctx).First(&record, "path_id = ?", pathID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Lease{}, fmt.Errorf("%w: no turn holds path %s", ErrNotFound, pathID)
	}
	if err != nil {
		return Lease{}, fmt.Errorf("read the lease on path %s: %w", pathID, err)
	}
	return Lease{
		PathID:     record.PathID,
		SessionID:  record.SessionID,
		TurnID:     record.TurnID,
		AcquiredAt: record.CreatedAt,
	}, nil
}

// ClaimLease takes a path nobody holds. ErrDuplicate when the insert lost a race
// to another turn that claimed it in the same instant — which is the answer to
// "did I get it", given by the only thing that can give it without a race.
func (r *GORMRepository) ClaimLease(ctx context.Context, lease Lease) error {
	if err := r.ready(); err != nil {
		return err
	}
	record := models.MasteryPathLease{
		PathID:    lease.PathID,
		SessionID: lease.SessionID,
		TurnID:    lease.TurnID,
		CreatedAt: lease.AcquiredAt,
	}
	err := r.db.WithContext(ctx).Create(&record).Error
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return fmt.Errorf("%w: path %s was claimed first", ErrDuplicate, lease.PathID)
	}
	return fmt.Errorf("claim path %s for turn %s: %w", lease.PathID, lease.TurnID, err)
}

// TakeoverLease replaces a lease held by fromTurnID with this one, and reports
// whether it was still there to replace.
//
// The condition is the whole method. `turn_id = ?` names the holder that the
// caller read, so of two turns that both decided an expired lease was theirs
// for the taking, exactly one gets a row; the other's WHERE matches nothing and
// it learns that from the count rather than from a lock.
func (r *GORMRepository) TakeoverLease(ctx context.Context, pathID, fromTurnID string, lease Lease) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	result := r.db.WithContext(ctx).Table("mastery_path_leases").
		Where("path_id = ? AND turn_id = ?", pathID, fromTurnID).
		Updates(map[string]any{
			"session_id": lease.SessionID,
			"turn_id":    lease.TurnID,
			"creat_time": lease.AcquiredAt,
		})
	if result.Error != nil {
		return false, fmt.Errorf("take over path %s from turn %s: %w", pathID, fromTurnID, result.Error)
	}
	return result.RowsAffected == 1, nil
}

// ReleaseLease drops a lease if this turn still holds it, and reports whether
// this call is the one that removed it.
func (r *GORMRepository) ReleaseLease(ctx context.Context, pathID, turnID string) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	result := r.db.WithContext(ctx).Table("mastery_path_leases").
		Where("path_id = ? AND turn_id = ?", pathID, turnID).
		Delete(nil)
	if result.Error != nil {
		return false, fmt.Errorf("release path %s from turn %s: %w", pathID, turnID, result.Error)
	}
	return result.RowsAffected == 1, nil
}

// FencingToken reads a turn's fence, and reports 0 for a turn that has never had
// one. Zero rather than an error because the column is nullable and its NULL
// means exactly that, so the two are one value and the caller that wants a
// starting point does not have to tell them apart.
func (r *GORMRepository) FencingToken(ctx context.Context, turnID string) (int64, error) {
	if err := r.ready(); err != nil {
		return 0, err
	}
	var row struct {
		Token *int64 `gorm:"column:fencing_token"`
	}
	err := r.db.WithContext(ctx).Table("turns").
		Select("fencing_token").
		Where("turn_id = ?", turnID).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, fmt.Errorf("%w: turn %s", ErrNotFound, turnID)
	}
	if err != nil {
		return 0, fmt.Errorf("read the fence of turn %s: %w", turnID, err)
	}
	if row.Token == nil {
		return 0, nil
	}
	return *row.Token, nil
}

// SetFencingToken writes a turn's fence.
//
// It writes the turn's row and nothing else on it: the fence is not the turn's
// state, it is the path's, borrowed for storage. A statement that wrote the
// whole row from a struct would have to know the turn's status, its version and
// its capability, and would be a way to lose a write that had nothing to do with
// leases.
func (r *GORMRepository) SetFencingToken(ctx context.Context, turnID string, token int64) error {
	if err := r.ready(); err != nil {
		return err
	}
	result := r.db.WithContext(ctx).Table("turns").
		Where("turn_id = ?", turnID).
		Update("fencing_token", token)
	if result.Error != nil {
		return fmt.Errorf("raise the fence of turn %s: %w", turnID, result.Error)
	}
	if result.RowsAffected > 0 {
		return nil
	}
	// No rows affected has two causes on this engine, and telling them apart is the
	// whole of this branch. `RowsAffected` counts the rows a statement *changed*,
	// not the rows it matched, so a turn whose fence is already the value being
	// written — one that released a path and took it again, or one taking a second
	// path, whose chain starts at one again — comes back as zero rows exactly like a
	// turn that is not in the table. Reporting the first as the second would fail a
	// claim that has nothing wrong with it, and it would do it only sometimes.
	var present struct {
		Count int64 `gorm:"column:present"`
	}
	err := r.db.WithContext(ctx).Raw("SELECT COUNT(*) AS present FROM turns WHERE turn_id = ?", turnID).
		Scan(&present).Error
	if err != nil {
		return fmt.Errorf("look for turn %s: %w", turnID, err)
	}
	if present.Count == 0 {
		// A turn that is not in the table cannot carry a fence, and saying so is
		// better than a lease that looks fenced and is not: the fence is what a
		// late writer is recognised by, and a lease taken under no fence would be
		// a lease with no way to spot one.
		return fmt.Errorf("%w: turn %s", ErrNotFound, turnID)
	}
	return nil
}
