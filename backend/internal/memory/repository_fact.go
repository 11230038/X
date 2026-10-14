package memory

// L2's storage: the facts the system admitted, and the evidence chain under
// them.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"backend/internal/data/models"
	"gorm.io/gorm"
)

// ActiveFact reads the fact in force for a fingerprint.
//
// A retracted or superseded fact is not one: the question this answers is "does
// the system already believe this", and a fact the user took back is not
// something the system believes. That is also what makes a new observation after
// a retraction able to admit the claim again, under a new id, with the retraction
// of the old one still on the record.
//
// The order is oldest first, and it is load-bearing in the case it exists for:
// the concurrent admits Dedupe repairs can leave two facts in force for one
// fingerprint, and the one the gate keeps reading has to be the same one on every
// call.
func (r *GORMRepository) ActiveFact(ctx context.Context, ownerID int64, fingerprint string) (Fact, error) {
	if err := r.ready(); err != nil {
		return Fact{}, err
	}
	var row models.MemoryFact
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND fingerprint = ? AND status = ?", ownerID, fingerprint, string(StatusActive)).
		Order("creat_time ASC, fact_id ASC").
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Fact{}, fmt.Errorf("%w: no fact of account %d says that", ErrNotFound, ownerID)
	}
	if err != nil {
		return Fact{}, fmt.Errorf("read the fact of account %d: %w", ownerID, err)
	}
	return factOf(row), nil
}

// Fact reads one fact of one account's.
//
// The owner is part of the WHERE clause rather than a check after the read, and
// that is MEM-04 expressed as a query: there is no statement in this package that
// could return another account's fact, so no caller can be handed one by mistake.
func (r *GORMRepository) Fact(ctx context.Context, ownerID int64, factID string) (Fact, error) {
	if err := r.ready(); err != nil {
		return Fact{}, err
	}
	var row models.MemoryFact
	err := r.db.WithContext(ctx).Where("owner_id = ? AND fact_id = ?", ownerID, factID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Fact{}, fmt.Errorf("%w: no fact %s in account %d", ErrNotFound, factID, ownerID)
	}
	if err != nil {
		return Fact{}, fmt.Errorf("read fact %s: %w", factID, err)
	}
	return factOf(row), nil
}

// Facts reads L2, most recently changed first.
//
// "Changed" and not "created" is the order the index on this table is built for,
// and the two differ exactly where it matters: a fact the user edited yesterday
// is more interesting than one the gate admitted last month. A fact that has
// never been edited has no update time at all, so the ordering falls back to when
// it was created rather than sorting an untouched fact behind an edited one as if
// it were older than it is.
func (r *GORMRepository) Facts(ctx context.Context, query FactQuery) ([]Fact, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	statement := r.db.WithContext(ctx).Model(&models.MemoryFact{}).Where("owner_id = ?", query.OwnerID)
	if len(query.Statuses) > 0 {
		statement = statement.Where("status IN ?", statusesOf(query.Statuses))
	}
	if query.Surface != "" {
		statement = statement.Where("surface = ?", string(query.Surface))
	}
	if !query.Before.IsZero() {
		// `creat_time` and not the column the ordering uses: see FactQuery.Before
		// for why the cutoff reads the admission.
		statement = statement.Where("creat_time < ?", query.Before)
	}
	if query.Limit > 0 {
		statement = statement.Limit(query.Limit)
	}

	var rows []models.MemoryFact
	if err := statement.Order("COALESCE(update_time, creat_time) DESC, fact_id DESC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("read the memory facts of account %d: %w", query.OwnerID, err)
	}
	facts := make([]Fact, 0, len(rows))
	for _, row := range rows {
		facts = append(facts, factOf(row))
	}
	return facts, nil
}

