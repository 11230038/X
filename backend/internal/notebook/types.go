// Package notebook owns the question bank: the questions conversations
// produced, the answers given to them, and the categories they are filed under.
//
// One row of `notebook_entries` holds two things with different lifetimes, and
// keeping them apart is this package's central rule. The question face —
// text, type, options, answer, explanation, where it came from — is written by
// whoever generates the question. The answer face — what the user answered,
// whether it was right, how many times it has been answered — is written by the
// review path. Rewriting a question never touches what was answered: a
// regenerated question that wiped the user's answer would cost exactly what a
// repeated review costs, one lost record instead of one too many.
//
// Categories live beside the row rather than in it, in their own table and a
// join table, because they are shared: two questions in one category must not
// be able to disagree about what that category is called.
package notebook

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrNotFound reports a question that is not in the bank.
	ErrNotFound = errors.New("notebook: not found")
	// ErrInvalidQuestion reports a question that could not be stored as it
	// stands. It is about what storage would refuse or could not make sense of,
	// not about what the model should phrase differently: a question the model
	// can fix is refused by the tool, with the reason handed back to it.
	ErrInvalidQuestion = errors.New("notebook: invalid question")
	// ErrInvalidFilter reports a listing filter this version cannot act on.
	// Silently ignoring a filter would answer a different question than the one
	// that was asked, and the caller would have no way to tell.
	ErrInvalidFilter = errors.New("notebook: invalid filter")
	// ErrEmptyChange reports a property change that names nothing to change.
	// It is separate from a change that asks for what is already stored: that
	// one is a no-op, and this one is a request with no subject.
	ErrEmptyChange = errors.New("notebook: nothing to change")
)

// QuestionType is the kind of question, and the three kinds are a closed set.
//
// It is closed on purpose: the answering surface has three shapes, the judge
// dispatches on this value, and a fourth kind would reach both of them as
// something neither knows what to do with.
type QuestionType string

// The three kinds of question this version knows.
const (
	QuestionChoice      QuestionType = "choice"
	QuestionFillBlank   QuestionType = "fill_blank"
	QuestionShortAnswer QuestionType = "short_answer"
)

// Valid reports whether the type is one of the three.
func (t QuestionType) Valid() bool {
	switch t {
	case QuestionChoice, QuestionFillBlank, QuestionShortAnswer:
		return true
	default:
		return false
	}
}

// Option is one choice of a multiple-choice question. The key is what an answer
// and the judging path refer to ("A", "B"); the text is what the user reads.
type Option struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}

// Question is the question face of a row: everything that was decided when the
// question was written, and nothing that happened after it was asked.
type Question struct {
	// QuestionID is the content address of Text and Type. It is what the
	// outside world calls this question; the row's own key stays inside.
	QuestionID string
	Text       string
	Type       QuestionType
	// Options is empty for the kinds that have none, and is stored as JSON.
	Options []Option
	// CorrectAnswer holds option keys for a choice question (sorted, comma
	// separated, so "A,C" and "C,A" are the same answer) and the answer text
	// for the other two kinds.
	CorrectAnswer string
	Explanation   string
	Difficulty    string
	// Source names where the question came from ("knowledge_base",
	// "attachment", "reading", …) and MaterialID names the particular document
	// or attachment within it. They are written in the same breath, because
	// MaterialID means whatever its Source says it means: the two id spaces are
	// different and there is nothing for a foreign key to reference.
	Source     string
	MaterialID string
	// KnowledgePoints are free-text tags. There is no knowledge point table in
	// this version, so they live in the row's extra JSON rather than in a
	// column that would have to point somewhere that does not exist.
	KnowledgePoints []string
	// Categories are the names this question is generated under. They are an
	// input rather than a column: the row holds none of them, the join table
	// does, and a question read back carries its stored ones in Entry.Categories.
	// Saving adds a name and never removes one, so a category the user filed a
	// question under by hand survives the question being generated again.
	Categories []string
	// SessionID and TurnID say where this question was first asked, and are
	// written once: they are not ownership, and a later conversation that
	// regenerates the same question does not take it over. Both may be empty —
	// a question outlives the conversation it came from, and the V4 sources
	// never had one.
	SessionID string
	TurnID    string
}

// Validate reports whether a question can be stored as it stands.
//
// The bar is what storage would refuse, not what a good question looks like:
// the text must not be blank once whitespace is folded, and the type must be
// one of the three. A blank text is not a cosmetic problem — it would hash to
// the same identity as every other blank question of its type, which is how one
// question's text ends up replacing another's.
func (q Question) Validate() error {
	if normalize(q.Text) == "" {
		return fmt.Errorf("%w: the question text is blank", ErrInvalidQuestion)
	}
	if !q.Type.Valid() {
		return fmt.Errorf("%w: %q is not a question type", ErrInvalidQuestion, q.Type)
	}
	return nil
}

// Answer is the answer face of a row: the most recent answer given to a
// question and the markers set on it. History is not here — every answer ever
// given is a review event, and the two must not be confused: this is the
// snapshot the question card shows, while a trend, an accuracy rate or a count
// of mistakes can only be computed from the events.
type Answer struct {
	// UserAnswer is empty when the question has never been answered, which is
	// how "not answered yet" is stored. There is no separate table of pending
	// questions, and no row meaning "nothing has happened".
	UserAnswer string
	// IsCorrect is the judgement of the most recent answer, absent while
	// nothing has been judged yet.
	IsCorrect *bool
	Result    string
	// Resolved is three-state, not a flag: nil means nobody has marked this
	// question either way yet, which is the most common state and a different
	// answer from "marked as still unresolved".
	Resolved *bool
	// AttemptCount counts answers, including the ones that were wrong.
	AttemptCount int
	Bookmarked   bool
	// UpdatedAt is when the answer face last changed. It does not move when the
	// question face is rewritten: it records answering, not editing.
	UpdatedAt time.Time
}

