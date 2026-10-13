package notebook

import (
	"context"
	"errors"
	"fmt"
	"time"

	"backend/internal/data/models"
	"gorm.io/gorm"
)

// GORMRepository reads and writes the question bank through GORM (seekdb over
// the MySQL protocol).
type GORMRepository struct {
	db *gorm.DB
}

// NewGORMRepository creates a storage adapter over an existing GORM handle.
func NewGORMRepository(db *gorm.DB) *GORMRepository {
	return &GORMRepository{db: db}
}

var _ Repository = (*GORMRepository)(nil)

// UpsertQuestion writes the question face of the question identified by its
// content address.
//
// The write is an UPDATE first and an INSERT only if nothing was updated. That
// is not the fast path people expect — a read would be the obvious way to ask
// "is it there" — but it is the one that cannot be wrong: a read followed by a
// write has a window between them, while "updated nothing, so insert" is
// decided by the statement itself. The same statement pair is what makes a
// rewrite of an identical question work: MySQL reports zero changed rows for a
// row written back the values it already holds, so that case falls through to
// the insert, meets the unique key on question_id, and is resolved by the
// second update as "already there".
//
// The row key is only used on the insert. On the rewrite the row keeps the key
// it has, and the key that comes back is therefore read from the row rather than
// reported from the caller's argument: the two are the same row, and a caller
// that was handed back the key it made up would file the question's categories
// under a row that is not there.
func (r *GORMRepository) UpsertQuestion(ctx context.Context, entryID string, question Question, now time.Time) (Upserted, error) {
	if err := r.ready(); err != nil {
		return Upserted{}, err
	}

	updated, err := r.writeQuestionFace(ctx, question)
	if err != nil {
		return Upserted{}, err
	}
	if updated == 0 {
		// Nothing was rewritten, so the question is not there and this call is the
		// one that puts it there — unless somebody else got there between the two
		// statements, which the unique key on question_id reports as a duplicate
		// rather than as two rows.
		created, err := r.insertQuestion(ctx, entryID, question, now)
		if err != nil {
			return Upserted{}, err
		}
		if created {
			return Upserted{ID: entryID, QuestionID: question.QuestionID, Created: true}, nil
		}
	}

	id, err := r.rowIDOf(ctx, question.QuestionID)
	if err != nil {
		return Upserted{}, err
	}
	return Upserted{ID: id, QuestionID: question.QuestionID}, nil
}

// insertQuestion inserts the row and reports whether this call is the one that
// made it. A duplicate key is not a failure: it means somebody else inserted
// this question between the update and this insert, and they were right.
func (r *GORMRepository) insertQuestion(ctx context.Context, entryID string, question Question, now time.Time) (bool, error) {
	model, err := questionModel(entryID, question, now)
	if err != nil {
		return false, err
	}
	switch err := r.db.WithContext(ctx).Create(&model).Error; {
	case err == nil:
		return true, nil
	case errors.Is(err, gorm.ErrDuplicatedKey):
		// The row is there, so this call rewrites it with the question face it was
		// given: of two writes that arrived together this is the one that arrived
		// second, and the fields that are not part of the address — the options,
		// the explanation, where it came from — are not the same in both.
		if _, err := r.writeQuestionFace(ctx, question); err != nil {
			return false, err
		}
		return false, nil
	default:
		return false, fmt.Errorf("insert question: %w", err)
	}
}

// rowIDOf reads the key the row holding a question has.
func (r *GORMRepository) rowIDOf(ctx context.Context, questionID string) (string, error) {
	var ids []string
	err := r.db.WithContext(ctx).
		Table("notebook_entries").
		Where("question_id = ?", questionID).
		Pluck("notebook_entries_id", &ids).Error
	if err != nil {
		return "", fmt.Errorf("read the row key of question %s: %w", questionID, err)
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("%w: %s", ErrNotFound, questionID)
	}
	return ids[0], nil
}

// writeQuestionFace applies the question-face columns to one row and reports how
// many rows it changed.
//
// The columns are named one by one rather than derived from the model, and that
// is the point: this is also the statement that must not touch the answer face,
// not the conversation the question was first asked in, and not update_time.
// A list of columns is a place where leaving something out is visible.
func (r *GORMRepository) writeQuestionFace(ctx context.Context, question Question) (int64, error) {
	options, err := jsonColumn(question.Options)
	if err != nil {
		return 0, fmt.Errorf("encode question options: %w", err)
	}
	extra, err := extraColumn(question.KnowledgePoints)
	if err != nil {
		return 0, fmt.Errorf("encode question knowledge points: %w", err)
	}
	result := r.db.WithContext(ctx).Table("notebook_entries").
		Where("question_id = ?", question.QuestionID).
		Updates(map[string]any{
			"question":       question.Text,
			"question_type":  string(question.Type),
			"options":        options,
			"correct_answer": nullable(question.CorrectAnswer),
			"explanation":    nullable(question.Explanation),
			"difficulty":     nullable(question.Difficulty),
			"source":         nullable(question.Source),
			"material_id":    nullable(question.MaterialID),
			"extra":          extra,
		})
	if result.Error != nil {
		return 0, fmt.Errorf("update question %s: %w", question.QuestionID, result.Error)
	}
	return result.RowsAffected, nil
}

// questionModel is the row to insert: the question face, the two keys, and the
// conversation it was first asked in. The answer face is left for the review
// path, so a question nobody has answered has nothing written on it.
func questionModel(entryID string, question Question, now time.Time) (models.NotebookEntry, error) {
	options, err := jsonColumn(question.Options)
	if err != nil {
		return models.NotebookEntry{}, fmt.Errorf("encode question options: %w", err)
	}
	extra, err := extraColumn(question.KnowledgePoints)
	if err != nil {
		return models.NotebookEntry{}, fmt.Errorf("encode question knowledge points: %w", err)
	}
	return models.NotebookEntry{
		NotebookEntriesID: entryID,
		QuestionID:        question.QuestionID,
		Question:          question.Text,
		QuestionType:      string(question.Type),
		Options:           options,
		CorrectAnswer:     nullable(question.CorrectAnswer),
		Explanation:       nullable(question.Explanation),
		Difficulty:        nullable(question.Difficulty),
		Source:            nullable(question.Source),
		MaterialID:        nullable(question.MaterialID),
		Extra:             extra,
		SessionID:         nullable(question.SessionID),
		TurnID:            nullable(question.TurnID),
		CreatedAt:         now,
	}, nil
}

func (r *GORMRepository) ready() error {
	if r == nil || r.db == nil {
		return fmt.Errorf("notebook repository is not initialized")
	}
	return nil
}

// toEntry converts a stored row into a question and its answer face.
