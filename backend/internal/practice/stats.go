package practice

// Practice statistics: how much was reviewed, how it went, and where the
// questions came from.
//
// Two things about this file are load-bearing and easy to get wrong:
//
// The correctness of a review is read out of the stored response
// (`outcome_json`) rather than out of a column, because the event table has no
// column for it and no index that could serve one. That is affordable at this
// version's scale — one person's practice history, thousands of rows — and the
// first thing to change when it stops being affordable is to add the column.
//
// An empty bucket is a bucket. The series a chart draws has one point per
// bucket in the window, and a day nobody practised has a point at zero; a
// series that only reports the days that have data cannot be plotted against a
// time axis without lying about where the gaps are. The gaps are filled here,
// from the same window the query was given, which is why the bucket labels are
// computed twice — once in SQL and once in Go — and why the two are checked
// against each other rather than assumed to agree.

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrInvalidStats reports a statistics request this version cannot act on: a
// bucket it does not know, a window that runs backwards, or one so wide that
// the answer would be a data export rather than a chart. It is refused rather
// than corrected, because a silently moved window is a number the user cannot
// explain.
var ErrInvalidStats = errors.New("practice: the statistics request cannot be served")

// Bucket is the width of one point in a series.
type Bucket string

const (
	// BucketDay is one point per calendar day.
	BucketDay Bucket = "day"
	// BucketWeek is one point per week, and the week is the ISO one: it starts
	// on Monday, and it is labelled by its Monday.
	BucketWeek Bucket = "week"
)

// Valid reports whether the bucket is one this version can draw.
func (b Bucket) Valid() bool {
	switch b {
	case BucketDay, BucketWeek:
		return true
	default:
		return false
	}
}

// span is how much time one bucket covers.
func (b Bucket) span() time.Duration {
	if b == BucketWeek {
		return 7 * 24 * time.Hour
	}
	return 24 * time.Hour
}

// start is the instant a bucket begins. For a day that is midnight UTC; for a
// week it is midnight UTC on its Monday, which is what makes every label a date
// that sorts chronologically as a string.
//
// The time is taken as it is, so callers pass UTC. Everything this package
// writes is UTC, and a bucket computed in a local zone would put a review made
// at 00:30 into the local previous day while storage still says the UTC one.
func (b Bucket) start(at time.Time) time.Time {
	day := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
	if b != BucketWeek {
		return day
	}
	// Monday is the first day. Go counts Sunday as zero, so shifting by six
	// days after the modulo turns Monday into zero and Sunday into six.
	offset := (int(day.Weekday()) + 6) % 7
	return day.AddDate(0, 0, -offset)
}

// label is the bucket's name, and it is the date of its first day. It is what
// storage produces too (see bucketSQL), and the two are compared rather than
// trusted.
func (b Bucket) label(at time.Time) string {
	return b.start(at).Format("2006-01-02")
}

// StatsQuery is one window of practice history.
type StatsQuery struct {
	// From and To bound the window. A zero To means now; a zero From means a
	// default span ending at To. Both are widened to whole buckets, so the
	// window the answer describes is not exactly the one that was asked for —
	// see normalizeStats.
	From time.Time
	To   time.Time
	// Bucket is the width of one point. The empty one means a day.
	Bucket Bucket
}

// Stats is the practice history of one window.
type Stats struct {
	// From and To are the window the answer covers: bucket-aligned, half-open,
	// and not necessarily the one the request named.
	From   time.Time
	To     time.Time
	Bucket Bucket
	// Series has one point per bucket in the window, empty ones included.
	Series []BucketStat
	// Sources and Categories say where the window's questions came from and how
	// they are filed, most first. They describe the questions *added* in the
	// window, not the whole bank: one request describes one period, and a
	// response whose parts describe different periods is a trap for whoever
	// draws it.
	Sources    []Distribution
	Categories []Distribution
}

// BucketStat is one point of the series: what happened in one bucket.
type BucketStat struct {
	// Label is the bucket's first day, "2006-01-02". Ordering the labels as
	// strings orders them as dates, which is why the series is labelled this way
	// and not with a serial number.
	Label string
	// Reviews counts every review in the bucket, including the ones that were
	// skipped: a skip is a decision the user made about a question, and a chart
	// of "how much did I practise" that hid them would disagree with the
	// history it is drawn from.
	Reviews int
	// Correct and Wrong count the reviews that were judged. They do not add up
	// to Reviews, and the difference is the skips — nothing is judged for a
	// skip, so a skip is neither.
	Correct int
	Wrong   int
	// Added counts the questions written in the bucket.
	Added int
	// Accuracy is Correct over the judgeable reviews — the two above, not
	// Reviews. A day of nothing but skips has an accuracy of zero, which is the
	// same number as a day of nothing but wrong answers; the two are told apart
	// by Correct and Wrong, not by this field.
	Accuracy float64
}

// Distribution is one row of a breakdown: a name, and how many questions carry
// it.
type Distribution struct {
	// Name is the source name, or the category name, or the empty string for a
	// question that came from nowhere in particular — which is not a rare case,
	// it is every question that was not written by a conversation.
	Name  string
	Count int
}

