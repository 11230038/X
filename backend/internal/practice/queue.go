package practice

// The review queue: what to practise now.
//
// It is one SELECT, and that is the whole design. There is no queue table, no
// background worker and no timer: a question is in the queue because two
// columns say so, and the answer changes the moment a review is recorded. The
// contrast worth keeping in mind is the embedding index — that one has to be a
// worker because it makes an external call that takes minutes, while this is a
// comparison between columns of this database, and it is answered in
// microseconds by the same transaction that would have to write a queue row.
//
// It reads `notebook_entries` directly, which is a schema coupling rather than a
// package dependency (§1.2): the question bank's read path cannot answer "which
// of these are due", because due is not a property of a question. If the two
// domains are ever split across databases, this file is the first thing that has
// to change.

import (
	"context"
	"fmt"
	"time"

	"backend/internal/data/models"
	"backend/internal/notebook"
)

// DefaultQueueSize is how many questions the queue returns when the caller
// passes no size. The size itself is a deployment's number (§5 configures it,
// and it is configured because it says how much practice one sitting hands
// out); a zero here means "the caller did not say", not "no questions".
const DefaultQueueSize = 20

// QueueReason names the group a question is in the queue because of. The two
// are not exclusive in the data — a mistake is due immediately, so it is also a
// due question — but each question enters the queue by exactly one of them, and
// that is what this reports.
type QueueReason string

const (
	// ReasonDue is a question whose next review is due, which is any question
	// with a state row whose due time has passed.
	ReasonDue QueueReason = "due"
	// ReasonNew is a question nothing has ever been recorded for.
	ReasonNew QueueReason = "new"
)

// QueueItem is one question to practise now: the question, the answer face it
// carries, and where the review of it stands.
type QueueItem struct {
	Question notebook.Question
	Answer   notebook.Answer
	Reason   QueueReason
	// IsMistake is true for a question answered wrong at least once, which is
	// what lifts it over the rest of the queue. It is never cleared, so a
	// question that has been put right is still marked — that mark says the
	// question was hard, and a question that was hard is worth seeing again.
	IsMistake bool
	// State is the review state, and it is nil exactly for the questions that
	// entered as new. Nil is not a state holding nothing: a question with no
	// state row has no version at all, and the submission that answers it sends
	// none, which is what makes the row get created.
	//
	// It is here because this is where a client gets the version it will send
	// back. A submission without one still works, but it turns the version guard
	// into a read-then-write and the guard is the point.
	State *ReviewState
}

// Queue returns the questions to practise now, in the order they should be
// asked.
//
// The order is one expression over three keys, and the three are the point:
// mistakes come first, then questions that are due by how overdue they are,
// then questions nobody has seen. Written as a single key over one column it
// looks right and is wrong — one of the three groups then always sits behind
// another no matter how long it has waited.
func (s *Service) Queue(ctx context.Context, size int) ([]QueueItem, error) {
	now := s.now().UTC()
	rows, err := s.repository.Queue(ctx, now, clampQueueSize(size))
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// clampQueueSize reads a zero or a negative size as "the caller did not say",
// and a large one as what it is: a client asking for a longer list than a
// sitting. Nothing here is expensive, so there is no maximum — an oversized
// queue is a client asking for more than it wants, not a mistake it could fix.
func clampQueueSize(size int) int {
	if size <= 0 {
		return DefaultQueueSize
	}
	return size
}

// queueOfDueAndNewSQL is the queue: the two ways a question enters it, unioned
// by one WHERE, ordered by one expression.
//
// Two readings of one row are taken, and the join direction is why they are the
// same one. The LEFT JOIN keeps a question that has no state row, which is the
// new questions — an inner join would have dropped exactly the group this
// query exists to find.
//
// `resolved IS NOT TRUE` and not `resolved = FALSE`: the column is three-state,
// and NULL means nobody has marked the question either way, which is the state
// most questions are in. Written as an equality, every unmarked question would
// be missing from the queue, and the queue would be empty for a new user.
//
// `user_answer IS NULL` is what makes a question new rather than untouched: a
// state row is created by the first review, so a question with no state row has
// never been answered — unless it was answered before this table existed, or a
// write was lost. Asking for both is asking for the same fact twice, and the
// two can only disagree when something has already gone wrong; then the queue
// leaves the question out rather than asking it again as if it were new.
//
// The aliases on the state columns are not decoration: both tables have a
// column called `extra`, and one of them is a JSON blob this query has no use
// for. Naming the columns it does want keeps the row it scans unambiguous.
const queueOfDueAndNewSQL = "SELECT e.*, " +
	"s.due_time AS review_due_time, s.is_mistake AS review_is_mistake, " +
	"s.`case` AS review_ease, s.streak AS review_streak, " +
	"s.lapses AS review_lapses, s.review_count AS review_review_count, " +
	"s.version AS review_version " +
	"FROM notebook_entries e " +
	"LEFT JOIN practice_review_state s ON s.notebook_entries_id = e.notebook_entries_id " +
	"WHERE e.resolved IS NOT TRUE " +
	"AND (s.due_time <= ? OR (s.notebook_entries_id IS NULL AND e.user_answer IS NULL)) " +
	"ORDER BY CASE WHEN s.is_mistake IS TRUE THEN 0 ELSE 1 END, " +
	"CASE WHEN s.notebook_entries_id IS NULL THEN 1 ELSE 0 END, " +
	"s.due_time ASC, e.creat_time DESC, e.notebook_entries_id ASC " +
	"LIMIT ?"

// queueRow is one row of the queue as storage returns it: the entry row, the
// three state columns the queue sorts by, and the version.
type queueRow struct {
	models.NotebookEntry
	DueTime     *time.Time `gorm:"column:review_due_time"`
	IsMistake   *bool      `gorm:"column:review_is_mistake"`
	Ease        *float64   `gorm:"column:review_ease"`
	Streak      *int32     `gorm:"column:review_streak"`
	Lapses      *int32     `gorm:"column:review_lapses"`
	ReviewCount *int32     `gorm:"column:review_review_count"`
	Version     *int64     `gorm:"column:review_version"`
}

// Queue reads the questions to practise now.
func (r *GORMRepository) Queue(ctx context.Context, now time.Time, size int) ([]QueueItem, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	var rows []queueRow
	err := r.db.WithContext(ctx).Raw(queueOfDueAndNewSQL, now, size).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read the review queue: %w", err)
	}

	items := make([]QueueItem, 0, len(rows))
	for _, row := range rows {
		question, err := notebook.QuestionOf(row.NotebookEntry)
		if err != nil {
			return nil, err
		}
		item := QueueItem{
			Question:  question,
			Answer:    notebook.AnswerOf(row.NotebookEntry),
			IsMistake: row.IsMistake != nil && *row.IsMistake,
			Reason:    ReasonDue,
		}
		if row.Version == nil {
			// No state row: nothing has been recorded for this question, so it
			// is new and there is no version for a client to send back.
			item.Reason = ReasonNew
		} else {
			item.State = &ReviewState{
				DueTime:     timesFrom(row.DueTime),
				Streak:      intsFrom(row.Streak),
				Lapses:      intsFrom(row.Lapses),
				ReviewCount: intsFrom(row.ReviewCount),
				Ease:        floatsFrom(row.Ease),
				IsMistake:   item.IsMistake,
				Version:     *row.Version,
			}
		}
		items = append(items, item)
	}
	return items, nil
}