// DuplicateGroups reads the facts in force that say the same thing.
//
// The grouping is a rule and it lives in SQL anyway, because the alternative is
// reading every fact an account has in order to find the handful of pairs — and
// the number of groups is by nature small: it is the number of times the system
// has been told the same thing twice.
func (r *GORMRepository) DuplicateGroups(ctx context.Context, ownerID int64) ([]FactGroup, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	var counted []struct {
		Fingerprint string `gorm:"column:fingerprint"`
	}
	err := r.db.WithContext(ctx).Model(&models.MemoryFact{}).
		Select("fingerprint").
		Where("owner_id = ? AND status = ?", ownerID, string(StatusActive)).
		Group("fingerprint").
		Having("COUNT(*) > 1").
		Order("fingerprint ASC").
		Find(&counted).Error
	if err != nil {
		return nil, fmt.Errorf("read the duplicate facts of account %d: %w", ownerID, err)
	}

	groups := make([]FactGroup, 0, len(counted))
	for _, group := range counted {
		facts, err := r.factsOfFingerprint(ctx, ownerID, group.Fingerprint)
		if err != nil {
			return nil, err
		}
		groups = append(groups, FactGroup{Fingerprint: group.Fingerprint, Facts: facts})
	}
	return groups, nil
}

// factsOfFingerprint reads the facts in force that share a fingerprint, oldest
// first, which is also the order a merge reads them as a candidate list.
func (r *GORMRepository) factsOfFingerprint(ctx context.Context, ownerID int64, fingerprint string) ([]Fact, error) {
	var rows []models.MemoryFact
	err := r.db.WithContext(ctx).
		Where("owner_id = ? AND fingerprint = ? AND status = ?", ownerID, fingerprint, string(StatusActive)).
		Order("creat_time ASC, fact_id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read the facts of account %d for one fingerprint: %w", ownerID, err)
	}
	facts := make([]Fact, 0, len(rows))
	for _, row := range rows {
		facts = append(facts, factOf(row))
	}
	return facts, nil
}

// CreateFact writes a fact.
func (r *GORMRepository) CreateFact(ctx context.Context, fact Fact) error {
	if err := r.ready(); err != nil {
		return err
	}
	record := models.MemoryFact{
		FactID:      fact.ID,
		OwnerID:     fact.OwnerID,
		Surface:     string(fact.Surface),
		Scope:       nullable(fact.Scope),
		Statement:   fact.Statement,
		Fingerprint: fact.Fingerprint,
		Confidence:  int16(fact.Confidence),
		Status:      string(fact.Status),
		Sensitive:   fact.Sensitive,
		ConfirmedBy: string(fact.ConfirmedBy),
		Version:     fact.Version,
		CreatedAt:   fact.CreatedAt,
		UpdatedAt:   moment(fact.UpdatedAt),
		RetractTime: moment(fact.RetractTime),
	}
	return duplicateOr(r.db.WithContext(ctx).Create(&record).Error, "record fact %s", fact.ID)
}

// SaveFact writes a fact over the version the caller read it at.
//
// One statement with the version in its WHERE clause, and the `RowsAffected == 0`
// branch is the whole point of it: a read followed by a write would let two
// callers both read version 1 and both write version 2, and the second edit would
// vanish without anybody noticing.
func (r *GORMRepository) SaveFact(ctx context.Context, fact Fact, expected int32, now time.Time) (int32, error) {
	if err := r.ready(); err != nil {
		return 0, err
	}
	reached := expected + 1
	columns := map[string]any{
		"scope":        nullable(fact.Scope),
		"statement":    fact.Statement,
		"fingerprint":  fact.Fingerprint,
		"confidence":   int16(fact.Confidence),
		"status":       string(fact.Status),
		"sensitive":    fact.Sensitive,
		"confirmed_by": string(fact.ConfirmedBy),
		"version":      reached,
		"update_time":  now,
		"retract_time": moment(fact.RetractTime),
	}
	updated := r.db.WithContext(ctx).Model(&models.MemoryFact{}).
		Where("fact_id = ? AND version = ?", fact.ID, expected).
		Updates(columns)
	if updated.Error != nil {
		return 0, fmt.Errorf("save fact %s: %w", fact.ID, updated.Error)
	}
	if updated.RowsAffected == 0 {
		// Nothing matched for one of two reasons, and they are not the same
		// answer: the fact is gone, or it is there and somebody else has written
		// it since. Reading it again is the only way to tell them apart, and the
		// caller has to read it again either way.
		if _, err := r.Fact(ctx, fact.OwnerID, fact.ID); err != nil {
			return 0, err
		}
		return 0, fmt.Errorf("%w: fact %s is not at version %d", ErrStaleState, fact.ID, expected)
	}
	return reached, nil
}