// The window a request that names neither end gets, and the widest one it can
// name. The maximum is not about cost: one bucket per day over a decade is a
// data export, and an endpoint that silently hands one back under the name of a
// chart has answered a question nobody asked.
const (
	defaultStatsBuckets = 30
	maxStatsBuckets     = 400
)

// Stats returns the practice history of one window.
func (s *Service) Stats(ctx context.Context, query StatsQuery) (Stats, error) {
	normalized, err := s.normalizeStats(query)
	if err != nil {
		return Stats{}, err
	}
	stats, err := s.repository.Stats(ctx, normalized)
	if err != nil {
		return Stats{}, err
	}
	series, err := fillSeries(normalized.Bucket, normalized.From, normalized.To, stats.Series)
	if err != nil {
		return Stats{}, err
	}
	stats.From = normalized.From
	stats.To = normalized.To
	stats.Bucket = normalized.Bucket
	stats.Series = series
	return stats, nil
}

// normalizeStats fills in what the request left out and widens what it named to
// whole buckets.
//
// Widening is the decision worth stating. A window of "the 14th at 09:30 to the
// 21st at 11:00" drawn in daily buckets has a first and a last point covering a
// third of a day each, and a bar chart of that says the ends of the range were
// quiet days when it is only saying they were short ones. So both ends are
// moved outwards to the bucket edges, and the answer reports the window it
// actually used.
func (s *Service) normalizeStats(query StatsQuery) (StatsQuery, error) {
	bucket := query.Bucket
	if bucket == "" {
		bucket = BucketDay
	}
	if !bucket.Valid() {
		return StatsQuery{}, fmt.Errorf("%w: %q is not a bucket", ErrInvalidStats, query.Bucket)
	}
	to := query.To.UTC()
	if query.To.IsZero() {
		to = s.now().UTC()
	}
	from := query.From.UTC()
	if query.From.IsZero() {
		// The default is a span of buckets ending with the one now falls in, so
		// the last point is today and the count is the number of buckets rather
		// than the number of days between the ends.
		from = bucket.start(to).Add(-bucket.span() * (defaultStatsBuckets - 1))
	}
	if from.After(to) {
		return StatsQuery{}, fmt.Errorf("%w: the window ends before it starts", ErrInvalidStats)
	}
	start := bucket.start(from)
	end := bucket.start(to).Add(bucket.span())
	if !end.After(start) {
		return StatsQuery{}, fmt.Errorf("%w: the window covers no bucket", ErrInvalidStats)
	}
	if count := int(end.Sub(start) / bucket.span()); count > maxStatsBuckets {
		return StatsQuery{}, fmt.Errorf("%w: the window covers %d %s buckets, and %d is the most",
			ErrInvalidStats, count, bucket, maxStatsBuckets)
	}
	return StatsQuery{From: start, To: end, Bucket: bucket}, nil
}

// fillSeries puts one point in the series for every bucket in the window,
// including the ones nothing happened in.
//
// A label storage produces that this window does not enumerate is an error
// rather than a row that gets dropped. The two sides compute the label
// separately — one in SQL, one in Go — and the only way they disagree is by
// both being wrong about the same thing: a locale, a time zone, an engine whose
// DATE_FORMAT means something else. Dropping the row would turn that into a
// chart with a hole in it and nothing to say about why.
func fillSeries(bucket Bucket, from, to time.Time, rows []BucketStat) ([]BucketStat, error) {
	byLabel := make(map[string]BucketStat, len(rows))
	for _, row := range rows {
		byLabel[row.Label] = row
	}
	filled := make([]BucketStat, 0, int(to.Sub(from)/bucket.span()))
	for at := from; at.Before(to); at = at.Add(bucket.span()) {
		label := bucket.label(at)
		row, found := byLabel[label]
		if !found {
			row = BucketStat{Label: label}
		} else {
			delete(byLabel, label)
			row.Label = label
		}
		if judged := row.Correct + row.Wrong; judged > 0 {
			row.Accuracy = float64(row.Correct) / float64(judged)
		}
		filled = append(filled, row)
	}
	if len(byLabel) > 0 {
		for label := range byLabel {
			return nil, fmt.Errorf("the statistics bucket %s is outside the window it was read for", label)
		}
	}
	return filled, nil
}

// Stats reads the window's counters and breakdowns.
//
// The series comes back with one row per bucket that has something in it, and
// the caller fills the empty ones — see fillSeries. Only the counters are read
// here: an accuracy is a division, and a second place in the code that divides
// is a second place the rule can change.
func (r *GORMRepository) Stats(ctx context.Context, query StatsQuery) (Stats, error) {
	if err := r.ready(); err != nil {
		return Stats{}, err
	}
	stats := Stats{From: query.From, To: query.To, Bucket: query.Bucket}

	err := r.db.WithContext(ctx).Raw(statsSeriesSQL(query.Bucket), query.From, query.To, query.From, query.To).
		Scan(&stats.Series).Error
	if err != nil {
		return Stats{}, fmt.Errorf("read the practice series: %w", err)
	}
	err = r.db.WithContext(ctx).Raw(statsSourcesSQL, query.From, query.To).Scan(&stats.Sources).Error
	if err != nil {
		return Stats{}, fmt.Errorf("read the practice sources: %w", err)
	}
	err = r.db.WithContext(ctx).Raw(statsCategoriesSQL, query.From, query.To).Scan(&stats.Categories).Error
	if err != nil {
		return Stats{}, fmt.Errorf("read the practice categories: %w", err)
	}
	return stats, nil
}

