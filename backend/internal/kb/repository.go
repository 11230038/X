package kb

import (
	"context"
	"errors"
	"fmt"

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
		// Locking every row of a table with a handful of rows is how two
		// creators on an empty table are serialised. A plain read would let both
		// find no default and both claim it.
		var bases []models.KnowledgeBase
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Find(&bases).Error; err != nil {
			return fmt.Errorf("read knowledge bases: %w", err)
		}
		isDefault := true
		for _, existing := range bases {
			if existing.IsDefault {
				isDefault = false
				break
			}
		}
		model := models.KnowledgeBase{
			KBID: record.ID, Name: record.Name,
			Description: nullableString(record.Description),
			Provider:    record.Provider, EmbeddingModel: record.EmbeddingModel,
			IsDefault: isDefault, CreatedAt: record.CreatedAt,
		}
		if err := tx.Create(&model).Error; err != nil {
			return fmt.Errorf("create knowledge base: %w", err)
		}
		return nil
	})
}

// KnowledgeBase returns one base with its counts.
func (r *GORMRepository) KnowledgeBase(ctx context.Context, kbID string) (KnowledgeBase, error) {
	if err := r.ready(); err != nil {
		return KnowledgeBase{}, err
	}
	var model models.KnowledgeBase
	err := r.db.WithContext(ctx).First(&model, "kb_id = ?", kbID).Error
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

// KnowledgeBases lists every base, the default first.
func (r *GORMRepository) KnowledgeBases(ctx context.Context) ([]KnowledgeBase, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	var records []models.KnowledgeBase
	err := r.db.WithContext(ctx).
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
func (r *GORMRepository) SetDefaultKnowledgeBase(ctx context.Context, kbID string) error {
	if err := r.ready(); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The lock is taken before either write, so two setters applying at once
		// cannot interleave their clear and their set and leave two defaults.
		// Locking the whole table is affordable here because a deployment holds
		// a handful of bases, and it does not depend on where a scan of an
		// unindexed column happens to stop.
		var bases []models.KnowledgeBase
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Find(&bases).Error; err != nil {
			return fmt.Errorf("lock knowledge bases: %w", err)
		}
		var target models.KnowledgeBase
		if err := tx.First(&target, "kb_id = ?", kbID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: knowledge base %s", ErrNotFound, kbID)
			}
			return fmt.Errorf("read knowledge base: %w", err)
		}
		clear := tx.Model(&models.KnowledgeBase{}).
			Where("is_default = TRUE AND kb_id <> ?", kbID).
			Update("is_default", false)
		if clear.Error != nil {
			return fmt.Errorf("clear the default knowledge base: %w", clear.Error)
		}
		set := tx.Model(&models.KnowledgeBase{}).
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
func (r *GORMRepository) DeleteKnowledgeBase(ctx context.Context, kbID string) error {
	if err := r.ready(); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var target models.KnowledgeBase
		if err := tx.First(&target, "kb_id = ?", kbID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: knowledge base %s", ErrNotFound, kbID)
			}
			return fmt.Errorf("read knowledge base: %w", err)
		}
		if err := tx.Delete(&models.KnowledgeBase{}, "kb_id = ?", kbID).Error; err != nil {
			return fmt.Errorf("delete knowledge base: %w", err)
		}
		if !target.IsDefault {
			return nil
		}
		var oldest models.KnowledgeBase
		err := tx.Order("creat_time ASC").First(&oldest).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read the next default knowledge base: %w", err)
		}
		if err := tx.Model(&models.KnowledgeBase{}).Where("kb_id = ?", oldest.KBID).
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
