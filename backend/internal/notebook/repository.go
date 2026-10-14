package notebook

import (
	"context"
	"errors"
	"fmt"
	"time"

	"backend/internal/authz"
	"backend/internal/data"
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
//
// Every statement here is narrowed to the caller, and the collision that makes
// is the one worth writing down. A question's row is looked up by the caller,
// because a question generated in somebody else's conversation is not this
// caller's to rewrite. An account that imports another account's question hits
// the update for nothing, then the insert meets the unique key question_id still
// carries from migration 00003 — a global key, from when there was one account —
// and the collision-tolerant re-read finds no row this caller may touch. The
// answer is ErrNotFound: as far as this account is concerned the question is not
// there, which is what §2.3 asks for, and the database-level collision says only
// that the address is taken. That global key is a landmine the schema still
// holds; it is reported and not papered over.
func (r *GORMRepository) UpsertQuestion(ctx context.Context, entryID string, question Question, now time.Time, caller authz.Caller) (Upserted, error) {
	if err := r.ready(); err != nil {
		return Upserted{}, err
	}

	updated, err := r.writeQuestionFace(ctx, question, caller)
	if err != nil {
		return Upserted{}, err
	}
	if updated == 0 {
		// Nothing was rewritten, so the question is not there and this call is the
		// one that puts it there — unless somebody else got there between the two
		// statements, which the unique key on question_id reports as a duplicate
		// rather than as two rows.
		created, err := r.insertQuestion(ctx, entryID, question, now, caller)
		if err != nil {
			return Upserted{}, err
		}
		if created {
			return Upserted{ID: entryID, QuestionID: question.QuestionID, Created: true}, nil
		}
	}

	id, err := r.rowIDOf(ctx, question.QuestionID, caller)
	if err != nil {
		return Upserted{}, err
	}
	return Upserted{ID: id, QuestionID: question.QuestionID}, nil
}

// DeleteQuestion removes one question, with everything the schema hangs off it.
//
// The row is reached by the question's own key rather than by the row key,
// because the row key is the importer's invention and the question's address is
// the thing two instances agree about: a package names a question id, and this is
// the statement §2.13's recovery needs to be able to answer with.
//
// The scope is the caller's, so an account deleting a question whose address is
// another account's finds nothing — which is the same answer as a question that
// is nowhere, and the one AUTH-06 asks for.
func (r *GORMRepository) DeleteQuestion(ctx context.Context, questionID string, caller authz.Caller) error {
	if err := r.ready(); err != nil {
		return err
	}
	query := data.OwnerScope(r.db.WithContext(ctx).Model(&models.NotebookEntry{}),
		caller.UserID, caller.IsAdmin(), "owner_id")
	result := query.Where("question_id = ?", questionID).Delete(&models.NotebookEntry{})
	if result.Error != nil {
		return fmt.Errorf("delete question %s: %w", questionID, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, questionID)
	}
	return nil
}

// insertQuestion inserts the row and reports whether this call is the one that
// made it. A duplicate key is not a failure: it means somebody else inserted
// this question between the update and this insert, and they were right.
func (r *GORMRepository) insertQuestion(ctx context.Context, entryID string, question Question, now time.Time, caller authz.Caller) (bool, error) {
	model, err := questionModel(entryID, question, now, caller)
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
		// the explanation, where it came from — are not the same in both. The
		// rewrite is scoped, so a collision with another account's row changes
		// nothing and is not an error here — the caller learns of it from the
		// scoped rowIDOf below.
		if _, err := r.writeQuestionFace(ctx, question, caller); err != nil {
			return false, err
		}
		return false, nil
	default:
		return false, fmt.Errorf("insert question: %w", err)
	}
}

// rowIDOf reads the key the row holding a question has.
func (r *GORMRepository) rowIDOf(ctx context.Context, questionID string, caller authz.Caller) (string, error) {
	var ids []string
	query := data.OwnerScope(r.db.WithContext(ctx).Table("notebook_entries"),
		caller.UserID, caller.IsAdmin(), "owner_id")
	err := query.Where("question_id = ?", questionID).Pluck("notebook_entries_id", &ids).Error
	if err != nil {
		return "", fmt.Errorf("read the row key of question %s: %w", questionID, err)
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("%w: %s", ErrNotFound, questionID)
	}
	return ids[0], nil
}

// writeQuestionFace applies the question-face columns to one row and reports how
// many rows changed.
//
// The columns are named one by one rather than derived from the model, and that
// is the point: this is also the statement that must not touch the answer face,
// not the conversation the question was first asked in, not the mastery
// coordinate it was generated from, and not update_time. A list of columns is a
// place where leaving something out is visible.
//
// The owner is not among the columns, and that is deliberate: a rewrite belongs
// to the row and does not re-own it. What the owner settles is which row this
// statement is allowed to reach, and that is the WHERE clause — through
// OwnerScope, so an account can rewrite its own question and no other.
func (r *GORMRepository) writeQuestionFace(ctx context.Context, question Question, caller authz.Caller) (int64, error) {
	options, err := jsonColumn(question.Options)
	if err != nil {
		return 0, fmt.Errorf("encode question options: %w", err)
	}
	extra, err := extraColumn(question.KnowledgePoints)
	if err != nil {
		return 0, fmt.Errorf("encode question knowledge points: %w", err)
	}
	query := data.OwnerScope(r.db.WithContext(ctx).Table("notebook_entries"),
		caller.UserID, caller.IsAdmin(), "owner_id")
	result := query.
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

// questionModel is the row to insert: the question face, the two keys, the path
// it was generated from, and the conversation it was first asked in. The answer
// face is left for the review path, so a question nobody has answered has
// nothing written on it.
//
// The owner is written here and nowhere else, because a row is owned once, when
// it is made. A caller with no account — a build nothing authenticates — writes
// NULL, which is the same state the rows from before V5 are in: visible to the
// administrator and to nobody else.
func questionModel(entryID string, question Question, now time.Time, caller authz.Caller) (models.NotebookEntry, error) {
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
		OwnerID:           data.OwnerPointer(caller.UserID),
		// The mastery coordinate is written here, on the way in, and only here:
		// it is where the question came from (§2.2), which is a fact about the
		// first write and not something a later one can improve on. Validate has
		// already refused half of it.
		MasteryPathID:    nullable(question.MasteryPathID),
		KnowledgePointID: nullable(question.KnowledgePointID),
		SessionID:        nullable(question.SessionID),
		TurnID:           nullable(question.TurnID),
		CreatedAt:        now,
	}, nil
}

func (r *GORMRepository) ready() error {
	if r == nil || r.db == nil {
		return fmt.Errorf("notebook repository is not initialized")
	}
	return nil
}

// toEntry converts a stored row into a question and its answer face.