// bucketSQL is the expression that turns a timestamp into its bucket label, and
// it has to agree with Bucket.label down to the string it produces.
//
// The column name is concatenated rather than bound because it is not a value:
// it comes from the two columns this package reads, never from a request. There
// is no caller text anywhere in these statements — every bound parameter is a
// time — which is why the concatenation is safe and why the escape rules a
// predicate would need do not apply.
func bucketSQL(bucket Bucket, column string) string {
	if bucket == BucketWeek {
		return "DATE_FORMAT(DATE_SUB(" + column + ", INTERVAL WEEKDAY(" + column + ") DAY), '%Y-%m-%d')"
	}
	return "DATE_FORMAT(" + column + ", '%Y-%m-%d')"
}

// statsSeriesSQL is the four series in one statement.
//
// It has to be one statement, because the two halves read different tables: the
// reviews are events and the questions added are rows. Unioning the two
// aggregations and summing them per bucket is what makes the series line up
// bucket for bucket without a second round trip to check that they do.
//
// The correctness of a review is `$.judgment.is_correct`, and the two things
// that path is not are both worth stating. It is not `$.is_correct`: the stored
// response is the API response, whose `is_correct` sits inside the judgement
// and inside the answer face, and a path that does not exist returns NULL
// rather than failing — every review would count as neither correct nor wrong,
// and the chart would show an accuracy of zero for a user who got everything
// right.
//
// It is not the answer face's `is_correct` either, which does exist at
// `$.entry.is_correct` and is the wrong fact: a skipped question writes no
// answer face, so that field is whatever the question carried before it was
// skipped. Counting it would credit a skip with the verdict of the review
// before it.
//
// A review that was judged is compared with TRUE and with FALSE separately.
// Neither comparison is true for a skip — the path is absent, so the comparison
// is NULL — which is what leaves skips out of both counts.
func statsSeriesSQL(bucket Bucket) string {
	review := bucketSQL(bucket, "review_time")
	created := bucketSQL(bucket, "creat_time")
	// The column is named `bucket` inside the statement and `label` on the way
	// out: the row this scans into is the answer's shape, and it is the label of
	// a point rather than the grouping key of a query.
	return "SELECT bucket AS label, " +
		"COALESCE(SUM(reviews), 0) AS reviews, " +
		"COALESCE(SUM(correct), 0) AS correct, " +
		"COALESCE(SUM(wrong), 0) AS wrong, " +
		"COALESCE(SUM(added), 0) AS added " +
		"FROM (" +
		"SELECT " + review + " AS bucket, COUNT(*) AS reviews, " +
		"SUM(JSON_EXTRACT(outcome_json, '$.judgment.is_correct') = TRUE) AS correct, " +
		"SUM(JSON_EXTRACT(outcome_json, '$.judgment.is_correct') = FALSE) AS wrong, " +
		"0 AS added " +
		"FROM practice_review_events WHERE review_time >= ? AND review_time < ? " +
		"GROUP BY " + review +
		" UNION ALL " +
		"SELECT " + created + " AS bucket, 0 AS reviews, 0 AS correct, 0 AS wrong, COUNT(*) AS added " +
		"FROM notebook_entries WHERE creat_time >= ? AND creat_time < ? " +
		"GROUP BY " + created +
		") AS buckets GROUP BY bucket ORDER BY bucket ASC"
}

// statsSourcesSQL is the breakdown by where the questions came from.
//
// A question with no source is counted under the empty name rather than
// dropped: it is a real answer — the question was not written by a conversation
// — and a breakdown that quietly omits a group makes the other groups look like
// the whole.
const statsSourcesSQL = "SELECT COALESCE(source, '') AS name, COUNT(*) AS count " +
	"FROM notebook_entries WHERE creat_time >= ? AND creat_time < ? " +
	"GROUP BY COALESCE(source, '') ORDER BY COUNT(*) DESC, name ASC"

// statsCategoriesSQL is the breakdown by how the questions are filed. The two
// joins are the bank's own filing tables, which this package reads rather than
// owns (§1.2).
const statsCategoriesSQL = "SELECT category.name AS name, COUNT(*) AS count " +
	"FROM notebook_entries e " +
	"JOIN notebook_entry_categories filing ON filing.notebook_entries_id = e.notebook_entries_id " +
	"JOIN notebook_categories category ON category.notebook_categories_id = filing.notebook_categories_id " +
	"WHERE e.creat_time >= ? AND e.creat_time < ? " +
	"GROUP BY category.name ORDER BY COUNT(*) DESC, category.name ASC"
