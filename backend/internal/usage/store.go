package usage

// The reading side of the usage table (§17.1).
//
// Every number below comes from a column and none of them from usage_json, and
// that is §2.8's rule rather than a preference: the same value is written in two
// places, so a reader that took the JSON would be reading the copy nothing
// indexes and would never notice the two drifting apart. What the migration
// promoted is what a report reads.
//
// What is absent is as much of the answer as what is present. There is no base
// URL, no prompt and no response anywhere in this file, and no column to hold
// one — which is what makes AC-13 a property of the shape rather than a filter
// somebody has to remember to write.
//
// The name: the write side of this table is a Store (types.go) and this is the
// Ledger, which is what AC-13 calls the table. The two are two directions over
// one table and they are deliberately not one type — a recorder that could also
// read a window would be a type whose job is "the usage table" rather than
// "recording one call".

import (
	"context"
	"fmt"
	"sort"
	"time"

	"backend/internal/data"
	"backend/internal/data/models"
	"gorm.io/gorm"
)

// GroupBy names what a report is broken down by.
//
// The set is closed here rather than taken as a column name from the caller: a
// query string that could name a column is SQL written through a query
// parameter, and the columns it could name are not a list anybody designed.
type GroupBy string

// The breakdowns a report can be asked for.
const (
	// GroupNone is the whole window as one set of numbers.
	GroupNone GroupBy = ""
	// GroupByModel is which model was called, GroupByProvider who served it and
	// GroupBySource which part of the product asked (§17.1).
	GroupByModel    GroupBy = "model"
	GroupByProvider GroupBy = "provider"
	GroupBySource   GroupBy = "source"
	// GroupBySession is the conversation. A call made from a route that is not a
	// conversation has no session (migration 00017) and lands in the empty key.
	GroupBySession GroupBy = "session"
	// GroupByDay buckets by the day a call started.
	GroupByDay GroupBy = "day"
)

// GroupBys is every breakdown this build serves. A name outside it is refused
// rather than ignored, because a report that quietly grouped by nothing would
// answer the question the caller did not ask and look like an answer.
var GroupBys = []GroupBy{GroupByModel, GroupByProvider, GroupBySource, GroupBySession, GroupByDay}

// KnownGroupBy reports whether a name is a breakdown this build serves.
func KnownGroupBy(id GroupBy) bool {
	for _, known := range GroupBys {
		if id == known {
			return true
		}
	}
	return false
}

// Query is one report: what it is about, over what window, and how it is broken
// down.
type Query struct {
	// OwnerID is the account whose calls the report is over. It is required: the
	// calls a report is about are somebody's, and a report over nobody's calls is
	// not a question this table can be asked (§2.9).
	OwnerID int64
	// Admin widens the account to the rows that belong to nobody, which are the
	// rows written before V5 and which somebody has to be able to see
	// (internal/data.OwnerScope). It does not widen it to another account.
	Admin bool
	// Since is inclusive and Until is exclusive, so that two windows meeting at
	// a moment do not both count the calls made in it. A zero time is no bound
	// at all, and the report says which bounds it was given rather than assuming
	// one.
	Since time.Time
	Until time.Time
	// GroupBy is the breakdown, and the zero value is "do not break it down".
	GroupBy GroupBy
	// The four narrowings. An empty one means "all of them", which is also what
	// an empty group key means: a call whose model nobody wrote down is a call
	// in the empty bucket, not a call this cannot see.
	Model     string
	Provider  string
	Source    string
	SessionID string
}

// Totals is the sum of the numeric columns over a set of rows.
//
// Every field is a sum of a column, including the two timings, and that is
// deliberate: an average would carry a denominator the reader cannot see, and
// whoever wants one has Calls and a division. The counts by outcome are columns
// as well (migration 00015), so "what did the failures cost" is a question this
// table answers rather than one it leaves to whoever still has the logs.
type Totals struct {
	// Calls is how many calls finished, and Ok, Error and Cancelled are how they
	// finished. The three add up to Calls.
	Calls     int64
	Ok        int64
	Error     int64
	Cancelled int64
	// The token columns are the provider's own counts, summed as they were
	// recorded.
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	// DurationMs and TTFTMs are sums of whole calls' timings.
	DurationMs int64
	TTFTMs     int64
}