// AttachSource records one observation as evidence for one fact.
func (r *GORMRepository) AttachSource(ctx context.Context, factID, traceID string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}
	record := models.MemoryFactSource{FactID: factID, TraceID: traceID, CreatedAt: now}
	err := r.db.WithContext(ctx).Create(&record).Error
	// The pair is the key, so a second attach of the same evidence collides. That
	// is not a mistake being reported: it is the record already saying what the
	// caller wanted it to say.
	if err == nil || errors.Is(err, gorm.ErrDuplicatedKey) {
		return nil
	}
	return fmt.Errorf("attach trace %s to fact %s: %w", traceID, factID, err)
}

// PurgeFacts removes the named facts of one account, and the evidence rows under
// them by the foreign key.
//
// The list is checked before it is used, and that is not politeness: `IN ()` with
// nothing in it is the statement GORM refuses to run without a WHERE clause at
// all, and the guard that stops a delete from being a delete-everything is exactly
// what an empty list would slip past. An empty list is a cleanup that found
// nothing, which is nothing to do rather than a failure.
func (r *GORMRepository) PurgeFacts(ctx context.Context, ownerID int64, factIDs []string) (int, error) {
	if err := r.ready(); err != nil {
		return 0, err
	}
	if len(factIDs) == 0 {
		return 0, nil
	}
	removed := r.db.WithContext(ctx).
		Where("owner_id = ? AND fact_id IN ?", ownerID, factIDs).
		Delete(&models.MemoryFact{})
	if removed.Error != nil {
		return 0, fmt.Errorf("purge the facts of account %d: %w", ownerID, removed.Error)
	}
	return int(removed.RowsAffected), nil
}

// MoveSources re-points every piece of evidence from one fact to another.
//
// It reads what the merging fact has and inserts what the survivor is missing
// rather than running one statement that moves rows wholesale, because the two
// can already share a piece of evidence: two observations of the same thing are
// what a duplicate *is*, and the same trace can be evidence for both facts when
// both were admitted from the same pair. A wholesale move would collide on the
// pair's key; this way the collision is the answer — that evidence is already
// where it should end up.
func (r *GORMRepository) MoveSources(ctx context.Context, fromFactID, toFactID string) error {
	if err := r.ready(); err != nil {
		return err
	}
	var sources []models.MemoryFactSource
	if err := r.db.WithContext(ctx).Where("fact_id = ?", fromFactID).Find(&sources).Error; err != nil {
		return fmt.Errorf("read the evidence of fact %s: %w", fromFactID, err)
	}
	for _, source := range sources {
		record := models.MemoryFactSource{FactID: toFactID, TraceID: source.TraceID, CreatedAt: source.CreatedAt}
		err := r.db.WithContext(ctx).Create(&record).Error
		if err != nil && !errors.Is(err, gorm.ErrDuplicatedKey) {
			return fmt.Errorf("move trace %s from fact %s to fact %s: %w", source.TraceID, fromFactID, toFactID, err)
		}
	}
	err := r.db.WithContext(ctx).
		Where("fact_id = ?", fromFactID).
		Delete(&models.MemoryFactSource{}).Error
	if err != nil {
		return fmt.Errorf("clear the evidence of fact %s: %w", fromFactID, err)
	}
	return nil
}

// factOf reads one stored row as the domain's fact.
func factOf(row models.MemoryFact) Fact {
	fact := Fact{
		ID:          row.FactID,
		OwnerID:     row.OwnerID,
		Surface:     Surface(row.Surface),
		Scope:       text(row.Scope),
		Statement:   row.Statement,
		Fingerprint: row.Fingerprint,
		Confidence:  int(row.Confidence),
		Status:      Status(row.Status),
		Sensitive:   row.Sensitive,
		ConfirmedBy: Confirmer(row.ConfirmedBy),
		Version:     row.Version,
		CreatedAt:   row.CreatedAt,
	}
	// A column that is NULL is a time that has not happened, and the zero time is
	// how this package says that. 1970 would be a time, and a fact "retracted in
	// 1970" is a lie a reader cannot tell from a fact that was never retracted.
	if row.UpdatedAt != nil {
		fact.UpdatedAt = *row.UpdatedAt
	}
	if row.RetractTime != nil {
		fact.RetractTime = *row.RetractTime
	}
	return fact
}
