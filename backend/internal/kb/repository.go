package kb

import (
	"context"
	"errors"
	"fmt"

	"backend/internal/authz"
	"backend/internal/data"
	"backend/internal/data/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GORMRepository stores the knowledge base tables through GORM (seekdb over the
// MySQL protocol).
//
// The SQL in here is shaped by what this seekdb build actually plans, not by
// what reads naturally. Each query that carries such a decision says so in a
// comment, because the natural spelling runs and returns the right rows while
// quietly falling off the index — the one kind of mistake that no test in this
// package can catch.
type GORMRepository struct {
	db *gorm.DB
}

// NewGORMRepository creates a storage adapter over an existing GORM handle.
func NewGORMRepository(db *gorm.DB) *GORMRepository {
	return &GORMRepository{db: db}
}

var _ Repository = (*GORMRepository)(nil)

// CreateKnowledgeBase stores one base. The first base in an empty table becomes
// the default one, so that "the default base" is never a question with no
// answer while bases exist; later ones are made default by
// SetDefaultKnowledgeBase.
func (r *GORMRepository) CreateKnowledgeBase(ctx context.Context, record KnowledgeBase) error {
	if err := r.ready(); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The read decides the common case — the first base is the default one,
		// a later base is not — and the unique index on default_marker decides
		// the race: two creators arriving at an empty table both read "no
		// default", both try to claim the mark, and the index hands it to one
		// of them. The other stores its base without the mark, which is what it
		// wanted anyway.
		//
		// A locking read was the first attempt at this, and it was not enough.
		// It serialises creators while the table has rows to lock, and this
		// database runs READ-COMMITTED, where a locking read over an empty table
		// locks nothing at all — so every creator claimed the mark. See the
		// deviation log.
		var bases []models.KnowledgeBase
		if err := tx.Find(&bases).Error; err != nil {
			return fmt.Errorf("read knowledge bases: %w", err)
		}
		isDefault := true
		for _, existing := range bases {
			if existing.IsDefault {
				isDefault = false
				break
			}
		}
		create := func(isDefault bool) error {
			model := models.KnowledgeBase{
				KBID: record.ID, Name: record.Name,
				Description: nullableString(record.Description),
				Provider:    record.Provider, EmbeddingModel: record.EmbeddingModel,
				IsDefault: isDefault, OwnerID: data.OwnerPointer(record.OwnerID),
				CreatedAt: record.CreatedAt,
			}
			return tx.Create(&model).Error
		}
		err := create(isDefault)
		if isDefault && errors.Is(err, gorm.ErrDuplicatedKey) {
			// Somebody else holds the mark by now. The base is still wanted, so
			// it is stored without it; a second refusal is a different problem
			// — the same id twice — and is reported as it is.
			err = create(false)
		}
		if err != nil {
			return fmt.Errorf("create knowledge base: %w", err)
		}
		return nil
	})
}

// KnowledgeBase returns one base with its counts.
func (r *GORMRepository) KnowledgeBase(ctx context.Context, kbID string, caller authz.Caller) (KnowledgeBase, error) {
	if err := r.ready(); err != nil {
		return KnowledgeBase{}, err
	}
	var model models.KnowledgeBase
	query := data.OwnerScope(r.db.WithContext(ctx), caller.UserID, caller.IsAdmin(), "owner_id")
	err := query.First(&model, "kb_id = ?", kbID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return KnowledgeBase{}, fmt.Errorf("%w: knowledge base %s", ErrNotFound, kbID)
		}
		return KnowledgeBase{}, fmt.Errorf("read knowledge base: %w", err)
	}
	record := toKnowledgeBase(model)
	counts, err := r.KnowledgeBaseCounts(ctx)
	if err != nil {
		return KnowledgeBase{}, err
	}
	if counted, ok := counts[kbID]; ok {
		record.Documents, record.ReadyDocuments = counted.Documents, counted.Ready
	}
	return record, nil
}