// add returns the two totals together, field by field. It is how a broken-down
// report's whole is derived from its parts, which makes "the total is the sum of
// the buckets" true by construction rather than by a second query that agrees
// with the first.
func (t Totals) add(other Totals) Totals {
	return Totals{
		Calls:            t.Calls + other.Calls,
		Ok:               t.Ok + other.Ok,
		Error:            t.Error + other.Error,
		Cancelled:        t.Cancelled + other.Cancelled,
		PromptTokens:     t.PromptTokens + other.PromptTokens,
		CompletionTokens: t.CompletionTokens + other.CompletionTokens,
		TotalTokens:      t.TotalTokens + other.TotalTokens,
		DurationMs:       t.DurationMs + other.DurationMs,
		TTFTMs:           t.TTFTMs + other.TTFTMs,
	}
}

// Group is one bucket of a broken-down report.
//
// Key is the bucket's label. For every breakdown but the conversation it is a
// value the column always has; for GroupBySession the empty key means the calls
// that belong to no conversation at all. The column keeps NULL for those
// (migration 00017) and the distinction is given up in the label on purpose: a
// label is not a count.
type Group struct {
	Key string
	// Finished and Unfinished are the same two numbers as the report's, for this
	// bucket. A bucket of calls that never came back has an Unfinished count and
	// no totals, and it is still a bucket.
	Finished   Totals
	Unfinished int64
}

// Report is what one read answers with.
type Report struct {
	// Since and Until are the window the report is over, and a nil end is the
	// end the caller did not give. They travel with the numbers because a number
	// without its window is one nobody looking at it can check.
	Since   *time.Time
	Until   *time.Time
	GroupBy GroupBy
	// Finished is every call that came back, whatever it came back as; the
	// outcome counts say which is which.
	Finished Totals
	// Unfinished is how many calls did not come back — the rows with no
	// finished_at (§2.7). They are not in Finished, and they are not dirty data
	// either: they are the state the two-part write exists to be able to show.
	Unfinished int64
	// Groups is empty when the query asked for no breakdown, and it is never
	// nil when it did: a window with nothing in it is an answer.
	Groups []Group
}

// Ledger is the usage table as the reading side reaches it.
type Ledger interface {
	// Read answers one report.
	Read(ctx context.Context, query Query) (Report, error)
	// Prune lets go of the calls that started before a moment, and reports how
	// many went.
	Prune(ctx context.Context, before time.Time) (int64, error)
	// Unfinished counts the calls in the whole table that have not come back.
	//
	// This is the one read here that is not narrowed to an account, and the
	// exception is narrow on purpose: what comes back is a count with no row
	// behind it and no owner in it. "Is the writer losing calls" is a question
	// about the process rather than about anybody's spending (§2.15), and no
	// account's data can be reached through a number (§7.2).
	Unfinished(ctx context.Context) (int64, error)
}

// GORMLedger reads llm_calls through GORM.
type GORMLedger struct {
	db *gorm.DB
}

// NewGORMLedger creates the ledger over an existing handle.
func NewGORMLedger(db *gorm.DB) *GORMLedger { return &GORMLedger{db: db} }

func (l *GORMLedger) ready() error {
	if l == nil || l.db == nil {
		return fmt.Errorf("usage ledger is not initialized")
	}
	return nil
}

// Read answers one report.
//
// A broken-down report is one query per question and the whole is added up from
// the buckets in Go, so that a client checking "does this add up" is checking
// something that cannot fail (§7.2's hand-summed comparison).
func (l *GORMLedger) Read(ctx context.Context, query Query) (Report, error) {
	if err := l.ready(); err != nil {
		return Report{}, err
	}
	if query.GroupBy == GroupNone {
		finished, err := l.totals(ctx, query)
		if err != nil {
			return Report{}, err
		}
		unfinished, err := l.unfinished(ctx, query)
		if err != nil {
			return Report{}, err
		}
		return Report{Finished: finished, Unfinished: unfinished}, nil
	}
	groups, err := l.groups(ctx, query)
	if err != nil {
		return Report{}, err
	}
	report := Report{Groups: groups}
	for _, group := range groups {
		report.Finished = report.Finished.add(group.Finished)
		report.Unfinished += group.Unfinished
	}
	return report, nil
}

// Prune lets go of the calls that started before a moment.
//
// It is bounded by time and by nothing else: there is no way to name the rows it
// removes, which is what makes a retention window a window rather than an
// editing tool. A window that removed nothing is not an error.
func (l *GORMLedger) Prune(ctx context.Context, before time.Time) (int64, error) {
	if err := l.ready(); err != nil {
		return 0, err
	}
	result := l.db.WithContext(ctx).
		Where("started_at < ?", before).
		Delete(&models.LLMCall{})
	if result.Error != nil {
		return 0, fmt.Errorf("prune usage rows: %w", result.Error)
	}
	return result.RowsAffected, nil
}