// Entry is one row of the bank: a question with the answer record kept beside it.
type Entry struct {
	// ID is the row's own key. It does not leave the service — a question is
	// addressed by its content, and this key exists for joins and for the tool
	// result that reports it back to the model.
	ID       string
	Question Question
	Answer   Answer
	// Categories are the names this question is filed under, in order.
	Categories []string
	CreatedAt  time.Time
}

// Upserted reports what an upsert did.
type Upserted struct {
	// ID is the row key: the one that existed, or the one just inserted.
	ID         string
	QuestionID string
	// Created is true when this call inserted the row rather than rewriting one.
	Created bool
}

// Resolution selects one of the three values the resolved column can hold.
//
// It is not a bool because the column is not one. "Unresolved" in the sense the
// review queue uses it — everything not marked resolved — is a query the queue
// writes for itself (resolved IS NOT TRUE); a filter value that meant either of
// two stored values would hide the distinction the column exists for.
type Resolution string

// The values the resolved filter accepts. The empty one is "do not filter".
const (
	ResolutionAny       Resolution = ""
	ResolutionUntouched Resolution = "untouched"
	ResolutionOpen      Resolution = "open"
	ResolutionDone      Resolution = "done"
)

// Valid reports whether the resolution is one the filter accepts.
func (r Resolution) Valid() bool {
	switch r {
	case ResolutionAny, ResolutionUntouched, ResolutionOpen, ResolutionDone:
		return true
	default:
		return false
	}
}

// Filter selects which questions a listing returns. Every field is optional;
// the zero value lists the whole bank, newest first.
type Filter struct {
	Source     string
	MaterialID string
	Difficulty string
	Type       QuestionType
	// Category selects by category name rather than by id: names are what the
	// API and the tool speak, and the join table is an implementation detail of
	// how a question is filed.
	Category string
	// Resolution selects among the three states the column can hold; see
	// Resolution for why this is not a bool.
	Resolution Resolution
	// Bookmarked selects the marked ones or the rest. The two values are
	// complements — TRUE is the questions carrying the mark, FALSE is everything
	// else, the unmarked ones whose column is NULL included — so that paging
	// through both shows every question exactly once. The question bank's listing
	// offers one switch, only the bookmarked ones, so nothing asks for the other
	// side today; a filter that lost rows if it did is still worth not writing.
	Bookmarked *bool
	// Query searches the question text, case-insensitively (the collation is
	// binary, so the SQL asks for a case-insensitive comparison explicitly).
	Query string
	// Limit and Offset page the listing. A zero Limit takes the default, and a
	// Limit above the maximum is clamped rather than refused: an oversized page
	// is a client asking for more than it should get, not an error it could fix.
	Limit  int
	Offset int
}

// Validate reports whether the filter can be acted on.
func (f Filter) Validate() error {
	if !f.Resolution.Valid() {
		return fmt.Errorf("%w: %q is not a resolution", ErrInvalidFilter, f.Resolution)
	}
	if f.Type != "" && !f.Type.Valid() {
		return fmt.Errorf("%w: %q is not a question type", ErrInvalidFilter, f.Type)
	}
	return nil
}

// Change is a property change: the marks that are set on a question directly
// rather than by answering it.
//
// These are idempotent and commutative — bookmarking twice is bookmarking once,
// and two devices flipping the same switch cannot conflict — so they take no
// version and retry freely, unlike a review, which is an accumulation and must
// be applied exactly once.
type Change struct {
	Bookmarked *bool
	Resolved   *bool
}

// Empty reports whether the change names nothing.
func (c Change) Empty() bool { return c.Bookmarked == nil && c.Resolved == nil }

// Repository is the storage seam over the three tables of the question bank. It
// reports what storage holds; what that means is decided above it.
type Repository interface {
	// UpsertQuestion writes the question face of the question identified by
	// its content address, inserting the row under entryID if it is not there.
	// It writes nothing else: not the answer face, not the conversation the
	// question was first asked in, and not the row's update time.
	UpsertQuestion(ctx context.Context, entryID string, question Question, now time.Time) (Upserted, error)
	// AddCategories files a question under the given names, creating the
	// categories that do not exist yet. It never removes one: this is the write
	// side of generating a question, and a category the user added by hand is
	// not something a later generation of the same question gets to drop.
	AddCategories(ctx context.Context, entryID string, names []string) error
	// Entry reads one question by its content address, with its answer face and
	// its categories.
	Entry(ctx context.Context, questionID string) (Entry, error)
	// List returns one page of the bank, newest first.
	List(ctx context.Context, filter Filter) ([]Entry, error)
	// Mark applies a property change and reports ErrNotFound when the question
	// is not there.
	Mark(ctx context.Context, questionID string, change Change) error
}

// Listing defaults. A listing with no bound is a listing that grows without one.
const (
	DefaultListLimit = 50
	MaxListLimit     = 200
)

// cleanCategoryName trims a category name the way the column's check constraint
// will look at it, and drops the ones that are empty after that. A name of only
// spaces cannot be stored — the table refuses it — so it is dropped here rather
// than turned into a constraint violation.
func cleanCategoryNames(names []string) []string {
	cleaned := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		cleaned = append(cleaned, trimmed)
	}
	return cleaned
}