// KnowledgeBases lists the caller's bases, the default first.
//
// Until V5 this listed every row in the table, which was right only while the
// table held one person's collections.
func (r *GORMRepository) KnowledgeBases(ctx context.Context, caller authz.Caller) ([]KnowledgeBase, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	var records []models.KnowledgeBase
	query := data.OwnerScope(r.db.WithContext(ctx), caller.UserID, caller.IsAdmin(), "owner_id")
	err := query.
		Order("is_default DESC, creat_time ASC").
		Find(&records).Error
	if err != nil {
		return nil, fmt.Errorf("read knowledge bases: %w", err)
	}
	counts, err := r.KnowledgeBaseCounts(ctx)
	if err != nil {
		return nil, err
	}
	bases := make([]KnowledgeBase, 0, len(records))
	for _, model := range records {
		record := toKnowledgeBase(model)
		if counted, ok := counts[model.KBID]; ok {
			record.Documents, record.ReadyDocuments = counted.Documents, counted.Ready
		}
		bases = append(bases, record)
	}
	return bases, nil
}

// documentCounts is one row of the per-base count.
type documentCounts struct {
	KBID      string `gorm:"column:kb_id"`
	Documents int    `gorm:"column:documents"`
	Ready     int    `gorm:"column:ready_documents"`
}

// KnowledgeBaseCounts counts each base's documents and how many of them have
// every chunk embedded.
//
// It is a second query rather than a join into the listing, because nothing
// indexes documents.kb_id — the vector index on document_chunks forbids it — so
// both shapes scan the same table and this one is the easier to read. The
// document list of a base is small enough for that to be the right trade while
// the alternative is a database error, which is the same trade the schema
// comment records.
func (r *GORMRepository) KnowledgeBaseCounts(ctx context.Context) (map[string]Counts, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	var rows []documentCounts
	err := r.db.WithContext(ctx).Raw(`
		SELECT kb_id,
		       COUNT(*) AS documents,
		       COUNT(CASE WHEN vector_state = 'ready' THEN 1 END) AS ready_documents
		FROM documents
		WHERE kb_id IS NOT NULL
		GROUP BY kb_id`).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("count knowledge base documents: %w", err)
	}
	counts := make(map[string]Counts, len(rows))
	for _, row := range rows {
		counts[row.KBID] = Counts{Documents: row.Documents, Ready: row.Ready}
	}
	return counts, nil
}

// SetDefaultKnowledgeBase moves the default mark to one base, leaving exactly
// one behind it.
func (r *GORMRepository) SetDefaultKnowledgeBase(ctx context.Context, kbID string, caller authz.Caller) error {
	if err := r.ready(); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The lock is taken before either write, so two setters applying at once
		// cannot interleave their clear and their set: the second waits, and
		// then clears the first one's mark before setting its own.
		//
		// It is not what keeps the invariant — the unique index on
		// default_marker is, and it would refuse a second default whatever this
		// code did. What the lock buys is that the ordinary case does not have
		// to fail and retry: here the table has rows to lock, so the setter that
		// arrives second waits rather than colliding.
		//
		// The clear and the target read are narrowed to the caller, which they
		// were not before V5. The mark itself is still instance-wide — the unique
		// index is on a generated column derived from is_default alone, with no
		// room for an account — so a second account cannot star its own base
		// while the first account holds the mark. That is recorded in the
		// deviation log rather than papered over here: clearing somebody else's
		// mark to make room would be the cross-account write this whole version
		// exists to stop.
		var bases []models.KnowledgeBase
		lock := data.OwnerScope(tx.Clauses(clause.Locking{Strength: "UPDATE"}),
			caller.UserID, caller.IsAdmin(), "owner_id")
		if err := lock.Find(&bases).Error; err != nil {
			return fmt.Errorf("lock knowledge bases: %w", err)
		}
		var target models.KnowledgeBase
		targetQuery := data.OwnerScope(tx, caller.UserID, caller.IsAdmin(), "owner_id")
		if err := targetQuery.First(&target, "kb_id = ?", kbID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: knowledge base %s", ErrNotFound, kbID)
			}
			return fmt.Errorf("read knowledge base: %w", err)
		}
		clear := data.OwnerScope(tx.Model(&models.KnowledgeBase{}),
			caller.UserID, caller.IsAdmin(), "owner_id").
			Where("is_default = TRUE AND kb_id <> ?", kbID).
			Update("is_default", false)
		if clear.Error != nil {
			return fmt.Errorf("clear the default knowledge base: %w", clear.Error)
		}
		set := data.OwnerScope(tx.Model(&models.KnowledgeBase{}),
			caller.UserID, caller.IsAdmin(), "owner_id").
			Where("kb_id = ?", kbID).
			Update("is_default", true)
		if set.Error != nil {
			return fmt.Errorf("set the default knowledge base: %w", set.Error)
		}
		return nil
	})
}