// Unfinished counts the calls that have not come back, over the whole table.
//
// It is deliberately not built on scope: there is no account, no window and no
// narrowing to read here, and the answer is one number. What it is asked is
// whether the two-part write is losing rows, which is a property of the process
// and not of anybody's usage.
func (l *GORMLedger) Unfinished(ctx context.Context) (int64, error) {
	if err := l.ready(); err != nil {
		return 0, err
	}
	var row unfinishedRow
	err := l.db.WithContext(ctx).
		Model(&models.LLMCall{}).
		Where("finished_at IS NULL").
		Select("COUNT(*) AS unfinished").
		Scan(&row).Error
	if err != nil {
		return 0, fmt.Errorf("count the calls that did not come back: %w", err)
	}
	return row.Unfinished, nil
}

// totals sums the columns over the calls that came back.
func (l *GORMLedger) totals(ctx context.Context, query Query) (Totals, error) {
	var row totalsRow
	err := l.scope(ctx, query).
		Where("finished_at IS NOT NULL").
		Select(totalsSelection, outcomeArgs()...).
		Scan(&row).Error
	if err != nil {
		return Totals{}, fmt.Errorf("sum the usage columns: %w", err)
	}
	return row.totals(), nil
}

// unfinished counts the calls in the window that never came back.
func (l *GORMLedger) unfinished(ctx context.Context, query Query) (int64, error) {
	var row unfinishedRow
	err := l.scope(ctx, query).
		Where("finished_at IS NULL").
		Select("COUNT(*) AS unfinished").
		Scan(&row).Error
	if err != nil {
		return 0, fmt.Errorf("count the calls that did not come back: %w", err)
	}
	return row.Unfinished, nil
}

// groups reads the same two questions broken down by one column.
//
// They are two queries because "finished" and "unfinished" are two different
// WHERE clauses rather than two columns, and the second is what makes a bucket
// of calls that never came back appear at all: a bucket whose every call hung
// has no row in the first query and is not a bucket the second one may drop.
func (l *GORMLedger) groups(ctx context.Context, query Query) ([]Group, error) {
	key := groupExpression(query.GroupBy)

	var buckets []totalsRow
	// The key is selected under the name the row reads it by, and the same name is
	// what the ORDER BY asks for. `bucket` is not a column of the table, so an
	// unaliased key leaves the ORDER BY naming something no table has — which is
	// an error rather than a silently unsorted report, but only a database says so.
	err := l.scope(ctx, query).
		Where("finished_at IS NOT NULL").
		Select(key+" AS bucket, "+totalsSelection, outcomeArgs()...).
		Group(key).
		Order("bucket ASC").
		Scan(&buckets).Error
	if err != nil {
		return nil, fmt.Errorf("group the usage columns: %w", err)
	}

	var open []unfinishedRow
	err = l.scope(ctx, query).
		Where("finished_at IS NULL").
		Select(key + " AS bucket, COUNT(*) AS unfinished").
		Group(key).
		Order("bucket ASC").
		Scan(&open).Error
	if err != nil {
		return nil, fmt.Errorf("group the calls that did not come back: %w", err)
	}

	groups := make([]Group, 0, len(buckets)+len(open))
	at := make(map[string]int, len(buckets))
	for _, row := range buckets {
		at[row.Bucket] = len(groups)
		groups = append(groups, Group{Key: row.Bucket, Finished: row.totals()})
	}
	for _, row := range open {
		index, seen := at[row.Bucket]
		if !seen {
			groups = append(groups, Group{Key: row.Bucket})
			index = len(groups) - 1
			at[row.Bucket] = index
		}
		groups[index].Unfinished = row.Unfinished
	}
	// Sorting the merged list rather than relying on either query's order: a
	// bucket only the second query knows about is appended after one only the
	// first one knew, and the key is the only thing that orders them together.
	sort.Slice(groups, func(i, j int) bool { return groups[i].Key < groups[j].Key })
	return groups, nil
}

// scope is the WHERE clause every query here starts from: whose calls, over
// what window, narrowed how.
//
// The owner comes through data.OwnerScope for the same reason every other
// owned read in this repository does — a read that forgets it does not fail, it
// lists another account's rows, and a list with too much in it looks exactly
// like a list that works.
//
// The window is on started_at rather than on finished_at, and it has to be: a
// call that never came back has no finish to filter on, and it is the call the
// report most needs to be able to count. idx_llm_calls_owner_started is the
// index this clause is written against.
func (l *GORMLedger) scope(ctx context.Context, query Query) *gorm.DB {
	db := data.OwnerScope(
		l.db.WithContext(ctx).Model(&models.LLMCall{}), query.OwnerID, query.Admin, "owner_id")
	if !query.Since.IsZero() {
		db = db.Where("started_at >= ?", query.Since)
	}
	if !query.Until.IsZero() {
		db = db.Where("started_at < ?", query.Until)
	}
	if query.Model != "" {
		db = db.Where("model = ?", query.Model)
	}
	if query.Provider != "" {
		db = db.Where("provider = ?", query.Provider)
	}
	if query.Source != "" {
		db = db.Where("source = ?", query.Source)
	}
	if query.SessionID != "" {
		db = db.Where("session_id = ?", query.SessionID)
	}
	return db
}

