package practice

import (
	"context"
	"fmt"
	"time"

	"backend/internal/authz"
)

// The wrong-answer set, as a progress.
//
// The home page asks "本周任务进度" for two bars, and only one of them has
// anything behind it: the review of the wrong-answer set. It is countable
// because both halves already exist — the state table says which questions are
// mistakes, and the event table says when each was reviewed — and neither half
// needs a new column.
//
// Two numbers and not a percentage: the division is the surface's (a bar has to
// divide), and a ratio without its terms cannot be labelled "3 / 9" or "本周 9 道
// 里的 3 道".

// MistakeProgress is the wrong-answer set read twice: how many of its questions
// were reviewed inside a window, and how many questions are in it now.
type MistakeProgress struct {
	// Reviewed counts distinct questions, not reviews: practising the same
	// mistake three times is still one question of the set that was seen again.
	Reviewed int
	// Total is the set as it stands now, not as it stood when the window
	// opened. The set only grows — a question answered wrong stays marked — so
	// "now" is also every question that existed during the window, plus the ones
	// that joined it after; Reviewed can therefore never exceed it.
	Total int
}

// Mistakes reads the wrong-answer set for a window.
//
// The window is half-open and both ends are required, the same shape the
// dashboard's other windowed reads take: the surface counts "this week", which
// is a boundary only the caller's calendar knows.
func (s *Service) Mistakes(ctx context.Context, from, to time.Time, caller authz.Caller) (MistakeProgress, error) {
	from, to = from.UTC(), to.UTC()
	if !from.Before(to) {
		return MistakeProgress{}, fmt.Errorf("%w: the window ends before it begins", ErrInvalidStats)
	}
	return s.repository.Mistakes(ctx, from, to, caller)
}

// mistakeProgressSQL is the two counts in one statement.
//
// Both sides are narrowed the same way as every other read in this package: the
// review tables have no owner column, so the predicate is on the question they
// are filed under, taken from ownerPredicate rather than spelled out again. The
// event side needs the state join to know whether the question was a mistake;
// the state side needs the entry join to reach the owner.
//
// "Reviewed" reads the events rather than the state's last_review_time because
// the state keeps only the latest one: a question looked at on Monday and again
// on Thursday has one timestamp, and the window is about what happened inside
// it, not about where the question ended up.
const mistakeProgressSQL = "SELECT " +
	"(SELECT COUNT(*) FROM practice_review_state s " +
	"JOIN notebook_entries e ON e.notebook_entries_id = s.notebook_entries_id " +
	"WHERE s.is_mistake = TRUE AND %s) AS total, " +
	"(SELECT COUNT(DISTINCT ev.notebook_entries_id) FROM practice_review_events ev " +
	"JOIN practice_review_state s ON s.notebook_entries_id = ev.notebook_entries_id " +
	"JOIN notebook_entries e ON e.notebook_entries_id = ev.notebook_entries_id " +
	"WHERE s.is_mistake = TRUE AND ev.review_time >= ? AND ev.review_time < ? AND %s) AS reviewed"

// Mistakes reads both counts.
func (r *GORMRepository) Mistakes(ctx context.Context, from, to time.Time, caller authz.Caller) (MistakeProgress, error) {
	if err := r.ready(); err != nil {
		return MistakeProgress{}, err
	}
	scope, owner := ownerPredicate("e", caller)
	var row struct {
		Total    int `gorm:"column:total"`
		Reviewed int `gorm:"column:reviewed"`
	}
	err := r.db.WithContext(ctx).
		Raw(fmt.Sprintf(mistakeProgressSQL, scope, scope), owner, from, to, owner).
		Scan(&row).Error
	if err != nil {
		return MistakeProgress{}, fmt.Errorf("read the wrong-answer set: %w", err)
	}
	return MistakeProgress{Reviewed: row.Reviewed, Total: row.Total}, nil
}