// DeleteKnowledgeBase removes a base and everything under it.
//
// When the default base is the one going, the oldest remaining base takes the
// mark: the mark has to be somewhere while bases exist, and promoting a row is
// not the substitution KB-07 forbids — that rule is about a *turn's* scope, and
// retrieval never consults the default when it searches.
func (r *GORMRepository) DeleteKnowledgeBase(ctx context.Context, kbID string, caller authz.Caller) error {
	if err := r.ready(); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var target models.KnowledgeBase
		read := data.OwnerScope(tx, caller.UserID, caller.IsAdmin(), "owner_id")
		if err := read.First(&target, "kb_id = ?", kbID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: knowledge base %s", ErrNotFound, kbID)
			}
			return fmt.Errorf("read knowledge base: %w", err)
		}
		remove := data.OwnerScope(tx, caller.UserID, caller.IsAdmin(), "owner_id")
		if err := remove.Delete(&models.KnowledgeBase{}, "kb_id = ?", kbID).Error; err != nil {
			return fmt.Errorf("delete knowledge base: %w", err)
		}
		if !target.IsDefault {
			return nil
		}
		// The successor is looked for among the caller's own bases, not the table.
		// Promoting another account's base would hand them a mark they did not ask
		// for and put this caller's next base at odds with the unique index; when
		// this account has none left the mark simply goes free, and the next base
		// any account creates takes it (CreateKnowledgeBase claims a free mark).
		var oldest models.KnowledgeBase
		promote := data.OwnerScope(tx, caller.UserID, caller.IsAdmin(), "owner_id")
		err := promote.Order("creat_time ASC").First(&oldest).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read the next default knowledge base: %w", err)
		}
		mark := data.OwnerScope(tx.Model(&models.KnowledgeBase{}),
			caller.UserID, caller.IsAdmin(), "owner_id")
		if err := mark.Where("kb_id = ?", oldest.KBID).
			Update("is_default", true).Error; err != nil {
			return fmt.Errorf("promote the next default knowledge base: %w", err)
		}
		return nil
	})
}

// Document returns one document.
func (r *GORMRepository) ready() error {
	if r == nil || r.db == nil {
		return fmt.Errorf("kb repository is not initialized")
	}
	return nil
}

// chunkRange turns a chunk id prefix into the half-open range that selects
// exactly the ids starting with it. Raising the last byte works because every
// prefix this package builds ends in '_' and no byte above that appears in a
// document id, which the compiler of those ids guarantees.
func toKnowledgeBase(model models.KnowledgeBase) KnowledgeBase {
	record := KnowledgeBase{
		ID: model.KBID, Name: model.Name, Provider: model.Provider,
		EmbeddingModel: model.EmbeddingModel, IsDefault: model.IsDefault,
		CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt,
	}
	if model.Description != nil {
		record.Description = *model.Description
	}
	return record
}

func nullableString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