// groupExpression is the SQL a breakdown groups by.
//
// The conversation is coalesced because a call made outside one has a NULL
// session (migration 00017) and a bare GROUP BY over a nullable column would put
// those rows in a bucket with no key at all — a bucket a client cannot name and
// therefore cannot show.
//
// The day is formatted rather than truncated so that the label the database
// produces is the label the report serves: a DATE would arrive as a timestamp at
// midnight and be formatted a second time here, which is two places for one
// answer to be spelled.
func groupExpression(by GroupBy) string {
	switch by {
	case GroupByModel:
		return "model"
	case GroupByProvider:
		return "provider"
	case GroupBySource:
		return "source"
	case GroupBySession:
		return "COALESCE(session_id, '')"
	case GroupByDay:
		return "DATE_FORMAT(started_at, '%Y-%m-%d')"
	default:
		return ""
	}
}

// totalsSelection is the SELECT every totals query is made of.
//
// The three outcome counts are COUNTs because COUNT returns a whole number;
// the sums are CAST because SUM over an integer column is a DECIMAL in MySQL,
// and a DECIMAL arrives over the wire as text rather than as a number. The
// COALESCE is what makes an empty window a row of zeros instead of a row of
// NULLs: a total nobody has spent yet is zero, and a report that answered NULL
// for it would be one a client has to special-case.
const totalsSelection = `
	COUNT(*) AS calls,
	COUNT(CASE WHEN outcome = ? THEN 1 END) AS ok,
	COUNT(CASE WHEN outcome = ? THEN 1 END) AS error,
	COUNT(CASE WHEN outcome = ? THEN 1 END) AS cancelled,
	COALESCE(CAST(SUM(prompt_tokens) AS SIGNED), 0) AS prompt_tokens,
	COALESCE(CAST(SUM(completion_tokens) AS SIGNED), 0) AS completion_tokens,
	COALESCE(CAST(SUM(total_tokens) AS SIGNED), 0) AS total_tokens,
	COALESCE(CAST(SUM(duration_ms) AS SIGNED), 0) AS duration_ms,
	COALESCE(CAST(SUM(ttft_ms) AS SIGNED), 0) AS ttft_ms`

// outcomeArgs are the three words totalsSelection counts, in the order its
// placeholders stand.
func outcomeArgs() []any {
	return []any{string(OutcomeOK), string(OutcomeError), string(OutcomeCancelled)}
}

// totalsRow is one row of a totals query. It is read as columns and converted
// here rather than scanned into Totals directly, so that the SQL's aliases are
// the only thing that has to line up.
type totalsRow struct {
	Bucket           string `gorm:"column:bucket"`
	Calls            int64  `gorm:"column:calls"`
	Ok               int64  `gorm:"column:ok"`
	Error            int64  `gorm:"column:error"`
	Cancelled        int64  `gorm:"column:cancelled"`
	PromptTokens     int64  `gorm:"column:prompt_tokens"`
	CompletionTokens int64  `gorm:"column:completion_tokens"`
	TotalTokens      int64  `gorm:"column:total_tokens"`
	DurationMs       int64  `gorm:"column:duration_ms"`
	TTFTMs           int64  `gorm:"column:ttft_ms"`
}

func (r totalsRow) totals() Totals {
	return Totals{
		Calls: r.Calls, Ok: r.Ok, Error: r.Error, Cancelled: r.Cancelled,
		PromptTokens: r.PromptTokens, CompletionTokens: r.CompletionTokens,
		TotalTokens: r.TotalTokens, DurationMs: r.DurationMs, TTFTMs: r.TTFTMs,
	}
}

// unfinishedRow is one row of the count of calls that did not come back, in
// either of its two shapes: with a bucket when the report is broken down, and
// without one when it is not.
type unfinishedRow struct {
	Bucket     string `gorm:"column:bucket"`
	Unfinished int64  `gorm:"column:unfinished"`
}

var _ Ledger = (*GORMLedger)(nil)
